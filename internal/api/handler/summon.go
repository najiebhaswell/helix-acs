package handler

import (
	"encoding/json"
	"net/http"

	"github.com/gorilla/mux"
)

// SummonTriggerer is implemented by cwmp.Server to decouple the API layer from
// the CWMP package.
type SummonTriggerer interface {
	TriggerFullSummon(serial string) bool
}

// SummonHandler handles requests to trigger full parameter summons on live devices.
type SummonHandler struct {
	cwmp SummonTriggerer
}

// NewSummonHandler creates a SummonHandler.
func NewSummonHandler(cwmp SummonTriggerer) *SummonHandler {
	return &SummonHandler{cwmp: cwmp}
}

// TriggerFullSummon handles POST /api/v1/devices/{serial}/summon.
// It requests a full GetParameterNames+GetParameterValues cycle on the next
// empty-body POST from the device. Returns 202 if the session is live, 503 if
// the device is not currently connected.
func (h *SummonHandler) TriggerFullSummon(w http.ResponseWriter, r *http.Request) {
	serial := mux.Vars(r)["serial"]
	if serial == "" {
		http.Error(w, "serial required", http.StatusBadRequest)
		return
	}
	h.cwmp.TriggerFullSummon(serial)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "summon scheduled — parameters will be refreshed on next device contact",
	})
}
