package datamodel

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// DetectFromRootObject

func TestDetectFromRootObject(t *testing.T) {
	assert.Equal(t, TR181, DetectFromRootObject("Device."))
	assert.Equal(t, TR181, DetectFromRootObject("Device"))
	assert.Equal(t, TR098, DetectFromRootObject("InternetGatewayDevice."))
	assert.Equal(t, TR098, DetectFromRootObject("InternetGatewayDevice"))
	assert.Equal(t, TR098, DetectFromRootObject(""))
	assert.Equal(t, TR098, DetectFromRootObject("unknown"))
}

// TR181Mapper path methods

func TestTR181Mapper(t *testing.T) {
	m := &TR181Mapper{}

	// WiFi band 0 (2.4 GHz)
	assert.Equal(t, "Device.WiFi.SSID.1.SSID", m.WiFiSSIDPath(0))
	assert.Equal(t, "Device.WiFi.AccessPoint.1.Security.KeyPassphrase", m.WiFiPasswordPath(0))
	assert.Equal(t, "Device.WiFi.SSID.1.Enable", m.WiFiEnabledPath(0))
	assert.Equal(t, "Device.WiFi.Radio.1.Channel", m.WiFiChannelPath(0))

	// WiFi band 1 (5 GHz)
	assert.Equal(t, "Device.WiFi.SSID.2.SSID", m.WiFiSSIDPath(1))
	assert.Equal(t, "Device.WiFi.AccessPoint.2.Security.KeyPassphrase", m.WiFiPasswordPath(1))
	assert.Equal(t, "Device.WiFi.SSID.2.Enable", m.WiFiEnabledPath(1))
	assert.Equal(t, "Device.WiFi.Radio.2.Channel", m.WiFiChannelPath(1))

	// WAN
	assert.Equal(t, "Device.IP.Interface.1.X_TP_ConnType", m.WANConnectionTypePath())
	assert.Equal(t, "Device.PPP.Interface.1.Username", m.WANPPPoEUserPath())
	assert.Equal(t, "Device.PPP.Interface.1.Password", m.WANPPPoEPassPath())
	assert.Equal(t, "Device.IP.Interface.1.IPv4Address.1.IPAddress", m.WANIPAddressPath())

	// Management server
	assert.Equal(t, "Device.ManagementServer.URL", m.ManagementServerURLPath())
	assert.Equal(t, "Device.ManagementServer.Username", m.ManagementServerUserPath())
	assert.Equal(t, "Device.ManagementServer.Password", m.ManagementServerPassPath())
	assert.Equal(t, "Device.ManagementServer.PeriodicInformInterval", m.ManagementServerInformIntervalPath())

	// LAN / DHCP
	assert.Equal(t, "Device.IP.Interface.2.IPv4Address.1.IPAddress", m.LANIPAddressPath())
	assert.Equal(t, "Device.IP.Interface.2.IPv4Address.1.SubnetMask", m.LANSubnetMaskPath())
	assert.Equal(t, "Device.DHCPv4.Server.Enable", m.DHCPServerEnablePath())
	assert.Equal(t, "Device.DHCPv4.Server.Pool.1.MinAddress", m.DHCPMinAddressPath())
	assert.Equal(t, "Device.DHCPv4.Server.Pool.1.MaxAddress", m.DHCPMaxAddressPath())
}

// TR098Mapper path methods

