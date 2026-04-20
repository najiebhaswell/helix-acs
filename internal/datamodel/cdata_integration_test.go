package datamodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ────────────────────────────────────────────────────────────────────────
// C-DATA TR-098 Comprehensive Integration Tests
// ────────────────────────────────────────────────────────────────────────

// TestCDATA_FullParameterSet verifies that C-DATA device parameters can be
// completely discovered and extracted. This simulates a real C-DATA GPON ONU
// sending all parameters via Inform.
func TestCDATA_FullParameterSet(t *testing.T) {
	// Simulated C-DATA FD514GD-R460 device parameters
	params := map[string]string{
		// Device info
		"InternetGatewayDevice.DeviceInfo.Manufacturer":       "ZTEG",
		"InternetGatewayDevice.DeviceInfo.ModelName":          "FD514GD-R460",
		"InternetGatewayDevice.DeviceInfo.SerialNumber":       "CDTCAF252D7F",
		"InternetGatewayDevice.DeviceInfo.ManufacturerOUI":    "00188F",
		"InternetGatewayDevice.DeviceInfo.ProductClass":       "GPON ONU",
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion":    "1.0.0",
		"InternetGatewayDevice.DeviceInfo.HardwareVersion":    "R460",
		"InternetGatewayDevice.DeviceInfo.UpTime":             "86400",
		"InternetGatewayDevice.DeviceInfo.MemoryStatus.Total": "134217728",
		"InternetGatewayDevice.DeviceInfo.MemoryStatus.Free":  "67108864",

		// LAN settings
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress":  "192.168.100.1",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceSubnetMask": "255.255.255.0",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerEnable":                    "1",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.MinAddress":                          "192.168.100.50",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.MaxAddress":                          "192.168.100.200",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DNSServers":                          "8.8.8.8,8.8.4.4",

		// WiFi 2.4GHz
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID":              "C-DATA-GPON",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Enable":            "1",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel":           "6",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BeaconType":        "WPA2",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Standard":          "802.11n",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BSSID":             "AA:BB:CC:DD:EE:FF",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.TransmitPower":     "100",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.TotalAssociations": "2",

		// WiFi 5GHz
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID":              "C-DATA-GPON-5G",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Enable":            "1",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Channel":           "36",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BeaconType":        "WPA2",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Standard":          "802.11ac",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BSSID":             "AA:BB:CC:DD:EE:11",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.TransmitPower":     "100",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.TotalAssociations": "1",

		// WAN - PPPoE
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username":          "user@isp.com",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionType":    "PPPoE",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionStatus":  "Connected",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress": "203.0.113.50",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DefaultGateway":    "203.0.113.1",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DNSServers":        "1.1.1.1,1.0.0.1",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MaxMRUSize":        "1500",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Uptime":            "3600",

		// WAN stats
		"InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.TotalBytesSent":       "1000000",
		"InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.TotalBytesReceived":   "2000000",
		"InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.TotalPacketsSent":     "5000",
		"InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.TotalPacketsReceived": "10000",

		// Management server
		"InternetGatewayDevice.ManagementServer.URL":                    "http://localhost:8080",
		"InternetGatewayDevice.ManagementServer.Username":               "admin",
		"InternetGatewayDevice.ManagementServer.Password":               "acs123",
		"InternetGatewayDevice.ManagementServer.PeriodicInformInterval": "300",
		"InternetGatewayDevice.ManagementServer.PeriodicInformEnable":   "1",

		// Connected hosts
		"InternetGatewayDevice.LANDevice.1.Hosts.HostNumberOfEntries": "2",
		"InternetGatewayDevice.LANDevice.1.Hosts.Host.1.IPAddress":    "192.168.100.100",
		"InternetGatewayDevice.LANDevice.1.Hosts.Host.1.MACAddress":   "11:22:33:44:55:66",
		"InternetGatewayDevice.LANDevice.1.Hosts.Host.2.IPAddress":    "192.168.100.101",
		"InternetGatewayDevice.LANDevice.1.Hosts.Host.2.MACAddress":   "11:22:33:44:55:77",

		// Port mapping
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.PortMappingNumberOfEntries":   "1",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.PortMapping.1.ExternalPort":   "8080",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.PortMapping.1.InternalPort":   "80",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.PortMapping.1.Protocol":       "TCP",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.PortMapping.1.InternalClient": "192.168.100.100",
	}

	// Detect model type
	mapper := &TR098Mapper{}
	modelType := mapper.DetectModel(params)
	assert.Equal(t, TR098, modelType, "C-DATA should be detected as TR-098")

	// Discover instances
	im := DiscoverInstances(params)
	assert.Equal(t, 1, im.WANDeviceIdx, "should discover WAN device index")
	assert.Equal(t, 1, im.WANConnDevIdx, "should discover WAN connection device index")
	assert.Equal(t, 1, im.WANPPPConnIdx, "should discover WAN PPP connection index")
	assert.Equal(t, 1, im.LANDeviceIdx, "should discover LAN device index")
	assert.Equal(t, []int{1, 2}, im.WLANIndices, "should discover both WiFi bands")

	// Extract device info
	dev := mapper.ExtractDeviceInfo(params)
	require.NotNil(t, dev)
	assert.Equal(t, "ZTEG", dev.Manufacturer)
	assert.Equal(t, "FD514GD-R460", dev.ModelName)
	assert.Equal(t, "CDTCAF252D7F", dev.Serial)
	assert.Equal(t, "00188F", dev.OUI)
	assert.Equal(t, "GPON ONU", dev.ProductClass)
	assert.Equal(t, "1.0.0", dev.SWVersion)
	assert.Equal(t, "R460", dev.HWVersion)

	// Verify critical paths are accessible
	assert.Equal(t, "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username", mapper.WANPPPoEUserPath())
	assert.Equal(t, "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password", mapper.WANPPPoEPassPath())
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID", mapper.WiFiSSIDPath(0))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID", mapper.WiFiSSIDPath(1))
	assert.Equal(t, "InternetGatewayDevice.ManagementServer.URL", mapper.ManagementServerURLPath())
}

