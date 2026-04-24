// Package schema — driver.go provides the DeviceDriver abstraction.
//
// A DeviceDriver is a YAML-driven configuration bundle for a specific
// vendor (and optionally model) ONT. It defines:
//   - Feature flags (GPON, band steering, IPv6, …)
//   - Device defaults (MTU, PPP auth protocol, …)
//   - Security mode mappings (UI label → TR-069 value)
//   - WiFi vendor-specific config (band steering path, …)
//   - Instance discovery hints (vendor-specific parameter paths)
//   - Provisioning flows (WAN PPPoE steps, etc.)
package schema

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ---------------------------------------------------------------------------
// YAML types
// ---------------------------------------------------------------------------

// DriverYAML is the on-disk representation of a driver.yaml file.
type DriverYAML struct {
	ID          string            `yaml:"id"`
	Vendor      string            `yaml:"vendor"`
	Model       string            `yaml:"model"`                  // data-model: "tr181" or "tr098"
	DeviceModel string            `yaml:"device_model,omitempty"` // specific ONT model, e.g. "XC220-G3v"
	Description string            `yaml:"description,omitempty"`
	Features    map[string]bool   `yaml:"features,omitempty"`
	Config      map[string]string `yaml:"config,omitempty"`

	SecurityModes map[string]string `yaml:"security_modes,omitempty"`

	// DefaultParams are pushed to the CPE once per full-summon cycle (≈ every 2 min)
	// if the current device value differs. Use absolute TR-069 parameter paths.
	// Example: "Device.IP.Interface.4.X_TP_ServiceType": "TR069"
	DefaultParams map[string]string `yaml:"default_params,omitempty"`

	WiFi       WiFiDriverYAML    `yaml:"wifi,omitempty"`
	Discovery  DiscoveryYAML     `yaml:"discovery,omitempty"`
	Provisions map[string]string `yaml:"provisions,omitempty"` // flow-name → filename
}

// WiFiDriverYAML holds vendor-specific WiFi configuration.
type WiFiDriverYAML struct {
	BandSteeringPath    string `yaml:"band_steering_path,omitempty"`
	MultiSSIDSuffix24G  string `yaml:"multi_ssid_suffix_24g,omitempty"`
	MultiSSIDSuffix5G   string `yaml:"multi_ssid_suffix_5g,omitempty"`
	MultiSSIDSuffix6G   string `yaml:"multi_ssid_suffix_6g,omitempty"`
	SyncBandsOnSteering bool   `yaml:"sync_bands_on_steering,omitempty"`
}

// DiscoveryYAML holds vendor-specific instance discovery hints.
type DiscoveryYAML struct {
	// WAN interface type detection
	WANTypePath   string            `yaml:"wan_type_path,omitempty"` // e.g. "Device.IP.Interface.{i}.X_TP_ConnType"
	WANTypeValues DiscoveryWANTypes `yaml:"wan_type_values,omitempty"`

	// GPON/optical link detection
	GPONEnablePath string `yaml:"gpon_enable_path,omitempty"` // e.g. "Device.X_TP_GPON.Link.{i}.Enable"

	// WAN uptime (vendor-specific)
	WANUptimePath      string `yaml:"wan_uptime_path,omitempty"`       // e.g. "Device.IP.Interface.{i}.X_TP_Uptime"
	WANServiceTypePath string `yaml:"wan_service_type_path,omitempty"` // e.g. "Device.IP.Interface.{i}.X_TP_ServiceType"

	// Connected host type detection
	HostConnTypePath   string            `yaml:"host_conn_type_path,omitempty"`   // e.g. "Hosts.Host.{i}.X_TP_LanConnType"
	HostConnTypeValues map[string]string `yaml:"host_conn_type_values,omitempty"` // "wifi" → "1", "lan" → "0"

	// WiFi SSID→band when SSID.LowerLayers is missing (per-model driver YAML).
	WiFiSSIDBandWithoutLowerLayers *WiFiSSIDBandWithoutLowerLayersYAML `yaml:"wifi_ssid_band_without_lower_layers,omitempty"`
}

// WiFiSSIDBandWithoutLowerLayersYAML configures TR-181 SSID instance index → band
// (0=2.4GHz, 1=5GHz, …) for discovery. Built-in strategies (see datamodel):
//   - pair_block_mod2 — band = ((ssidIndex-1)/2)%2 (TP-Link XC220-G3 style)
//   - explicit — list SSID indices per band; use for arbitrary layouts without Go changes
//   - legacy_tplink_multi — historical TP-Link multi-SSID switch
type WiFiSSIDBandWithoutLowerLayersYAML struct {
	Strategy string           `yaml:"strategy,omitempty"`
	Explicit map[string][]int `yaml:"explicit,omitempty"` // keys "0","1","2" → SSID indices
}