func TestTR098Mapper(t *testing.T) {
	m := &TR098Mapper{}

	// WiFi band 0 (2.4 GHz)
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID", m.WiFiSSIDPath(0))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.PreSharedKey.1.KeyPassphrase", m.WiFiPasswordPath(0))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Enable", m.WiFiEnabledPath(0))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel", m.WiFiChannelPath(0))

	// WiFi band 1 (5 GHz)
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID", m.WiFiSSIDPath(1))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.PreSharedKey.1.KeyPassphrase", m.WiFiPasswordPath(1))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Enable", m.WiFiEnabledPath(1))
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Channel", m.WiFiChannelPath(1))

	// WAN
	assert.Equal(t, "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ConnectionType", m.WANConnectionTypePath())
	assert.Equal(t, "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username", m.WANPPPoEUserPath())
	assert.Equal(t, "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password", m.WANPPPoEPassPath())
	assert.Equal(t, "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress", m.WANIPAddressPath())

	// Management server
	assert.Equal(t, "InternetGatewayDevice.ManagementServer.URL", m.ManagementServerURLPath())
	assert.Equal(t, "InternetGatewayDevice.ManagementServer.Username", m.ManagementServerUserPath())
	assert.Equal(t, "InternetGatewayDevice.ManagementServer.Password", m.ManagementServerPassPath())
	assert.Equal(t, "InternetGatewayDevice.ManagementServer.PeriodicInformInterval", m.ManagementServerInformIntervalPath())

	// LAN / DHCP
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress", m.LANIPAddressPath())
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceSubnetMask", m.LANSubnetMaskPath())
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerEnable", m.DHCPServerEnablePath())
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.MinAddress", m.DHCPMinAddressPath())
	assert.Equal(t, "InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.MaxAddress", m.DHCPMaxAddressPath())
}

// DetectModel via TR181Mapper

func TestTR181DetectModel(t *testing.T) {
	m := &TR181Mapper{}

	// At least one key starting with "Device." → TR181
	params := map[string]string{
		"Device.DeviceInfo.Manufacturer": "Intelbras",
		"Device.DeviceInfo.SerialNumber": "SN001",
	}
	assert.Equal(t, TR181, m.DetectModel(params))
}

func TestTR181DetectModelFallback(t *testing.T) {
	m := &TR181Mapper{}

	// No "Device." prefix → fallback TR098
	params := map[string]string{
		"InternetGatewayDevice.DeviceInfo.Manufacturer": "TP-Link",
	}
	assert.Equal(t, TR098, m.DetectModel(params))
}

// DetectModel via TR098Mapper

func TestTR098DetectModel(t *testing.T) {
	m := &TR098Mapper{}

	params := map[string]string{
		"InternetGatewayDevice.DeviceInfo.Manufacturer": "TP-Link",
		"InternetGatewayDevice.DeviceInfo.SerialNumber": "SN002",
	}
	assert.Equal(t, TR098, m.DetectModel(params))
}

func TestTR098DetectModelAlwaysTR098(t *testing.T) {
	m := &TR098Mapper{}

	// TR098Mapper always returns TR098 regardless
	params := map[string]string{
		"Device.DeviceInfo.Manufacturer": "Intelbras",
	}
	assert.Equal(t, TR098, m.DetectModel(params))
}

// NewMapper

func TestNewMapper(t *testing.T) {
	m181 := NewMapper(TR181)
	require.NotNil(t, m181)
	_, ok := m181.(*TR181Mapper)
	assert.True(t, ok, "TR181 should return *TR181Mapper")

	m098 := NewMapper(TR098)
	require.NotNil(t, m098)
	_, ok2 := m098.(*TR098Mapper)
	assert.True(t, ok2, "TR098 should return *TR098Mapper")

	// Unknown falls back to TR098
	mUnknown := NewMapper(ModelType("unknown"))
	require.NotNil(t, mUnknown)
	_, ok3 := mUnknown.(*TR098Mapper)
	assert.True(t, ok3, "unknown model type should fall back to *TR098Mapper")
}

// ExtractDeviceInfo – TR181

func TestExtractDeviceInfoTR181(t *testing.T) {
	m := &TR181Mapper{}

	params := map[string]string{
		"Device.DeviceInfo.Manufacturer":                "Intelbras",
		"Device.DeviceInfo.ModelName":                   "W5-1200F",
		"Device.DeviceInfo.SerialNumber":                "SN111",
		"Device.DeviceInfo.ManufacturerOUI":             "001122",
		"Device.DeviceInfo.ProductClass":                "WiFiRouter",
		"Device.DeviceInfo.SoftwareVersion":             "1.2.3",
		"Device.DeviceInfo.HardwareVersion":             "v2",
		"Device.IP.Interface.1.IPv4Address.1.IPAddress": "203.0.113.10",
		"Device.IP.Interface.2.IPv4Address.1.IPAddress": "192.168.1.1",
	}

	dev := m.ExtractDeviceInfo(params)
	require.NotNil(t, dev)
	assert.Equal(t, "Intelbras", dev.Manufacturer)
	assert.Equal(t, "W5-1200F", dev.ModelName)
	assert.Equal(t, "SN111", dev.Serial)
	assert.Equal(t, "001122", dev.OUI)
	assert.Equal(t, "WiFiRouter", dev.ProductClass)
	assert.Equal(t, "1.2.3", dev.SWVersion)
	assert.Equal(t, "v2", dev.HWVersion)
	assert.Equal(t, "203.0.113.10", dev.WANIP)
	assert.Equal(t, "192.168.1.1", dev.LANIP)
}