// TestCDATA_InstanceDiscovery verifies that C-DATA instance indices are
// correctly discovered from device parameters.
func TestCDATA_InstanceDiscovery(t *testing.T) {
	params := map[string]string{
		// Multiple WAN devices (unlikely but possible)
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress": "203.0.113.1",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username":         "user@isp",

		// LANDevice
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress": "192.168.1.1",

		// WLAN configurations
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID": "WIFI-2G",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID": "WIFI-5G",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.3.SSID": "WIFI-6",
	}

	im := DiscoverInstances(params)

	// Verify WAN discovery
	assert.Equal(t, 1, im.WANDeviceIdx)
	assert.Equal(t, 1, im.WANConnDevIdx)
	assert.Equal(t, 1, im.WANIPConnIdx)
	assert.Equal(t, 1, im.WANPPPConnIdx)

	// Verify LAN discovery
	assert.Equal(t, 1, im.LANDeviceIdx)

	// Verify all WLAN indices are discovered (not just the defaults)
	require.Equal(t, 3, len(im.WLANIndices))
	assert.Equal(t, []int{1, 2, 3}, im.WLANIndices)
}

// TestCDATA_ProvisioningFlow verifies that C-DATA uses set_params provisioning
// (single SetParameterValues) instead of multi-step AddObject.
func TestCDATA_ProvisioningFlow(t *testing.T) {
	mapper := &TR098Mapper{}

	// C-DATA should use set_params (pre-existing WAN objects)
	provisioningType := mapper.WANProvisioningType()
	assert.Equal(t, "set_params", provisioningType, "C-DATA should use set_params provisioning")

	// C-DATA has no service type path (returns empty)
	assert.Equal(t, "", mapper.WANServiceTypePath(), "C-DATA has no service type path")

	// C-DATA has no band steering path (returns empty)
	assert.Equal(t, "", mapper.BandSteeringPath(), "C-DATA has no band steering support")
}

// TestCDATA_ParameterTypeValidation verifies parameter type validation for C-DATA.
func TestCDATA_ParameterTypeValidation(t *testing.T) {
	testCases := []struct {
		name  string
		typ   string
		value string
		valid bool
	}{
		// String parameters (always valid)
		{"ssid_string", TypeString, "C-DATA-GPON", true},
		{"empty_string", TypeString, "", true},

		// Boolean parameters
		{"dhcp_enable_0", TypeBoolean, "0", true},
		{"dhcp_enable_1", TypeBoolean, "1", true},
		{"dhcp_enable_true", TypeBoolean, "true", true},
		{"dhcp_enable_false", TypeBoolean, "false", true},
		{"dhcp_enable_invalid", TypeBoolean, "yes", false},

		// UnsignedInt parameters
		{"uptime_valid", TypeUnsignedInt, "86400", true},
		{"uptime_zero", TypeUnsignedInt, "0", true},
		{"uptime_negative", TypeUnsignedInt, "-1", false},
		{"channel_valid", TypeUnsignedInt, "6", true},

		// DateTime (NTP sync timestamp, etc.)
		{"timestamp_valid", TypeDateTime, "2024-01-01T12:00:00Z", true},
		{"timestamp_invalid", TypeDateTime, "not-a-date", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateType(tc.typ, tc.value)
			if tc.valid {
				assert.NoError(t, err, "should validate %q as %q", tc.value, tc.typ)
			} else {
				assert.Error(t, err, "should reject %q as %q", tc.value, tc.typ)
			}
		})
	}
}

// TestCDATA_TR098MapperWithCustomIndices verifies that discovered indices
// are properly applied to generate correct parameter paths.
func TestCDATA_TR098MapperWithCustomIndices(t *testing.T) {
	// If instance discovery finds WANDevice.2 and LANDevice.2
	mapper := &TR098Mapper{
		WANDeviceIdx:  2,
		WANConnDevIdx: 1,
		WANPPPConnIdx: 1,
		LANDeviceIdx:  2,
		WLANIndices:   []int{1, 2},
	}

	// Verify paths use custom indices
	expectedUserPath := "InternetGatewayDevice.WANDevice.2.WANConnectionDevice.1.WANPPPConnection.1.Username"
	assert.Equal(t, expectedUserPath, mapper.WANPPPoEUserPath())

	expectedLANPath := "InternetGatewayDevice.LANDevice.2.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress"
	assert.Equal(t, expectedLANPath, mapper.LANIPAddressPath())

	expectedWiFiPath := "InternetGatewayDevice.LANDevice.2.WLANConfiguration.1.SSID"
	assert.Equal(t, expectedWiFiPath, mapper.WiFiSSIDPath(0))
}
