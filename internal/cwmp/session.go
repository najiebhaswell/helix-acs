package cwmp

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/raykavin/helix-acs/internal/datamodel"
	"github.com/raykavin/helix-acs/internal/device"
	"github.com/raykavin/helix-acs/internal/logger"
	"github.com/raykavin/helix-acs/internal/parameter"
	"github.com/raykavin/helix-acs/internal/schema"
	"github.com/raykavin/helix-acs/internal/task"
)

type SessionState int

const (
	StateNew        SessionState = iota // freshly created, no Inform yet
	StateInform                         // Inform received and processed
	StateProcessing                     // executing pending tasks
	StateDone                           // session complete
)

// Session tracks per-connection CWMP state.
type Session struct {
	ID           string
	DeviceSerial string
	State        SessionState
	CreatedAt    time.Time
	mu           sync.Mutex
	pendingTasks []*task.Task          // tasks waiting to be dispatched to the CPE
	currentTask  *task.Task            // task currently awaiting a CPE response
	mapper       datamodel.Mapper      // data-model mapper resolved during Inform
	instanceMap  datamodel.InstanceMap // instance indices discovered during Inform / summon
	driver       *schema.DeviceDriver  // YAML-driven device driver resolved during Inform

	// Port-forwarding AddObject follow-up: after receiving AddObjectResponse
	// this function is called with the new instance number to build the
	// subsequent SetParameterValues request.
	addObjFollowUp func(instanceNum int) ([]byte, error)

	// wanProvision drives the multi-step vendor-agnostic provisioning state machine.
	wanProvision *WANProvision

	// Parameter summon state: after Inform, ACS fetches all CPE parameters
	// via GetParameterNames → batched GetParameterValues and stores them in MongoDB.
	// summonPhase: 0=inactive, 1=waiting GetParameterNamesResponse, 2=waiting GetParameterValuesResponse batch
	summonPhase      int
	summonSchemaName string
	summonAllNames   []string          // all leaf names discovered by GetParameterNames
	summonBatchIdx   int               // index of current batch being fetched
	summonAllParams  map[string]string // accumulated params from all completed batches

	// lastSetParams holds the parameters sent in the most recent SetParameterValues
	// dispatch so that handleSetParamValuesResponse can sync them to PostgreSQL.
	lastSetParams map[string]string

	// pendingWANCredentials holds PPPoE credentials from a WAN task to be
	// persisted to PostgreSQL once the task completes successfully.
	// Keys: "_helix.provision.pppoe_username", "_helix.provision.pppoe_password",
	//       "_helix.provision.vlan_id", "_helix.provision.connection_type".
	// This is needed because TP-Link ONTs never return the PPPoE password
	// in GetParameterValues responses.
	pendingWANCredentials map[string]string
}

func (s *Session) setState(st SessionState) {
	s.mu.Lock()
	s.State = st
	s.mu.Unlock()
}

// SessionManager manages active CWMP sessions, keyed by session ID.
type SessionManager struct {
	sessions sync.Map
	mu       sync.RWMutex
}

// NewSessionManager creates a new SessionManager.
func NewSessionManager() *SessionManager { return &SessionManager{} }

// GetOrCreate returns the Session for the given sessionID, creating a new one if needed.
func (sm *SessionManager) GetOrCreate(sessionID string) *Session {
	s := &Session{
		ID:        sessionID,
		State:     StateNew,
		CreatedAt: time.Now().UTC(),
	}
	actual, _ := sm.sessions.LoadOrStore(sessionID, s)
	return actual.(*Session)
}

// Delete removes the Session for the given sessionID.
func (sm *SessionManager) Delete(sessionID string) { sm.sessions.Delete(sessionID) }

// Cleanup removes sessions older than 30 minutes.
func (sm *SessionManager) Cleanup() {
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	sm.sessions.Range(func(key, value any) bool {
		s := value.(*Session)
		if s.CreatedAt.Before(cutoff) {
			sm.sessions.Delete(key)
		}
		return true
	})
}

// Handler implements http.Handler and manages CWMP sessions and message handling.
type Handler struct {
	deviceSvc      device.Service
	taskQueue      task.Queue
	parameterRepo  parameter.Repository
	sessionMgr     *SessionManager
	log            logger.Logger
	acsUsername    string
	acsPassword    string
	acsURL         string
	informInterval time.Duration
	schemaRegistry *schema.Registry
	schemaResolver *schema.Resolver
	driverRegistry *schema.DeviceDriverRegistry
	lastSummonTime sync.Map // serial → time.Time
}

// NewHandler creates a new CWMP Handler with the given dependencies and configuration.
func NewHandler(
	deviceSvc device.Service,
	taskQueue task.Queue,
	parameterRepo parameter.Repository,
	log logger.Logger,
	username, password, acsURL string,
	informInterval time.Duration,
	schemaReg *schema.Registry,
	driverReg *schema.DeviceDriverRegistry,
) *Handler {
	return &Handler{
		deviceSvc:      deviceSvc,
		taskQueue:      taskQueue,
		parameterRepo:  parameterRepo,
		sessionMgr:     NewSessionManager(),
		log:            log,
		acsUsername:    username,
		acsPassword:    password,
		acsURL:         acsURL,
		informInterval: informInterval,
		schemaRegistry: schemaReg,
		schemaResolver: schema.NewResolver(schemaReg),
		driverRegistry: driverReg,
	}
}

