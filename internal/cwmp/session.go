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

// wifiSSIDBandHintsFromYAML converts driver WiFi SSID→band YAML into runtime hints.
func wifiSSIDBandHintsFromYAML(y *schema.WiFiSSIDBandWithoutLowerLayersYAML) *datamodel.WiFiSSIDBandWithoutLowerLayersHints {
	if y == nil {
		return nil
	}
	strategy := strings.TrimSpace(y.Strategy)
	h := &datamodel.WiFiSSIDBandWithoutLowerLayersHints{Strategy: strategy}
	if strategy == "explicit" && len(y.Explicit) > 0 {
		h.ExplicitSSIDBand = make(map[int]int)
		for bandStr, ssids := range y.Explicit {
			band, err := strconv.Atoi(strings.TrimSpace(bandStr))
			if err != nil {
				continue
			}
			for _, ssidIdx := range ssids {
				if ssidIdx < 1 {
					continue
				}
				h.ExplicitSSIDBand[ssidIdx] = band
			}
		}
	}
	return h
}

// discoveryHintsFromDriver builds datamodel.DiscoveryHints from a loaded driver.
func discoveryHintsFromDriver(drv *schema.DeviceDriver) *datamodel.DiscoveryHints {
	if drv == nil {
		return nil
	}

	// Derive the TR-098 VLAN path suffix from wan_vlan_path by taking the
	// last ".ParameterName" segment. This lets the engine match any instance
	// of the parameter without knowing the concrete instance numbers up front.
	// e.g. "...WANPPPConnection.1.X_CT-COM_VLANIDMark" → ".X_CT-COM_VLANIDMark"
	vlanSuffix := ""
	if p := drv.Discovery.WANVLANPath; p != "" {
		if idx := strings.LastIndex(p, "."); idx >= 0 {
			vlanSuffix = p[idx:] // includes the leading dot
		}
	}

	return &datamodel.DiscoveryHints{
		WANTypePath:                    drv.Discovery.WANTypePath,
		WANTypeValuesWAN:               drv.Discovery.WANTypeValues.WAN,
		WANTypeValuesLAN:               drv.Discovery.WANTypeValues.LAN,
		WANServiceTypePath:             drv.Discovery.WANServiceTypePath,
		GPONEnablePath:                 drv.Discovery.GPONEnablePath,
		WANTR098VLANPathSuffix:         vlanSuffix,
		WiFiSSIDBandWithoutLowerLayers: wifiSSIDBandHintsFromYAML(drv.Discovery.WiFiSSIDBandWithoutLowerLayers),
	}
}

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

	// Parameter summon state.
	//
	// Full summon  (triggered periodically):
	//   phase 1 → send GetParameterNames
	//   phase 2 → receive/accumulate batched GetParameterValues, save to DB, rebuild mapper
	//
	// Targeted summon (triggered before WiFi/WAN task to refresh only relevant subtree):
	//   phase 3 → send single GetParameterValues([paths…])
	//   phase 4 → receive response → rebuild mapper, do NOT write to DB
	summonPhase       int
	summonSchemaName  string
	summonMode        string            // "ui_refresh" or "task_discovery"
	summonAllNames    []string          // full summon: leaf names from GetParameterNames
	summonBatchIdx    int               // full summon: current batch index
	summonAllParams   map[string]string // full summon: accumulated params
	summonTargetPaths []string          // targeted summon: object paths to fetch

	// lastSetParams holds the parameters sent in the most recent SetParameterValues
	// dispatch so that handleSetParamValuesResponse can sync them to PostgreSQL.
	lastSetParams map[string]string

	// isBootstrap is true when the Inform that started this session carried
	// the "0 BOOTSTRAP" event (factory reset or first-ever boot).
	isBootstrap bool

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
	sessions       sync.Map
	serialSessions sync.Map // serial -> *Session
	rpcIDSessions  sync.Map // cwmp:ID (ACS request id) -> *Session
	mu             sync.RWMutex
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

// BindSerial indexes a session by device serial for reconnect continuity.
func (sm *SessionManager) BindSerial(serial string, s *Session) {
	if serial == "" || s == nil {
		return
	}
	sm.serialSessions.Store(serial, s)
}

// GetBySerial returns the last seen session for a device serial.
func (sm *SessionManager) GetBySerial(serial string) *Session {
	if serial == "" {
		return nil
	}
	if v, ok := sm.serialSessions.Load(serial); ok {
		if s, ok := v.(*Session); ok {
			return s
		}
	}
	return nil
}

// BindRPCID indexes an outbound ACS RPC ID to its owning session so that
// subsequent CPE responses can be matched even when CPE rotates HTTP cookies.
func (sm *SessionManager) BindRPCID(id string, s *Session) {
	if id == "" || s == nil {
		return
	}
	sm.rpcIDSessions.Store(id, s)
}

// TakeByRPCID returns and removes a session binding for a cwmp:ID.
func (sm *SessionManager) TakeByRPCID(id string) *Session {
	if id == "" {
		return nil
	}
	v, ok := sm.rpcIDSessions.LoadAndDelete(id)
	if !ok {
		return nil
	}
	s, _ := v.(*Session)
	return s
}

// Cleanup removes sessions older than 30 minutes.
func (sm *SessionManager) Cleanup() {
	cutoff := time.Now().UTC().Add(-30 * time.Minute)
	sm.sessions.Range(func(key, value any) bool {
		s := value.(*Session)
		if s.CreatedAt.Before(cutoff) {
			sm.sessions.Delete(key)
			sm.serialSessions.Range(func(serialKey, sessionVal any) bool {
				if sessionVal == s {
					sm.serialSessions.Delete(serialKey)
				}
				return true
			})
			sm.rpcIDSessions.Range(func(idKey, sessionVal any) bool {
				if sessionVal == s {
					sm.rpcIDSessions.Delete(idKey)
				}
				return true
			})
		}
		return true
	})
}

