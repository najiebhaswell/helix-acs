package api

import (
	"io/fs"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"github.com/redis/go-redis/v9"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/raykavin/helix-acs/internal/api/handler"
	"github.com/raykavin/helix-acs/internal/api/middleware"
	"github.com/raykavin/helix-acs/internal/auth"
	"github.com/raykavin/helix-acs/internal/device"
	"github.com/raykavin/helix-acs/internal/logger"
	"github.com/raykavin/helix-acs/internal/parameter"
	"github.com/raykavin/helix-acs/internal/task"
	webUI "github.com/raykavin/helix-acs/web"
)

type Config struct {
	ACSUsername string
	ACSPassword string
	MaxAttempts int
	CORS        map[string]string
}

// NewRouter wires every route to its handler and wraps them with the
// appropriate middleware stack.
//
// Route structure:
//
//	GET  /health                                 no auth
//	POST /api/v1/auth/login                      no auth
//	POST /api/v1/auth/refresh                    no auth
//
//	All /api/v1/* routes below require a valid JWT Bearer token.
//
//	GET    /api/v1/devices
//	GET    /api/v1/devices/{serial}
//	PUT    /api/v1/devices/{serial}
//	DELETE /api/v1/devices/{serial}
//	GET    /api/v1/devices/{serial}/parameters
//
//	POST /api/v1/devices/{serial}/tasks/wifi
//	POST /api/v1/devices/{serial}/tasks/wan
//	POST /api/v1/devices/{serial}/tasks/lan
//	POST /api/v1/devices/{serial}/tasks/reboot
//	POST /api/v1/devices/{serial}/tasks/factory-reset
//	POST /api/v1/devices/{serial}/tasks/parameters
//	POST /api/v1/devices/{serial}/tasks/firmware
//	POST /api/v1/devices/{serial}/tasks/diagnostic
//	GET  /api/v1/devices/{serial}/tasks
//
//	GET    /api/v1/tasks/{task_id}
//	DELETE /api/v1/tasks/{task_id}
func NewRouter(
	deviceSvc device.Service,
	taskQueue task.Queue,
	jwtSvc *auth.JWTService,
	mongoClient *mongo.Client,
	redisClient *redis.Client,
	paramRepo parameter.Repository,
	log logger.Logger,
	cfg Config,
) http.Handler {

	// Handlers
	authHandler := handler.NewAuthHandler(jwtSvc, cfg.ACSUsername, cfg.ACSPassword)
	deviceHandler := handler.NewDeviceHandler(deviceSvc, paramRepo)
	taskHandler := handler.NewTaskHandler(taskQueue, deviceSvc, cfg.MaxAttempts)
	healthHandler := handler.NewHealthHandler(mongoClient, redisClient)
	snapshotHandler := handler.NewSnapshotHandler(deviceSvc, paramRepo)

	// Root router global middleware applied to every route.
	r := mux.NewRouter()
	r.Use(middleware.Recovery(log))
	r.Use(middleware.Logging(log))
	r.Use(middleware.CORS(cfg.CORS))
	r.Use(middleware.RateLimit())

	// Health endpoint no authentication required.
	r.HandleFunc("/health", healthHandler.Check).Methods(http.MethodGet)

	// Public API routes (no auth required)
	pub := r.PathPrefix("/api/v1").Subrouter()
	pub.HandleFunc("/auth/login", authHandler.Login).Methods(http.MethodPost)
	pub.HandleFunc("/auth/refresh", authHandler.Refresh).Methods(http.MethodPost)

	// Protected API routes (no auth required)
	api := r.PathPrefix("/api/v1").Subrouter()
	api.Use(middleware.JWTAuth(jwtSvc))

	// Device routes
	api.HandleFunc("/devices", deviceHandler.List).Methods(http.MethodGet)
	api.HandleFunc("/devices/{serial}", deviceHandler.Get).Methods(http.MethodGet)
	api.HandleFunc("/devices/{serial}", deviceHandler.Update).Methods(http.MethodPut)
	api.HandleFunc("/devices/{serial}", deviceHandler.Delete).Methods(http.MethodDelete)
	api.HandleFunc("/devices/{serial}/parameters", deviceHandler.GetParameters).Methods(http.MethodGet)
	api.HandleFunc("/devices/{serial}/provision", deviceHandler.GetProvisionInfo).Methods(http.MethodGet)

	// Snapshot routes
	api.HandleFunc("/devices/{serial}/snapshots/last-known-good", snapshotHandler.SaveLastKnownGood).Methods(http.MethodPost)

	// Task creation routes
	api.HandleFunc("/devices/{serial}/tasks/wifi", taskHandler.CreateWifi).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/wan", taskHandler.CreateWAN).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/lan", taskHandler.CreateLAN).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/reboot", taskHandler.CreateReboot).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/factory-reset", taskHandler.CreateFactoryReset).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/parameters", taskHandler.CreateSetParams).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/firmware", taskHandler.CreateFirmware).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/diagnostic", taskHandler.CreateDiagnostic).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/ping", taskHandler.CreatePingTest).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/traceroute", taskHandler.CreateTraceroute).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/speed-test", taskHandler.CreateSpeedTest).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/connected-devices", taskHandler.CreateConnectedDevices).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/cpe-stats", taskHandler.CreateCPEStats).Methods(http.MethodPost)
	api.HandleFunc("/devices/{serial}/tasks/port-forwarding", taskHandler.CreatePortForwarding).Methods(http.MethodPost)

	// Task list for a specific device
	api.HandleFunc("/devices/{serial}/tasks", taskHandler.ListByDevice).Methods(http.MethodGet)

	// Global task routes
	api.HandleFunc("/tasks/{task_id}", taskHandler.Get).Methods(http.MethodGet)
	api.HandleFunc("/tasks/{task_id}", taskHandler.Cancel).Methods(http.MethodDelete)

	// Embedded UI assets: avoid stale browsers holding old app.js after deploy.
	r.HandleFunc("/app.js", func(w http.ResponseWriter, r *http.Request) {
		serveEmbeddedStatic(w, r, "app.js", "application/javascript; charset=utf-8")
	}).Methods(http.MethodGet, http.MethodHead)
	r.HandleFunc("/style.css", func(w http.ResponseWriter, r *http.Request) {
		serveEmbeddedStatic(w, r, "style.css", "text/css; charset=utf-8")
	}).Methods(http.MethodGet, http.MethodHead)

	// Serve embedded Web UI for direct access.
	webFS := http.FileServer(http.FS(webUI.FS))
	r.PathPrefix("/").Handler(http.StripPrefix("/", webFS))

	return r
}

// serveEmbeddedStatic serves one file from the embedded web UI with headers
// that discourage long-lived caching (UI is embedded in the binary and
// changes on every deploy).
func serveEmbeddedStatic(w http.ResponseWriter, r *http.Request, name, contentType string) {
	b, err := fs.ReadFile(webUI.FS, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache, must-revalidate")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_, _ = w.Write(b)
}
