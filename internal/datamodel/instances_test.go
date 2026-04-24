package datamodel

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TR-181 WAN/LAN discovery

func TestDiscoverTR181WANAndLAN(t *testing.T) {
	params := map[string]string{
		"Device.IP.Interface.1.IPv4Address.1.IPAddress": "192.168.1.1", // LAN (private)
		"Device.IP.Interface.2.IPv4Address.1.IPAddress": "203.0.113.5", // WAN (public)
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 2, im.WANIPIfaceIdx, "WAN interface should be 2 (public IP)")
	assert.Equal(t, 1, im.LANIPIfaceIdx, "LAN interface should be 1 (private IP)")
}

func TestDiscoverTR181WANDefaultOrder(t *testing.T) {
	// Most common case: Interface.1 = WAN, Interface.2 = LAN.
	params := map[string]string{
		"Device.IP.Interface.1.IPv4Address.1.IPAddress": "198.51.100.10",
		"Device.IP.Interface.2.IPv4Address.1.IPAddress": "10.0.0.1",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 1, im.WANIPIfaceIdx)
	assert.Equal(t, 2, im.LANIPIfaceIdx)
}

func TestDiscoverTR181WANWithHintsPrefersInternetServiceType(t *testing.T) {
	params := map[string]string{
		"Device.IP.Interface.1.X_TP_ConnType":    "PPPoE",
		"Device.IP.Interface.1.X_TP_ServiceType": "TR069_INTERNET",
		"Device.IP.Interface.2.X_TP_ConnType":    "PPPoE",
		"Device.IP.Interface.2.X_TP_ServiceType": "Internet",
	}
	hints := &DiscoveryHints{
		WANTypePath:        "Device.IP.Interface.{i}.X_TP_ConnType",
		WANTypeValuesWAN:   []string{"PPPoE", "DHCP", "Static"},
		WANServiceTypePath: "Device.IP.Interface.{i}.X_TP_ServiceType",
	}

	im := DiscoverInstancesWithHints(params, hints)
	assert.Equal(t, 2, im.WANIPIfaceIdx, "WAN interface should prefer ServiceType=Internet")
}

func TestDiscoverTR181NoIPParams(t *testing.T) {
	// Without IP parameters the indices stay zero (mapper falls back to defaults).
	params := map[string]string{
		"Device.DeviceInfo.Manufacturer": "Intelbras",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 0, im.WANIPIfaceIdx)
	assert.Equal(t, 0, im.LANIPIfaceIdx)
}

func TestDiscoverTR181EmptyIPIgnored(t *testing.T) {
	params := map[string]string{
		"Device.IP.Interface.1.IPv4Address.1.IPAddress": "",
		"Device.IP.Interface.2.IPv4Address.1.IPAddress": "203.0.113.5",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 2, im.WANIPIfaceIdx)
	assert.Equal(t, 0, im.LANIPIfaceIdx) // empty string → not classified
}

// TR-181 PPP discover

func TestDiscoverTR181PPP(t *testing.T) {
	params := map[string]string{
		"Device.PPP.Interface.3.Username": "user@isp",
		"Device.PPP.Interface.3.Password": "secret",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 3, im.PPPIfaceIdx)
}

func TestDiscoverTR181PPPPreferWANLowerLayerReference(t *testing.T) {
	params := map[string]string{
		// WAN IP interface is .4 and points to PPP interface .5.
		"Device.IP.Interface.4.IPv4Address.1.IPAddress": "203.0.113.8",
		"Device.IP.Interface.4.LowerLayers":             "Device.PPP.Interface.5.",
		// Another PPP interface exists with lower index but is not WAN-linked.
		"Device.PPP.Interface.2.Username": "old@isp",
		"Device.PPP.Interface.5.Username": "new@isp",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 4, im.WANIPIfaceIdx)
	assert.Equal(t, 5, im.PPPIfaceIdx, "PPP index should follow WAN LowerLayers reference")
}

// TR-181 WiFi discovery via OperatingFrequencyBand

