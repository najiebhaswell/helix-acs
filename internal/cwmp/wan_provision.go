package cwmp

import (
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/raykavin/helix-acs/internal/schema"
	"github.com/raykavin/helix-acs/internal/task"
)

// WANProvision drives a multi-step WAN provisioning flow for any vendor.
// It wraps schema.ProvisionExecutor which reads steps from YAML driver files,
// replacing the previous TP-Link-specific hardcoded implementation.
type WANProvision struct {
	t   *task.Task
	exe *schema.ProvisionExecutor
}

// newWANProvisionFromDriver creates a new WANProvision using the driver's
// provision flow YAML. flowName selects which flow to use (e.g. "wan_pppoe_new"
// or "wan_pppoe_delete_add").
//
// inputVars are runtime variables from the task payload, e.g.:
//
//	"vlan_id"          → "100"
//	"username"         → "user@isp"
//	"password"         → "secret"
//	"gpon_idx"         → "3" (0 or "" to create new)
//	"ip_iface_idx"     → "2" (for delete+add flows)
//	"ppp_iface_idx"    → "1"
//	"vlan_term_idx"    → "1"
//	"eth_link_idx"     → "1"
func newWANProvisionFromDriver(
	t *task.Task,
	driver *schema.DeviceDriver,
	flowName string,
	inputVars map[string]string,
) (*WANProvision, error) {
	flow := driver.GetProvisionFlow(flowName)
	if flow == nil {
		return nil, fmt.Errorf("driver %s has no provision flow %q", driver.ID, flowName)
	}
	return &WANProvision{
		t:   t,
		exe: schema.NewProvisionExecutor(flow, driver, inputVars),
	}, nil
}

// done reports whether all steps have been executed.
func (p *WANProvision) done() bool { return p.exe.Done() }

// buildCurrentXML returns the CWMP XML envelope for the current step.
func (p *WANProvision) buildCurrentXML() ([]byte, error) {
	step := p.exe.CurrentStep()
	if step == nil {
		return nil, fmt.Errorf("wan provision already complete")
	}
	id := uuid.NewString()
	switch step.Kind {
	case schema.StepAddObject:
		return BuildAddObject(id, step.Object)
	case schema.StepSet:
		return BuildSetParameterValues(id, step.Params)
	case schema.StepDelete:
		return BuildDeleteObject(id, step.Object)
	}
	return nil, fmt.Errorf("unknown step kind %d", step.Kind)
}

// onAddObject records the instance number for the current AddObject step,
// advances to the next step, and returns its XML.
func (p *WANProvision) onAddObject(instanceNum int) ([]byte, error) {
	step := p.exe.CurrentStep()
	if step == nil || step.Kind != schema.StepAddObject {
		return nil, fmt.Errorf("expected AddObject step")
	}
	p.exe.AdvanceAddObject(instanceNum)
	if p.exe.Done() {
		return nil, nil
	}
	return p.buildCurrentXML()
}

// onSetParams advances past the current Set step and returns the next XML.
func (p *WANProvision) onSetParams() ([]byte, error) {
	if p.exe.Done() {
		return nil, nil
	}
	p.exe.Advance()
	if p.exe.Done() {
		return nil, nil
	}
	return p.buildCurrentXML()
}

// onDeleteObject advances past the current Delete step and returns the next XML.
func (p *WANProvision) onDeleteObject() ([]byte, error) {
	if p.exe.Done() {
		return nil, nil
	}
	p.exe.Advance()
	if p.exe.Done() {
		return nil, nil
	}
	return p.buildCurrentXML()
}

// currentStepKind returns the kind of the current step, or -1 if done.
func (p *WANProvision) currentStepKind() schema.ProvisionStepKind {
	s := p.exe.CurrentStep()
	if s == nil {
		return -1
	}
	return s.Kind
}

// stepIndex returns the current step index for logging.
func (p *WANProvision) stepIndex() int { return p.exe.StepIndex() }

// totalSteps returns the total number of steps for logging.
func (p *WANProvision) totalSteps() int { return p.exe.TotalSteps() }

// varsForLogging returns the current variables (for debug logging).
func (p *WANProvision) varsForLogging() map[string]string { return p.exe.Vars() }