// DiscoveryWANTypes maps semantic roles to parameter values.
type DiscoveryWANTypes struct {
	WAN    []string `yaml:"wan,omitempty"`    // e.g. ["PPPoE", "DHCP", "Static"]
	LAN    []string `yaml:"lan,omitempty"`    // e.g. ["LAN"]
	Bridge []string `yaml:"bridge,omitempty"` // e.g. ["Bridge"]
}

// ---------------------------------------------------------------------------
// DeviceDriver — runtime representation
// ---------------------------------------------------------------------------

// DeviceDriver is the loaded, ready-to-use driver for a device.
type DeviceDriver struct {
	ID          string
	Vendor      string
	Model       string // data-model
	DeviceModel string // specific ONT model (optional)
	Description string

	Features      map[string]bool
	Config        map[string]string
	SecurityModes map[string]string
	// DefaultParams are enforced on every full-summon cycle.
	// Keys are absolute TR-069 paths; values are the required values.
	DefaultParams map[string]string
	WiFi          WiFiDriverYAML
	Discovery     DiscoveryYAML
	Provisions    map[string]*ProvisionFlow // loaded step sequences
}

// HasFeature returns true if the driver declares the given feature as enabled.
func (d *DeviceDriver) HasFeature(name string) bool {
	if d == nil || d.Features == nil {
		return false
	}
	return d.Features[name]
}

// ConfigValue returns the driver config value for the given key, or fallback
// if not set.
func (d *DeviceDriver) ConfigValue(key, fallback string) string {
	if d == nil || d.Config == nil {
		return fallback
	}
	if v, ok := d.Config[key]; ok && v != "" {
		return v
	}
	return fallback
}

// MapSecurityMode maps a UI security mode label to the TR-069 value.
// Returns the input unchanged if no mapping is defined.
func (d *DeviceDriver) MapSecurityMode(uiMode string) string {
	if d == nil || d.SecurityModes == nil {
		return uiMode
	}
	if mapped, ok := d.SecurityModes[uiMode]; ok {
		return mapped
	}
	return uiMode
}

// GetProvisionFlow returns the provision flow for the given name (e.g. "wan_pppoe").
// Returns nil if not found.
func (d *DeviceDriver) GetProvisionFlow(name string) *ProvisionFlow {
	if d == nil || d.Provisions == nil {
		return nil
	}
	return d.Provisions[name]
}

// ---------------------------------------------------------------------------
// DeviceDriverRegistry — load and resolve drivers
// ---------------------------------------------------------------------------

// DeviceDriverRegistry holds all loaded drivers indexed by their resolved name.
//
// Driver names follow these conventions:
//   - Vendor default:   "vendor/tplink/tr181"
//   - Model-specific:   "vendor/tplink/XC220-G3v/tr181"
//
// Each name maps to exactly one DeviceDriver.
type DeviceDriverRegistry struct {
	drivers map[string]*DeviceDriver
}

// NewDeviceDriverRegistry returns an empty registry.
func NewDeviceDriverRegistry() *DeviceDriverRegistry {
	return &DeviceDriverRegistry{drivers: make(map[string]*DeviceDriver)}
}

// LoadDir walks the given schemas directory and loads every driver.yaml file.
// It also loads provision YAML files referenced by each driver.
//
// Expected directory structure:
//
//	<root>/vendors/<vendor>/tr181/driver.yaml              → "vendor/<vendor>/tr181"
//	<root>/vendors/<vendor>/models/<model>/tr181/driver.yaml → "vendor/<vendor>/<model>/tr181"
func (r *DeviceDriverRegistry) LoadDir(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk driver dir: %w", err)
		}
		if d.IsDir() || d.Name() != "driver.yaml" {
			return nil
		}

		driver, lerr := LoadDriverFileWithProvisions(path)
		if lerr != nil {
			return lerr
		}

		name := driverNameFromPath(root, path)
		r.drivers[name] = driver
		return nil
	})
}

// LoadDriverFileWithProvisions loads a driver and its provision flows from disk.
// This is the full self-contained loader used by DeviceDriverRegistry.LoadDir.
func LoadDriverFileWithProvisions(path string) (*DeviceDriver, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read driver file %q: %w", path, err)
	}
	var raw DriverYAML
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse driver file %q: %w", path, err)
	}

	driver := &DeviceDriver{
		ID:            raw.ID,
		Vendor:        raw.Vendor,
		Model:         raw.Model,
		DeviceModel:   raw.DeviceModel,
		Description:   raw.Description,
		Features:      raw.Features,
		Config:        raw.Config,
		SecurityModes: raw.SecurityModes,
		DefaultParams: raw.DefaultParams,
		WiFi:          raw.WiFi,
		Discovery:     raw.Discovery,
		Provisions:    make(map[string]*ProvisionFlow),
	}

	// Load referenced provision flows from the same directory.
	dir := filepath.Dir(path)
	for flowName, filename := range raw.Provisions {
		flowPath := filepath.Join(dir, filename)
		flow, ferr := LoadProvisionFlowFile(flowPath)
		if ferr != nil {
			return nil, fmt.Errorf("load provision flow %q for driver %q: %w", flowName, driver.ID, ferr)
		}
		driver.Provisions[flowName] = flow
	}

	return driver, nil
}