// adoptInFlightBySerial moves WAN/task in-flight state from a prior session of
// the same device serial into the current session when the CPE rotates session IDs.
func (h *Handler) adoptInFlightBySerial(serial string, current *Session) bool {
	prev := h.sessionMgr.GetBySerial(serial)
	if prev == nil || prev == current {
		return false
	}

	prev.mu.Lock()
	prevTask := prev.currentTask
	prevWAN := prev.wanProvision
	prevFollow := prev.addObjFollowUp
	prevSetParams := make(map[string]string, len(prev.lastSetParams))
	for k, v := range prev.lastSetParams {
		prevSetParams[k] = v
	}
	prevCreds := make(map[string]string, len(prev.pendingWANCredentials))
	for k, v := range prev.pendingWANCredentials {
		prevCreds[k] = v
	}
	prev.mu.Unlock()

	if prevTask == nil && prevWAN == nil && prevFollow == nil {
		return false
	}

	current.mu.Lock()
	if current.currentTask == nil {
		current.currentTask = prevTask
		current.wanProvision = prevWAN
		current.addObjFollowUp = prevFollow
		if len(prevSetParams) > 0 {
			current.lastSetParams = prevSetParams
		}
		if len(prevCreds) > 0 {
			current.pendingWANCredentials = prevCreds
		}
	}
	current.mu.Unlock()

	// Clear moved state from previous session to avoid double-processing.
	prev.mu.Lock()
	prev.currentTask = nil
	prev.wanProvision = nil
	prev.addObjFollowUp = nil
	prev.lastSetParams = nil
	prev.pendingWANCredentials = nil
	prev.mu.Unlock()

	return true
}