// ServeHTTP implements the full CWMP session lifecycle per TR-069.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sessionID := h.getSessionID(r)
	session := h.sessionMgr.GetOrCreate(sessionID)

	http.SetCookie(w, &http.Cookie{
		Name:     "cwmp-session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
	})

	ctx := r.Context()

	body, err := io.ReadAll(io.LimitReader(r.Body, 16<<20))
	if err != nil {
		h.log.WithError(err).
			WithField("session", sessionID).Error("CWMP: Failed to read request body")

		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Empty body - CPE acknowledges the last ACS response.
	// Dispatch next pending task, or close session.
	if len(bytes.TrimSpace(body)) == 0 {
		// If summon is pending, send GetParameterNames before any real tasks.
		session.mu.Lock()
		summonPhase := session.summonPhase
		session.mu.Unlock()

		if summonPhase == 1 {
			h.sendGetParameterNamesSummon(w, session)
			return
		}

		session.mu.Lock()
		var next *task.Task
		if len(session.pendingTasks) > 0 {
			next = session.pendingTasks[0]
			session.pendingTasks = session.pendingTasks[1:]
		}
		session.mu.Unlock()

		if next != nil {
			h.dispatchTask(ctx, w, session, next)
			return
		}
		session.setState(StateDone)
		h.sessionMgr.Delete(sessionID)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	env, err := ParseEnvelope(body)
	if err != nil {
		h.log.WithError(err).WithField("session", sessionID).Error("CWMP: Failed to parse SOAP envelope")
		h.writeSoapFault(w, sessionID, "Client", "9003", "Invalid arguments")
		return
	}

	switch {
	case env.Body.Inform != nil:
		h.handleInform(ctx, w, r, env, session)

	case env.Body.GetRPCMethods != nil:
		respXML, ferr := h.handleGetRPCMethods(ctx, env)
		if ferr != nil {
			h.log.WithError(ferr).Error("CWMP: handleGetRPCMethods")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeXML(w, respXML)

	case env.Body.TransferComplete != nil:
		respXML, ferr := h.handleTransferComplete(ctx, env)
		if ferr != nil {
			h.log.WithError(ferr).Error("CWMP: handleTransferComplete")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		writeXML(w, respXML)

	// CPE responses to ACS-initiated RPC requests
	case env.Body.SetParameterValuesResponse != nil:
		h.handleSetParamValuesResponse(ctx, w, session)

	case env.Body.GetParameterNamesResponse != nil:
		h.handleGetParameterNamesResponse(ctx, w, session, env.Body.GetParameterNamesResponse)

	case env.Body.GetParameterValuesResponse != nil:
		h.handleGetParamValuesResponse(ctx, w, session, env.Body.GetParameterValuesResponse)

	case env.Body.RebootResponse != nil:
		h.handleTaskResponse(ctx, w, session, nil, "")

	case env.Body.FactoryResetResponse != nil:
		h.handleTaskResponse(ctx, w, session, nil, "")

	case env.Body.DownloadResponse != nil:
		h.handleDownloadResponse(ctx, w, session, env.Body.DownloadResponse)

	case env.Body.AddObjectResponse != nil:
		h.handleAddObjectResponse(ctx, w, session, env.Body.AddObjectResponse)

	case env.Body.DeleteObjectResponse != nil:
		h.handleDeleteObjectResponse(ctx, w, session)

	case env.Body.Fault != nil:
		cwmpCode := env.Body.Fault.Detail.CWMPFault.FaultCode
		cwmpMsg := env.Body.Fault.Detail.CWMPFault.FaultString
		if cwmpCode == "" {
			cwmpCode = env.Body.Fault.FaultCode
			cwmpMsg = env.Body.Fault.FaultString
		}
		faultMsg := fmt.Sprintf("CWMP %s: %s", cwmpCode, cwmpMsg)
		session.mu.Lock()
		hadWANProvision := session.wanProvision != nil
		session.wanProvision = nil
		session.addObjFollowUp = nil
		session.mu.Unlock()
		h.log.WithField("session", sessionID).
			WithField("cwmp_code", cwmpCode).
			WithField("cwmp_msg", cwmpMsg).
			WithField("raw_fault", string(body)).
			WithField("cleared_wan_provision", hadWANProvision).
			Warn("CWMP: CPE returned fault")
		h.handleTaskResponse(ctx, w, session, nil, faultMsg)

	default:
		h.log.WithField("session", sessionID).
			Warn("CWMP: Unrecognised message body; closing session")
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleInform processes the Inform message, upserts the device, loads pending tasks,
func (h *Handler) handleInform(ctx context.Context, w http.ResponseWriter, _ *http.Request, env *Envelope, session *Session) {
	id := headerID(env)
	upsertReq := h.extractInformParams(env)

	session.mu.Lock()
	session.DeviceSerial = upsertReq.Serial
	session.mu.Unlock()

	h.log.
		WithField("session", session.ID).
		WithField("serial", upsertReq.Serial).
		WithField("manufacturer", upsertReq.Manufacturer).
		Info("CWMP: Inform received from CPE")

	dev, err := h.deviceSvc.UpsertFromInform(ctx, upsertReq)
	if err != nil {
		h.log.WithError(err).WithField("serial", upsertReq.Serial).Error("CWMP: Upsert device failed")
		h.writeSoapFault(w, id, "Server", "9002", "Internal error")
		return
	}

	h.log.WithField("serial", dev.Serial).
		WithField("id", dev.ID.Hex()).Debug("CWMP: Device upserted")

	// Record device parameters in PostgreSQL repository (non-blocking)
	if len(upsertReq.Parameters) > 0 {
		go func() {
			recordCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := h.parameterRepo.UpdateParameters(recordCtx, upsertReq.Serial, upsertReq.Parameters); err != nil {
				h.log.WithError(err).WithField("serial", upsertReq.Serial).Warn("CWMP: Failed to record parameters")
			}
		}()
	}

	modelType := datamodel.DetectFromRootObject(firstRootObject(upsertReq.Parameters))

	// Resolve device driver for vendor-specific provisioning.
	var driver *schema.DeviceDriver
	if h.driverRegistry != nil {
		driver = h.driverRegistry.Resolve(
			upsertReq.Manufacturer,
			upsertReq.ProductClass,
			string(modelType),
		)
		if driver != nil {
			h.log.
				WithField("serial", upsertReq.Serial).
				WithField("driver", driver.ID).
				Info("CWMP: resolved device driver")
		}
	}

	// Use discovery hints from the resolved driver when available.
	var discoveryHints *datamodel.DiscoveryHints
	if driver != nil {
		discoveryHints = &datamodel.DiscoveryHints{
			WANTypePath:        driver.Discovery.WANTypePath,
			WANTypeValuesWAN:   driver.Discovery.WANTypeValues.WAN,
			WANTypeValuesLAN:   driver.Discovery.WANTypeValues.LAN,
			WANServiceTypePath: driver.Discovery.WANServiceTypePath,
			GPONEnablePath:     driver.Discovery.GPONEnablePath,
		}
	}
	instanceMap := datamodel.DiscoverInstancesWithHints(upsertReq.Parameters, discoveryHints)

	// Resolve the schema name for this device (e.g. "tr181", "vendor/huawei/tr181").
	schemaName := h.schemaResolver.Resolve(upsertReq.Manufacturer, upsertReq.ProductClass, upsertReq.DataModel)
	upsertReq.Schema = schemaName

	h.log.
		WithField("serial", upsertReq.Serial).
		WithField("schema", schemaName).
		Debug("CWMP: resolved device schema")

	// Build a schema-driven mapper; fall back to the standard mapper when
	// the registry is empty or the schema is not found.
	var mapper datamodel.Mapper
	if sm := schema.NewSchemaMapper(h.schemaRegistry, schemaName, instanceMap); sm != nil {
		mapper = sm
	} else {
		mapper = datamodel.ApplyInstanceMap(datamodel.NewMapper(modelType), instanceMap)
	}

	// Detect factory reset synchronously BEFORE DequeuePending so the restore task
	// is included in the current session's pending queue.
	h.detectAndHandleReset(ctx, upsertReq.Serial, env.Body.Inform, mapper)

	// Fetch real pending tasks first so we can factor them into the summon decision.
	pendingTasks, err := h.taskQueue.DequeuePending(ctx, upsertReq.Serial)
	if err != nil {
		h.log.WithError(err).WithField("serial", upsertReq.Serial).Error("CWMP: Dequeue pending tasks failed")
	}

	// Trigger full parameter summon to keep device data (WAN, WiFi, LAN)
	// up-to-date. Throttle to at most once every 2 minutes per device to avoid
	// overwhelming devices with very short periodic inform intervals (e.g.
	// CDATA/ZTE at 10s).
	shouldSummon := true
	if lastSummon, ok := h.lastSummonTime.Load(upsertReq.Serial); ok {
		if time.Since(lastSummon.(time.Time)) < 2*time.Minute {
			shouldSummon = false
		}
	}
	// WAN tasks require accurate instance discovery (PPP/IP/VLAN indices).
	// Force a summon even within the throttle window so that executeTask always
	// sees the real instanceMap and chooses update vs. fresh-provision correctly.
	if !shouldSummon {
		for _, pt := range pendingTasks {
			if pt.Type == task.TypeWAN {
				shouldSummon = true
				h.log.WithField("serial", upsertReq.Serial).
					Info("CWMP: forcing summon for pending WAN task")
				break
			}
		}
	}
	if shouldSummon {
		session.mu.Lock()
		session.summonPhase = 1
		session.summonSchemaName = schemaName
		session.mu.Unlock()
		h.lastSummonTime.Store(upsertReq.Serial, time.Now())
		h.log.WithField("serial", upsertReq.Serial).
			Info("CWMP: scheduling full parameter summon")
	}

	// On "8 DIAGNOSTICS COMPLETE", prepend synthetic result-collection tasks
	// so results are fetched before executing new configuration tasks.
	var synthTasks []*task.Task
	if hasDiagnosticsCompleteEvent(env.Body.Inform) {
		diagTasks, _ := h.taskQueue.FindExecutingDiagnostics(ctx, upsertReq.Serial)
		for _, dt := range diagTasks {
			paths := task.BuildDiagResultPaths(dt.Type, mapper)
			if len(paths) == 0 {
				continue
			}
			payload, _ := json.Marshal(task.GetDiagResultPayload{
				OriginalTaskID:   dt.ID,
				OriginalTaskType: dt.Type,
				Paths:            paths,
			})
			synthTasks = append(synthTasks, &task.Task{
				ID:      dt.ID + "_collect",
				Serial:  upsertReq.Serial,
				Type:    task.TypeGetDiagResult,
				Payload: json.RawMessage(payload),
				Status:  task.StatusPending,
			})
		}
	}

	allTasks := append(synthTasks, pendingTasks...)

	session.mu.Lock()
	session.pendingTasks = allTasks
	session.mapper = mapper
	session.instanceMap = instanceMap
	session.driver = driver
	session.State = StateProcessing
	session.mu.Unlock()

	respXML, err := BuildInformResponse(id)
	if err != nil {
		h.log.WithError(err).Error("CWMP: Build InformResponse failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeXML(w, respXML)
}

// handleTransferComplete processes the TransferComplete message, marking the related task done or failed.
func (h *Handler) handleTransferComplete(ctx context.Context, env *Envelope) ([]byte, error) {
	tc := env.Body.TransferComplete
	id := headerID(env)

	h.log.
		WithField("command_key", tc.CommandKey).
		WithField("fault_code", tc.FaultStruct.FaultCode).
		Info("CWMP: Transfer complete received")

	if tc.CommandKey != "" {
		t, err := h.taskQueue.GetByID(ctx, tc.CommandKey)
		if err == nil && t != nil {
			now := time.Now().UTC()
			t.CompletedAt = &now
			if tc.FaultStruct.FaultCode == 0 {
				t.Status = task.StatusDone
			} else {
				t.Status = task.StatusFailed
				t.Error = fmt.Sprintf("fault %d: %s", tc.FaultStruct.FaultCode, tc.FaultStruct.FaultString)
			}
			if uerr := h.taskQueue.UpdateStatus(ctx, t); uerr != nil {
				h.log.WithError(uerr).WithField("task_id", t.ID).Error("CWMP: Failed to update task after TransferComplete")
			}
		}
	}

	return BuildEnvelope(id, Body{TransferComplete: nil})
}

// handleGetRPCMethods returns the list of supported RPC methods.
func (h *Handler) handleGetRPCMethods(_ context.Context, env *Envelope) ([]byte, error) {
	id := headerID(env)
	methods := []string{
		"GetRPCMethods", "SetParameterValues", "GetParameterValues",
		"GetParameterNames", "AddObject", "DeleteObject",
		"Download", "Reboot", "FactoryReset",
	}
	return BuildEnvelope(id, Body{
		GetRPCMethodsResponse: &GetRPCMethodsResponse{
			MethodList: MethodList{Methods: methods},
		},
	})
}

// handleSetParamValuesResponse handles SetParameterValuesResponse.
// For async diagnostic tasks the response only means "diagnostic started"
// we keep them executing until DIAGNOSTICS COMPLETE arrives in a later Inform.
func (h *Handler) handleSetParamValuesResponse(ctx context.Context, w http.ResponseWriter, session *Session) {
	// If a WANProvision state machine is running, advance it instead of
	// immediately completing the task.
	session.mu.Lock()
	wp := session.wanProvision
	session.mu.Unlock()

	if wp != nil {
		h.log.WithField("step", wp.stepIndex()).
			WithField("total", wp.totalSteps()).
			Info("CWMP: WAN provision SetParams response")
		nextXML, err := wp.onSetParams()
		if err != nil {
			h.log.WithError(err).Error("CWMP: WAN provision SetParams step failed")
			session.mu.Lock()
			session.wanProvision = nil
			session.mu.Unlock()
			h.handleTaskResponse(ctx, w, session, nil, err.Error())
			return
		}
		if nextXML != nil {
			// More steps to go — keep currentTask set and send next request.
			writeXML(w, nextXML)
			return
		}
		// All steps done — clear provision state and mark task complete.
		session.mu.Lock()
		session.wanProvision = nil
		creds := session.pendingWANCredentials
		session.pendingWANCredentials = nil
		serial := session.DeviceSerial
		session.mu.Unlock()
		h.log.WithField("task_id", wp.t.ID).Info("CWMP: WAN provisioning complete")
		// Persist PPPoE credentials to PostgreSQL now that provisioning succeeded.
		if len(creds) > 0 {
			h.persistWANCredentials(ctx, serial, creds)
		}
		h.handleTaskResponse(ctx, w, session, nil, "")
		return
	}

	session.mu.Lock()
	t := session.currentTask
	session.mu.Unlock()

	if t != nil && task.IsDiagnosticAsync(t.Type) {
		// Diagnostic is running asynchronously on the CPE.
		// Clear currentTask so the slot is available, but leave the task's
		// Redis status as StatusExecuting.
		session.mu.Lock()
		session.currentTask = nil
		session.mu.Unlock()

		h.log.
			WithField("task_id", t.ID).
			WithField("type", string(t.Type)).
			Info("CWMP: Async diagnostic started on CPE")

		h.dispatchNextOrClose(ctx, w, session)
		return
	}

	// On success, sync the parameters that were sent to PostgreSQL so that
	// SaveSnapshot captures the latest device state (including recent task changes).
	session.mu.Lock()
	setParams := session.lastSetParams
	serial := session.DeviceSerial
	session.lastSetParams = nil
	wanCreds := session.pendingWANCredentials
	session.pendingWANCredentials = nil
	session.mu.Unlock()

	if len(setParams) > 0 {
		// Sync synchronously so the task is only marked done AFTER PostgreSQL is updated.
		// This prevents a race where the user saves a snapshot before params are persisted.
		pgCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := h.parameterRepo.UpdateParameters(pgCtx, serial, setParams); err != nil {
			h.log.WithError(err).WithField("serial", serial).Warn("CWMP: failed to sync task params to PostgreSQL")
		}
	}

	// Persist PPPoE credentials if this was a WAN task (set_params flow).
	if len(wanCreds) > 0 {
		h.persistWANCredentials(ctx, serial, wanCreds)
	}

	h.handleTaskResponse(ctx, w, session, nil, "")
}

// buildSetParamsXML stores params in session.lastSetParams (for PostgreSQL sync
// on success) then delegates to BuildSetParameterValues.
func (h *Handler) buildSetParamsXML(session *Session, id string, params map[string]string) ([]byte, error) {
	session.mu.Lock()
	session.lastSetParams = params
	session.mu.Unlock()
	return BuildSetParameterValues(id, params)
}

// sendGetParameterNamesSummon sends a GetParameterNames RPC to the CPE to
// begin the full parameter summon process.
func (h *Handler) sendGetParameterNamesSummon(w http.ResponseWriter, session *Session) {
	session.mu.Lock()
	session.summonPhase = 1
	serial := session.DeviceSerial
	mapper := session.mapper
	schemaName := session.summonSchemaName
	session.mu.Unlock()

	// Determine root path from data-model, not vendor name.
	// TR-098 devices use "InternetGatewayDevice."; TR-181 devices use "Device."
	// Priority: schema name (contains tr098/tr181) → mapper type → default Device.
	rootPath := "Device."

	// Check schema name first (most reliable).
	if schemaName != "" {
		lowerSchema := strings.ToLower(schemaName)
		if strings.Contains(lowerSchema, "tr098") {
			rootPath = "InternetGatewayDevice."
		} else if strings.Contains(lowerSchema, "tr181") {
			rootPath = "Device."
		}
	}

	// Fall back to mapper type check if schema didn't determine path
	if rootPath == "Device." && mapper != nil {
		switch mapper.(type) {
		case *datamodel.TR098Mapper:
			rootPath = "InternetGatewayDevice."
		}
	}

	h.log.WithField("serial", serial).
		WithField("schema", schemaName).
		WithField("path", rootPath).
		Info("CWMP: sending GetParameterNames for summon")

	id := uuid.NewString()
	env, err := BuildGetParameterNames(id, rootPath, false)
	if err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: build GetParameterNames failed")
		session.mu.Lock()
		session.summonPhase = 0
		session.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeXML(w, env)
}

// summonBatchSizeDefault is the default number of parameter names sent per
// GetParameterValues RPC during a parameter summon. Keep this small for TR-181
// TP-Link devices that can disconnect on large batches.
const summonBatchSizeDefault = 500

// summonBatchSizeTR098 is used for TR-098 devices to reduce the number of
// summon round-trips. This helps devices with very short Inform intervals
// (e.g. 10s) complete full summons before the next Inform starts.
const summonBatchSizeTR098 = 5000

func summonBatchSizeForSchema(schemaName string) int {
	lower := strings.ToLower(schemaName)
	if strings.Contains(lower, "tr098") {
		return summonBatchSizeTR098
	}
	return summonBatchSizeDefault
}

// handleGetParameterNamesResponse collects all leaf parameter names from the
// CPE's response and starts the first batched GetParameterValues fetch.
func (h *Handler) handleGetParameterNamesResponse(
	ctx context.Context,
	w http.ResponseWriter,
	session *Session,
	resp *GetParameterNamesResponse,
) {
	session.mu.Lock()
	serial := session.DeviceSerial
	schemaName := session.summonSchemaName
	session.mu.Unlock()

	// Collect only leaf parameters (skip object paths ending in ".").
	var names []string
	for _, info := range resp.ParameterList.ParameterInfoStructs {
		if !strings.HasSuffix(info.Name, ".") {
			names = append(names, info.Name)
		}
	}

	batchSize := summonBatchSizeForSchema(schemaName)
	nBatches := (len(names) + batchSize - 1) / batchSize
	h.log.WithField("serial", serial).
		WithField("total", len(names)).
		WithField("batch_size", batchSize).
		WithField("batches", nBatches).
		Info("CWMP: GetParameterNames received, starting batched fetch")

	if len(names) == 0 {
		session.mu.Lock()
		session.summonPhase = 0
		session.mu.Unlock()
		h.dispatchNextOrClose(ctx, w, session)
		return
	}

	session.mu.Lock()
	session.summonPhase = 2
	session.summonAllNames = names
	session.summonBatchIdx = 0
	session.summonAllParams = make(map[string]string, len(names))
	session.mu.Unlock()

	h.sendNextSummonBatch(ctx, w, session)
}

// sendNextSummonBatch sends the next GetParameterValues batch during a summon.
func (h *Handler) sendNextSummonBatch(ctx context.Context, w http.ResponseWriter, session *Session) {
	session.mu.Lock()
	names := session.summonAllNames
	idx := session.summonBatchIdx
	serial := session.DeviceSerial
	schemaName := session.summonSchemaName
	session.mu.Unlock()

	batchSize := summonBatchSizeForSchema(schemaName)
	start := idx * batchSize
	if start >= len(names) {
		// All batches done — save and finish.
		h.finishSummon(ctx, w, session)
		return
	}
	end := start + batchSize
	if end > len(names) {
		end = len(names)
	}
	batch := names[start:end]

	h.log.WithField("serial", serial).
		WithField("batch", idx+1).
		WithField("of", (len(names)+batchSize-1)/batchSize).
		WithField("params", len(batch)).
		Info("CWMP: fetching parameter batch")

	id := uuid.NewString()
	env, err := BuildGetParameterValues(id, batch)
	if err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: build GetParameterValues batch failed")
		session.mu.Lock()
		session.summonPhase = 0
		session.mu.Unlock()
		h.dispatchNextOrClose(ctx, w, session)
		return
	}
	writeXML(w, env)
}

// finishSummon saves all accumulated parameters to MongoDB and rebuilds the mapper.
func (h *Handler) finishSummon(ctx context.Context, w http.ResponseWriter, session *Session) {
	session.mu.Lock()
	serial := session.DeviceSerial
	params := session.summonAllParams
	schemaName := session.summonSchemaName
	session.summonPhase = 0
	session.summonAllNames = nil
	session.summonAllParams = nil
	session.mu.Unlock()

	if err := h.deviceSvc.UpdateParameters(ctx, serial, params); err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: save summoned parameters failed")
	} else {
		h.log.WithField("serial", serial).WithField("count", len(params)).Info("CWMP: full parameter summon complete")
	}

	// Persist full parameter set to PostgreSQL synchronously so that:
	// 1. GetAllParameters (used by SaveSnapshot) returns the complete dataset.
	// 2. The write completes BEFORE tasks are dispatched, preventing an async
	//    goroutine from overwriting param values that subsequent task syncs set.
	{
		pgCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.parameterRepo.UpdateParameters(pgCtx, serial, params); err != nil {
			h.log.WithError(err).WithField("serial", serial).Warn("CWMP: failed to persist summon params to PostgreSQL")
		}
	}

	// Rebuild mapper with newly discovered instance indices from full param set.
	session.mu.Lock()
	drv := session.driver
	session.mu.Unlock()
	var discoveryHints *datamodel.DiscoveryHints
	if drv != nil {
		discoveryHints = &datamodel.DiscoveryHints{
			WANTypePath:        drv.Discovery.WANTypePath,
			WANTypeValuesWAN:   drv.Discovery.WANTypeValues.WAN,
			WANTypeValuesLAN:   drv.Discovery.WANTypeValues.LAN,
			WANServiceTypePath: drv.Discovery.WANServiceTypePath,
			GPONEnablePath:     drv.Discovery.GPONEnablePath,
		}
	}
	instanceMap := datamodel.DiscoverInstancesWithHints(params, discoveryHints)
	h.log.WithField("serial", serial).
		WithField("wan_iface", instanceMap.WANIPIfaceIdx).
		WithField("lan_iface", instanceMap.LANIPIfaceIdx).
		WithField("ppp_iface", instanceMap.PPPIfaceIdx).
		WithField("free_gpon", instanceMap.FreeGPONLinkIdx).
		Info("CWMP: summon DiscoverInstances result")
	modelType := datamodel.DetectFromRootObject(firstRootObject(params))
	var newMapper datamodel.Mapper
	if sm := schema.NewSchemaMapper(h.schemaRegistry, schemaName, instanceMap); sm != nil {
		newMapper = sm
	} else {
		newMapper = datamodel.ApplyInstanceMap(datamodel.NewMapper(modelType), instanceMap)
	}
	session.mu.Lock()
	session.mapper = newMapper
	session.instanceMap = instanceMap
	session.mu.Unlock()

	// Extract WAN, WiFi, and LAN info from summon parameters and persist to device.

	// Extract WiFi info for both bands
	wifi24 := extractWiFiInfo(0, params, newMapper)
	wifi5 := extractWiFiInfo(1, params, newMapper)

	// Extract Band Steering status (uses mapper.BandSteeringPath — returns nil for non-TP-Link)
	bandSteeringStatus := extractBandSteeringStatus(params, newMapper)
	wifi24.BandSteeringEnabled = bandSteeringStatus
	wifi5.BandSteeringEnabled = bandSteeringStatus

	h.log.WithField("serial", serial).
		WithField("wifi_24_ssid", wifi24.SSID).
		WithField("wifi_24_channel", wifi24.Channel).
		WithField("wifi_24_standard", wifi24.Standard).
		WithField("wifi_24_band_steering", bandSteeringStatus).
		WithField("wifi_5_ssid", wifi5.SSID).
		WithField("wifi_5_channel", wifi5.Channel).
		WithField("wifi_5_standard", wifi5.Standard).
		Info("CWMP: extractWiFiInfo from summon")

	// Extract LAN info
	lanInfo := extractLANInfo(params, newMapper)
	h.log.WithField("serial", serial).
		WithField("lan_ip", lanInfo.IPAddress).
		WithField("lan_subnet", lanInfo.SubnetMask).
		WithField("dhcp_enabled", lanInfo.DHCPEnabled).
		Info("CWMP: extractLANInfo from summon")

	wansInfo := extractWANInfos(params, newMapper, drv)
	stats, _ := parseCPEStats(params, newMapper)

	// Determine best WAN IP for root (prefer PPPoE, then anything non-empty).
	// Sort wansInfo deterministically: PPPoE entries first, then others.
	sort.SliceStable(wansInfo, func(i, j int) bool {
		// PPPoE entries come first (higher priority)
		iPPPoE := wansInfo[i].ConnectionType == "PPPoE"
		jPPPoE := wansInfo[j].ConnectionType == "PPPoE"
		if iPPPoE != jPPPoE {
			return iPPPoE // true (PPPoE) sorts before false (non-PPPoE)
		}
		// Stable sort: if both are same type, maintain original order by index
		return false
	})

	var bestWanIP string
	for _, w := range wansInfo {
		if w.IPAddress != "" {
			bestWanIP = w.IPAddress
			break // First non-empty IP (after sorting) is deterministically chosen
		}
	}

	acsUrl := params["Device.ManagementServer.URL"]
	if acsUrl == "" {
		acsUrl = params["InternetGatewayDevice.ManagementServer.URL"]
	}
	var cpuUsage *int64
	// TR-181: Device.DeviceInfo.ProcessStatus.CPUUsage
	// TR-098 CDATA/ZTE: InternetGatewayDevice.DeviceInfo.X_CMS_CPUUsage
	for _, cpuPath := range []string{
		"Device.DeviceInfo.ProcessStatus.CPUUsage",
		"InternetGatewayDevice.DeviceInfo.X_CMS_CPUUsage",
	} {
		if cpuStr := params[cpuPath]; cpuStr != "" {
			if c, err := strconv.ParseInt(cpuStr, 10, 64); err == nil {
				cpuUsage = &c
				break
			}
		}
	}

	// Persist all collected info to device
	infoUpdate := device.InfoUpdate{
		WANs:          wansInfo,
		UptimeSeconds: &stats.UptimeSeconds,
		RAMTotal:      &stats.RAMTotalKB,
		RAMFree:       &stats.RAMFreeKB,
		CPUUsage:      cpuUsage,
		ACSURL:        &acsUrl,
	}
	if lanInfo.IPAddress != "" {
		infoUpdate.IPAddress = &lanInfo.IPAddress
	}
	if bestWanIP != "" {
		infoUpdate.WANIP = &bestWanIP
	}
	if wifi24.SSID != "" || wifi24.Enabled || wifi24.Channel > 0 {
		infoUpdate.WiFi24 = &wifi24
	}
	if wifi5.SSID != "" || wifi5.Enabled || wifi5.Channel > 0 {
		infoUpdate.WiFi5 = &wifi5
	}
	if lanInfo.IPAddress != "" || lanInfo.DHCPEnabled {
		infoUpdate.LAN = &lanInfo
	}

	if err := h.deviceSvc.UpdateInfo(ctx, serial, infoUpdate); err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: persist WAN/WiFi/LAN info failed")
	} else {
		h.log.WithField("serial", serial).Info("CWMP: persist WAN/WiFi/LAN info success")
	}

	h.dispatchNextOrClose(ctx, w, session)
}

// handleGetParamValuesResponse routes the parsed parameter map to the correct
// result handler depending on the current task type.
func (h *Handler) handleGetParamValuesResponse(
	ctx context.Context,
	w http.ResponseWriter,
	session *Session,
	resp *GetParameterValuesResponse,
) {
	params := buildGetParamResult(resp)

	// Handle summon phase 2: accumulate batch results, send next or finish.
	session.mu.Lock()
	summonPhase := session.summonPhase
	session.mu.Unlock()

	if summonPhase == 2 {
		// Accumulate results from this batch.
		session.mu.Lock()
		for k, v := range params {
			session.summonAllParams[k] = v
		}
		session.summonBatchIdx++
		session.mu.Unlock()
		// Send next batch or finish if all done.
		h.sendNextSummonBatch(ctx, w, session)
		return
	}

	session.mu.Lock()
	t := session.currentTask
	session.currentTask = nil
	mapper := session.mapper
	drv := session.driver
	session.mu.Unlock()

	if t == nil {
		h.dispatchNextOrClose(ctx, w, session)
		return
	}

	switch t.Type {

	case task.TypeGetDiagResult:
		// Synthetic collect task resolve original diagnostic task.
		var p task.GetDiagResultPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			h.log.WithError(err).WithField("task_id", t.ID).Error("CWMP: Unmarshal GetDiagResultPayload")
			break
		}
		origTask, err := h.taskQueue.GetByID(ctx, p.OriginalTaskID)
		if err != nil || origTask == nil {
			h.log.WithError(err).WithField("orig_id", p.OriginalTaskID).Error("CWMP: Original diag task not found")
			break
		}
		result := parseDiagResult(p.OriginalTaskType, params, mapper, origTask)
		now := time.Now().UTC()
		origTask.CompletedAt = &now
		origTask.Status = task.StatusDone
		origTask.Result = result
		if uerr := h.taskQueue.UpdateStatus(ctx, origTask); uerr != nil {
			h.log.WithError(uerr).WithField("task_id", origTask.ID).Error("CWMP: Update diag task status")
		}
		// Persist result into device info where applicable.
		h.persistDiagToDevice(ctx, origTask.Serial, p.OriginalTaskType, result, params, mapper)

	case task.TypeConnectedDevices:
		hosts := parseConnectedHosts(params, mapper, drv)
		now := time.Now().UTC()
		t.CompletedAt = &now
		t.Status = task.StatusDone
		t.Result = hosts
		_ = h.taskQueue.UpdateStatus(ctx, t)
		_ = h.deviceSvc.UpdateInfo(ctx, t.Serial, device.InfoUpdate{ConnectedHosts: hosts})

	case task.TypeCPEStats:
		statsResult, wanPartial := parseCPEStats(params, mapper)
		now := time.Now().UTC()
		t.CompletedAt = &now
		t.Status = task.StatusDone
		t.Result = statsResult
		_ = h.taskQueue.UpdateStatus(ctx, t)
		_ = h.deviceSvc.UpdateInfo(ctx, t.Serial, device.InfoUpdate{WANs: []device.WANInfo{wanPartial}})

	case task.TypePortForwarding:
		rules := parsePortMappingRules(params, mapper)
		now := time.Now().UTC()
		t.CompletedAt = &now
		t.Status = task.StatusDone
		t.Result = rules
		_ = h.taskQueue.UpdateStatus(ctx, t)

	default:
		// Generic GetParams  store raw map.
		h.completeTask(ctx, t, params, "")
	}

	h.dispatchNextOrClose(ctx, w, session)
}

// handleDownloadResponse handles the CPE's immediate reply to a Download RPC.
func (h *Handler) handleDownloadResponse(ctx context.Context, w http.ResponseWriter, session *Session, resp *DownloadResponse) {
	if resp.Status == 0 {
		h.handleTaskResponse(ctx, w, session, nil, "")
		return
	}
	// Status=1: async download; keep currentTask alive for TransferComplete.
	session.mu.Lock()
	var next *task.Task
	if len(session.pendingTasks) > 0 {
		next = session.pendingTasks[0]
		session.pendingTasks = session.pendingTasks[1:]
	}
	session.mu.Unlock()

	if next != nil {
		h.dispatchTask(ctx, w, session, next)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAddObjectResponse receives the new instance number and sends the
// follow-up SetParameterValues to configure the newly created object.
func (h *Handler) handleAddObjectResponse(ctx context.Context, w http.ResponseWriter, session *Session, resp *AddObjectResponse) {
	// WANProvision takes priority over the legacy addObjFollowUp hook.
	session.mu.Lock()
	wp := session.wanProvision
	fn := session.addObjFollowUp
	session.addObjFollowUp = nil
	session.mu.Unlock()

	if wp != nil {
		h.log.WithField("step", wp.stepIndex()).
			WithField("total", wp.totalSteps()).
			WithField("instance", resp.InstanceNumber).
			Info("CWMP: WAN provision AddObject response")
		nextXML, err := wp.onAddObject(resp.InstanceNumber)
		if err != nil {
			h.log.WithError(err).Error("CWMP: WAN provision AddObject step failed")
			session.mu.Lock()
			session.wanProvision = nil
			session.mu.Unlock()
			h.handleTaskResponse(ctx, w, session, nil, err.Error())
			return
		}
		if nextXML != nil {
			writeXML(w, nextXML)
			return
		}
		// All steps complete.
		session.mu.Lock()
		session.wanProvision = nil
		session.mu.Unlock()
		h.log.WithField("task_id", wp.t.ID).Info("CWMP: WAN provisioning complete")
		h.handleTaskResponse(ctx, w, session, nil, "")
		return
	}

	if fn == nil {
		h.handleTaskResponse(ctx, w, session, nil, "")
		return
	}

	taskXML, err := fn(resp.InstanceNumber)
	if err != nil {
		h.log.WithError(err).WithField("instance", resp.InstanceNumber).Error("CWMP: build port-mapping SetParameterValues")
		h.handleTaskResponse(ctx, w, session, nil, err.Error())
		return
	}
	writeXML(w, taskXML)
}

// handleDeleteObjectResponse handles DeleteObjectResponse.
// If a WANProvision delete flow is running, advances the state machine.
func (h *Handler) handleDeleteObjectResponse(ctx context.Context, w http.ResponseWriter, session *Session) {
	session.mu.Lock()
	wp := session.wanProvision
	session.mu.Unlock()

	if wp != nil {
		h.log.WithField("step", wp.stepIndex()).
			WithField("total", wp.totalSteps()).
			Info("CWMP: WAN provision DeleteObject response")
		nextXML, err := wp.onDeleteObject()
		if err != nil {
			h.log.WithError(err).Error("CWMP: WAN provision Delete step failed")
			session.mu.Lock()
			session.wanProvision = nil
			session.mu.Unlock()
			h.handleTaskResponse(ctx, w, session, nil, err.Error())
			return
		}
		if nextXML != nil {
			writeXML(w, nextXML)
			return
		}
		// All steps complete.
		session.mu.Lock()
		session.wanProvision = nil
		session.mu.Unlock()
		h.log.WithField("task_id", wp.t.ID).Info("CWMP: WAN provisioning (delete+add) complete")
		h.handleTaskResponse(ctx, w, session, nil, "")
		return
	}

	// No provision running — simple delete task completed.
	h.handleTaskResponse(ctx, w, session, nil, "")
}

// handleTaskResponse marks the current in-flight task done or failed and
// dispatches the next pending task.
func (h *Handler) handleTaskResponse(ctx context.Context, w http.ResponseWriter, session *Session, result any, errMsg string) {
	session.mu.Lock()
	t := session.currentTask
	session.currentTask = nil
	session.mu.Unlock()

	if t != nil {
		h.completeTask(ctx, t, result, errMsg)
	}

	h.dispatchNextOrClose(ctx, w, session)
}

// dispatchTask marks t as executing, builds its CWMP XML, and writes it to w.
func (h *Handler) dispatchTask(ctx context.Context, w http.ResponseWriter, session *Session, t *task.Task) {
	// Synthetic tasks (TypeGetDiagResult) are never written to Redis.
	if t.Type != task.TypeGetDiagResult {
		now := time.Now().UTC()
		t.Status = task.StatusExecuting
		t.ExecutedAt = &now
		t.Attempts++
		if err := h.taskQueue.UpdateStatus(ctx, t); err != nil {
			h.log.WithError(err).WithField("task_id", t.ID).Error("CWMP: Mark task executing failed")
			h.dispatchNextOrClose(ctx, w, session)
			return
		}
	}

	session.mu.Lock()
	mapper := session.mapper
	session.mu.Unlock()

	taskXML, buildErr := h.executeTask(ctx, t, mapper, session, w)
	if buildErr != nil {
		now2 := time.Now().UTC()
		if t.Type != task.TypeGetDiagResult {
			t.Status = task.StatusFailed
			t.CompletedAt = &now2
			t.Error = buildErr.Error()
			if uerr := h.taskQueue.UpdateStatus(ctx, t); uerr != nil {
				h.log.WithError(uerr).WithField("task_id", t.ID).Error("CWMP: Update failed task status")
			}
		}
		h.log.WithError(buildErr).WithField("task_id", t.ID).Error("CWMP: execute task failed")
		h.dispatchNextOrClose(ctx, w, session)
		return
	}

	if taskXML == nil {
		// executeTask handled the response itself (e.g. AddObject sets up follow-up).
		return
	}

	session.mu.Lock()
	session.currentTask = t
	session.mu.Unlock()

	h.log.
		WithField("task_id", t.ID).
		WithField("type", string(t.Type)).
		Info("CWMP: dispatching task to CPE")

	writeXML(w, taskXML)
}

// executeTask converts a Task into CWMP XML bytes.
// For port-forwarding add it also configures session.addObjFollowUp and returns nil.
func (h *Handler) executeTask(ctx context.Context, t *task.Task, mapper datamodel.Mapper, session *Session, w http.ResponseWriter) ([]byte, error) {
	// Build executor with driver hints for vendor-specific behaviour.
	session.mu.Lock()
	drv := session.driver
	session.mu.Unlock()

	var exe *task.Executor
	if drv != nil {
		exe = task.NewExecutorWithHints(&task.DriverHints{
			BandSteeringPath:   drv.WiFi.BandSteeringPath,
			SecurityModeMapper: drv.MapSecurityMode,
		})
	} else {
		exe = task.NewExecutor()
	}

	switch t.Type {

	// WAN task: full PPPoE provisioning or credential-only update.
	case task.TypeWAN:
		var p task.WANPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, fmt.Errorf("unmarshal wan payload: %w", err)
		}

		session.mu.Lock()
		im := session.instanceMap
		session.mu.Unlock()

		connType := strings.TrimSpace(strings.ToLower(p.ConnectionType))
		isPPPoE := connType == "pppoe"
		if !isPPPoE && connType == "" {
			// Some API callers omit connection_type for PPPoE updates/provisioning.
			// Infer PPPoE intent when PPP credentials or VLAN are supplied.
			isPPPoE = p.Username != "" || p.Password != "" || p.VLAN > 0
		}

		if isPPPoE && im.WANIPIfaceIdx == 0 {
			// No working WAN IP interface: device needs full provisioning.
			if p.VLAN == 0 {
				return nil, fmt.Errorf("WAN full provisioning requires VLAN ID")
			}
			if p.Username == "" {
				return nil, fmt.Errorf("WAN full provisioning requires PPPoE username")
			}

			session.mu.Lock()
			drv := session.driver
			session.mu.Unlock()

			var wp *WANProvision
			if drv != nil && drv.GetProvisionFlow("wan_pppoe_new") != nil {
				// Use YAML-driven provisioning flow from device driver.
				var err error
				wp, err = newWANProvisionFromDriver(t, drv, "wan_pppoe_new", map[string]string{
					"vlan_id":  strconv.Itoa(p.VLAN),
					"username": p.Username,
					"password": p.Password,
					"gpon_idx": strconv.Itoa(im.FreeGPONLinkIdx),
				})
				if err != nil {
					return nil, fmt.Errorf("create driver provision flow: %w", err)
				}
				h.log.WithField("task_id", t.ID).
					WithField("driver", drv.ID).
					WithField("flow", "wan_pppoe_new").
					WithField("gpon_idx", im.FreeGPONLinkIdx).
					WithField("vlan", p.VLAN).
					Info("CWMP: starting driver-based WAN PPPoE provisioning")
			} else if mapper.WANProvisioningType() == "add_object" {
				// Legacy add_object fallback (no driver loaded).
				wp = newWANProvision(t, im.FreeGPONLinkIdx, p.VLAN, p.Username, p.Password)
				h.log.WithField("task_id", t.ID).
					WithField("gpon_idx", im.FreeGPONLinkIdx).
					WithField("vlan", p.VLAN).
					Info("CWMP: starting legacy WAN PPPoE provisioning (add_object)")
			}

			if wp != nil {
				session.mu.Lock()
				session.wanProvision = wp
				session.currentTask = t
				session.pendingWANCredentials = buildWANCredentialMap(p)
				session.mu.Unlock()
				xmlBytes, err := wp.buildCurrentXML()
				if err != nil {
					return nil, err
				}
				writeXML(w, xmlBytes)
				return nil, nil // response already written
			}

			// Generic devices (set_params): single SetParameterValues.
			genParams, err := buildGenericWANParams(p, im, mapper)
			if err != nil {
				return nil, err
			}
			h.log.WithField("task_id", t.ID).
				WithField("provisioning_type", mapper.WANProvisioningType()).
				Info("CWMP: starting generic PPPoE provisioning (set_params)")
			// Store credentials for persistence after task succeeds.
			session.mu.Lock()
			session.pendingWANCredentials = buildWANCredentialMap(p)
			session.mu.Unlock()
			return h.buildSetParamsXML(session, t.ID, genParams)
		}

		if isPPPoE && im.PPPIfaceIdx > 0 {
			// PPPoE already provisioned: update credentials and/or VLAN

			// Determine update mode based on whether VLAN is changing
			vlanChanging := p.VLAN > 0 && p.VLAN != im.WANCurrentVLAN
			credentialsChanging := p.Username != "" || p.Password != ""

			if vlanChanging {
				if p.Username == "" {
					return nil, fmt.Errorf("WAN PPPoE VLAN change requires PPPoE username")
				}
			}

			if !vlanChanging && !credentialsChanging {
				return nil, fmt.Errorf("WAN update: no credentials or VLAN change provided")
			}

			session.mu.Lock()
			drv := session.driver
			session.mu.Unlock()

			if drv != nil {
				// Choose the correct update flow.
				// Both flows are single-step YAML (single SetParameterValues RPC) so
				// an Inform arriving mid-flow cannot leave the device in a broken state.
				// BuildSetParameterValues sorts: Enable=0 → VLANID → credentials → Enable=1.
				flowName := "wan_pppoe_update"
				inputVars := map[string]string{
					"ip_iface_idx":  strconv.Itoa(im.WANIPIfaceIdx),
					"ppp_iface_idx": strconv.Itoa(im.PPPIfaceIdx),
					"username":      p.Username,
					"password":      p.Password,
				}

				if vlanChanging {
					if im.WANIPIfaceIdx == 0 || im.PPPIfaceIdx == 0 || im.WANVLANTermIdx == 0 {
						return nil, fmt.Errorf("WAN PPPoE VLAN change requires instance map (ip=%d ppp=%d vlan=%d)",
							im.WANIPIfaceIdx, im.PPPIfaceIdx, im.WANVLANTermIdx)
					}
					flowName = "wan_pppoe_update_vlan"
					inputVars["vlan_id"] = strconv.Itoa(p.VLAN)
					inputVars["vlan_term_idx"] = strconv.Itoa(im.WANVLANTermIdx)
				}

				wp, err := newWANProvisionFromDriver(t, drv, flowName, inputVars)
				if err != nil {
					return nil, fmt.Errorf("create driver WAN update flow %q: %w", flowName, err)
				}

				h.log.WithField("task_id", t.ID).
					WithField("driver", drv.ID).
					WithField("flow", flowName).
					WithField("ppp_idx", im.PPPIfaceIdx).
					WithField("ip_idx", im.WANIPIfaceIdx).
					WithField("old_vlan", im.WANCurrentVLAN).
					WithField("new_vlan", p.VLAN).
					WithField("vlan_change", vlanChanging).
					Info("CWMP: WAN PPPoE update via driver YAML flow (atomic single-step)")

				session.mu.Lock()
				session.wanProvision = wp
				session.currentTask = t
				session.pendingWANCredentials = buildWANCredentialMap(p)
				session.mu.Unlock()

				xmlBytes, err := wp.buildCurrentXML()
				if err != nil {
					return nil, err
				}
				writeXML(w, xmlBytes)
				return nil, nil
			}

			// Fallback: no driver loaded — build params directly using mapper paths.
			// This path is only hit for devices without a YAML driver registered.
			params := make(map[string]string)
			h.log.WithField("task_id", t.ID).
				WithField("vlan_change", vlanChanging).
				Info("CWMP: WAN PPPoE update fallback (no driver, set_params)")

			if p.Username != "" {
				params[mapper.WANPPPoEUserPath()] = p.Username
			}
			if p.Password != "" {
				params[mapper.WANPPPoEPassPath()] = p.Password
			}
			if vlanChanging && im.WANVLANTermIdx > 0 {
				params[fmt.Sprintf("Device.Ethernet.VLANTermination.%d.VLANID", im.WANVLANTermIdx)] = strconv.Itoa(p.VLAN)
				params[fmt.Sprintf("Device.IP.Interface.%d.Enable", im.WANIPIfaceIdx)] = "0"
			}
			if im.PPPIfaceIdx > 0 {
				params[fmt.Sprintf("Device.PPP.Interface.%d.AuthenticationProtocol", im.PPPIfaceIdx)] = "AUTO_AUTH"
				params[fmt.Sprintf("Device.PPP.Interface.%d.Enable", im.PPPIfaceIdx)] = "1"
			}
			if im.WANIPIfaceIdx > 0 {
				params[fmt.Sprintf("Device.IP.Interface.%d.Enable", im.WANIPIfaceIdx)] = "1"
			}
			if len(params) == 0 {
				return nil, fmt.Errorf("WAN update fallback: no parameters could be built")
			}

			session.mu.Lock()
			session.pendingWANCredentials = buildWANCredentialMap(p)
			session.mu.Unlock()
			return h.buildSetParamsXML(session, t.ID, params)
		}

		// Non-PPPoE WAN task: use the generic executor.
		params, err := exe.BuildSetParams(ctx, t, mapper)
		if err != nil {
			return nil, err
		}
		if len(params) == 0 {
			return nil, fmt.Errorf("task %s produced no parameters", t.ID)
		}
		return h.buildSetParamsXML(session, t.ID, params)

	// WiFi task with Band Steering support
	case task.TypeWifi:
		var wifiPayload task.WiFiPayload
		if err := json.Unmarshal(t.Payload, &wifiPayload); err != nil {
			return nil, fmt.Errorf("unmarshal wifi payload: %w", err)
		}

		// Log the received WiFi payload for debugging
		h.log.WithField("task_id", t.ID).
			WithField("band", wifiPayload.Band).
			WithField("ssid", wifiPayload.SSID).
			WithField("security", wifiPayload.Security).
			WithField("password", "***").
			WithField("channel", wifiPayload.Channel).
			Info("CWMP: WiFi task payload received")

		params, err := exe.BuildSetParams(ctx, t, mapper)
		if err != nil {
			return nil, err
		}
		if len(params) == 0 {
			return nil, fmt.Errorf("task %s produced no parameters", t.ID)
		}

		// Log all parameters being sent for WiFi task
		for path, value := range params {
			if path == mapper.WiFiPasswordPath(0) || path == mapper.WiFiPasswordPath(1) {
				h.log.WithField("task_id", t.ID).
					WithField("param", path).
					WithField("value", "***").
					Debug("CWMP: WiFi parameter to be set")
			} else {
				h.log.WithField("task_id", t.ID).
					WithField("param", path).
					WithField("value", value).
					Debug("CWMP: WiFi parameter to be set")
			}
		}

		// Log mapper path information for debugging
		h.log.WithField("task_id", t.ID).
			WithField("secPath0", mapper.WiFiSecurityModePath(0)).
			WithField("secPath1", mapper.WiFiSecurityModePath(1)).
			WithField("passwdPath0", mapper.WiFiPasswordPath(0)).
			WithField("passwdPath1", mapper.WiFiPasswordPath(1)).
			WithField("payloadSecurity", wifiPayload.Security).
			WithField("payloadPassword", wifiPayload.Password != "").
			Debug("CWMP: WiFi mapper paths and payload validation")

		// Only sync SSID/password/security to both bands if Band Steering is EXPLICITLY being enabled in this task
		// Don't sync based on device's current state - only if this task is turning Band Steering ON
		bandSteeringWillBeEnabled := wifiPayload.BandSteeringEnabled != nil && *wifiPayload.BandSteeringEnabled

		// If Band Steering is enabled in this task, sync SSID, password, and security mode to both bands
		if bandSteeringWillBeEnabled && (wifiPayload.SSID != "" || wifiPayload.Password != "") {
			ssidPath24 := mapper.WiFiSSIDPath(0)
			ssidPath5 := mapper.WiFiSSIDPath(1)
			passwdPath24 := mapper.WiFiPasswordPath(0)
			passwdPath5 := mapper.WiFiPasswordPath(1)
			secPath24 := mapper.WiFiSecurityModePath(0)
			secPath5 := mapper.WiFiSecurityModePath(1)

			if wifiPayload.SSID != "" {
				// Sync SSID to both bands - only if paths are valid
				if ssidPath24 != "" && ssidPath5 != "" {
					params[ssidPath24] = wifiPayload.SSID
					params[ssidPath5] = wifiPayload.SSID
					h.log.WithField("task_id", t.ID).
						WithField("ssid", wifiPayload.SSID).
						Info("CWMP: Band Steering enabled - syncing SSID to both bands")
				} else {
					h.log.WithField("task_id", t.ID).
						WithField("path24", ssidPath24).
						WithField("path5", ssidPath5).
						Warn("CWMP: SSID paths invalid for Band Steering sync")
				}
			}

			if wifiPayload.Password != "" {
				// Sync password to both bands - only if paths are valid
				if passwdPath24 != "" && passwdPath5 != "" {
					params[passwdPath24] = wifiPayload.Password
					params[passwdPath5] = wifiPayload.Password
					h.log.WithField("task_id", t.ID).
						Info("CWMP: Band Steering enabled - syncing password to both bands")
				} else {
					h.log.WithField("task_id", t.ID).
						WithField("path24", passwdPath24).
						WithField("path5", passwdPath5).
						Warn("CWMP: Password paths invalid for Band Steering sync")
				}
			}

			// Also sync security mode to both bands if it was set in the payload
			if wifiPayload.Security != "" {
				session.mu.Lock()
				drv := session.driver
				session.mu.Unlock()

				var mode string
				if drv != nil {
					mode = drv.MapSecurityMode(wifiPayload.Security)
				} else {
					// Legacy hardcoded mapping as fallback
					mode = wifiPayload.Security
					if wifiPayload.Security == "WPA2-PSK" {
						mode = "WPA2-Personal"
					} else if wifiPayload.Security == "WPA-WPA2-PSK" {
						mode = "WPA-WPA2-Personal"
					}
				}

				if secPath24 != "" && secPath5 != "" {
					params[secPath24] = mode
					params[secPath5] = mode
					h.log.WithField("task_id", t.ID).
						WithField("security_mode", mode).
						Info("CWMP: Band Steering enabled - syncing security mode to both bands")
				} else {
					h.log.WithField("task_id", t.ID).
						WithField("path24", secPath24).
						WithField("path5", secPath5).
						WithField("security_mode", mode).
						Warn("CWMP: Security mode paths invalid for Band Steering sync")
				}
			}
		}

		return h.buildSetParamsXML(session, t.ID, params)

	// SetParameterValues-based tasks
	case task.TypeLAN, task.TypeSetParams,
		task.TypePingTest, task.TypeTraceroute, task.TypeSpeedTest:
		params, err := exe.BuildSetParams(ctx, t, mapper)
		if err != nil {
			return nil, err
		}
		if len(params) == 0 {
			return nil, fmt.Errorf("task %s produced no parameters", t.ID)
		}
		return h.buildSetParamsXML(session, t.ID, params)

	// Legacy diagnostic
	case task.TypeDiagnostic:
		var p task.DiagnosticPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, fmt.Errorf("unmarshal diagnostic payload: %w", err)
		}
		params, _ := exe.BuildSetParams(ctx, t, mapper)
		if len(params) == 0 {
			params = map[string]string{diagnosticStatePath(p.DiagType, mapper): "Requested"}
		}
		return BuildSetParameterValues(t.ID, params)

	// GetParameterValues-based tasks
	case task.TypeGetParams, task.TypeConnectedDevices, task.TypeCPEStats:
		names, err := exe.BuildGetParams(ctx, t, mapper)
		if err != nil {
			return nil, err
		}
		if len(names) == 0 {
			return nil, fmt.Errorf("task %s has no parameter names", t.ID)
		}
		return BuildGetParameterValues(t.ID, names)

	// Synthetic diagnostic result collection
	case task.TypeGetDiagResult:
		var p task.GetDiagResultPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, fmt.Errorf("unmarshal GetDiagResultPayload: %w", err)
		}
		if len(p.Paths) == 0 {
			return nil, fmt.Errorf("GetDiagResult task %s has no paths", t.ID)
		}
		return BuildGetParameterValues(t.ID, p.Paths)

	// Port forwarding
	case task.TypePortForwarding:
		var p task.PortForwardingPayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, fmt.Errorf("unmarshal port_forwarding payload: %w", err)
		}
		switch p.Action {
		case task.PortForwardingAdd:
			// Step 1: AddObject to create the new instance.
			// Step 2 (on AddObjectResponse): SetParameterValues to configure it.
			base := mapper.PortMappingBasePath()
			xml, err := BuildAddObject(t.ID, base)
			if err != nil {
				return nil, err
			}
			pCopy := p
			session.mu.Lock()
			session.currentTask = t
			session.addObjFollowUp = func(instanceNum int) ([]byte, error) {
				params := buildPortMappingParams(base, instanceNum, pCopy)
				return BuildSetParameterValues(t.ID, params)
			}
			session.mu.Unlock()
			writeXML(w, xml)
			return nil, nil // signal: already wrote response

		case task.PortForwardingRemove:
			if p.InstanceNumber <= 0 {
				return nil, fmt.Errorf("port_forwarding remove requires instance_number")
			}
			objPath := fmt.Sprintf("%s%d.", mapper.PortMappingBasePath(), p.InstanceNumber)
			return BuildDeleteObject(t.ID, objPath)

		case task.PortForwardingList:
			names, _ := exe.BuildGetParams(ctx, t, mapper)
			if len(names) == 0 {
				return nil, fmt.Errorf("port_forwarding list produced no paths")
			}
			return BuildGetParameterValues(t.ID, names)

		default:
			return nil, fmt.Errorf("unknown port_forwarding action: %s", p.Action)
		}

	// Simple one-shot tasks
	case task.TypeReboot:
		// Save current parameters snapshot before reboot (async, non-blocking)
		go func() {
			snapCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			deviceSerial := session.DeviceSerial
			if deviceSerial == "" {
				return // No device serial available yet
			}

			// Get current parameters
			params, err := h.parameterRepo.GetAllParameters(snapCtx, deviceSerial)
			if err != nil {
				h.log.WithError(err).WithField("serial", deviceSerial).Warn("CWMP: Failed to get parameters for pre-reset snapshot")
				return
			}

			// Save as pre_reset_params snapshot
			if err := h.parameterRepo.SaveSnapshot(snapCtx, deviceSerial, "pre_reset_params", params); err != nil {
				h.log.WithError(err).WithField("serial", deviceSerial).Warn("CWMP: Failed to save pre-reset snapshot")
			} else {
				h.log.WithField("serial", deviceSerial).WithField("param_count", len(params)).Debug("CWMP: Pre-reset snapshot saved")
			}
		}()

		return BuildReboot(t.ID, t.ID)

	case task.TypeFactoryReset:
		return BuildFactoryReset(t.ID)

	case task.TypeFirmware:
		var p task.FirmwarePayload
		if err := json.Unmarshal(t.Payload, &p); err != nil {
			return nil, err
		}
		ft := p.FileType
		if ft == "" {
			ft = "1 Firmware Upgrade Image"
		}
		return BuildDownload(t.ID, &Download{
			CommandKey: t.ID,
			FileType:   ft,
			URL:        p.URL,
			Username:   p.Username,
			Password:   p.Password,
		})

	default:
		return nil, fmt.Errorf("unknown task type: %s", t.Type)
	}
}

// dispatchNextOrClose pops the next pending task and dispatches it, or closes
// the session with HTTP 204 if the queue is empty.
func (h *Handler) dispatchNextOrClose(ctx context.Context, w http.ResponseWriter, session *Session) {
	session.mu.Lock()
	var next *task.Task
	if len(session.pendingTasks) > 0 {
		next = session.pendingTasks[0]
		session.pendingTasks = session.pendingTasks[1:]
	}
	session.mu.Unlock()

	if next != nil {
		h.dispatchTask(ctx, w, session, next)
		return
	}
	session.setState(StateDone)
	w.WriteHeader(http.StatusNoContent)
}

// completeTask marks a task done or failed and persists the update to Redis.
func (h *Handler) completeTask(ctx context.Context, t *task.Task, result any, errMsg string) {
	now := time.Now().UTC()
	t.CompletedAt = &now
	if errMsg != "" {
		t.Status = task.StatusFailed
		t.Error = errMsg
	} else {
		t.Status = task.StatusDone
		if result != nil {
			t.Result = result
		}
	}
	if uerr := h.taskQueue.UpdateStatus(ctx, t); uerr != nil {
		h.log.WithError(uerr).WithField("task_id", t.ID).Error("CWMP: Update completed task status")
		return
	}

	h.log.
		WithField("task_id", t.ID).
		WithField("status", string(t.Status)).
		Info("CWMP: task completed")
}

// parseDiagResult dispatches to the correct result parser based on task type.
func parseDiagResult(taskType task.Type, params map[string]string, mapper datamodel.Mapper, origTask *task.Task) any {
	switch taskType {
	case task.TypePingTest, task.TypeDiagnostic:
		return parsePingResult(params, mapper)
	case task.TypeTraceroute:
		return parseTracerouteResult(params, mapper)
	case task.TypeSpeedTest:
		var p task.SpeedTestPayload
		_ = json.Unmarshal(origTask.Payload, &p)
		return parseSpeedTestResult(params, mapper, p)
	}
	return params
}

// persistDiagToDevice stores diagnostic results back into the device document
// where applicable (e.g. connected hosts → device.connected_hosts).
func (h *Handler) persistDiagToDevice(
	ctx context.Context,
	serial string,
	taskType task.Type,
	result any,
	params map[string]string,
	mapper datamodel.Mapper,
) {
	switch taskType {
	case task.TypeConnectedDevices:
		if hosts, ok := result.([]device.ConnectedHost); ok {
			_ = h.deviceSvc.UpdateInfo(ctx, serial, device.InfoUpdate{ConnectedHosts: hosts})
		}
	case task.TypeCPEStats:
		_, wan := parseCPEStats(params, mapper)
		_ = h.deviceSvc.UpdateInfo(ctx, serial, device.InfoUpdate{WANs: []device.WANInfo{wan}})
	}
}

func (h *Handler) extractInformParams(env *Envelope) *device.UpsertRequest {
	inf := env.Body.Inform
	req := &device.UpsertRequest{
		Serial:       inf.DeviceId.SerialNumber,
		OUI:          inf.DeviceId.OUI,
		Manufacturer: inf.DeviceId.Manufacturer,
		ProductClass: inf.DeviceId.ProductClass,
		Parameters:   make(map[string]string, len(inf.ParameterList.ParameterValueStructs)),
	}

	for _, pv := range inf.ParameterList.ParameterValueStructs {
		name := pv.Name
		val := pv.Value.Data
		req.Parameters[name] = val

		lower := strings.ToLower(name)
		switch {
		case strings.HasSuffix(lower, "modelname"):
			req.ModelName = val
		case strings.HasSuffix(lower, "softwareversion"):
			req.SWVersion = val
		case strings.HasSuffix(lower, "hardwareversion"):
			req.HWVersion = val
		case strings.HasSuffix(lower, "bootloaderversion"):
			req.BLVersion = val
		case strings.HasSuffix(lower, "externalipaddress") || strings.HasSuffix(lower, "wanipaddress"):
			req.WANIP = val
		case strings.HasSuffix(lower, "ipaddress") && req.IPAddress == "":
			req.IPAddress = val
		case strings.HasSuffix(lower, "uptime"):
			if v, err := parseInt64(val); err == nil {
				req.UptimeSeconds = v
			}
		case strings.HasSuffix(lower, "memorystatus.total"):
			if v, err := parseInt64(val); err == nil {
				req.RAMTotal = v
			}
		case strings.HasSuffix(lower, "memorystatus.free"):
			if v, err := parseInt64(val); err == nil {
				req.RAMFree = v
			}
		case strings.HasSuffix(lower, "managementserver.url") && req.ACSURL == "":
			req.ACSURL = val
		}
	}

	// Detect data model by scanning all Inform parameters in one pass.
	// InternetGatewayDevice.* (TR-098) takes priority over Device.* (TR-181)
	// because some CDATA/ZTE ONTs send both prefixes in a single Inform.
	hasIGD, hasDev := false, false
	for name := range req.Parameters {
		if strings.HasPrefix(name, "InternetGatewayDevice.") {
			hasIGD = true
		} else if strings.HasPrefix(name, "Device.") {
			hasDev = true
		}
	}
	if hasIGD {
		req.DataModel = "tr098"
	} else if hasDev {
		req.DataModel = "tr181"
	}

	return req
}

// hasDiagnosticsCompleteEvent returns true when the Inform carries the
// "8 DIAGNOSTICS COMPLETE" event code.
func hasDiagnosticsCompleteEvent(inf *InformRequest) bool {
	if inf == nil {
		return false
	}
	for _, ev := range inf.Event.Events {
		if strings.Contains(ev.EventCode, "DIAGNOSTICS COMPLETE") ||
			ev.EventCode == "8 DIAGNOSTICS COMPLETE" {
			return true
		}
	}
	return false
}

func (h *Handler) getSessionID(r *http.Request) string {
	if c, err := r.Cookie("cwmp-session"); err == nil && c.Value != "" {
		return c.Value
	}
	if body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)); err == nil && len(body) > 0 {
		r.Body = io.NopCloser(bytes.NewReader(body))
		var env Envelope
		if xmlErr := xml.NewDecoder(bytes.NewReader(body)).Decode(&env); xmlErr == nil {
			if id := headerID(&env); id != "" {
				return id
			}
		}
	}
	return uuid.NewString()
}

