package schema

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML types for provision flow files
// ---------------------------------------------------------------------------

// ProvisionFlowYAML is the on-disk representation of a provision flow file
// (e.g. provision_wan.yaml).
type ProvisionFlowYAML struct {
	ID          string              `yaml:"id"`
	Vendor      string              `yaml:"vendor,omitempty"`
	Model       string              `yaml:"model,omitempty"`
	Description string              `yaml:"description,omitempty"`
	Steps       []ProvisionStepYAML `yaml:"steps"`
}

// ProvisionStepYAML is one step in a provisioning sequence.
type ProvisionStepYAML struct {
	Kind   string            `yaml:"kind"`              // "add_object", "set", "delete"
	When   string            `yaml:"when,omitempty"`     // condition: "gpon_reuse", "feature_nat", etc.
	Object string            `yaml:"object,omitempty"`   // for add_object/delete: object path
	As     string            `yaml:"as,omitempty"`       // for add_object: variable to store instance number
	Params map[string]string `yaml:"params,omitempty"`   // for set: key→value templates
}

// ---------------------------------------------------------------------------
// ProvisionFlow — loaded runtime representation
// ---------------------------------------------------------------------------

// ProvisionFlow is a parsed sequence of provisioning steps.
type ProvisionFlow struct {
	ID          string
	Description string
	Steps       []ProvisionStep
}

// ProvisionStep is one executable step.
type ProvisionStep struct {
	Kind   ProvisionStepKind
	When   string            // condition expression
	Object string            // for AddObject/Delete
	As     string            // variable name for AddObject result
	Params map[string]string // for Set: param templates
}

// ProvisionStepKind enumerates step types.
type ProvisionStepKind int

const (
	StepAddObject ProvisionStepKind = iota
	StepSet
	StepDelete
)

// LoadProvisionFlowFile reads and parses a provision flow YAML file.
func LoadProvisionFlowFile(path string) (*ProvisionFlow, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read provision flow %q: %w", path, err)
	}
	var raw ProvisionFlowYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse provision flow %q: %w", path, err)
	}

	steps := make([]ProvisionStep, 0, len(raw.Steps))
	for i, s := range raw.Steps {
		kind, err := parseStepKind(s.Kind)
		if err != nil {
			return nil, fmt.Errorf("provision flow %q step %d: %w", path, i, err)
		}
		steps = append(steps, ProvisionStep{
			Kind:   kind,
			When:   s.When,
			Object: s.Object,
			As:     s.As,
			Params: s.Params,
		})
	}

	return &ProvisionFlow{
		ID:          raw.ID,
		Description: raw.Description,
		Steps:       steps,
	}, nil
}

func parseStepKind(s string) (ProvisionStepKind, error) {
	switch strings.ToLower(s) {
	case "add_object", "add":
		return StepAddObject, nil
	case "set", "set_params":
		return StepSet, nil
	case "delete", "delete_object":
		return StepDelete, nil
	default:
		return 0, fmt.Errorf("unknown step kind %q", s)
	}
}

// ---------------------------------------------------------------------------
// ProvisionExecutor — runtime step-by-step executor
// ---------------------------------------------------------------------------

// ResolvedStep is a fully-resolved provision step ready for the CWMP layer to
// convert into a SOAP envelope. It avoids a circular dependency between the
// schema package and the cwmp package: the schema layer resolves variables and
// conditions, and the cwmp layer converts the result into XML.
type ResolvedStep struct {
	Kind   ProvisionStepKind
	Object string            // resolved object path (for AddObject/Delete)
	Params map[string]string // resolved params (for Set)
}

// ProvisionExecutor runs a ProvisionFlow step-by-step during a CWMP session.
// It tracks the current position, runtime variables (instance numbers from
// AddObject responses), and evaluates conditional steps.
//
// The executor does NOT build XML directly — it produces ResolvedStep values
// that the cwmp package converts into SOAP envelopes, avoiding a circular
// import.
type ProvisionExecutor struct {
	flow     *ProvisionFlow
	driver   *DeviceDriver
	vars     map[string]string // runtime variables: {vlan_id}, {eth}, {ppp}, etc.
	features map[string]bool   // evaluated conditions
	cur      int               // current step index (after skipping)
}