// ExtractDeviceInfo – TR098

func TestExtractDeviceInfoTR098(t *testing.T) {
	m := &TR098Mapper{}

	params := map[string]string{
		"InternetGatewayDevice.DeviceInfo.Manufacturer":                                                "TP-Link",
		"InternetGatewayDevice.DeviceInfo.ModelName":                                                   "TL-WR841N",
		"InternetGatewayDevice.DeviceInfo.SerialNumber":                                                "SN222",
		"InternetGatewayDevice.DeviceInfo.ManufacturerOUI":                                             "AABBCC",
		"InternetGatewayDevice.DeviceInfo.ProductClass":                                                "Router",
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion":                                             "4.0.0",
		"InternetGatewayDevice.DeviceInfo.HardwareVersion":                                             "v1",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress":  "198.51.100.5",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress": "192.168.0.1",
	}

	dev := m.ExtractDeviceInfo(params)
	require.NotNil(t, dev)
	assert.Equal(t, "TP-Link", dev.Manufacturer)
	assert.Equal(t, "TL-WR841N", dev.ModelName)
	assert.Equal(t, "SN222", dev.Serial)
	assert.Equal(t, "AABBCC", dev.OUI)
	assert.Equal(t, "Router", dev.ProductClass)
	assert.Equal(t, "4.0.0", dev.SWVersion)
	assert.Equal(t, "v1", dev.HWVersion)
	assert.Equal(t, "198.51.100.5", dev.WANIP)
	assert.Equal(t, "192.168.0.1", dev.LANIP)
}

// ValidateType

func TestValidateTypeString(t *testing.T) {
	assert.NoError(t, ValidateType(TypeString, ""))
	assert.NoError(t, ValidateType(TypeString, "any arbitrary value 123!@#"))
}

func TestValidateTypeBoolean(t *testing.T) {
	assert.NoError(t, ValidateType(TypeBoolean, "0"))
	assert.NoError(t, ValidateType(TypeBoolean, "1"))
	assert.NoError(t, ValidateType(TypeBoolean, "true"))
	assert.NoError(t, ValidateType(TypeBoolean, "false"))

	assert.Error(t, ValidateType(TypeBoolean, "True"))
	assert.Error(t, ValidateType(TypeBoolean, "False"))
	assert.Error(t, ValidateType(TypeBoolean, "yes"))
	assert.Error(t, ValidateType(TypeBoolean, "no"))
	assert.Error(t, ValidateType(TypeBoolean, ""))
}

func TestValidateTypeUnsignedInt(t *testing.T) {
	assert.NoError(t, ValidateType(TypeUnsignedInt, "0"))
	assert.NoError(t, ValidateType(TypeUnsignedInt, "3600"))
	assert.NoError(t, ValidateType(TypeUnsignedInt, "18446744073709551615")) // max uint64

	assert.Error(t, ValidateType(TypeUnsignedInt, "-1"))
	assert.Error(t, ValidateType(TypeUnsignedInt, "abc"))
	assert.Error(t, ValidateType(TypeUnsignedInt, "3.14"))
}

func TestValidateTypeInt(t *testing.T) {
	assert.NoError(t, ValidateType(TypeInt, "0"))
	assert.NoError(t, ValidateType(TypeInt, "-100"))
	assert.NoError(t, ValidateType(TypeInt, "9223372036854775807")) // max int64

	assert.Error(t, ValidateType(TypeInt, "abc"))
	assert.Error(t, ValidateType(TypeInt, "1.5"))
}

