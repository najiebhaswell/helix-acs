package cwmp

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/raykavin/helix-acs/internal/task"
)

// WANProvision drives a multi-step TP-Link TR-181 PPPoE provisioning flow.
// Each step is either an AddObject or a SetParameterValues RPC. AddObject
// steps record the returned InstanceNumber as a named variable which later
// steps can reference as {varName} in parameter paths and values.
type WANProvision struct {
	t     *task.Task
	steps []wanStep
	cur   int
	vars  map[string]string // e.g. "gpon"->"3", "eth"->"5", ...
}

type wanStepKind int

const (
	wanStepAdd wanStepKind = iota
	wanStepSet
)

type wanStep struct {
	kind    wanStepKind
	obj     string            // AddObject: object name (trailing dot required)
	fillVar string            // AddObject: var name to store new InstanceNumber
	params  map[string]string // SetParams: keys/values may contain {varName}
}

// newWANProvision builds the complete step sequence for a fresh TP-Link
// PPPoE provisioning following the SmartOLT packet-capture flow:
//
//  1. Reuse existing GPON Link → AddObject Ethernet Link → VLANTermination
//  2. PPP Interface  →  IP Interface  →  NAT  →  DHCPv6
//  3. Enable everything
//
// gponIdx is the existing GPON Link index to reuse (e.g. 3 for Link.3).
// Pass gponIdx=0 to create a new GPON Link via AddObject.
func newWANProvision(t *task.Task, gponIdx, vlanID int, username, password string) *WANProvision {
	v := strconv.Itoa(vlanID)
	var gponSteps []wanStep
	gi := "0"
	if gponIdx > 0 {
		// Reuse existing disabled GPON Link.
		gi = strconv.Itoa(gponIdx)
		gponSteps = []wanStep{
			{kind: wanStepSet, params: map[string]string{
				"Device.X_TP_GPON.Link." + gi + ".Alias":       "TpLink_" + v,
				"Device.X_TP_GPON.Link." + gi + ".LowerLayers": "Device.Optical.Interface.1.",
			}},
		}
	} else {
		// No free slot — create a new GPON Link (SmartOLT-style).
		gponSteps = []wanStep{
			{kind: wanStepAdd, obj: "Device.X_TP_GPON.Link.", fillVar: "gpon"},
			{kind: wanStepSet, params: map[string]string{
				"Device.X_TP_GPON.Link.{gpon}.Alias":       "TpLink_" + v,
				"Device.X_TP_GPON.Link.{gpon}.LowerLayers": "Device.Optical.Interface.1.",
			}},
		}
	}
	steps := append(gponSteps, []wanStep{
		// ---- Layer 2: Ethernet Link ----
		{kind: wanStepAdd, obj: "Device.Ethernet.Link.", fillVar: "eth"},
		{kind: wanStepSet, params: map[string]string{
			"Device.Ethernet.Link.{eth}.Alias":       "ethlink_" + v,
			"Device.Ethernet.Link.{eth}.Enable":      "1",
			"Device.Ethernet.Link.{eth}.LowerLayers": "Device.X_TP_GPON.Link.{gpon}.",
		}},
		// ---- Layer 3: VLAN Termination ----
		{kind: wanStepAdd, obj: "Device.Ethernet.VLANTermination.", fillVar: "vterm"},
		{kind: wanStepSet, params: map[string]string{
			"Device.Ethernet.VLANTermination.{vterm}.Alias":       "VLAN_" + v,
			"Device.Ethernet.VLANTermination.{vterm}.Enable":      "1",
			"Device.Ethernet.VLANTermination.{vterm}.LowerLayers": "Device.Ethernet.Link.{eth}.",
			"Device.Ethernet.VLANTermination.{vterm}.VLANID":      v,
		}},
		{kind: wanStepSet, params: map[string]string{
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_MulticastStatus": "1",
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_VLANEnable":      "1",
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_VLANMode":        "2",
		}},
		// ---- Layer 4: PPP Interface ----
		{kind: wanStepAdd, obj: "Device.PPP.Interface.", fillVar: "ppp"},
		// ---- Layer 5: IP Interface ----
		{kind: wanStepAdd, obj: "Device.IP.Interface.", fillVar: "ip"},
		// Set IP LowerLayers before configuring PPP (same order as SmartOLT)
		{kind: wanStepSet, params: map[string]string{
			"Device.IP.Interface.{ip}.LowerLayers": "Device.PPP.Interface.{ppp}.",
		}},
		// Configure PPP Interface
		{kind: wanStepSet, params: map[string]string{
			"Device.PPP.Interface.{ppp}.Alias":                     "Internet_PPPoE",
			"Device.PPP.Interface.{ppp}.AuthenticationProtocol":    "AUTO_AUTH",
			"Device.PPP.Interface.{ppp}.LowerLayers":               "Device.Ethernet.VLANTermination.{vterm}.",
			"Device.PPP.Interface.{ppp}.Password":                  password,
			"Device.PPP.Interface.{ppp}.Username":                  username,
			"Device.PPP.Interface.{ppp}.X_TP_UsernameDomainEnable": "1",
		}},
		// Configure IP Interface
		{kind: wanStepSet, params: map[string]string{
			"Device.IP.Interface.{ip}.Alias":             "Internet_PPPoE",
			"Device.IP.Interface.{ip}.IPv4Enable":        "1",
			"Device.IP.Interface.{ip}.MaxMTUSize":        "1492",
			"Device.IP.Interface.{ip}.X_TP_ConnName":     "Internet_PPPoE",
			"Device.IP.Interface.{ip}.X_TP_ConnType":     "PPPoE",
			"Device.IP.Interface.{ip}.X_TP_IPv6AddrType": "SLAAC",
			"Device.IP.Interface.{ip}.X_TP_ServiceType":  "Internet",
		}},
		// ---- Layer 6: NAT ----
		{kind: wanStepAdd, obj: "Device.NAT.InterfaceSetting.", fillVar: "nat"},
		{kind: wanStepSet, params: map[string]string{
			"Device.NAT.InterfaceSetting.{nat}.Interface": "Device.IP.Interface.{ip}.",
		}},
		// ---- IPv6 ----
		{kind: wanStepSet, params: map[string]string{
			"Device.IP.Interface.{ip}.IPv6Enable": "1",
		}},
		// ---- Layer 7: DHCPv6 Client ----
		{kind: wanStepAdd, obj: "Device.DHCPv6.Client.", fillVar: "dhcpv6"},
		{kind: wanStepSet, params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.Interface": "Device.IP.Interface.{ip}.",
		}},
		{kind: wanStepSet, params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.RequestAddresses":    "0",
			"Device.DHCPv6.Client.{dhcpv6}.RequestPrefixes":     "1",
			"Device.DHCPv6.Client.{dhcpv6}.RequestedOptions":    "23",
			"Device.DHCPv6.Client.{dhcpv6}.X_TP_EnableRaRouter": "1",
			"Device.DHCPv6.Client.{dhcpv6}.X_TP_EnableSLAAC":    "1",
		}},
		// ---- Default Gateway (TP-Link proprietary) ----
		{kind: wanStepSet, params: map[string]string{
			"Device.X_TP_DefaultGateway.IPv4DefaultGatewayType":   "Manual",
			"Device.X_TP_DefaultGateway.CustomIPv4DefaultGateway": "Internet_PPPoE",
			"Device.X_TP_DefaultGateway.IPv6DefaultGatewayType":   "Manual",
			"Device.X_TP_DefaultGateway.CustomIPv6DefaultGateway": "Internet_PPPoE",
		}},
		// ---- Enable everything ----
		{kind: wanStepSet, params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.Enable":           "1",
			"Device.Ethernet.Link.{eth}.Enable":              "1",
			"Device.Ethernet.VLANTermination.{vterm}.Enable": "1",
			"Device.IP.Interface.{ip}.Enable":                "1",
			"Device.NAT.InterfaceSetting.{nat}.Enable":       "1",
			"Device.PPP.Interface.{ppp}.Enable":              "1",
			"Device.X_TP_GPON.Link.{gpon}.Enable":            "1",
			"Device.DeviceInfo.ProvisioningCode":             "helix.rPPP",
		}},
	}...)
	initVars := map[string]string{}
	if gponIdx > 0 {
		initVars["gpon"] = gi
	}
	return &WANProvision{t: t, steps: steps, cur: 0, vars: initVars}
}

