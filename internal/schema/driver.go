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
	Model       string            `yaml:"model"` // data-model: "tr181" or "tr098"
	DeviceModel string            `yaml:"device_model,omitempty"` // specific ONT model, e.g. "XC220-G3v"
	Description string            `yaml:"description,omitempty"`
	Features    map[string]bool   `yaml:"features,omitempty"`
	Config      map[string]string `yaml:"config,omitempty"`

	SecurityModes map[string]string `yaml:"security_modes,omitempty"`

	WiFi      WiFiDriverYAML      `yaml:"wifi,omitempty"`
	Discovery DiscoveryYAML       `yaml:"discovery,omitempty"`
	Provisions map[string]string  `yaml:"provisions,omitempty"` // flow-name → filename
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
	WANTypePath   string            `yaml:"wan_type_path,omitempty"`   // e.g. "Device.IP.Interface.{i}.X_TP_ConnType"
	WANTypeValues DiscoveryWANTypes `yaml:"wan_type_values,omitempty"`

	// GPON/optical link detection
	GPONEnablePath string `yaml:"gpon_enable_path,omitempty"` // e.g. "Device.X_TP_GPON.Link.{i}.Enable"

	// WAN uptime (vendor-specific)
	WANUptimePath      string `yaml:"wan_uptime_path,omitempty"`       // e.g. "Device.IP.Interface.{i}.X_TP_Uptime"
	WANServiceTypePath string `yaml:"wan_service_type_path,omitempty"` // e.g. "Device.IP.Interface.{i}.X_TP_ServiceType"

	// Connected host type detection
	HostConnTypePath   string            `yaml:"host_conn_type_path,omitempty"` // e.g. "Hosts.Host.{i}.X_TP_LanConnType"
	HostConnTypeValues map[string]string `yaml:"host_conn_type_values,omitempty"` // "wifi" → "1", "lan" → "0"
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

// Resolve returns the best-matching DeviceDriver for a device.
//
// Resolution priority (highest first):
//  1. vendor/<vendor>/<model>/<dataModel>   — model-specific
//  2. vendor/<vendor>/<dataModel>           — vendor default
//
// Returns nil when no driver is registered for the device.
func (r *DeviceDriverRegistry) Resolve(vendor, model, dataModel string) *DeviceDriver {
	if r == nil {
		return nil
	}

	vendorSlug := normaliseVendor(vendor)
	modelSlug := normaliseModel(model)

	// Priority 1: model-specific driver
	if vendorSlug != "" && modelSlug != "" {
		key := "vendor/" + vendorSlug + "/" + modelSlug + "/" + dataModel
		if d, ok := r.drivers[key]; ok {
			return d
		}
	}

	// Priority 2: vendor default driver
	if vendorSlug != "" {
		key := "vendor/" + vendorSlug + "/" + dataModel
		if d, ok := r.drivers[key]; ok {
			return d
		}
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