func (h *Handler) writeSoapFault(w http.ResponseWriter, id, faultCode, cwmpCode, cwmpMsg string) {
	body := Body{
		Fault: &SOAPFault{
			FaultCode:   faultCode,
			FaultString: cwmpMsg,
			Detail: FaultDetail{
				CWMPFault: CWMPFault{
					FaultCode:   cwmpCode,
					FaultString: cwmpMsg,
				},
			},
		},
	}
	respXML, err := BuildEnvelope(id, body)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeXML(w, respXML)
}

func writeXML(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func headerID(env *Envelope) string {
	if env != nil && env.Header.ID != nil {
		return env.Header.ID.Value
	}
	return uuid.NewString()
}

// firstRootObject returns the dominant root object namespace present in params.
// InternetGatewayDevice. (TR-098) takes priority when both prefixes coexist,
// because some CDATA/ZTE ONTs report both namespaces in a single session.
func firstRootObject(params map[string]string) string {
	hasIGD, hasDev := false, false
	for k := range params {
		if strings.HasPrefix(k, "InternetGatewayDevice.") {
			hasIGD = true
		} else if strings.HasPrefix(k, "Device.") {
			hasDev = true
		}
	}
	if hasIGD {
		return "InternetGatewayDevice."
	}
	if hasDev {
		return "Device."
	}
	return ""
}

func diagnosticStatePath(diagType string, mapper datamodel.Mapper) string {
	lower := strings.ToLower(diagType)
	if lower == "traceroute" {
		return mapper.TracerouteDiagBasePath() + "DiagnosticsState"
	}
	return mapper.PingDiagBasePath() + "DiagnosticsState"
}

func unmarshalPayload(t *task.Task, dst any) error {
	if err := json.Unmarshal(t.Payload, dst); err != nil {
		return fmt.Errorf("unmarshal payload for task %s: %w", t.ID, err)
	}
	return nil
}

// buildGetParamResult converts a GetParameterValuesResponse into a flat
// map[string]string suitable for storage in Task.Result.
func buildGetParamResult(resp *GetParameterValuesResponse) map[string]string {
	out := make(map[string]string, len(resp.ParameterList.ParameterValueStructs))
	for _, pv := range resp.ParameterList.ParameterValueStructs {
		out[pv.Name] = pv.Value.Data
	}
	return out
}

func parseInt64(s string) (int64, error) {
	var v int64
	_, err := fmt.Sscanf(s, "%d", &v)
	return v, err
}

// detectAndHandleReset checks for factory reset via TR-069 "0 BOOTSTRAP" event
// or ProvisioningCode reset, and queues a parameter restore task synchronously
// so it is picked up by DequeuePending in the same CWMP session.
func (h *Handler) detectAndHandleReset(ctx context.Context, serial string, inf *InformRequest, mapper datamodel.Mapper) {
	isBootstrap := hasBootstrapEvent(inf)
	informProvCode := informParamValue(inf, "Device.DeviceInfo.ProvisioningCode")

	// Log every Inform's reset-relevant signals for diagnostics.
	h.log.WithField("serial", serial).
		WithField("bootstrap_event", isBootstrap).
		WithField("provisioning_code", informProvCode).
		Debug("CWMP: reset detection signals")

	// Case 1: ACS-triggered reboot had a pre_reset_params snapshot saved.
	preResetParams, err := h.parameterRepo.GetSnapshot(ctx, serial, "pre_reset_params")
	if err == nil && preResetParams != nil {
		// Always delete the snapshot so it is only used once.
		_ = h.parameterRepo.DeleteSnapshot(ctx, serial, "pre_reset_params")

		if !isBootstrap {
			// Regular reboot (not factory reset) — parameters unchanged, no restore needed.
			h.log.WithField("serial", serial).Debug("CWMP: Regular ACS reboot, no parameter restore needed")
			return
		}

		// Factory reset triggered after ACS reboot: restore from pre_reset_params.
		h.log.WithField("serial", serial).
			WithField("param_count", len(preResetParams)).
			Info("CWMP: ACS-triggered factory reset detected, queuing parameter restore")
		h.restoreParameters(ctx, serial, preResetParams, "pre_reset_params")
		return
	}

	// Case 2: Detect physical/manual factory reset.
	// Primary: TR-069 "0 BOOTSTRAP" event (§3.7.1.4).
	// Fallback: ProvisioningCode in Inform is empty but snapshot has non-empty value —
	//           TP-Link always includes ProvisioningCode in Inform; factory reset clears it.
	lastKnownGood, err := h.parameterRepo.GetSnapshot(ctx, serial, "last_known_good")
	if err != nil || lastKnownGood == nil {
		if isBootstrap {
			h.log.WithField("serial", serial).
				Warn("CWMP: Bootstrap event detected but no last_known_good snapshot — save a snapshot first")
		}
		return
	}

	snapshotProvCode := lastKnownGood["Device.DeviceInfo.ProvisioningCode"]
	isProvCodeReset := informProvCode == "" && snapshotProvCode != ""

	if !isBootstrap && !isProvCodeReset {
		return
	}

	reason := "Bootstrap event"
	if !isBootstrap {
		reason = "ProvisioningCode cleared"
	}

	h.log.WithField("serial", serial).
		WithField("reason", reason).
		WithField("snapshot_params", len(lastKnownGood)).
		Info("CWMP: Factory reset detected, queuing WiFi restore from last_known_good")
	// Clear stored parameters so the next Inform triggers a fresh summon.
	_ = h.parameterRepo.DeleteDeviceParameters(ctx, serial)
	h.restoreWiFiConfig(ctx, serial, lastKnownGood, mapper)
}

// informParamValue returns the value of a named parameter from the Inform
// ParameterList, or empty string if not present.
func informParamValue(inf *InformRequest, name string) string {
	if inf == nil {
		return ""
	}
	for _, pv := range inf.ParameterList.ParameterValueStructs {
		if pv.Name == name {
			return pv.Value.Data
		}
	}
	return ""
}

// restoreParameters enqueues a SetParams task to restore critical device parameters
// into Redis so DequeuePending (called immediately after) picks it up in the same session.
func (h *Handler) restoreParameters(ctx context.Context, serial string, params map[string]string, snapshotType string) {
	if len(params) == 0 {
		return
	}

	// Filter to critical parameters only (SSID, WiFi password, PPPoE, etc.)
	criticalParams := filterCriticalParameters(params)
	if len(criticalParams) == 0 {
		h.log.WithField("serial", serial).Warn("CWMP: No critical parameters to restore after reset")
		return
	}

	// Build SetParameterValues task
	payload, err := json.Marshal(task.SetParamsPayload{
		Parameters: criticalParams,
	})
	if err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: Failed to marshal restore payload")
		return
	}

	restoreTask := &task.Task{
		ID:      fmt.Sprintf("restore_%s_%d", snapshotType, time.Now().Unix()),
		Serial:  serial,
		Type:    task.TypeSetParams,
		Status:  task.StatusPending,
		Payload: payload,
	}

	// Queue the restore task
	if err := h.taskQueue.Enqueue(ctx, restoreTask); err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: Failed to queue restore task")
		return
	}

	h.log.WithField("serial", serial).
		WithField("task_id", restoreTask.ID).
		WithField("param_count", len(criticalParams)).
		Info("CWMP: Parameter restoration task queued")
}