// ---------------------------------------------------------------------------
// Legacy constructors — kept temporarily for backward compatibility with
// devices that connect before a driver is loaded. These build the same
// TP-Link-specific step sequence as before, but via dynamic wanStep slices
// rather than YAML. They should be removed once the TP-Link driver YAML is
// validated in production.
// ---------------------------------------------------------------------------

type wanStepKind = schema.ProvisionStepKind

var (
	wanStepAdd    = schema.StepAddObject
	wanStepSet    = schema.StepSet
	wanStepDelete = schema.StepDelete
)

type wanStep struct {
	kind    wanStepKind
	obj     string
	fillVar string
	params  map[string]string
}

func newWANProvision(t *task.Task, gponIdx, vlanID int, username, password string) *WANProvision {
	v := strconv.Itoa(vlanID)
	gi := strconv.Itoa(gponIdx)

	// Build a dynamic ProvisionFlow from the old hardcoded steps.
	var steps []schema.ProvisionStep

	if gponIdx > 0 {
		steps = append(steps, schema.ProvisionStep{
			Kind: schema.StepSet,
			Params: map[string]string{
				"Device.X_TP_GPON.Link." + gi + ".Alias":       "TpLink_" + v,
				"Device.X_TP_GPON.Link." + gi + ".LowerLayers": "Device.Optical.Interface.1.",
			},
		})
	} else {
		steps = append(steps,
			schema.ProvisionStep{Kind: schema.StepAddObject, Object: "Device.X_TP_GPON.Link.", As: "gpon_idx"},
			schema.ProvisionStep{Kind: schema.StepSet, Params: map[string]string{
				"Device.X_TP_GPON.Link.{gpon_idx}.Alias":       "TpLink_" + v,
				"Device.X_TP_GPON.Link.{gpon_idx}.LowerLayers": "Device.Optical.Interface.1.",
			}},
		)
	}

	steps = append(steps, []schema.ProvisionStep{
		{Kind: schema.StepAddObject, Object: "Device.Ethernet.Link.", As: "eth"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.Ethernet.Link.{eth}.Alias":       "ethlink_" + v,
			"Device.Ethernet.Link.{eth}.Enable":      "1",
			"Device.Ethernet.Link.{eth}.LowerLayers": "Device.X_TP_GPON.Link." + gi + ".",
		}},
		{Kind: schema.StepAddObject, Object: "Device.Ethernet.VLANTermination.", As: "vterm"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.Ethernet.VLANTermination.{vterm}.Alias":       "VLAN_" + v,
			"Device.Ethernet.VLANTermination.{vterm}.Enable":      "1",
			"Device.Ethernet.VLANTermination.{vterm}.LowerLayers": "Device.Ethernet.Link.{eth}.",
			"Device.Ethernet.VLANTermination.{vterm}.VLANID":      v,
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_MulticastStatus": "1",
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_VLANEnable":      "1",
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_VLANMode":        "2",
		}},
		{Kind: schema.StepAddObject, Object: "Device.PPP.Interface.", As: "ppp"},
		{Kind: schema.StepAddObject, Object: "Device.IP.Interface.", As: "ip"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.IP.Interface.{ip}.LowerLayers": "Device.PPP.Interface.{ppp}.",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.PPP.Interface.{ppp}.Alias":                     "Internet_PPPoE",
			"Device.PPP.Interface.{ppp}.AuthenticationProtocol":    "AUTO_AUTH",
			"Device.PPP.Interface.{ppp}.LowerLayers":               "Device.Ethernet.VLANTermination.{vterm}.",
			"Device.PPP.Interface.{ppp}.Password":                  password,
			"Device.PPP.Interface.{ppp}.Username":                  username,
			"Device.PPP.Interface.{ppp}.X_TP_UsernameDomainEnable": "1",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.IP.Interface.{ip}.Alias":             "Internet_PPPoE",
			"Device.IP.Interface.{ip}.IPv4Enable":        "1",
			"Device.IP.Interface.{ip}.MaxMTUSize":        "1492",
			"Device.IP.Interface.{ip}.X_TP_ConnName":     "Internet_PPPoE",
			"Device.IP.Interface.{ip}.X_TP_ConnType":     "PPPoE",
			"Device.IP.Interface.{ip}.X_TP_IPv6AddrType": "SLAAC",
			"Device.IP.Interface.{ip}.X_TP_ServiceType":  "Internet",
		}},
		{Kind: schema.StepAddObject, Object: "Device.NAT.InterfaceSetting.", As: "nat"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.NAT.InterfaceSetting.{nat}.Interface": "Device.IP.Interface.{ip}.",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.IP.Interface.{ip}.IPv6Enable": "1",
		}},
		{Kind: schema.StepAddObject, Object: "Device.DHCPv6.Client.", As: "dhcpv6"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.Interface": "Device.IP.Interface.{ip}.",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.RequestAddresses":    "0",
			"Device.DHCPv6.Client.{dhcpv6}.RequestPrefixes":     "1",
			"Device.DHCPv6.Client.{dhcpv6}.RequestedOptions":    "23",
			"Device.DHCPv6.Client.{dhcpv6}.X_TP_EnableRaRouter": "1",
			"Device.DHCPv6.Client.{dhcpv6}.X_TP_EnableSLAAC":    "1",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.X_TP_DefaultGateway.IPv4DefaultGatewayType":   "Manual",
			"Device.X_TP_DefaultGateway.CustomIPv4DefaultGateway": "Internet_PPPoE",
			"Device.X_TP_DefaultGateway.IPv6DefaultGatewayType":   "Manual",
			"Device.X_TP_DefaultGateway.CustomIPv6DefaultGateway": "Internet_PPPoE",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.Enable":           "1",
			"Device.Ethernet.Link.{eth}.Enable":              "1",
			"Device.Ethernet.VLANTermination.{vterm}.Enable": "1",
			"Device.IP.Interface.{ip}.Enable":                "1",
			"Device.NAT.InterfaceSetting.{nat}.Enable":       "1",
			"Device.PPP.Interface.{ppp}.Enable":              "1",
			"Device.X_TP_GPON.Link." + gi + ".Enable":        "1",
			"Device.DeviceInfo.ProvisioningCode":             "helix.rPPP",
		}},
	}...)

	flow := &schema.ProvisionFlow{ID: "legacy_wan_pppoe", Steps: steps}
	initVars := map[string]string{}
	if gponIdx > 0 {
		initVars["gpon_idx"] = gi
	}

	return &WANProvision{
		t:   t,
		exe: schema.NewProvisionExecutor(flow, nil, initVars),
	}
}