func TestDiscoverTR181WiFiViaFrequencyBand(t *testing.T) {
	params := map[string]string{
		// Radio 1 = 2.4 GHz, Radio 2 = 5 GHz
		"Device.WiFi.Radio.1.OperatingFrequencyBand": "2.4GHz",
		"Device.WiFi.Radio.2.OperatingFrequencyBand": "5GHz",
		// SSID 1 → Radio 1 (2.4 GHz), SSID 2 → Radio 2 (5 GHz)
		"Device.WiFi.SSID.1.LowerLayers": "Device.WiFi.Radio.1.",
		"Device.WiFi.SSID.2.LowerLayers": "Device.WiFi.Radio.2.",
		// AP 1 → SSID 1, AP 2 → SSID 2
		"Device.WiFi.AccessPoint.1.SSIDReference": "Device.WiFi.SSID.1.",
		"Device.WiFi.AccessPoint.2.SSIDReference": "Device.WiFi.SSID.2.",
	}
	im := DiscoverInstances(params)

	assert.Equal(t, []int{1, 2}, im.WiFiRadioIndices)
	assert.Equal(t, []int{1, 2}, im.WiFiSSIDIndices)
	assert.Equal(t, []int{1, 2}, im.WiFiAPIndices)
}

func TestDiscoverTR181WiFiTPLinkFourSSIDNoLowerLayers(t *testing.T) {
	// TP-Link block layout without SSID LowerLayers: SSID 1–2 → 2.4 GHz, 3–4 → 5 GHz.
	params := map[string]string{
		"Device.WiFi.Radio.1.OperatingFrequencyBand": "2.4GHz",
		"Device.WiFi.Radio.2.OperatingFrequencyBand": "5GHz",
		"Device.WiFi.SSID.1.SSID":                    "HOME_24_A",
		"Device.WiFi.SSID.2.SSID":                    "HOME_24_B",
		"Device.WiFi.SSID.3.SSID":                    "HOME_5_PRIMARY",
		"Device.WiFi.SSID.4.SSID":                    "HOME_5_GUEST",
	}
	hints := &DiscoveryHints{
		WiFiSSIDBandWithoutLowerLayers: &WiFiSSIDBandWithoutLowerLayersHints{Strategy: "pair_block_mod2"},
	}
	im := DiscoverInstancesWithHints(params, hints)

	assert.Equal(t, 1, im.WiFiSSIDIndices[0], "2.4 GHz primary SSID should be smallest in block {1,2}")
	assert.Equal(t, 3, im.WiFiSSIDIndices[1], "5 GHz primary SSID should be smallest in block {3,4} → SSID.3")
}

func TestDiscoverTR181WiFiTPLinkEightSSIDNoLowerLayers(t *testing.T) {
	// Pairs (1,2),(5,6) → 2.4 GHz; (3,4),(7,8) → 5 GHz (TP-Link multi-SSID, no LowerLayers).
	params := map[string]string{
		"Device.WiFi.Radio.1.OperatingFrequencyBand": "2.4GHz",
		"Device.WiFi.Radio.2.OperatingFrequencyBand": "5GHz",
	}
	for i := 1; i <= 8; i++ {
		params[fmt.Sprintf("Device.WiFi.SSID.%d.SSID", i)] = fmt.Sprintf("S%d", i)
	}
	hints := &DiscoveryHints{
		WiFiSSIDBandWithoutLowerLayers: &WiFiSSIDBandWithoutLowerLayersHints{Strategy: "pair_block_mod2"},
	}
	im := DiscoverInstancesWithHints(params, hints)

	assert.Equal(t, 1, im.WiFiSSIDIndices[0], "primary 2.4 GHz = min of {1,2,5,6}")
	assert.Equal(t, 3, im.WiFiSSIDIndices[1], "primary 5 GHz = min of {3,4,7,8}")
}

func TestTPLinkXC220G3BlockPairVsLegacyOnSSID10(t *testing.T) {
	assert.Equal(t, 0, wifiSSIDPairBlockMod2Band(10), "pair_block_mod2: SSID 10 → 2.4 GHz block")
	assert.Equal(t, 1, tplinkLegacyMultiRadioNoLowerLayersBand(10), "legacy TP-Link heuristic: SSID 10 → 5 GHz")
}