// done reports whether all steps have been executed.
func (p *WANProvision) done() bool { return p.cur >= len(p.steps) }

// buildCurrentXML returns the CWMP XML envelope for the current step.
func (p *WANProvision) buildCurrentXML() ([]byte, error) {
	if p.done() {
		return nil, fmt.Errorf("wan provision already complete")
	}
	s := p.steps[p.cur]
	id := uuid.NewString()
	switch s.kind {
	case wanStepAdd:
		return BuildAddObject(id, s.obj)
	case wanStepSet:
		return BuildSetParameterValues(id, p.resolveParams(s.params))
	}
	return nil, fmt.Errorf("unknown step kind %d", s.kind)
}

// onAddObject records the new instance number for the current AddObject step
// and returns the XML for the immediately following step.
func (p *WANProvision) onAddObject(instanceNum int) ([]byte, error) {
	s := p.steps[p.cur]
	if s.kind != wanStepAdd {
		return nil, fmt.Errorf("expected AddObject at step %d", p.cur)
	}
	p.vars[s.fillVar] = strconv.Itoa(instanceNum)
	p.cur++
	return p.buildCurrentXML()
}

// onSetParams advances past the current SetParams step and returns the XML for
// the next step, or nil when provisioning is complete.
func (p *WANProvision) onSetParams() ([]byte, error) {
	s := p.steps[p.cur]
	if s.kind != wanStepSet {
		return nil, fmt.Errorf("expected SetParams at step %d", p.cur)
	}
	p.cur++
	if p.done() {
		return nil, nil
	}
	return p.buildCurrentXML()
}

// resolveParams substitutes all {varName} tokens in both keys and values.
func (p *WANProvision) resolveParams(raw map[string]string) map[string]string {
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		out[p.subst(k)] = p.subst(v)
	}
	return out
}

func (p *WANProvision) subst(s string) string {
	for name, val := range p.vars {
		s = strings.ReplaceAll(s, "{"+name+"}", val)
	}
	return s
}