// hasBootstrapEvent returns true when the Inform carries the "0 BOOTSTRAP" event.
// Per TR-069 §3.7.1.4 CPEs MUST send this event after factory reset or first boot.
func hasBootstrapEvent(inf *InformRequest) bool {
	if inf == nil {
		return false
	}
	for _, ev := range inf.Event.Events {
		if ev.EventCode == "0 BOOTSTRAP" {
			return true
		}
	}
	return false
}

// restoreWiFiConfig creates TypeWifi tasks from the snapshot to restore SSID/password
// after a factory reset, using the exact same mechanism as manual WiFi configuration.
// It rebuilds the mapper from the full snapshot params (not Bootstrap Inform params)
// so the correct device-specific TR-181 instance numbers (e.g. SSID.3 for 5GHz) are used.
func (h *Handler) restoreWiFiConfig(ctx context.Context, serial string, snapshot map[string]string, mapper datamodel.Mapper) {
	// Rebuild mapper from snapshot (full param set) so instance discovery is accurate.
	// Bootstrap Inform carries only a few params and defaults to SSID.1/SSID.2,
	// but the device may use SSID.3 (or higher) for 5GHz.
	snapshotInstanceMap := datamodel.DiscoverInstances(snapshot)
	modelType := datamodel.DetectFromRootObject(firstRootObject(snapshot))
	snapshotMapper := datamodel.ApplyInstanceMap(datamodel.NewMapper(modelType), snapshotInstanceMap)

	h.log.WithField("serial", serial).
		WithField("ssid_idx_24", snapshotInstanceMap.WiFiSSIDIndices).
		Debug("CWMP: restore WiFi mapper rebuilt from snapshot")

	type bandCfg struct {
		band     string
		ssidPath string
		passPath string
		secPath  string
	}
	bands := []bandCfg{
		{"2.4", snapshotMapper.WiFiSSIDPath(0), snapshotMapper.WiFiPasswordPath(0), snapshotMapper.WiFiSecurityModePath(0)},
		{"5", snapshotMapper.WiFiSSIDPath(1), snapshotMapper.WiFiPasswordPath(1), snapshotMapper.WiFiSecurityModePath(1)},
	}

	// Read band steering state from snapshot. Include it in the first (2.4GHz) task
	// so it is restored before individual SSID tasks run. This prevents the device
	// from syncing SSIDs across bands when band steering is enabled by default after reset.
	bandSteering := extractBandSteeringStatus(snapshot, snapshotMapper)

	queued := 0
	for i, b := range bands {
		ssid := snapshot[b.ssidPath]
		if ssid == "" {
			h.log.WithField("serial", serial).
				WithField("band", b.band).
				WithField("path", b.ssidPath).
				Warn("CWMP: SSID not found in snapshot for band, skipping")
			continue
		}
		password := snapshot[b.passPath]
		security := deviceSecToPayloadSec(snapshot[b.secPath])

		wifiPayload := task.WiFiPayload{
			Band:     b.band,
			SSID:     ssid,
			Password: password,
			Security: security,
		}
		// Attach band steering to the first task only so it is set once.
		if i == 0 {
			wifiPayload.BandSteeringEnabled = bandSteering
		}
		payload, err := json.Marshal(wifiPayload)
		if err != nil {
			h.log.WithError(err).WithField("serial", serial).Error("CWMP: Failed to marshal WiFi restore payload")
			continue
		}
		t := &task.Task{
			ID:      fmt.Sprintf("restore_wifi_%s_%d", strings.ReplaceAll(b.band, ".", "_"), time.Now().UnixNano()),
			Serial:  serial,
			Type:    task.TypeWifi,
			Status:  task.StatusPending,
			Payload: payload,
		}
		if err := h.taskQueue.Enqueue(ctx, t); err != nil {
			h.log.WithError(err).WithField("serial", serial).Error("CWMP: Failed to queue WiFi restore task")
			continue
		}
		h.log.WithField("serial", serial).
			WithField("band", b.band).
			WithField("ssid", ssid).
			WithField("task_id", t.ID).
			Info("CWMP: WiFi restore task queued")
		queued++
	}

	// Also restore ProvisioningCode as a single-param task to stop re-detection loop.
	if provCode := snapshot["Device.DeviceInfo.ProvisioningCode"]; provCode != "" {
		payload, err := json.Marshal(task.SetParamsPayload{
			Parameters: map[string]string{"Device.DeviceInfo.ProvisioningCode": provCode},
		})
		if err == nil {
			provTask := &task.Task{
				ID:      fmt.Sprintf("restore_provcode_%d", time.Now().UnixNano()),
				Serial:  serial,
				Type:    task.TypeSetParams,
				Status:  task.StatusPending,
				Payload: payload,
			}
			if err := h.taskQueue.Enqueue(ctx, provTask); err == nil {
				queued++
			}
		}
	}

	h.log.WithField("serial", serial).
		WithField("tasks_queued", queued).
		Info("CWMP: WiFi restore tasks queued after factory reset")
}

