package handler

import (
	"net/http"

	"github.com/gorilla/mux"
	"github.com/raykavin/helix-acs/internal/device"
	"github.com/raykavin/helix-acs/internal/parameter"
)

// SnapshotHandler handles device parameter snapshot operations.
type SnapshotHandler struct {
	deviceSvc  device.Service
	paramRepo  parameter.Repository
}

// NewSnapshotHandler creates a SnapshotHandler.
func NewSnapshotHandler(deviceSvc device.Service, paramRepo parameter.Repository) *SnapshotHandler {
	return &SnapshotHandler{deviceSvc: deviceSvc, paramRepo: paramRepo}
}

// SaveLastKnownGood handles POST /api/v1/devices/{serial}/snapshots/last-known-good
// Reads the current parameters from PostgreSQL and saves them as the
// "last_known_good" snapshot used by factory-reset auto-restore.
func (h *SnapshotHandler) SaveLastKnownGood(w http.ResponseWriter, r *http.Request) {
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

	params, err := h.paramRepo.GetAllParameters(r.Context(), serial)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to read device parameters")
		return
	}
	if len(params) == 0 {
		writeError(w, http.StatusConflict, "no parameters stored for this device yet; wait for a full parameter summon first")
		return
	}

	if err := h.paramRepo.SaveSnapshot(r.Context(), serial, "last_known_good", params); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save snapshot")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "ok",
		"serial":      serial,
		"param_count": len(params),
	})
}
