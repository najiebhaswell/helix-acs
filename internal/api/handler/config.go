package handler

import (
	"encoding/json"
	"net/http"

	"github.com/raykavin/helix-acs/internal/parameter"
)

const systemSerial = "__system__"

// ConfigHandler handles system-wide configuration endpoints.
type ConfigHandler struct {
	paramRepo parameter.Repository
}

// NewConfigHandler creates a ConfigHandler.
func NewConfigHandler(paramRepo parameter.Repository) *ConfigHandler {
	return &ConfigHandler{paramRepo: paramRepo}
}

// GetDefaultParams handles GET /api/v1/config/default-params.
// Returns the map of global default TR-069 parameters that are pushed to every
// CPE on its first connection or after a factory reset (bootstrap).
func (h *ConfigHandler) GetDefaultParams(w http.ResponseWriter, r *http.Request) {
	params, err := h.paramRepo.GetAllParameters(r.Context(), systemSerial)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load default params")
		return
	}
	if params == nil {
		params = map[string]string{}
	}
	writeJSON(w, http.StatusOK, params)
}

// UpdateDefaultParams handles PUT /api/v1/config/default-params.
// Body: {"Device.ManagementServer.Username": "admin", ...}
// Replaces the entire global default params set atomically (delete-then-insert).
func (h *ConfigHandler) UpdateDefaultParams(w http.ResponseWriter, r *http.Request) {
	var params map[string]string
	if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := h.paramRepo.DeleteDeviceParameters(r.Context(), systemSerial); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear default params")
		return
	}
	if len(params) > 0 {
		if err := h.paramRepo.UpdateParameters(r.Context(), systemSerial, params); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save default params")
			return
		}
	}
	writeJSON(w, http.StatusOK, params)
}