// Handler implements http.Handler and manages CWMP sessions and message handling.
type Handler struct {
	deviceSvc         device.Service
	taskQueue         task.Queue
	parameterRepo     parameter.Repository
	sessionMgr        *SessionManager
	log               logger.Logger
	acsUsername       string
	acsPassword       string
	acsURL            string
	informInterval    time.Duration
	schemaRegistry    *schema.Registry
	schemaResolver    *schema.Resolver
	driverRegistry    *schema.DeviceDriverRegistry
	lastSummonTime    sync.Map // serial → time.Time
	pendingFullSummon sync.Map // serial → bool: trigger full GPN on next Inform
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
		if summonPhase == 3 {
			h.sendTargetedSummon(ctx, w, session)
			return
		}

		session.mu.Lock()
		wp := session.wanProvision
		t := session.currentTask
		var next *task.Task

		if wp != nil {
			// We have an adopted mid-flight WAN provision. Do not pop a new task.
		} else if t != nil {
			// We have an adopted simple task. Re-dispatch it.
			next = t
		} else if len(session.pendingTasks) > 0 {
			next = session.pendingTasks[0]
			session.pendingTasks = session.pendingTasks[1:]
		}
		session.mu.Unlock()

		// If a WAN provision is mid-flight, resume by resending the current step's XML.
		// (This handles the case where the CPE dropped the connection before sending the response).
		if wp != nil {
			h.log.WithField("task_id", wp.t.ID).
				WithField("step", wp.stepIndex()).
				Info("CWMP: resuming in-flight WAN provision")
			xmlBytes, err := wp.buildCurrentXML()
			if err != nil {
				h.log.WithError(err).Error("CWMP: resume WAN provision failed")
				session.mu.Lock()
				session.wanProvision = nil
				session.mu.Unlock()
				h.handleTaskResponse(ctx, w, session, nil, err.Error())
				return
			}
			h.writeSessionXML(w, session, xmlBytes)
			return
		}

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

	// CPEs that rotate cookies/session IDs between requests still echo the
	// cwmp:ID of the ACS RPC they are replying to. Prefer this binding for
	// non-Inform messages to keep multi-step provisioning state consistent.
	if env.Body.Inform == nil {
		if reqID := requestID(env); reqID != "" {
			if mapped := h.sessionMgr.TakeByRPCID(reqID); mapped != nil {
				session = mapped
			}
		}
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
		h.writeSessionXML(w, session, respXML)

	case env.Body.TransferComplete != nil:
		respXML, ferr := h.handleTransferComplete(ctx, env)
		if ferr != nil {
			h.log.WithError(ferr).Error("CWMP: handleTransferComplete")
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		h.writeSessionXML(w, session, respXML)

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
	if h.adoptInFlightBySerial(upsertReq.Serial, session) {
		h.log.
			WithField("session", session.ID).
			WithField("serial", upsertReq.Serial).
			Info("CWMP: adopted in-flight session state by serial")
	}
	h.sessionMgr.BindSerial(upsertReq.Serial, session)

	// Track bootstrap event for global default param injection.
	session.mu.Lock()
	session.isBootstrap = hasBootstrapEvent(env.Body.Inform)
	session.mu.Unlock()

	// Resolve the schema name before upsert so it is persisted to MongoDB.
	upsertReq.Schema = h.schemaResolver.Resolve(upsertReq.Manufacturer, upsertReq.ProductClass, upsertReq.DataModel)

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

	// If model_name, cpu_usage, or WAN service_type are missing, try to backfill
	// from the parameter store (Redis-cached). Covers devices like Huawei whose
	// targeted GPV (partial-path) fails so finishSummon never runs.
	wansNeedService := false
	for _, w := range dev.WANs {
		if w.ServiceType == "" {
			wansNeedService = true
			break
		}
	}
	needBackfill := dev.ModelName == "" || dev.CPUUsage == 0 || wansNeedService
	if needBackfill {
		go func() {
			bfCtx, bfCancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer bfCancel()
			storedParams, bfErr := h.parameterRepo.GetAllParameters(bfCtx, upsertReq.Serial)
			if bfErr != nil || len(storedParams) == 0 {
				return
			}
			upd := device.InfoUpdate{}
			if dev.ModelName == "" {
				mn := storedParams["InternetGatewayDevice.DeviceInfo.ModelName"]
				if mn == "" {
					mn = storedParams["Device.DeviceInfo.ModelName"]
				}
				if mn != "" {
					upd.ModelName = &mn
				}
			}
			if dev.CPUUsage == 0 {
				if cpu := extractCPUUsage(storedParams, nil); cpu != nil {
					upd.CPUUsage = cpu
				}
			}
			if wansNeedService {
				hasServiceList := false
				for k := range storedParams {
					if strings.Contains(k, "SERVICELIST") || strings.Contains(strings.ToLower(k), "servicelist") {
						hasServiceList = true
						break
					}
				}
				bfMapper := datamodel.Mapper(schema.NewSchemaMapper(h.schemaRegistry, upsertReq.Schema, datamodel.InstanceMap{}))
				wans := extractWANInfos(storedParams, bfMapper, nil)
				h.log.WithField("serial", upsertReq.Serial).
					WithField("has_service_list", hasServiceList).
					WithField("wans_extracted", len(wans)).
					WithField("wans_with_svc", func() int {
						c := 0
						for _, w := range wans {
							if w.ServiceType != "" {
								c++
							}
						}
						return c
					}()).
					Info("CWMP: backfill WAN service_type")
				if len(wans) > 0 {
					upd.WANs = wans
				}
			}
			if upd.ModelName != nil || upd.CPUUsage != nil || upd.WANs != nil {
				_ = h.deviceSvc.UpdateInfo(bfCtx, upsertReq.Serial, upd)
			}
		}()
	}

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
	discoveryHints := discoveryHintsFromDriver(driver)
	instanceMap := datamodel.DiscoverInstancesWithHints(upsertReq.Parameters, discoveryHints)

	schemaName := upsertReq.Schema

	h.log.
		WithField("serial", upsertReq.Serial).
		WithField("schema", schemaName).
		Debug("CWMP: resolved device schema")

	mapper := datamodel.Mapper(schema.NewSchemaMapper(h.schemaRegistry, schemaName, instanceMap))

	// Detect factory reset synchronously BEFORE DequeuePending so the restore task
	// is included in the current session's pending queue.
	h.detectAndHandleReset(ctx, upsertReq.Serial, env.Body.Inform, mapper)

	// Fetch real pending tasks first so we can factor them into the summon decision.
	pendingTasks, err := h.taskQueue.DequeuePending(ctx, upsertReq.Serial)
	if err != nil {
		h.log.WithError(err).WithField("serial", upsertReq.Serial).Error("CWMP: Dequeue pending tasks failed")
	}

	// If a full summon was explicitly requested via the API, perform it now and
	// skip the targeted summon entirely.
	if _, pending := h.pendingFullSummon.LoadAndDelete(upsertReq.Serial); pending {
		session.mu.Lock()
		session.summonPhase = 1
		session.summonSchemaName = schemaName
		session.mu.Unlock()
		h.log.WithField("serial", upsertReq.Serial).Info("CWMP: executing pending full summon (API-triggered)")
		h.dispatchNextOrClose(ctx, w, session)
		return
	}

	// Trigger a targeted UI refresh summon (instead of full summon) so we only
	// fetch subtrees used by the dashboard/detail views. This is throttled to at
	// most once every 2 minutes per device to avoid overwhelming devices.
	shouldSummon := true
	if lastSummon, ok := h.lastSummonTime.Load(upsertReq.Serial); ok {
		if time.Since(lastSummon.(time.Time)) < 2*time.Minute {
			shouldSummon = false
		}
	}
	if shouldSummon {
		paths := uiSummonPaths(schemaName, driver)
		if len(paths) > 0 {
			session.mu.Lock()
			session.summonPhase = 3
			session.summonMode = "ui_refresh"
			session.summonSchemaName = schemaName
			session.summonTargetPaths = paths
			session.mu.Unlock()
			h.lastSummonTime.Store(upsertReq.Serial, time.Now())
			h.log.WithField("serial", upsertReq.Serial).
				WithField("paths", paths).
				Info("CWMP: scheduling targeted UI summon")
		}
	} else {
		// Within throttle window: if a WiFi or WAN task is pending, do a targeted
		// summon that fetches only the relevant subtree (one GPV round-trip) so
		// that instanceMap is accurate before the task is dispatched.
		targetedPaths, targetedType := targetedSummonPaths(schemaName, pendingTasks)
		if len(targetedPaths) > 0 {
			session.mu.Lock()
			session.summonPhase = 3
			session.summonMode = "task_discovery"
			session.summonSchemaName = schemaName
			session.summonTargetPaths = targetedPaths
			session.mu.Unlock()
			h.log.WithField("serial", upsertReq.Serial).
				WithField("task_type", targetedType).
				WithField("paths", targetedPaths).
				Info("CWMP: scheduling targeted summon for task requiring instance discovery")
		}
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

	// Inject global default params on bootstrap or first-ever provision using
	// the Inform parameters as the current-value baseline. ManagementServer.*
	// paths are always present in Inform messages, making this reliable for all
	// device types (TR-181 and TR-098) regardless of whether a summon fires.
	h.injectGlobalDefaultParams(ctx, session, upsertReq.Serial, upsertReq.Parameters)

	respXML, err := BuildInformResponse(id)
	if err != nil {
		h.log.WithError(err).Error("CWMP: Build InformResponse failed")
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.writeSessionXML(w, session, respXML)
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
			h.writeSessionXML(w, session, nextXML)
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

	// Fall back to mapper model type if schema didn't determine path
	if rootPath == "Device." {
		if sm, ok := mapper.(*schema.SchemaMapper); ok && sm.ModelType() == datamodel.TR098 {
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
	h.writeSessionXML(w, session, env)
}

// sendTargetedSummon sends a single GetParameterValues for specific object
// path prefixes (e.g. "Device.WiFi." or "Device.IP.Interface."), fetching only
// the parameters needed for instance discovery before a WiFi or WAN task.
// This completes in one round-trip instead of the full 10-batch summon.
func (h *Handler) sendTargetedSummon(ctx context.Context, w http.ResponseWriter, session *Session) {
	session.mu.Lock()
	paths := session.summonTargetPaths
	mode := session.summonMode
	serial := session.DeviceSerial
	session.summonPhase = 4 // waiting targeted GetParameterValuesResponse
	session.mu.Unlock()

	h.log.WithField("serial", serial).
		WithField("mode", mode).
		WithField("paths", paths).
		Info("CWMP: sending targeted GetParameterValues for instance discovery")

	id := uuid.NewString()
	env, err := BuildGetParameterValues(id, paths)
	if err != nil {
		h.log.WithError(err).WithField("serial", serial).Error("CWMP: build targeted GetParameterValues failed")
		session.mu.Lock()
		session.summonPhase = 0
		session.mu.Unlock()
		h.dispatchNextOrClose(ctx, w, session)
		return
	}
	h.writeSessionXML(w, session, env)
}

// handleTargetedSummonResponse processes the GetParameterValuesResponse for a
// targeted summon (phase 4). It rebuilds the session mapper from the returned
// params WITHOUT touching MongoDB/PostgreSQL, then dispatches the pending task.
func (h *Handler) handleTargetedSummonResponse(ctx context.Context, w http.ResponseWriter, session *Session, params map[string]string) {
	session.mu.Lock()
	serial := session.DeviceSerial
	drv := session.driver
	mode := session.summonMode
	schemaName := session.summonSchemaName
	session.summonPhase = 0
	session.summonMode = ""
	session.summonTargetPaths = nil
	session.mu.Unlock()

	discoveryHints := discoveryHintsFromDriver(drv)
	instanceMap := datamodel.DiscoverInstancesWithHints(params, discoveryHints)

	h.log.WithField("serial", serial).
		WithField("wifi_ssid_indices", instanceMap.WiFiSSIDIndices).
		WithField("wifi_ap_indices", instanceMap.WiFiAPIndices).
		WithField("ppp_iface", instanceMap.PPPIfaceIdx).
		WithField("wan_iface", instanceMap.WANIPIfaceIdx).
		WithField("vlan_term", instanceMap.WANVLANTermIdx).
		WithField("wan_dev", instanceMap.WANDeviceIdx).
		WithField("wan_conn_dev", instanceMap.WANConnDevIdx).
		WithField("wan_ppp_conn", instanceMap.WANPPPConnIdx).
		WithField("wan_current_vlan", instanceMap.WANCurrentVLAN).
		Info("CWMP: targeted summon DiscoverInstances result")

	newMapper := datamodel.Mapper(schema.NewSchemaMapper(h.schemaRegistry, schemaName, instanceMap))

	session.mu.Lock()
	session.mapper = newMapper
	session.instanceMap = instanceMap
	session.mu.Unlock()

	if mode == "ui_refresh" {
		if err := h.deviceSvc.UpdateParameters(ctx, serial, params); err != nil {
			h.log.WithError(err).WithField("serial", serial).Warn("CWMP: save targeted UI params to MongoDB failed")
		}
		{
			pgCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := h.parameterRepo.UpdateParameters(pgCtx, serial, params); err != nil {
				h.log.WithError(err).WithField("serial", serial).Warn("CWMP: save targeted UI params to PostgreSQL failed")
			}
		}

		// Keep dashboard/device detail sections fresh from the targeted refresh set.
		wifi24 := extractWiFiInfo(0, params, newMapper)
		wifi5 := extractWiFiInfo(1, params, newMapper)
		bandSteeringStatus := extractBandSteeringStatus(params, newMapper)
		wifi24.BandSteeringEnabled = bandSteeringStatus
		wifi5.BandSteeringEnabled = bandSteeringStatus
		lanInfo := extractLANInfo(params, newMapper)
		wansInfo := extractWANInfos(params, newMapper, drv)
		hosts := parseConnectedHosts(params, newMapper, drv)
		stats, _ := parseCPEStats(params, newMapper)

		sort.SliceStable(wansInfo, func(i, j int) bool {
			iPPPoE := wansInfo[i].ConnectionType == "PPPoE"
			jPPPoE := wansInfo[j].ConnectionType == "PPPoE"
			if iPPPoE != jPPPoE {
				return iPPPoE
			}
			return false
		})

		var bestWanIP string
		for _, w := range wansInfo {
			if w.IPAddress != "" {
				bestWanIP = w.IPAddress
				break
			}
		}

		acsURL := params["Device.ManagementServer.URL"]
		if acsURL == "" {
			acsURL = params["InternetGatewayDevice.ManagementServer.URL"]
		}
		cpuUsage := extractCPUUsage(params, drv)
		patchRAMFromPct(drv, params, stats)
		devInfoUI := newMapper.ExtractDeviceInfo(params)

		infoUpdate := device.InfoUpdate{
			WANs:          wansInfo,
			UptimeSeconds: &stats.UptimeSeconds,
			RAMTotal:      &stats.RAMTotalKB,
			RAMFree:       &stats.RAMFreeKB,
			CPUUsage:      cpuUsage,
			ACSURL:        &acsURL,
			ModelName:     &devInfoUI.ModelName,
			SWVersion:     &devInfoUI.SWVersion,
			HWVersion:     &devInfoUI.HWVersion,
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
		infoUpdate.ConnectedHosts = hosts
		if err := h.deviceSvc.UpdateInfo(ctx, serial, infoUpdate); err != nil {
			h.log.WithError(err).WithField("serial", serial).Warn("CWMP: persist UI info from targeted summon failed")
		}

		h.injectDefaultParamTask(session, serial, drv, params)

		// Inject global default params on bootstrap or first-ever provision.
		h.injectGlobalDefaultParams(ctx, session, serial, params)
	}

	h.dispatchNextOrClose(ctx, w, session)
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
	h.writeSessionXML(w, session, env)
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
	discoveryHints := discoveryHintsFromDriver(drv)
	instanceMap := datamodel.DiscoverInstancesWithHints(params, discoveryHints)
	h.log.WithField("serial", serial).
		WithField("wan_iface", instanceMap.WANIPIfaceIdx).
		WithField("lan_iface", instanceMap.LANIPIfaceIdx).
		WithField("ppp_iface", instanceMap.PPPIfaceIdx).
		WithField("free_gpon", instanceMap.FreeGPONLinkIdx).
		Info("CWMP: summon DiscoverInstances result")
	newMapper := datamodel.Mapper(schema.NewSchemaMapper(h.schemaRegistry, schemaName, instanceMap))
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
	cpuUsage := extractCPUUsage(params, drv)
	patchRAMFromPct(drv, params, stats)

	// Extract model/version info from full param set (fills fields missing from Inform).
	devInfo := newMapper.ExtractDeviceInfo(params)

	// Persist all collected info to device
	infoUpdate := device.InfoUpdate{
		WANs:          wansInfo,
		UptimeSeconds: &stats.UptimeSeconds,
		RAMTotal:      &stats.RAMTotalKB,
		RAMFree:       &stats.RAMFreeKB,
		CPUUsage:      cpuUsage,
		ACSURL:        &acsUrl,
		ModelName:     &devInfo.ModelName,
		SWVersion:     &devInfo.SWVersion,
		HWVersion:     &devInfo.HWVersion,
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

	// Enforce driver default_params: push any param whose current value differs
	// from the required default. Runs once per full-summon cycle (≈ every 2 min).
	h.injectDefaultParamTask(session, serial, drv, params)

	// Inject global default params on bootstrap or first-ever provision.
	h.injectGlobalDefaultParams(ctx, session, serial, params)

	h.dispatchNextOrClose(ctx, w, session)
}

// virtualParamSpec is the JSON shape stored as the value of a _vp.* global default entry.
type virtualParamSpec struct {
	Value      string   `json:"value"`
	Candidates []string `json:"candidates"`
}

// expandVirtualParams resolves any _vp.* virtual-parameter entries in raw into
// concrete path→value pairs. For each virtual param, only candidate paths that
// are present in currentParams (i.e. the device actually has that parameter) are
// included. Regular (non-_vp) entries are passed through unchanged.
func expandVirtualParams(raw map[string]string, currentParams map[string]string) map[string]string {
	out := make(map[string]string, len(raw))
	for key, val := range raw {
		if !strings.HasPrefix(key, "_vp.") {
			out[key] = val
			continue
		}
		var spec virtualParamSpec
		if err := json.Unmarshal([]byte(val), &spec); err != nil || spec.Value == "" || len(spec.Candidates) == 0 {
			continue // malformed virtual param — skip silently
		}
		for _, candidate := range spec.Candidates {
			if _, exists := currentParams[candidate]; exists {
				out[candidate] = spec.Value
			}
		}
	}
	return out
}

// injectGlobalDefaultParams pushes global default parameters (stored under the
// "__system__" pseudo-serial in PostgreSQL) to the CPE when:
//   - The current Inform carries "0 BOOTSTRAP" (factory reset / first boot), OR
//   - The device has never been provisioned (_helix.provisioned flag not set).
//
// After applying (or confirming all values already match), the device is marked
// provisioned so subsequent Informs are not re-provisioned.
func (h *Handler) injectGlobalDefaultParams(ctx context.Context, session *Session, serial string, currentParams map[string]string) {
	if h.parameterRepo == nil || serial == "" {
		return
	}

	// Load global defaults; bail early when none are configured.
	globalDefaults, err := h.parameterRepo.GetAllParameters(ctx, "__system__")
	if err != nil || len(globalDefaults) == 0 {
		return
	}

	// Determine whether we should apply.
	session.mu.Lock()
	isBootstrap := session.isBootstrap
	session.mu.Unlock()

	if !isBootstrap {
		provisioned, _ := h.parameterRepo.GetParameter(ctx, serial, "_helix.provisioned")
		if provisioned == "true" {
			return
		}
	}

	// Mark device as provisioned so later Informs skip this check.
	markCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = h.parameterRepo.UpdateParameters(markCtx, serial, map[string]string{"_helix.provisioned": "true"})

	// Detect device data model from current params to filter mismatched paths.
	// TR-098 devices use InternetGatewayDevice.* root; TR-181 use Device.*.
	isIGD := false
	for k := range currentParams {
		if strings.HasPrefix(k, "InternetGatewayDevice.") {
			isIGD = true
			break
		}
	}

	// Expand virtual params (_vp.*) into concrete paths that exist on this device.
	expanded := expandVirtualParams(globalDefaults, currentParams)

	// Build diff: only push params that differ from the device's current value,
	// and skip paths whose root doesn't match the device's data model.
	toSet := make(map[string]string, len(expanded))
	for path, want := range expanded {
		if strings.HasPrefix(path, "_") {
			continue // internal keys (_helix.*, _vp.* residuals) — never push to CPE
		}
		if isIGD && strings.HasPrefix(path, "Device.") {
			continue
		}
		if !isIGD && strings.HasPrefix(path, "InternetGatewayDevice.") {
			continue
		}
		if strings.TrimSpace(currentParams[path]) != strings.TrimSpace(want) {
			toSet[path] = want
		}
	}
	if len(toSet) == 0 {
		h.log.WithField("serial", serial).Debug("CWMP: global default params already match CPE; skipping")
		return
	}

	payload, err := json.Marshal(task.SetParamsPayload{Parameters: toSet})
	if err != nil {
		h.log.WithError(err).WithField("serial", serial).Warn("CWMP: marshal global default params failed")
		return
	}
	synth := &task.Task{
		ID:      "global_defaults_" + serial,
		Serial:  serial,
		Type:    task.TypeSetParams,
		Payload: json.RawMessage(payload),
		Status:  task.StatusPending,
	}
	h.log.WithField("serial", serial).
		WithField("params", toSet).
		Info("CWMP: injecting global default params via SetParameterValues")
	session.mu.Lock()
	session.pendingTasks = append([]*task.Task{synth}, session.pendingTasks...)
	session.mu.Unlock()
}

// injectDefaultParamTask prepends a synthetic SetParameterValues task for any
// default_params entry that does not match the device's current (summoned) value.
// Runs only after a full parameter summon (not targeted summon), so currentParams
// contains the complete leaf set from the CPE.
func (h *Handler) injectDefaultParamTask(session *Session, serial string, drv *schema.DeviceDriver, currentParams map[string]string) {
	if drv == nil || len(drv.DefaultParams) == 0 {
		return
	}
	h.log.WithField("serial", serial).
		WithField("driver", drv.ID).
		WithField("default_param_keys", len(drv.DefaultParams)).
		Info("CWMP: evaluating driver default_params (post full summon)")

	toSet := make(map[string]string, len(drv.DefaultParams))
	for path, want := range drv.DefaultParams {
		cur := ""
		if currentParams != nil {
			cur = currentParams[path]
		}
		if strings.TrimSpace(cur) != strings.TrimSpace(want) {
			toSet[path] = want
		}
	}
	if len(toSet) == 0 {
		h.log.WithField("serial", serial).
			WithField("driver", drv.ID).
			Info("CWMP: driver default_params already match CPE; skipping SetParameterValues")
		return
	}
	payload, err := json.Marshal(task.SetParamsPayload{Parameters: toSet})
	if err != nil {
		h.log.WithError(err).WithField("serial", serial).Warn("CWMP: marshal default_params payload failed")
		return
	}
	synth := &task.Task{
		ID:      "default_params_" + serial,
		Serial:  serial,
		Type:    task.TypeSetParams,
		Payload: json.RawMessage(payload),
		Status:  task.StatusPending,
	}
	h.log.WithField("serial", serial).
		WithField("params", toSet).
		Info("CWMP: enforcing driver default_params via SetParameterValues")
	session.mu.Lock()
	session.pendingTasks = append([]*task.Task{synth}, session.pendingTasks...)
	session.mu.Unlock()
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
	if summonPhase == 4 {
		h.handleTargetedSummonResponse(ctx, w, session, params)
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
			h.writeSessionXML(w, session, nextXML)
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
	h.writeSessionXML(w, session, taskXML)
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
			h.writeSessionXML(w, session, nextXML)
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
	serial := session.DeviceSerial
	session.mu.Unlock()

	if t != nil {
		// After a successful WAN task, force a fresh full summon on the next Inform
		// so UI sections (Network / Information) are updated immediately with the
		// latest interface state instead of waiting for summon throttle timeout.
		if t.Type == task.TypeWAN && errMsg == "" {
			h.lastSummonTime.Delete(serial)
		}
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

	h.writeSessionXML(w, session, taskXML)
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
		wanDevIdx := im.WANDeviceIdx
		if wanDevIdx == 0 {
			wanDevIdx = 1
		}
		wanConnIdx := im.WANConnDevIdx
		if wanConnIdx == 0 {
			wanConnIdx = 1
		}
		wanIPConnIdx := im.WANIPConnIdx
		if wanIPConnIdx == 0 {
			wanIPConnIdx = 1
		}
		wanPPPConnIdx := im.WANPPPConnIdx
		if wanPPPConnIdx == 0 {
			wanPPPConnIdx = 1
		}

		connType := strings.TrimSpace(strings.ToLower(p.ConnectionType))
		isPPPoE := connType == "pppoe"
		if !isPPPoE && connType == "" {
			// Some API callers omit connection_type for PPPoE updates/provisioning.
			// Infer PPPoE intent when PPP credentials or VLAN are supplied.
			isPPPoE = p.Username != "" || p.Password != "" || p.VLAN > 0
		}

		// isTR098PPPoEProvisioned returns true when the device is TR-098 and
		// already has a WANPPPConnection object (C-DATA/ZTE pre-provisioned WANs).
		isTR098PPPoEProvisioned := im.WANPPPConnIdx > 0
		// isTR181PPPoEProvisioned returns true when the device is TR-181 and
		// already has both a WAN IP interface and a PPP interface.
		isTR181PPPoEProvisioned := im.WANIPIfaceIdx > 0 && im.PPPIfaceIdx > 0

		if isPPPoE && !isTR098PPPoEProvisioned && !isTR181PPPoEProvisioned {
			// Missing WAN/PPP runtime interface: device needs full PPPoE provisioning.
			// This commonly happens after WAN is deleted from the ONT web UI: WAN IP
			// objects may still exist, but PPP interface is gone. In that case we must
			// not fall back to generic SetParameterValues (it can push invalid
			// X_TP_ConnType/PPP paths and trigger 9003 faults).
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
				newVars := map[string]string{
					"vlan_id":       strconv.Itoa(p.VLAN),
					"username":      p.Username,
					"password":      p.Password,
					"gpon_idx":      strconv.Itoa(im.FreeGPONLinkIdx),
					"wan_dev":       strconv.Itoa(wanDevIdx),
					"wan_conn":      strconv.Itoa(wanConnIdx),
					"wan_ip_conn":   strconv.Itoa(wanIPConnIdx),
					"wan_ppp_conn":  strconv.Itoa(wanPPPConnIdx),
					"ipv6_enabled":  wanIPv6EnabledStr(p),
				}
				if v := resolveWANIPMode(p, drv); v != "" {
					newVars["wan_ip_mode"] = v
				}
				wp, err = newWANProvisionFromDriver(t, drv, "wan_pppoe_new", newVars)
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
				h.writeSessionXML(w, session, xmlBytes)
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

		if isPPPoE && (isTR181PPPoEProvisioned || isTR098PPPoEProvisioned) {
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
					"wan_dev":       strconv.Itoa(wanDevIdx),
					"wan_conn":      strconv.Itoa(wanConnIdx),
					"wan_ip_conn":   strconv.Itoa(wanIPConnIdx),
					"wan_ppp_conn":  strconv.Itoa(wanPPPConnIdx),
					"ipv6_enabled":  wanIPv6EnabledStr(p),
				}
				if v := resolveWANIPMode(p, drv); v != "" {
					inputVars["wan_ip_mode"] = v
				}

				if vlanChanging {
					// For TR-181, VLAN change requires a VLANTermination index.
					// For TR-098 (C-DATA), VLAN is embedded in WANPPPConnection via
					// X_CT-COM_VLANIDMark — no separate WANVLANTermination exists.
					if isTR181PPPoEProvisioned && (im.WANIPIfaceIdx == 0 || im.PPPIfaceIdx == 0 || im.WANVLANTermIdx == 0) {
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
				h.writeSessionXML(w, session, xmlBytes)
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

	// WiFi task: YAML-driven via device driver flow.
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

		session.mu.Lock()
		drv := session.driver
		session.mu.Unlock()

		if drv == nil {
			return nil, fmt.Errorf("wifi update requires device driver")
		}
		if drv.GetProvisionFlow("wifi_update") == nil {
			return nil, fmt.Errorf("wifi update flow %q not found in driver %s", "wifi_update", drv.ID)
		}

		params, err := buildWiFiParamsFromDriverFlow(t, drv, mapper, wifiPayload)
		if err != nil {
			return nil, err
		}
		if len(params) == 0 {
			return nil, fmt.Errorf("task %s produced no parameters", t.ID)
		}

		h.log.WithField("task_id", t.ID).
			WithField("driver", drv.ID).
			WithField("flow", "wifi_update").
			Info("CWMP: WiFi update via driver YAML flow")

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
			h.writeSessionXML(w, session, xml)
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

func (h *Handler) writeSessionXML(w http.ResponseWriter, session *Session, data []byte) {
	h.bindOutboundRPCID(data, session)
	writeXML(w, data)
}

func requestID(env *Envelope) string {
	if env != nil && env.Header.ID != nil {
		return strings.TrimSpace(env.Header.ID.Value)
	}
	return ""
}

func (h *Handler) bindOutboundRPCID(xmlBytes []byte, session *Session) {
	if h == nil || h.sessionMgr == nil || session == nil || len(xmlBytes) == 0 {
		return
	}
	env, err := ParseEnvelope(xmlBytes)
	if err != nil {
		return
	}
	if id := requestID(env); id != "" {
		h.sessionMgr.BindRPCID(id, session)
	}
}

func headerID(env *Envelope) string {
	if env != nil && env.Header.ID != nil {
		return env.Header.ID.Value
	}
	return uuid.NewString()
}

// firstRootObject returns the dominant root object namespace present in params.
// InternetGatewayDevice. (TR-098) takes priority when both prefixes coexist,
// targetedSummonPaths returns the GPV object paths needed for a targeted summon
// and the task type that triggered it. Returns nil, "" if no targeted summon is needed.
//
//	WiFi  task → Device.WiFi.                           (TR-181)
//	           → InternetGatewayDevice.LANDevice.        (TR-098)
//	WAN   task → Device.IP.Interface. + Device.PPP.Interface. + vlan/link subtrees  (TR-181)
//	           → InternetGatewayDevice.WANDevice.        (TR-098)
func targetedSummonPaths(schemaName string, pending []*task.Task) ([]string, string) {
	isTR098 := strings.Contains(strings.ToLower(schemaName), "tr098")

	for _, pt := range pending {
		switch pt.Type {
		case task.TypeWifi:
			if isTR098 {
				return []string{"InternetGatewayDevice.LANDevice."}, string(pt.Type)
			}
			return []string{"Device.WiFi."}, string(pt.Type)
		case task.TypeWAN:
			if isTR098 {
				return []string{"InternetGatewayDevice.WANDevice."}, string(pt.Type)
			}
			return []string{
				"Device.IP.Interface.",
				"Device.PPP.Interface.",
				"Device.Ethernet.VLANTermination.",
				"Device.Ethernet.Link.",
			}, string(pt.Type)
		}
	}
	return nil, ""
}

// uiSummonPaths returns object paths needed to refresh fields shown in the web UI
// (Information/Network/WiFi/LAN/traffic-related counters), without triggering
// full GetParameterNames + many GPV batches.
// Driver extra_summon_paths are appended when a driver is provided.
func uiSummonPaths(schemaName string, drv *schema.DeviceDriver) []string {
	isTR098 := strings.Contains(strings.ToLower(schemaName), "tr098")
	var paths []string
	if isTR098 {
		paths = []string{
			"InternetGatewayDevice.DeviceInfo.",
			"InternetGatewayDevice.WANDevice.",
			"InternetGatewayDevice.LANDevice.",
			"InternetGatewayDevice.ManagementServer.",
		}
	} else {
		paths = []string{
			"Device.DeviceInfo.",
			"Device.IP.Interface.",
			"Device.PPP.Interface.",
			"Device.WiFi.",
			"Device.WiFi.AccessPoint.",
			"Device.Optical.Interface.",
			"Device.Ethernet.VLANTermination.",
			"Device.Ethernet.Link.",
			"Device.ManagementServer.",
			"Device.Hosts.",
		}
	}
	if drv != nil {
		paths = append(paths, drv.Discovery.ExtraSummonPaths...)
	}
	return paths
}

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

// buildWiFiParamsFromDriverFlow resolves WiFi parameters using a YAML flow
// from the resolved device driver (flow name: "wifi_update").
func buildWiFiParamsFromDriverFlow(
	t *task.Task,
	drv *schema.DeviceDriver,
	mapper datamodel.Mapper,
	p task.WiFiPayload,
) (map[string]string, error) {
	flow := drv.GetProvisionFlow("wifi_update")
	if flow == nil {
		return nil, fmt.Errorf("driver %s has no provision flow %q", drv.ID, "wifi_update")
	}

	bandIdx := 0
	if p.Band == "5" {
		bandIdx = 1
	}
	otherBandIdx := 1
	if bandIdx == 1 {
		otherBandIdx = 0
	}

	securityMode := ""
	if p.Security != "" {
		if p.Security == "None" {
			securityMode = "None"
		} else {
			securityMode = drv.MapSecurityMode(p.Security)
		}
	} else if p.Password != "" {
		// Keep current behavior: password-only update implies WPA2-Personal.
		securityMode = drv.MapSecurityMode("WPA2-PSK")
	}

	password := p.Password
	if securityMode == "None" {
		password = ""
	}

	enabledVal := ""
	if p.Enabled != nil {
		enabledVal = strconv.FormatBool(*p.Enabled)
	}
	channelVal := ""
	if p.Channel != 0 {
		channelVal = strconv.Itoa(p.Channel)
	}

	bandSteeringPath := strings.TrimSpace(drv.WiFi.BandSteeringPath)
	bandSteeringEnabled := ""
	if p.BandSteeringEnabled != nil && bandSteeringPath != "" {
		bandSteeringEnabled = strconv.FormatBool(*p.BandSteeringEnabled)
	}

	// When a task explicitly enables band steering, sync SSID/password/security
	// to the other radio band so both profiles stay aligned.
	steeringSync := p.BandSteeringEnabled != nil && *p.BandSteeringEnabled

	ssidPath := strings.TrimSpace(mapper.WiFiSSIDPath(bandIdx))
	securityPath := strings.TrimSpace(mapper.WiFiSecurityModePath(bandIdx))
	passwordPath := strings.TrimSpace(mapper.WiFiPasswordPath(bandIdx))
	enabledPath := strings.TrimSpace(mapper.WiFiEnabledPath(bandIdx))
	channelPath := strings.TrimSpace(mapper.WiFiChannelPath(bandIdx))

	syncSSIDPath := ""
	syncSecurityPath := ""
	syncPasswordPath := ""
	syncSSID := ""
	syncSecurityMode := ""
	syncPassword := ""
	if steeringSync {
		syncSSIDPath = strings.TrimSpace(mapper.WiFiSSIDPath(otherBandIdx))
		syncSecurityPath = strings.TrimSpace(mapper.WiFiSecurityModePath(otherBandIdx))
		syncPasswordPath = strings.TrimSpace(mapper.WiFiPasswordPath(otherBandIdx))
		syncSSID = p.SSID
		syncSecurityMode = securityMode
		syncPassword = password
	}

	// Guard against empty parameter keys: when a path is empty, force value empty
	// so ProvisionExecutor.resolveParams() drops that entry.
	valueIfPath := func(path, val string) string {
		if strings.TrimSpace(path) == "" {
			return ""
		}
		return val
	}

	inputVars := map[string]string{
		"band_steering_path":    bandSteeringPath,
		"band_steering_enabled": valueIfPath(bandSteeringPath, bandSteeringEnabled),
		"ssid_path":             ssidPath,
		"ssid":                  valueIfPath(ssidPath, p.SSID),
		"security_path":         securityPath,
		"security_mode":         valueIfPath(securityPath, securityMode),
		"password_path":         passwordPath,
		"password":              valueIfPath(passwordPath, password),
		"enabled_path":          enabledPath,
		"enabled":               valueIfPath(enabledPath, enabledVal),
		"channel_path":          channelPath,
		"channel":               valueIfPath(channelPath, channelVal),
		"sync_ssid_path":        syncSSIDPath,
		"sync_ssid":             valueIfPath(syncSSIDPath, syncSSID),
		"sync_security_path":    syncSecurityPath,
		"sync_security_mode":    valueIfPath(syncSecurityPath, syncSecurityMode),
		"sync_password_path":    syncPasswordPath,
		"sync_password":         valueIfPath(syncPasswordPath, syncPassword),
	}

	exe := schema.NewProvisionExecutor(flow, drv, inputVars)
	step := exe.CurrentStep()
	if step == nil {
		return nil, fmt.Errorf("driver wifi flow %q has no executable step", flow.ID)
	}
	if step.Kind != schema.StepSet {
		return nil, fmt.Errorf("driver wifi flow %q must start with set step", flow.ID)
	}
	return step.Params, nil
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

// extractCPUUsage reads CPU usage percentage from device parameters.
// It checks the driver-configured path first, then falls back to the standard
// TR-181 path. The value may be a plain integer ("42") or a multi-core string
// like "1%;2%" — the first numeric token is used.
func extractCPUUsage(params map[string]string, drv *schema.DeviceDriver) *int64 {
	paths := []string{
		"Device.DeviceInfo.ProcessStatus.CPUUsage",
		"InternetGatewayDevice.DeviceInfo.ProcessStatus.CPUUsage",
	}
	if drv != nil && drv.Discovery.SystemCPUPath != "" {
		paths = append([]string{drv.Discovery.SystemCPUPath}, paths...)
	}
	for _, p := range paths {
		raw := strings.TrimSpace(params[p])
		if raw == "" {
			continue
		}
		// Handle "core1%;core2%;…" — take first token, strip "%".
		token := strings.SplitN(raw, ";", 2)[0]
		token = strings.TrimSuffix(strings.TrimSpace(token), "%")
		if c, err := strconv.ParseInt(token, 10, 64); err == nil {
			return &c
		}
	}
	return nil
}

// patchRAMFromPct populates stats.RAMTotalKB/RAMFreeKB from a percentage path
// (e.g. "56%") when the driver provides SystemMemPctPath and standard KB paths
// returned zero. We store Total=100, Free=(100-pct) so callers can display "%".
func patchRAMFromPct(drv *schema.DeviceDriver, params map[string]string, stats *task.CPEStatsResult) {
	if drv == nil || drv.Discovery.SystemMemPctPath == "" || stats == nil {
		return
	}
	if stats.RAMTotalKB != 0 {
		return // standard paths already populated
	}
	raw := strings.TrimSpace(params[drv.Discovery.SystemMemPctPath])
	if raw == "" {
		return
	}
	raw = strings.TrimSuffix(raw, "%")
	pct, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || pct < 0 || pct > 100 {
		return
	}
	stats.RAMTotalKB = 100
	stats.RAMFreeKB = 100 - pct
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
	modelType := datamodel.DetectFromRootObject(firstRootObject(snapshot))
	var discoveryHints *datamodel.DiscoveryHints
	if h.driverRegistry != nil {
		drv := h.driverRegistry.Resolve(
			snapshot["Device.DeviceInfo.Manufacturer"],
			snapshot["Device.DeviceInfo.ProductClass"],
			string(modelType),
		)
		discoveryHints = discoveryHintsFromDriver(drv)
	}
	snapshotInstanceMap := datamodel.DiscoverInstancesWithHints(snapshot, discoveryHints)
	// Use SchemaMapper.Clone so instance indices from snapshot are applied to the
	// same YAML parameter table as the live session mapper.
	var snapshotMapper datamodel.Mapper
	if sm, ok := mapper.(*schema.SchemaMapper); ok {
		snapshotMapper = sm.Clone(snapshotInstanceMap)
	} else {
		snapshotMapper = datamodel.ApplyInstanceMap(mapper, snapshotInstanceMap)
	}

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

// TriggerFullSummon schedules a full GetParameterNames+GetParameterValues summon
// for the given serial. It works across session boundaries: if the device is
// currently connected the active session is marked immediately; otherwise the
// flag persists until the device's next Inform cycle.
// Always returns true (the request is accepted even if device is currently offline).
func (h *Handler) TriggerFullSummon(serial string) bool {
	h.pendingFullSummon.Store(serial, true)
	// Also mark the live session immediately if one exists.
	if session := h.sessionMgr.GetBySerial(serial); session != nil {
		session.mu.Lock()
		if session.State == StateNew || session.State == StateInform || session.State == StateProcessing {
			session.summonPhase = 1
		}
		session.mu.Unlock()
	}
	h.log.WithField("serial", serial).Info("CWMP: full summon scheduled via API")
	return true
}