// NewProvisionExecutor creates a new executor for the given flow.
//
// inputVars are the initial runtime variables (e.g. from task payload):
//
//	"vlan_id"   → "100"
//	"username"  → "user@isp"
//	"password"  → "secret"
//	"gpon_idx"  → "3"
//
// The driver's Config values are also merged in as variables so templates
// like {ppp_auth_protocol} and {default_mtu} resolve automatically.
func NewProvisionExecutor(flow *ProvisionFlow, driver *DeviceDriver, inputVars map[string]string) *ProvisionExecutor {
	vars := make(map[string]string)

	// Merge driver config as base variables.
	if driver != nil && driver.Config != nil {
		for k, v := range driver.Config {
			vars[k] = v
		}
	}

	// Merge input vars (override driver config if same key).
	for k, v := range inputVars {
		vars[k] = v
	}

	// Build feature map from driver + input conditions.
	features := make(map[string]bool)
	if driver != nil {
		for k, v := range driver.Features {
			features["feature_"+k] = v
		}
	}
	// Runtime conditions based on input vars.
	if gpon, ok := vars["gpon_idx"]; ok && gpon != "" && gpon != "0" {
		features["gpon_reuse"] = true
		features["gpon_create"] = false
	} else {
		features["gpon_reuse"] = false
		features["gpon_create"] = true
	}
	for k, v := range vars {
		if strings.TrimSpace(v) != "" && v != "0" {
			features["has_"+k] = true
		}
	}

	pe := &ProvisionExecutor{
		flow:     flow,
		driver:   driver,
		vars:     vars,
		features: features,
		cur:      0,
	}

	// Skip initial non-matching conditions.
	pe.skipInactive()
	return pe
}

// Done reports whether all steps have been executed.
func (pe *ProvisionExecutor) Done() bool {
	return pe.cur >= len(pe.flow.Steps)
}

// CurrentStep returns the resolved current step ready for the cwmp layer
// to convert into XML. Returns nil if done.
func (pe *ProvisionExecutor) CurrentStep() *ResolvedStep {
	if pe.Done() {
		return nil
	}
	s := pe.flow.Steps[pe.cur]
	return &ResolvedStep{
		Kind:   s.Kind,
		Object: pe.substitute(s.Object),
		Params: pe.resolveParams(s.Params),
	}
}

// Advance moves past the current step. For AddObject steps, call
// AdvanceAddObject instead to record the instance number.
func (pe *ProvisionExecutor) Advance() {
	if !pe.Done() {
		pe.cur++
		pe.skipInactive()
	}
}

// AdvanceAddObject records the new instance number and advances to the next
// active step. Panics if the current step is not an AddObject.
func (pe *ProvisionExecutor) AdvanceAddObject(instanceNum int) {
	if pe.Done() {
		return
	}
	s := pe.flow.Steps[pe.cur]
	if s.As != "" {
		pe.vars[s.As] = fmt.Sprintf("%d", instanceNum)
	}
	pe.cur++
	pe.skipInactive()
}

// StepIndex returns the current step index.
func (pe *ProvisionExecutor) StepIndex() int { return pe.cur }

// TotalSteps returns the total number of steps in the flow.
func (pe *ProvisionExecutor) TotalSteps() int { return len(pe.flow.Steps) }

// Vars returns the current variable map (read-only view for logging).
func (pe *ProvisionExecutor) Vars() map[string]string {
	out := make(map[string]string, len(pe.vars))
	for k, v := range pe.vars {
		out[k] = v
	}
	return out
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// skipInactive advances pe.cur past any steps whose When condition is not met.
func (pe *ProvisionExecutor) skipInactive() {
	for pe.cur < len(pe.flow.Steps) {
		s := pe.flow.Steps[pe.cur]
		if s.When == "" || pe.evalCondition(s.When) {
			return // this step is active
		}
		pe.cur++
	}
}

// evalCondition evaluates a simple condition string.
// Supported forms:
//   - "feature_gpon"       → true if driver feature "gpon" is enabled
//   - "gpon_reuse"         → true if gpon_idx was provided (>0)
//   - "gpon_create"        → true if gpon_idx was not provided
//   - "has_username"       → true if input var "username" is non-empty/non-zero
//   - "!feature_gpon"      → negation
func (pe *ProvisionExecutor) evalCondition(cond string) bool {
	cond = strings.TrimSpace(cond)
	if cond == "" {
		return true
	}

	negate := false
	if strings.HasPrefix(cond, "!") {
		negate = true
		cond = strings.TrimPrefix(cond, "!")
	}

	result := pe.features[cond]

	if negate {
		return !result
	}
	return result
}

// substitute replaces all {placeholder} tokens in s with runtime variables.
func (pe *ProvisionExecutor) substitute(s string) string {
	for name, val := range pe.vars {
		s = strings.ReplaceAll(s, "{"+name+"}", val)
	}
	return s
}

// resolveParams substitutes all {placeholder} tokens in both keys and values.
// Entries whose resolved value is an empty string are omitted so that optional
// variables (e.g. username / password not provided by the caller) do not result
// in clearing the parameter on the device.
func (pe *ProvisionExecutor) resolveParams(raw map[string]string) map[string]string {
	if raw == nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		resolvedKey := pe.substitute(k)
		resolvedVal := pe.substitute(v)
		if resolvedVal == "" {
			continue // skip params whose value resolves to empty
		}
		out[resolvedKey] = resolvedVal
	}
	return out
}
