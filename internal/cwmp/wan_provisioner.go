package cwmp

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/raykavin/helix-acs/internal/datamodel"
	"github.com/raykavin/helix-acs/internal/task"
)

// buildGenericWANParams builds a flat SetParameterValues map for devices that
// provision/update WAN via a single RPC (WANProvisioningType == "set_params").
//
// It uses the mapper's PPPoE paths so the correct vendor-specific parameters
// are addressed. This covers generic TR-181 and TR-098 devices that have
// pre-existing WAN objects and only need username/password/VLAN written.
func buildGenericWANParams(p task.WANPayload, im datamodel.InstanceMap, mapper datamodel.Mapper) (map[string]string, error) {
	params := make(map[string]string)

	userPath := mapper.WANPPPoEUserPath()
	passPath := mapper.WANPPPoEPassPath()

	if userPath == "" || passPath == "" {
		return nil, fmt.Errorf("mapper has no PPPoE credential paths for this device")
	}

	if p.Username != "" {
		params[userPath] = p.Username
	}
	if p.Password != "" {
		params[passPath] = p.Password
	}

	if len(params) == 0 {
		return nil, fmt.Errorf("WAN generic provisioning: username or password is required")
	}

	return params, nil
}

// buildGenericVLANUpdate builds params for a VLAN change on generic TR-181 devices
// that expose the VLAN ID as a settable parameter.  When the mapper's
// WANConnectionTypePath is empty the device is assumed to have no accessible
// VLAN parameter and an error is returned.
func buildGenericVLANUpdate(p task.WANPayload, im datamodel.InstanceMap, mapper datamodel.Mapper) (map[string]string, error) {
	if im.WANVLANTermIdx == 0 {
		return nil, fmt.Errorf("WAN VLAN update: no VLANTermination index found in instance map")
	}
	if p.VLAN == 0 {
		return nil, fmt.Errorf("WAN VLAN update: VLAN ID is required")
	}

	params := make(map[string]string)

	vlanPath := fmt.Sprintf("Device.Ethernet.VLANTermination.%d.VLANID", im.WANVLANTermIdx)
	params[vlanPath] = strconv.Itoa(p.VLAN)

	if p.Username != "" {
		if path := mapper.WANPPPoEUserPath(); path != "" {
			params[path] = p.Username
		}
	}
	if p.Password != "" {
		if path := mapper.WANPPPoEPassPath(); path != "" {
			params[path] = p.Password
		}
	}

	return params, nil
}

// buildWANCredentialMap converts a WANPayload into the key-value map that is
// stored in PostgreSQL device_parameters with the "_helix.provision.*" prefix.
//
// These keys are intentionally namespaced so they never collide with real
// TR-069 parameter names. They let the dashboard display the last-provisioned
// username and connection type even when the CPE doesn't expose the password
// via GetParameterValues (as is the case with TP-Link ONTs).
func buildWANCredentialMap(p task.WANPayload) map[string]string {
	creds := make(map[string]string)
	if p.Username != "" {
		creds["_helix.provision.pppoe_username"] = p.Username
	}
	if p.Password != "" {
		creds["_helix.provision.pppoe_password"] = p.Password
	}
	if p.VLAN > 0 {
		creds["_helix.provision.vlan_id"] = strconv.Itoa(p.VLAN)
	}
	connType := p.ConnectionType
	if connType == "" && (p.Username != "" || p.Password != "" || p.VLAN > 0) {
		connType = "pppoe"
	}
	if connType != "" {
		creds["_helix.provision.connection_type"] = connType
	}
	return creds
}

// persistWANCredentials saves PPPoE/WAN provisioning credentials into the
// PostgreSQL device_parameters table using the "_helix.provision.*" namespace.
//
// This is called after a WAN task completes successfully so that:
//  1. The dashboard can display the PPPoE username without reading it from the CPE.
//  2. Future re-provisioning tasks can pre-fill the form with the last-known credentials.
//  3. Password is preserved even though TP-Link ONTs redact it in GetParameterValues.
func (h *Handler) persistWANCredentials(ctx context.Context, serial string, creds map[string]string) {
	if len(creds) == 0 || serial == "" {
		return
	}
	pgCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := h.parameterRepo.UpdateParameters(pgCtx, serial, creds); err != nil {
		h.log.WithError(err).
			WithField("serial", serial).
			Warn("CWMP: failed to persist WAN credentials to PostgreSQL")
	} else {
		h.log.WithField("serial", serial).
			WithField("keys", len(creds)).
			Info("CWMP: WAN credentials persisted to PostgreSQL")
	}
}