// deviceSecToPayloadSec converts a device-native TR-181 security mode string
// (e.g. "WPA2-Personal") to the WiFiPayload security format (e.g. "WPA2-PSK").
func deviceSecToPayloadSec(deviceSec string) string {
	switch deviceSec {
	case "WPA2-Personal":
		return "WPA2-PSK"
	case "WPA-WPA2-Personal":
		return "WPA-WPA2-PSK"
	case "WPA-Personal":
		return "WPA-PSK"
	default:
		return deviceSec
	}
}

// filterCriticalParameters extracts user-configured parameters that must be
// restored after a factory reset: PPPoE credentials, WiFi SSID/password/security.
// Patterns are intentionally narrow to avoid pushing read-only or volatile params.
func filterCriticalParameters(params map[string]string) map[string]string {
	critical := make(map[string]string)

	criticalPatterns := []string{
		".SSID",        // Device.WiFi.SSID.X.SSID
		"PreSharedKey", // WiFi WPA pre-shared key
		"ModeEnabled",  // WiFi security mode (e.g. WPA2-Personal)
		"WPAAuthenticationMode",
		"WPAEncryptionModes",
		"BeaconType",
		".Username",        // Device.PPP.Interface.X.Username
		".Password",        // Device.PPP.Interface.X.Password
		"VLANID",           // Ethernet.VLANTermination.X.VLANID
		"ProvisioningCode", // Device.DeviceInfo.ProvisioningCode — restored to stop re-detection loop
	}

	for key, value := range params {
		for _, pattern := range criticalPatterns {
			if strings.Contains(key, pattern) {
				// Exclude read-only, volatile, and system-managed parameters.
				// NOTE: TR-069 SetParameterValues is atomic — one non-writable param
				// rejects the entire request (9003/9008), so exclusions must be tight.
				if !strings.Contains(key, ".Stats.") &&
					!strings.Contains(key, ".Status") &&
					!strings.Contains(key, ".AutoDetect") &&
					!strings.Contains(key, ".ConfigFile") &&
					!strings.Contains(key, "ManagementServer") &&
					!strings.HasSuffix(key, ".BSSID") &&
					!strings.HasSuffix(key, ".Name") &&
					!strings.HasSuffix(key, ".LastChange") &&
					!strings.HasSuffix(key, "NumberOfEntries") &&
					!strings.HasSuffix(key, "PasswordReset") &&
					!strings.Contains(key, ".MultiAP.") &&
					!strings.Contains(key, ".DataElements.") &&
					!strings.Contains(key, "Users.User.") {
					// For WiFi paths, only restore primary instances (1 and 2).
					// Pushing all 16 SSID/AccessPoint instances at once can hang devices.
					if strings.Contains(key, "WiFi.") {
						if !strings.Contains(key, "WiFi.SSID.1.") &&
							!strings.Contains(key, "WiFi.SSID.2.") &&
							!strings.Contains(key, "WiFi.AccessPoint.1.") &&
							!strings.Contains(key, "WiFi.AccessPoint.2.") {
							break
						}
					}
					critical[key] = value
					break
				}
			}
		}
	}

	return critical
}