func TestWiFiSSIDBandExplicitStrategy(t *testing.T) {
	params := map[string]string{
		"Device.WiFi.Radio.1.OperatingFrequencyBand": "2.4GHz",
		"Device.WiFi.Radio.2.OperatingFrequencyBand": "5GHz",
		"Device.WiFi.SSID.1.SSID":                    "A",
		"Device.WiFi.SSID.2.SSID":                    "B",
		"Device.WiFi.SSID.3.SSID":                    "C",
		"Device.WiFi.SSID.4.SSID":                    "D",
	}
	hints := &DiscoveryHints{
		WiFiSSIDBandWithoutLowerLayers: &WiFiSSIDBandWithoutLowerLayersHints{
			Strategy: "explicit",
			ExplicitSSIDBand: map[int]int{
				1: 0, 3: 0,
				2: 1, 4: 1,
			},
		},
	}
	im := DiscoverInstancesWithHints(params, hints)
	assert.Equal(t, 1, im.WiFiSSIDIndices[0], "2.4 GHz min SSID index = 1")
	assert.Equal(t, 2, im.WiFiSSIDIndices[1], "5 GHz min SSID index = 2")
}

func TestDiscoverTR181WiFiNonStandardIndices(t *testing.T) {
	// CPE uses Radio.1 for 5 GHz and Radio.2 for 2.4 GHz (inverted).
	params := map[string]string{
		"Device.WiFi.Radio.1.OperatingFrequencyBand": "5GHz",
		"Device.WiFi.Radio.2.OperatingFrequencyBand": "2.4GHz",
		"Device.WiFi.SSID.1.LowerLayers":             "Device.WiFi.Radio.1.",
		"Device.WiFi.SSID.3.LowerLayers":             "Device.WiFi.Radio.2.",
		"Device.WiFi.AccessPoint.1.SSIDReference":    "Device.WiFi.SSID.1.",
		"Device.WiFi.AccessPoint.3.SSIDReference":    "Device.WiFi.SSID.3.",
	}
	im := DiscoverInstances(params)

	// band 0 = 2.4GHz → Radio.2, SSID.3, AP.3
	// band 1 = 5GHz   → Radio.1, SSID.1, AP.1
	assert.Equal(t, 2, im.WiFiRadioIndices[0], "band 0 radio should be 2")
	assert.Equal(t, 1, im.WiFiRadioIndices[1], "band 1 radio should be 1")
	assert.Equal(t, 3, im.WiFiSSIDIndices[0], "band 0 SSID should be 3")
	assert.Equal(t, 1, im.WiFiSSIDIndices[1], "band 1 SSID should be 1")
	assert.Equal(t, 3, im.WiFiAPIndices[0], "band 0 AP should be 3")
	assert.Equal(t, 1, im.WiFiAPIndices[1], "band 1 AP should be 1")
}

func TestDiscoverTR181WiFiFallbackSortedIndex(t *testing.T) {
	// No OperatingFrequencyBand  fall back to sorted-index heuristic.
	params := map[string]string{
		"Device.WiFi.SSID.3.SSID": "HomeNet",  // lowest → band 0
		"Device.WiFi.SSID.5.SSID": "HomeNet5", // next   → band 1
	}
	im := DiscoverInstances(params)

	assert.Equal(t, 2, len(im.WiFiSSIDIndices))
	assert.Equal(t, 3, im.WiFiSSIDIndices[0], "band 0 should get SSID index 3")
	assert.Equal(t, 5, im.WiFiSSIDIndices[1], "band 1 should get SSID index 5")
}

// TR-098 WAN discover