func TestValidateTypeDateTime(t *testing.T) {
	assert.NoError(t, ValidateType(TypeDateTime, "2024-01-01T00:00:00Z"))
	assert.NoError(t, ValidateType(TypeDateTime, "2024-06-15T12:30:00+05:00"))
	assert.NoError(t, ValidateType(TypeDateTime, "2024-06-15T12:30:00"))
	assert.NoError(t, ValidateType(TypeDateTime, "2024-06-15"))
	// TR-069 unknown time sentinel
	assert.NoError(t, ValidateType(TypeDateTime, "0001-01-01T00:00:00Z"))
	assert.NoError(t, ValidateType(TypeDateTime, "0001-01-01T00:00:00"))

	assert.Error(t, ValidateType(TypeDateTime, "not-a-date"))
	assert.Error(t, ValidateType(TypeDateTime, "01/01/2024"))
}

func TestValidateTypeBase64(t *testing.T) {
	// Base64 is accepted without structural validation
	assert.NoError(t, ValidateType(TypeBase64, "SGVsbG8gV29ybGQ="))
	assert.NoError(t, ValidateType(TypeBase64, ""))
}

func TestValidateTypeUnknown(t *testing.T) {
	// Unknown types are silently accepted
	assert.NoError(t, ValidateType("xsd:anyType", "whatever"))
	assert.NoError(t, ValidateType("vendor:custom", "value"))
}

// ────────────────────────────────────────────────────────────────────────
// C-DATA TR-098 Integration Tests
// ────────────────────────────────────────────────────────────────────────

// TestTR098MapperCDATA verifies that C-DATA GPON ONUs (using TR-098) have
// correct path mappings for PPPoE provisioning via WANPPPConnection.
func TestTR098MapperCDATA(t *testing.T) {
	// C-DATA ONUs: FD514GD-R460 series, uses TR-098 with pre-configured WAN objects
	m := &TR098Mapper{
		WANDeviceIdx:  1,
		WANConnDevIdx: 1,
		WANPPPConnIdx: 1,
	}

	// Verify PPPoE credential paths for C-DATA
	// C-DATA provides standard TR-098 WANPPPConnection paths
	expectedUserPath := "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username"
	expectedPassPath := "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password"

	assert.Equal(t, expectedUserPath, m.WANPPPoEUserPath(), "C-DATA PPPoE username path")
	assert.Equal(t, expectedPassPath, m.WANPPPoEPassPath(), "C-DATA PPPoE password path")

	// Verify WAN status paths
	expectedStatusPath := "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionStatus"
	assert.Equal(t, expectedStatusPath, m.WANStatusPath(), "C-DATA WAN connection status path")

	// Verify WAN IP address can be read from WANIPConnection (fallback)
	expectedIPPath := "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress"
	assert.Equal(t, expectedIPPath, m.WANIPAddressPath(), "C-DATA WAN IP address path")

	// Verify traffic statistics paths (from WANCommonInterfaceConfig)
	expectedBytesRxPath := "InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.TotalBytesReceived"
	assert.Equal(t, expectedBytesRxPath, m.WANBytesReceivedPath(), "C-DATA WAN bytes received path")

	expectedBytesTxPath := "InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.TotalBytesSent"
	assert.Equal(t, expectedBytesTxPath, m.WANBytesSentPath(), "C-DATA WAN bytes sent path")
}

// TestTR098MapperCDATA_CustomIndices verifies that custom instance indices
// (discovered at runtime) are properly applied to C-DATA TR-098 paths.
func TestTR098MapperCDATA_CustomIndices(t *testing.T) {
	// If instance discovery finds different indices (e.g. WANDevice.2)
	m := &TR098Mapper{
		WANDeviceIdx:  2,
		WANConnDevIdx: 2,
		WANPPPConnIdx: 1,
		LANDeviceIdx:  1,
		WLANIndices:   []int{1, 2},
	}

	// Paths should use the custom indices
	expectedUserPath := "InternetGatewayDevice.WANDevice.2.WANConnectionDevice.2.WANPPPConnection.1.Username"
	assert.Equal(t, expectedUserPath, m.WANPPPoEUserPath())

	// WiFi paths should also respect custom indices
	expectedWiFi2GPath := "InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID"
	expectedWiFi5GPath := "InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID"

	assert.Equal(t, expectedWiFi2GPath, m.WiFiSSIDPath(0))
	assert.Equal(t, expectedWiFi5GPath, m.WiFiSSIDPath(1))
}

