package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"

	"github.com/raykavin/helix-acs/internal/device"
	"github.com/raykavin/helix-acs/internal/parameter"
)

// DeviceHandler handles all device-related REST endpoints.
type DeviceHandler struct {
	deviceSvc device.Service
	paramRepo parameter.Repository
}

// NewDeviceHandler creates a DeviceHandler.
func NewDeviceHandler(deviceSvc device.Service, paramRepo parameter.Repository) *DeviceHandler {
	return &DeviceHandler{deviceSvc: deviceSvc, paramRepo: paramRepo}
}

// listResponse is the paginated response envelope for device listings.
type listResponse struct {
	Data  any   `json:"data"`
	Total int64 `json:"total"`
	Page  int   `json:"page"`
	Limit int   `json:"limit"`
}

// List handles GET /api/v1/devices
// Query params: page, limit, manufacturer, model, online, tag, wan_ip
func (h *DeviceHandler) List(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	filter := device.DeviceFilter{
		Manufacturer: q.Get("manufacturer"),
		ModelName:    q.Get("model"),
		Tag:          q.Get("tag"),
		WANIP:        q.Get("wan_ip"),
	}

	if onlineStr := q.Get("online"); onlineStr != "" {
		online, err := strconv.ParseBool(onlineStr)
		if err == nil {
			filter.Online = &online
		}
	}

	page, limit := paginationParams(r)

	devices, total, err := h.deviceSvc.List(r.Context(), filter, page, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list devices")
		return
	}

	// Return an empty array instead of null when there are no results.
	if devices == nil {
		devices = []*device.Device{}
	}

	writeJSON(w, http.StatusOK, listResponse{
		Data:  devices,
		Total: total,
		Page:  page,
		Limit: limit,
	})
}

// Get handles GET /api/v1/devices/:serial
func (h *DeviceHandler) Get(w http.ResponseWriter, r *http.Request) {
	serial := mux.Vars(r)["serial"]
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}

	dev, err := h.deviceSvc.FindBySerial(r.Context(), serial)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch device")
		return
	}
	if dev == nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}

	writeJSON(w, http.StatusOK, dev)
}

// updateRequest is the JSON body accepted by Update.
type updateRequest struct {
	Tags []string `json:"tags"`
}

// Update handles PUT /api/v1/devices/:serial
// Body: {"tags": ["tag1", "tag2"]}
func (h *DeviceHandler) Update(w http.ResponseWriter, r *http.Request) {
	serial := mux.Vars(r)["serial"]
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}

	var req updateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	// Ensure tags is never nil in the stored document.
	if req.Tags == nil {
		req.Tags = []string{}
	}

	dev, err := h.deviceSvc.UpdateTags(r.Context(), serial, req.Tags)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update device")
		return
	}
	if dev == nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}

	writeJSON(w, http.StatusOK, dev)
}

// Delete handles DELETE /api/v1/devices/:serial
func (h *DeviceHandler) Delete(w http.ResponseWriter, r *http.Request) {
	serial := mux.Vars(r)["serial"]
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}

	// Verify the device exists before attempting deletion.
	dev, err := h.deviceSvc.FindBySerial(r.Context(), serial)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch device")
		return
	}
	if dev == nil {
		writeError(w, http.StatusNotFound, "device not found")
		return
	}

	if err := h.deviceSvc.Delete(r.Context(), serial); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete device")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GetParameters handles GET /api/v1/devices/:serial/parameters
// Returns the full parameter map from PostgreSQL (updated every CWMP summon).
func (h *DeviceHandler) GetParameters(w http.ResponseWriter, r *http.Request) {
	serial := mux.Vars(r)["serial"]
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}

	params, err := h.paramRepo.GetAllParameters(r.Context(), serial)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch parameters")
		return
	}
	if params == nil {
		params = map[string]string{}
	}

	writeJSON(w, http.StatusOK, params)
}

// provisionInfoResponse carries the last-known WAN provisioning credentials
// stored by the ACS after a successful WAN task.
type provisionInfoResponse struct {
	PPPoEUsername  string `json:"pppoe_username"`
	PPPoEPassword  string `json:"pppoe_password"`   // masked in logs; returned so form can be pre-filled
	VLANID         string `json:"vlan_id"`
	ConnectionType string `json:"connection_type"`
}

// GetProvisionInfo handles GET /api/v1/devices/:serial/provision
// Returns the last WAN provisioning credentials stored by the ACS in PostgreSQL.
// These values are written by the CWMP handler after every successful WAN task
// because TP-Link ONTs never return the PPPoE password via GetParameterValues.
func (h *DeviceHandler) GetProvisionInfo(w http.ResponseWriter, r *http.Request) {
	serial := mux.Vars(r)["serial"]
	if serial == "" {
		writeError(w, http.StatusBadRequest, "serial is required")
		return
	}

	params, err := h.paramRepo.GetParametersByPrefix(r.Context(), serial, "_helix.provision.")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to fetch provision info")
		return
	}

	resp := provisionInfoResponse{
		PPPoEUsername:  params["_helix.provision.pppoe_username"],
		PPPoEPassword:  params["_helix.provision.pppoe_password"],
		VLANID:         params["_helix.provision.vlan_id"],
		ConnectionType: params["_helix.provision.connection_type"],
	}

	writeJSON(w, http.StatusOK, resp)
}