func TestDiscoverTR098WAN(t *testing.T) {
	params := map[string]string{
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.2.ExternalIPAddress": "203.0.113.1",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 1, im.WANDeviceIdx)
	assert.Equal(t, 1, im.WANConnDevIdx)
	assert.Equal(t, 2, im.WANIPConnIdx)
}

func TestDiscoverTR098PPP(t *testing.T) {
	params := map[string]string{
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.2.Username": "user@isp",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 1, im.WANDeviceIdx)
	assert.Equal(t, 1, im.WANConnDevIdx)
	assert.Equal(t, 2, im.WANPPPConnIdx)
}

// TR-098 WLAN discover-

func TestDiscoverTR098WLAN(t *testing.T) {
	params := map[string]string{
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID": "HomeNet",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.3.SSID": "HomeNet5",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, []int{1, 3}, im.WLANIndices)
}

func TestDiscoverTR098LANDevice(t *testing.T) {
	params := map[string]string{
		"InternetGatewayDevice.LANDevice.2.LANHostConfigManagement.DHCPServerEnable": "true",
	}
	im := DiscoverInstances(params)
	assert.Equal(t, 2, im.LANDeviceIdx)
}

// ApplyInstanceMa

func TestApplyInstanceMapTR181(t *testing.T) {
	base := &TR181Mapper{}
	im := InstanceMap{
		WANIPIfaceIdx:    3,
		LANIPIfaceIdx:    4,
		WiFiSSIDIndices:  []int{1, 3},
		WiFiRadioIndices: []int{1, 3},
		WiFiAPIndices:    []int{1, 3},
	}
	m := ApplyInstanceMap(base, im).(*TR181Mapper)

	assert.Equal(t, "Device.IP.Interface.3.IPv4Address.1.IPAddress", m.WANIPAddressPath())
	assert.Equal(t, "Device.IP.Interface.4.IPv4Address.1.IPAddress", m.LANIPAddressPath())
	assert.Equal(t, "Device.WiFi.SSID.3.SSID", m.WiFiSSIDPath(1))
	assert.Equal(t, "Device.WiFi.Radio.3.Channel", m.WiFiChannelPath(1))
	assert.Equal(t, "Device.WiFi.AccessPoint.3.Security.KeyPassphrase", m.WiFiPasswordPath(1))
}

func TestApplyInstanceMapTR181FallbackUnchanged(t *testing.T) {
	// Zero InstanceMap must not change any paths.
	base := &TR181Mapper{}
	m := ApplyInstanceMap(base, InstanceMap{}).(*TR181Mapper)

	assert.Equal(t, "Device.IP.Interface.1.IPv4Address.1.IPAddress", m.WANIPAddressPath())
	assert.Equal(t, "Device.IP.Interface.2.IPv4Address.1.IPAddress", m.LANIPAddressPath())
	assert.Equal(t, "Device.WiFi.SSID.1.SSID", m.WiFiSSIDPath(0))
}

func TestApplyInstanceMapTR098(t *testing.T) {
	base := &TR098Mapper{}
	im := InstanceMap{
		WANDeviceIdx:  1,
		WANConnDevIdx: 1,
		WANIPConnIdx:  2,
		WLANIndices:   []int{1, 3},
	}
	m := ApplyInstanceMap(base, im).(*TR098Mapper)

	assert.Equal(t,
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.2.ExternalIPAddress",
		m.WANIPAddressPath(),
	)
	assert.Equal(t,
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.3.SSID",
		m.WiFiSSIDPath(1),
	)
}

func TestApplyInstanceMapUnknownMapper(t *testing.T) {
	// A mapper that is not TR181Mapper or TR098Mapper must be returned as-is.
	var m Mapper = &TR181Mapper{}
	result := ApplyInstanceMap(m, InstanceMap{WANIPIfaceIdx: 99})
	// Should be the resolved copy, not the original pointer  but still TR181Mapper.
	_, ok := result.(*TR181Mapper)
	assert.True(t, ok)
}

// isPublicIP / isPrivateIP

func TestIsPrivateIP(t *testing.T) {
	assert.True(t, isPrivateIP("192.168.1.1"))
	assert.True(t, isPrivateIP("10.0.0.1"))
	assert.True(t, isPrivateIP("172.16.0.1"))
	assert.True(t, isPrivateIP("172.31.255.255"))
	assert.True(t, isPrivateIP("127.0.0.1"))
	assert.True(t, isPrivateIP("169.254.0.1"))
	assert.True(t, isPrivateIP("100.64.0.1")) // CGNAT

	assert.False(t, isPrivateIP("203.0.113.1"))
	assert.False(t, isPrivateIP("8.8.8.8"))
	assert.False(t, isPrivateIP(""))
	assert.False(t, isPrivateIP("not-an-ip"))
}

func TestIsPublicIP(t *testing.T) {
	assert.True(t, isPublicIP("203.0.113.1"))
	assert.True(t, isPublicIP("8.8.8.8"))

	assert.False(t, isPublicIP("192.168.1.1"))
	assert.False(t, isPublicIP("10.0.0.1"))
	assert.False(t, isPublicIP("127.0.0.1"))
	assert.False(t, isPublicIP(""))
}