// TestTR098MapperCDATA_DetectModel verifies that parameters with InternetGatewayDevice
// prefix are correctly detected as TR-098.
func TestTR098MapperCDATA_DetectModel(t *testing.T) {
	m := &TR098Mapper{}

	// Simulated C-DATA device parameters (TR-098 hierarchy)
	params := map[string]string{
		"InternetGatewayDevice.DeviceInfo.Manufacturer": "ZTEG",
		"InternetGatewayDevice.DeviceInfo.ModelName":    "FD514GD-R460",
		"InternetGatewayDevice.DeviceInfo.SerialNumber": "CDTCAF252D7F",
	}

	// Should always return TR098 for TR098Mapper
	assert.Equal(t, TR098, m.DetectModel(params))
}

// TestTR098MapperCDATA_ExtractDeviceInfo verifies device info extraction from
// C-DATA device parameters.
func TestTR098MapperCDATA_ExtractDeviceInfo(t *testing.T) {
	m := &TR098Mapper{}

	params := map[string]string{
		"InternetGatewayDevice.DeviceInfo.Manufacturer":                                                "ZTEG",
		"InternetGatewayDevice.DeviceInfo.ModelName":                                                   "FD514GD-R460",
		"InternetGatewayDevice.DeviceInfo.SerialNumber":                                                "CDTCAF252D7F",
		"InternetGatewayDevice.DeviceInfo.ManufacturerOUI":                                             "00188F",
		"InternetGatewayDevice.DeviceInfo.ProductClass":                                                "GPON ONU",
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion":                                             "1.0.0",
		"InternetGatewayDevice.DeviceInfo.HardwareVersion":                                             "R460",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress": "203.0.113.100",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPInterfaceIPAddress": "192.168.100.1",
	}

	dev := m.ExtractDeviceInfo(params)
	require.NotNil(t, dev)
	assert.Equal(t, "ZTEG", dev.Manufacturer)
	assert.Equal(t, "FD514GD-R460", dev.ModelName)
	assert.Equal(t, "CDTCAF252D7F", dev.Serial)
	assert.Equal(t, "00188F", dev.OUI)
	assert.Equal(t, "GPON ONU", dev.ProductClass)
	assert.Equal(t, "1.0.0", dev.SWVersion)
	assert.Equal(t, "R460", dev.HWVersion)
	// Note: ExtractDeviceInfo uses WANIPConnection path for WANIP, so PPP external IP won't be extracted
	assert.Equal(t, "192.168.100.1", dev.LANIP)
}

// TestTR098Mapper_ApplyInstanceMap verifies that discovered instance indices
// are correctly applied to the mapper.
func TestTR098Mapper_ApplyInstanceMap(t *testing.T) {
	original := &TR098Mapper{}

	im := InstanceMap{
		WANDeviceIdx:  3,
		WANConnDevIdx: 2,
		WANIPConnIdx:  1,
		WANPPPConnIdx: 1,
		LANDeviceIdx:  1,
		WLANIndices:   []int{1, 2},
	}

	result := ApplyInstanceMap(original, im)
	mapped, ok := result.(*TR098Mapper)
	require.True(t, ok, "ApplyInstanceMap should return *TR098Mapper")

	assert.Equal(t, 3, mapped.WANDeviceIdx)
	assert.Equal(t, 2, mapped.WANConnDevIdx)
	assert.Equal(t, 1, mapped.WANIPConnIdx)
	assert.Equal(t, 1, mapped.WANPPPConnIdx)
	assert.Equal(t, 1, mapped.LANDeviceIdx)
	assert.Equal(t, []int{1, 2}, mapped.WLANIndices)

	// Verify paths now use the new indices
	assert.Equal(t,
		"InternetGatewayDevice.WANDevice.3.WANConnectionDevice.2.WANPPPConnection.1.Username",
		mapped.WANPPPoEUserPath(),
	)
}

// TestTR098Mapper_ProvisioningType verifies that TR-098 devices report
// "set_params" as the provisioning type (pre-existing WAN objects).
func TestTR098Mapper_ProvisioningType(t *testing.T) {
	m := &TR098Mapper{}
	assert.Equal(t, "set_params", m.WANProvisioningType())
}