// driverNameFromPath derives the driver registry name from a file path.
//
// Examples (root = "./schemas"):
//
//	./schemas/vendors/tplink/tr181/driver.yaml                    → "vendor/tplink/tr181"
//	./schemas/vendors/tplink/models/XC220-G3v/tr181/driver.yaml   → "vendor/tplink/XC220-G3v/tr181"
//	./schemas/vendors/huawei/tr181/driver.yaml                    → "vendor/huawei/tr181"
func driverNameFromPath(root, path string) string {
	root = filepath.ToSlash(filepath.Clean(root))
	path = filepath.ToSlash(filepath.Clean(path))
	rel := strings.TrimPrefix(path, root+"/")

	// Strip the filename (driver.yaml), keep directory.
	dir := filepath.ToSlash(filepath.Dir(rel))

	// "vendors/tplink/tr181" → "vendor/tplink/tr181"
	// "vendors/tplink/models/XC220-G3v/tr181" → "vendor/tplink/XC220-G3v/tr181"
	parts := strings.Split(dir, "/")

	if len(parts) >= 2 && parts[0] == "vendors" {
		vendor := parts[1]

		// Check for models/<model>/dataModel pattern
		if len(parts) >= 5 && parts[2] == "models" {
			model := parts[3]
			dataModel := parts[4]
			return "vendor/" + vendor + "/" + model + "/" + dataModel
		}

		// Vendor default: vendors/<vendor>/<dataModel>
		if len(parts) >= 3 {
			dataModel := parts[2]
			return "vendor/" + vendor + "/" + dataModel
		}
	}

	return dir
}

// mergedDefaultParams combines vendor-level default_params with a model-specific
// driver: vendor entries apply first, then model entries override.
func mergedDefaultParams(vendor, model *DeviceDriver) map[string]string {
	if vendor == nil || len(vendor.DefaultParams) == 0 {
		if model == nil {
			return nil
		}
		return model.DefaultParams
	}
	if model == nil {
		return maps.Clone(vendor.DefaultParams)
	}
	if len(model.DefaultParams) == 0 {
		return maps.Clone(vendor.DefaultParams)
	}
	out := make(map[string]string, len(vendor.DefaultParams)+len(model.DefaultParams))
	for k, v := range vendor.DefaultParams {
		out[k] = v
	}
	for k, v := range model.DefaultParams {
		out[k] = v
	}
	return out
}

// Resolve returns the best-matching DeviceDriver for a device.
//
// Resolution priority (highest first):
//  1. vendor/<vendor>/<model>/<dataModel>   — model-specific
//  2. vendor/<vendor>/<dataModel>           — vendor default
//
// When a model-specific driver is used, default_params from the vendor default
// driver are merged underneath model default_params (model wins on key conflict).
//
// Returns nil when no driver is registered for the device.
func (r *DeviceDriverRegistry) Resolve(vendor, model, dataModel string) *DeviceDriver {
	if r == nil {
		return nil
	}

	vendorSlug := normaliseVendor(vendor)
	modelSlug := normaliseModel(model)

	var vendorDrv *DeviceDriver
	if vendorSlug != "" {
		vendorDrv = r.drivers["vendor/"+vendorSlug+"/"+dataModel]
	}

	// Priority 1: model-specific driver
	if vendorSlug != "" && modelSlug != "" {
		modelKey := "vendor/" + vendorSlug + "/" + modelSlug + "/" + dataModel
		if chosen, ok := r.drivers[modelKey]; ok {
			effective := mergedDefaultParams(vendorDrv, chosen)
			if maps.Equal(effective, chosen.DefaultParams) {
				return chosen
			}
			merged := *chosen
			merged.DefaultParams = effective
			return &merged
		}
	}

	// Priority 2: vendor default driver
	if vendorDrv != nil {
		return vendorDrv
	}

	return nil
}

// Has returns true when a driver is registered under the given name.
func (r *DeviceDriverRegistry) Has(name string) bool {
	if r == nil {
		return false
	}
	_, ok := r.drivers[name]
	return ok
}

// All returns all loaded drivers (for debug/admin purposes).
func (r *DeviceDriverRegistry) All() map[string]*DeviceDriver {
	if r == nil {
		return nil
	}
	return r.drivers
}

// normaliseModel converts a product class / model name to a filesystem-safe slug.
//
// Examples:
//
//	"XC220-G3v"  → "XC220-G3v"   (kept as-is, dashes are safe)
//	"HG8245H"    → "HG8245H"
//	""           → ""
func normaliseModel(productClass string) string {
	s := strings.TrimSpace(productClass)
	if s == "" {
		return ""
	}
	// Remove characters that are not safe for filesystem paths.
	// Keep alphanumeric, dash, underscore, dot.
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '-' || r == '_' || r == '.' {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}