func newWANProvisionDeleteAndAdd(
	t *task.Task,
	ipIfaceIdx, pppIfaceIdx, vlanTermIdx, ethLinkIdx, gponIdx,
	vlanID int,
	username, password string,
) *WANProvision {
	v := strconv.Itoa(vlanID)
	gi := strconv.Itoa(gponIdx)

	var steps []schema.ProvisionStep

	// Phase 1: Delete
	steps = append(steps,
		schema.ProvisionStep{Kind: schema.StepDelete, Object: fmt.Sprintf("Device.IP.Interface.%d.", ipIfaceIdx)},
		schema.ProvisionStep{Kind: schema.StepDelete, Object: fmt.Sprintf("Device.PPP.Interface.%d.", pppIfaceIdx)},
		schema.ProvisionStep{Kind: schema.StepDelete, Object: fmt.Sprintf("Device.Ethernet.VLANTermination.%d.", vlanTermIdx)},
		schema.ProvisionStep{Kind: schema.StepDelete, Object: fmt.Sprintf("Device.Ethernet.Link.%d.", ethLinkIdx)},
	)

	// Phase 2: Recreate (same as newWANProvision but always reuses GPON)
	steps = append(steps, []schema.ProvisionStep{
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.X_TP_GPON.Link." + gi + ".Alias":       "TpLink_" + v,
			"Device.X_TP_GPON.Link." + gi + ".LowerLayers": "Device.Optical.Interface.1.",
		}},
		{Kind: schema.StepAddObject, Object: "Device.Ethernet.Link.", As: "eth"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.Ethernet.Link.{eth}.Alias":       "ethlink_" + v,
			"Device.Ethernet.Link.{eth}.Enable":      "1",
			"Device.Ethernet.Link.{eth}.LowerLayers": "Device.X_TP_GPON.Link." + gi + ".",
		}},
		{Kind: schema.StepAddObject, Object: "Device.Ethernet.VLANTermination.", As: "vterm"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.Ethernet.VLANTermination.{vterm}.Alias":       "VLAN_" + v,
			"Device.Ethernet.VLANTermination.{vterm}.Enable":      "1",
			"Device.Ethernet.VLANTermination.{vterm}.LowerLayers": "Device.Ethernet.Link.{eth}.",
			"Device.Ethernet.VLANTermination.{vterm}.VLANID":      v,
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_MulticastStatus": "1",
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_VLANEnable":      "1",
			"Device.Ethernet.VLANTermination.{vterm}.X_TP_VLANMode":        "2",
		}},
		{Kind: schema.StepAddObject, Object: "Device.PPP.Interface.", As: "ppp"},
		{Kind: schema.StepAddObject, Object: "Device.IP.Interface.", As: "ip"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.IP.Interface.{ip}.LowerLayers": "Device.PPP.Interface.{ppp}.",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.PPP.Interface.{ppp}.Alias":                     "Internet_PPPoE",
			"Device.PPP.Interface.{ppp}.AuthenticationProtocol":    "AUTO_AUTH",
			"Device.PPP.Interface.{ppp}.LowerLayers":               "Device.Ethernet.VLANTermination.{vterm}.",
			"Device.PPP.Interface.{ppp}.Password":                  password,
			"Device.PPP.Interface.{ppp}.Username":                  username,
			"Device.PPP.Interface.{ppp}.X_TP_UsernameDomainEnable": "1",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.IP.Interface.{ip}.Alias":             "Internet_PPPoE",
			"Device.IP.Interface.{ip}.IPv4Enable":        "1",
			"Device.IP.Interface.{ip}.MaxMTUSize":        "1492",
			"Device.IP.Interface.{ip}.X_TP_ConnName":     "Internet_PPPoE",
			"Device.IP.Interface.{ip}.X_TP_ConnType":     "PPPoE",
			"Device.IP.Interface.{ip}.X_TP_IPv6AddrType": "SLAAC",
			"Device.IP.Interface.{ip}.X_TP_ServiceType":  "Internet",
		}},
		{Kind: schema.StepAddObject, Object: "Device.NAT.InterfaceSetting.", As: "nat"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.NAT.InterfaceSetting.{nat}.Interface": "Device.IP.Interface.{ip}.",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.IP.Interface.{ip}.IPv6Enable": "1",
		}},
		{Kind: schema.StepAddObject, Object: "Device.DHCPv6.Client.", As: "dhcpv6"},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.Interface": "Device.IP.Interface.{ip}.",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.RequestAddresses":    "0",
			"Device.DHCPv6.Client.{dhcpv6}.RequestPrefixes":     "1",
			"Device.DHCPv6.Client.{dhcpv6}.RequestedOptions":    "23",
			"Device.DHCPv6.Client.{dhcpv6}.X_TP_EnableRaRouter": "1",
			"Device.DHCPv6.Client.{dhcpv6}.X_TP_EnableSLAAC":    "1",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.X_TP_DefaultGateway.IPv4DefaultGatewayType":   "Manual",
			"Device.X_TP_DefaultGateway.CustomIPv4DefaultGateway": "Internet_PPPoE",
			"Device.X_TP_DefaultGateway.IPv6DefaultGatewayType":   "Manual",
			"Device.X_TP_DefaultGateway.CustomIPv6DefaultGateway": "Internet_PPPoE",
		}},
		{Kind: schema.StepSet, Params: map[string]string{
			"Device.DHCPv6.Client.{dhcpv6}.Enable":           "1",
			"Device.Ethernet.Link.{eth}.Enable":              "1",
			"Device.Ethernet.VLANTermination.{vterm}.Enable": "1",
			"Device.IP.Interface.{ip}.Enable":                "1",
			"Device.NAT.InterfaceSetting.{nat}.Enable":       "1",
			"Device.PPP.Interface.{ppp}.Enable":              "1",
			"Device.X_TP_GPON.Link." + gi + ".Enable":        "1",
			"Device.DeviceInfo.ProvisioningCode":             "helix.rPPP",
		}},
	}...)
	flow := &schema.ProvisionFlow{ID: "legacy_wan_delete_add", Steps: steps}
	initVars := map[string]string{"gpon_idx": gi}

	return &WANProvision{
		t:   t,
		exe: schema.NewProvisionExecutor(flow, nil, initVars),
	}
}
