package cwmp

// results.go  helpers that parse flat GetParameterValues response maps into
// typed result structs for diagnostic and informational tasks.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/raykavin/helix-acs/internal/datamodel"
	"github.com/raykavin/helix-acs/internal/device"
	"github.com/raykavin/helix-acs/internal/task"
)

// parsePingResult converts a GetParameterValuesResponse map into a PingResult.
func parsePingResult(params map[string]string, mapper datamodel.Mapper) *task.PingResult {
	base := mapper.PingDiagBasePath()

	host := params[base+"Host"]
	sent, _ := strconv.Atoi(params[base+"NumberOfRepetitions"])
	success, _ := strconv.Atoi(params[base+"SuccessCount"])
	failure, _ := strconv.Atoi(params[base+"FailureCount"])
	avg, _ := strconv.Atoi(params[base+"AverageResponseTime"])
	min, _ := strconv.Atoi(params[base+"MinimumResponseTime"])
	max, _ := strconv.Atoi(params[base+"MaximumResponseTime"])

	if sent == 0 {
		sent = success + failure
	}

	var lossPct float64
	if sent > 0 {
		lossPct = float64(failure) / float64(sent) * 100
	}

	return &task.PingResult{
		Host:            host,
		PacketsSent:     sent,
		PacketsReceived: success,
		PacketLossPct:   lossPct,
		MinRTTMs:        min,
		AvgRTTMs:        avg,
		MaxRTTMs:        max,
	}
}

// parseTracerouteResult converts a GetParameterValuesResponse map into a
// TracerouteResult by iterating over RouteHops.{i}.* entries.
func parseTracerouteResult(params map[string]string, mapper datamodel.Mapper) *task.TracerouteResult {
	base := mapper.TracerouteDiagBasePath()

	hopCount, _ := strconv.Atoi(params[base+"NumberOfRouteHops"])
	maxHops, _ := strconv.Atoi(params[base+"MaxHopCount"])

	// Collect hops by scanning keys matching base + "RouteHops.{i}."
	hopBase := base + "RouteHops."
	hopMap := make(map[int]*task.TracerouteHop)
	for k, v := range params {
		if !strings.HasPrefix(k, hopBase) {
			continue
		}
		rest := k[len(hopBase):]
		before, after, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		idxStr := before
		field := after
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		if hopMap[idx] == nil {
			hopMap[idx] = &task.TracerouteHop{HopNumber: idx}
		}
		switch field {
		case "HopHost", "Host":
			hopMap[idx].Host = v
		case "HopRTTimes", "RTTimes", "HopErrorCode":
			if rtt, err := strconv.Atoi(v); err == nil {
				hopMap[idx].RTTMs = rtt
			}
		}
	}

	hops := make([]task.TracerouteHop, 0, len(hopMap))
	for i := 1; i <= len(hopMap); i++ {
		if h, ok := hopMap[i]; ok {
			hops = append(hops, *h)
		}
	}

	return &task.TracerouteResult{
		Host:     params[base+"Host"],
		MaxHops:  maxHops,
		HopCount: hopCount,
		Hops:     hops,
	}
}

// parseSpeedTestResult converts a DownloadDiagnostics GetParameterValues
// response into a SpeedTestResult.
func parseSpeedTestResult(
	params map[string]string,
	mapper datamodel.Mapper,
	originalPayload task.SpeedTestPayload,
) *task.SpeedTestResult {
	base := mapper.DownloadDiagBasePath()

	bomStr := params[base+"BOMTime"]
	eomStr := params[base+"EOMTime"]
	bytesStr := params[base+"TestBytesReceived"]
	totalStr := params[base+"TotalBytesReceived"]

	bytes, _ := strconv.ParseInt(bytesStr, 10, 64)
	if bytes == 0 {
		bytes, _ = strconv.ParseInt(totalStr, 10, 64)
	}

	// BOMTime and EOMTime are millisecond timestamps; duration = EOM - BOM.
	var durationMs int
	bom, errB := strconv.ParseInt(bomStr, 10, 64)
	eom, errE := strconv.ParseInt(eomStr, 10, 64)
	if errB == nil && errE == nil && eom > bom {
		durationMs = int(eom - bom)
	}

	var speedMbps float64
	if durationMs > 0 && bytes > 0 {
		speedMbps = float64(bytes) * 8 / float64(durationMs) / 1000 // Mbps
	}

	return &task.SpeedTestResult{
		DownloadURL:        originalPayload.DownloadURL,
		DownloadSpeedMbps:  speedMbps,
		DownloadDurationMs: durationMs,
		DownloadBytesTotal: bytes,
	}
}

// parseCPEStats converts a CPE stats GetParameterValues response into a
// CPEStatsResult and a partial WANInfo for device persistence.
func parseCPEStats(params map[string]string, mapper datamodel.Mapper) (*task.CPEStatsResult, device.WANInfo) {
	parseInt := func(key string) int64 {
		v, _ := strconv.ParseInt(params[key], 10, 64)
		return v
	}

	res := &task.CPEStatsResult{
		UptimeSeconds: parseInt(mapper.CPEUptimePath()),
		RAMTotalKB:    parseInt(mapper.RAMTotalPath()),
		RAMFreeKB:     parseInt(mapper.RAMFreePath()),
		WANBytesSent:  parseInt(mapper.WANBytesSentPath()),
		WANBytesRecv:  parseInt(mapper.WANBytesReceivedPath()),
		WANPktsSent:   parseInt(mapper.WANPacketsSentPath()),
		WANPktsRecv:   parseInt(mapper.WANPacketsReceivedPath()),
		WANErrsSent:   parseInt(mapper.WANErrorsSentPath()),
		WANErrsRecv:   parseInt(mapper.WANErrorsReceivedPath()),
	}

	wan := device.WANInfo{
		LinkStatus:      params[mapper.WANStatusPath()],
		UptimeSeconds:   parseInt(mapper.WANUptimePath()),
		BytesSent:       res.WANBytesSent,
		BytesReceived:   res.WANBytesRecv,
		PacketsSent:     res.WANPktsSent,
		PacketsReceived: res.WANPktsRecv,
		ErrorsSent:      res.WANErrsSent,
		ErrorsReceived:  res.WANErrsRecv,
	}

	return res, wan
}

// reExternalIPAddress matches any TR-098 WANIPConnection or WANPPPConnection
// ExternalIPAddress parameter, used as fallback when the mapper path is empty.
var reExternalIPAddress = regexp.MustCompile(`^InternetGatewayDevice\.WANDevice\.\d+\.WANConnectionDevice\.\d+\.WAN(?:IP|PPP)Connection\.\d+\.ExternalIPAddress$`)

// fallbackExternalIP scans params for the first non-empty ExternalIPAddress
// from a TR-098 WAN connection (WANIPConnection or WANPPPConnection).
func fallbackExternalIP(params map[string]string) string {
	for k, v := range params {
		if v != "" && v != "0.0.0.0" && reExternalIPAddress.MatchString(k) {
			return v
		}
	}
	return ""
}

// extractWANInfo reads WAN detail fields from the full summon parameter map.
// It is called from finishSummon to populate the Network tab automatically
// without requiring a separate CPE Stats task for the static fields.
func extractWANInfo(params map[string]string, mapper datamodel.Mapper) device.WANInfo {
	parseInt := func(key string) int64 {
		if key == "" {
			return 0
		}
		v, _ := strconv.ParseInt(params[key], 10, 64)
		return v
	}
	mtu, _ := strconv.Atoi(params[mapper.WANMTUPath()])

	// Derive WAN interface base path from the WAN IP address path.
	// e.g. "Device.IP.Interface.5.IPv4Address.1.IPAddress" → "Device.IP.Interface.5."
	wanIface := ""
	ipAddrPath := mapper.WANIPAddressPath()
	if idx := strings.Index(ipAddrPath, ".IPv4Address."); idx > 0 {
		wanIface = ipAddrPath[:idx] + "."
	}

	// Derive subnet mask from the same IPv4Address entry as the IP address.
	subnetMask := ""
	if ipAddrPath != "" {
		smPath := strings.Replace(ipAddrPath, ".IPAddress", ".SubnetMask", 1)
		subnetMask = params[smPath]
	}

	gateway := findGateway(wanIface, params[ipAddrPath], params)

	// Parse DNS servers from PPP.IPCP.DNSServers (comma-separated)
	dnsStr := params[mapper.WANDNS1Path()]
	dns1, dns2 := "", ""
	if dnsStr != "" {
		parts := strings.Split(dnsStr, ",")
		if len(parts) > 0 {
			dns1 = strings.TrimSpace(parts[0])
		}
		if len(parts) > 1 {
			dns2 = strings.TrimSpace(parts[1])
		}
	}

	// Read service type via mapper (e.g. X_TP_ServiceType for TP-Link, "" for generic).
	serviceType := ""
	if stPath := mapper.WANServiceTypePath(); stPath != "" {
		serviceType = params[stPath]
	} else if wanIface != "" {
		// Fallback: derive suffix from WANServiceTypePath if mapper returns empty
		// but wanIface is known (supports legacy TR181Mapper without schema).
		// For generic devices this will also be empty, which is correct.
		serviceType = ""
	}

	// For TR-098 devices that use DHCP (WANIPConnection), the mapper's
	// WANIPAddressPath may point to the PPPoE path (WANPPPConnection) which
	// will be empty. Fall back to scanning params for any ExternalIPAddress.
	wanIP := params[ipAddrPath]
	if wanIP == "" || wanIP == "0.0.0.0" {
		wanIP = fallbackExternalIP(params)
	}

	return device.WANInfo{
		ConnectionType: params[mapper.WANConnectionTypePath()],
		ServiceType:    serviceType,
		IPAddress:      wanIP,
		SubnetMask:     subnetMask,
		Gateway:        gateway,
		DNS1:           dns1,
		DNS2:           dns2,
		MACAddress:     params[mapper.WANMACPath()],
		PPPoEUsername:  params[mapper.WANPPPoEUserPath()],
		MTU:            mtu,
		LinkStatus:     params[mapper.WANStatusPath()],
		UptimeSeconds:  parseInt(mapper.WANUptimePath()),
	}
}

func findGateway(wanIface string, wanIP string, params map[string]string) string {
	gateway := ""
	if wanIface != "" {
		for k, v := range params {
			if strings.HasSuffix(k, ".Interface") && v == wanIface && strings.Contains(k, "IPv4Forwarding") {
				base := strings.TrimSuffix(k, ".Interface")
				enabled := params[base+".Enable"]
				if enabled == "1" || enabled == "true" || enabled == "" {
					if gw := params[base+".GatewayIPAddress"]; gw != "" && gw != "0.0.0.0" {
						gateway = gw
						break
					}
				}
			}
		}
	}
	if gateway == "" {
		for k, v := range params {
			if !strings.HasSuffix(k, ".GatewayIPAddress") || !strings.Contains(k, "IPv4Forwarding") {
				continue
			}
			if v == "" || v == "0.0.0.0" {
				continue
			}
			if wanIP != "" && samePrefix(wanIP, v) {
				gateway = v
				break
			}
			if gateway == "" {
				gateway = v
			}
		}
	}
	return gateway
}

// extractWANInfos reads all WAN interfaces (main WAN + TP-Link additional WANs)
func extractWANInfos(params map[string]string, mapper datamodel.Mapper) []device.WANInfo {
	var wans []device.WANInfo
	seen := make(map[string]bool)

	macAddress := params[mapper.WANMACPath()]

	// Main WAN
	mainWan := extractWANInfo(params, mapper)
	if mainWan.IPAddress != "" || mainWan.ConnectionType != "" {
		wans = append(wans, mainWan)
		seen[mainWan.IPAddress] = true
	}

	// Additional WAN interfaces: scan for connection-type parameters that match
	// the vendor-specific path pattern derived from the mapper.
	//
	// For TP-Link: "Device.IP.Interface.{n}.X_TP_ConnType"
	// For generic TR-181: "Device.IP.Interface.{n}.ConnectionType"
	//
	// The pattern is extracted by replacing the instance placeholder in the
	// resolved WANConnectionTypePath with a regex wildcard.
	connTypePat := buildConnTypePattern(mapper)
	serviceTypeSuffix := serviceTypeSuffixFromMapper(mapper)

	for k, v := range params {
		m := connTypePat.FindStringSubmatch(k)
		if m == nil {
			continue
		}
		if v == "LAN" || v == "Bridge" || v == "" {
			continue
		}
		idx := m[1]
		ipAddr := params[fmt.Sprintf("Device.IP.Interface.%s.IPv4Address.1.IPAddress", idx)]

		if ipAddr == "" || ipAddr == "0.0.0.0" || seen[ipAddr] {
			continue
		}

		mtu, _ := strconv.Atoi(params[fmt.Sprintf("Device.IP.Interface.%s.MaxMTUSize", idx)])

		serviceType := ""
		if serviceTypeSuffix != "" {
			serviceType = params[fmt.Sprintf("Device.IP.Interface.%s.", idx)+serviceTypeSuffix]
		}

		wan := device.WANInfo{
			ConnectionType: v,
			ServiceType:    serviceType,
			IPAddress:      ipAddr,
			SubnetMask:     params[fmt.Sprintf("Device.IP.Interface.%s.IPv4Address.1.SubnetMask", idx)],
			LinkStatus:     params[fmt.Sprintf("Device.IP.Interface.%s.Status", idx)],
			Gateway:        findGateway(fmt.Sprintf("Device.IP.Interface.%s.", idx), ipAddr, params),
			MACAddress:     macAddress,
			MTU:            mtu,
		}

		uptimeStr := params[fmt.Sprintf("Device.IP.Interface.%s.X_TP_Uptime", idx)]
		if uptimeStr == "" {
			uptimeStr = params[fmt.Sprintf("Device.IP.Interface.%s.LastChange", idx)]
		}
		if uptimeStr != "" {
			if u, err := strconv.ParseInt(uptimeStr, 10, 64); err == nil {
				wan.UptimeSeconds = u
			}
		}

		if v == "PPPoE" {
			lower := params[fmt.Sprintf("Device.IP.Interface.%s.LowerLayers", idx)]
			if pppMatch := regexp.MustCompile(`Device\.PPP\.Interface\.(\d+)`).FindStringSubmatch(lower); pppMatch != nil {
				pppIdx := pppMatch[1]
				wan.PPPoEUsername = params[fmt.Sprintf("Device.PPP.Interface.%s.Username", pppIdx)]

				dnsStr := params[fmt.Sprintf("Device.PPP.Interface.%s.IPCP.DNSServers", pppIdx)]
				if dnsStr != "" {
					parts := strings.Split(dnsStr, ",")
					if len(parts) > 0 {
						wan.DNS1 = strings.TrimSpace(parts[0])
					}
					if len(parts) > 1 {
						wan.DNS2 = strings.TrimSpace(parts[1])
					}
				}
			}
		} else if v == "DHCP" {
			wanIfacePath := fmt.Sprintf("Device.IP.Interface.%s.", idx)
			for pk, pval := range params {
				if strings.HasSuffix(pk, ".Interface") && strings.HasPrefix(pk, "Device.DHCPv4.Client.") && pval == wanIfacePath {
					base := strings.TrimSuffix(pk, ".Interface")
					dnsStr := params[base+".DNSServers"]
					if dnsStr != "" {
						parts := strings.Split(dnsStr, ",")
						if len(parts) > 0 {
							wan.DNS1 = strings.TrimSpace(parts[0])
						}
						if len(parts) > 1 {
							wan.DNS2 = strings.TrimSpace(parts[1])
						}
					}
					break
				}
			}
		}

		wans = append(wans, wan)
		seen[ipAddr] = true
	}

	return wans
}

// buildConnTypePattern derives a per-interface connection-type regex from the
// mapper's WANConnectionTypePath. It replaces the numeric instance segment with
// a capture group so all interfaces can be scanned in a single pass.
//
// Example: "Device.IP.Interface.5.X_TP_ConnType" → ^Device\.IP\.Interface\.(\d+)\.X_TP_ConnType$
// Falls back to the standard TR-181 path when the mapper returns "".
func buildConnTypePattern(mapper datamodel.Mapper) *regexp.Regexp {
	path := mapper.WANConnectionTypePath()
	if path == "" {
		path = "Device.IP.Interface.1.ConnectionType"
	}
	escaped := regexp.QuoteMeta(path)
	// Replace the quoted instance number with a capture group.
	pat := regexp.MustCompile(`\\\.\d+\\\.`).ReplaceAllLiteralString(escaped, `\.(\d+)\.`)
	return regexp.MustCompile("^" + pat + "$")
}

// serviceTypeSuffixFromMapper extracts the field suffix (the part after
// "Device.IP.Interface.{n}.") from the mapper's WANServiceTypePath.
// Returns "" when the path is empty or does not follow the expected structure.
func serviceTypeSuffixFromMapper(mapper datamodel.Mapper) string {
	path := mapper.WANServiceTypePath()
	if path == "" {
		return ""
	}
	const prefix = "Device.IP.Interface."
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	dotIdx := strings.Index(rest, ".")
	if dotIdx < 0 {
		return ""
	}
	return rest[dotIdx+1:]
}

// samePrefix returns true when the first two octets of a and b match.
// This is a rough heuristic used to associate a gateway with a WAN IP that
// may be in an ISP CGNAT range (100.64.0.0/10) or similar non-standard block.
func samePrefix(a, b string) bool {
	aParts := strings.SplitN(a, ".", 3)
	bParts := strings.SplitN(b, ".", 3)
	if len(aParts) < 2 || len(bParts) < 2 {
		return false
	}
	return aParts[0] == bParts[0] && aParts[1] == bParts[1]
}

// parseConnectedHosts parses a Hosts.Host.{i}.* GetParameterValues response
// into a slice of ConnectedHost structs.
func parseConnectedHosts(params map[string]string, mapper datamodel.Mapper) []device.ConnectedHost {
	base := mapper.HostsBasePath() // e.g. "Device.Hosts.Host."

	hostMap := make(map[int]*device.ConnectedHost)
	for k, v := range params {
		if !strings.HasPrefix(k, base) {
			continue
		}
		rest := k[len(base):]
		dotIdx := strings.Index(rest, ".")
		if dotIdx < 0 {
			continue
		}
		idxStr := rest[:dotIdx]
		field := rest[dotIdx+1:]
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		if hostMap[idx] == nil {
			hostMap[idx] = &device.ConnectedHost{Active: true}
		}
		h := hostMap[idx]
		switch field {
		case "MACAddress", "PhysAddress":
			h.MACAddress = v
		case "IPAddress":
			h.IPAddress = v
		case "HostName":
			h.Hostname = v
		case "InterfaceType", "Layer1Interface":
			h.Interface = normaliseInterface(v)
		case "Active":
			h.Active = strings.EqualFold(v, "true") || v == "1"
		case "LeaseTimeRemaining":
			h.LeaseTime, _ = strconv.Atoi(v)
		case "X_TP_LanConnType":
			if v == "1" {
				h.Interface = "Wi-Fi"
			} else if v == "0" {
				h.Interface = "LAN"
			}
		case "AssociatedDevice":
			if v != "" {
				if strings.Contains(v, "AccessPoint.1") {
					h.Interface = "Wi-Fi 2.4GHz"
				} else if strings.Contains(v, "AccessPoint.2") || strings.Contains(v, "AccessPoint.5") {
					h.Interface = "Wi-Fi 5GHz"
				} else {
					h.Interface = "Wi-Fi"
				}

				// Try to get RSSI if the tree was fetched
				if rssiStr := params[v+"SignalStrength"]; rssiStr != "" {
					if r, err := strconv.Atoi(rssiStr); err == nil {
						h.RSSI = &r
					}
				}
			}
		default:
			// ignored
		}
	}

	hosts := make([]device.ConnectedHost, 0, len(hostMap))
	for _, h := range hostMap {
		if h.MACAddress != "" {
			hosts = append(hosts, *h)
		}
	}
	return hosts
}

// parsePortMappingRules parses a PortMapping.{i}.* GetParameterValues response
// into a slice of PortForwardingRule structs.
func parsePortMappingRules(params map[string]string, mapper datamodel.Mapper) []task.PortForwardingRule {
	base := mapper.PortMappingBasePath()

	ruleMap := make(map[int]*task.PortForwardingRule)
	for k, v := range params {
		if !strings.HasPrefix(k, base) {
			continue
		}
		rest := k[len(base):]
		before, after, ok := strings.Cut(rest, ".")
		if !ok {
			continue
		}
		idxStr := before
		field := after
		idx, err := strconv.Atoi(idxStr)
		if err != nil {
			continue
		}
		if ruleMap[idx] == nil {
			ruleMap[idx] = &task.PortForwardingRule{InstanceNumber: idx}
		}
		r := ruleMap[idx]
		switch field {
		case "PortMappingEnabled", "Enable":
			r.Enabled = strings.EqualFold(v, "true") || v == "1"
		case "PortMappingProtocol", "Protocol":
			r.Protocol = v
		case "ExternalPort":
			r.ExternalPort, _ = strconv.Atoi(v)
		case "InternalClient":
			r.InternalIP = v
		case "InternalPort":
			r.InternalPort, _ = strconv.Atoi(v)
		case "PortMappingDescription", "Description":
			r.Description = v
		}
	}

	rules := make([]task.PortForwardingRule, 0, len(ruleMap))
	for i := 1; i <= len(ruleMap); i++ {
		if r, ok := ruleMap[i]; ok {
			rules = append(rules, *r)
		}
	}
	return rules
}

// buildPortMappingParams converts a PortForwardingPayload + instance number
// into a SetParameterValues parameter map.
func buildPortMappingParams(base string, instanceNum int, p task.PortForwardingPayload) map[string]string {
	prefix := fmt.Sprintf("%s%d.", base, instanceNum)
	enabled := "1"
	if p.Enabled != nil && !*p.Enabled {
		enabled = "0"
	}
	proto := p.Protocol
	if proto == "" {
		proto = "TCP"
	}
	return map[string]string{
		prefix + "PortMappingEnabled":       enabled,
		prefix + "PortMappingProtocol":      proto,
		prefix + "ExternalPort":             strconv.Itoa(p.ExternalPort),
		prefix + "InternalClient":           p.InternalIP,
		prefix + "InternalPort":             strconv.Itoa(p.InternalPort),
		prefix + "PortMappingDescription":   p.Description,
		prefix + "PortMappingLeaseDuration": "0", // permanent
	}
}

// normaliseInterface converts TR-069 interface type strings to a simple label.
func normaliseInterface(raw string) string {
	lower := strings.ToLower(raw)
	switch {
	case strings.Contains(lower, "wifi") || strings.Contains(lower, "wlan") || strings.Contains(lower, "wireless"):
		return "WiFi"
	case strings.Contains(lower, "ethernet") || strings.Contains(lower, "lan"):
		return "LAN"
	}
	return raw
}

// extractWiFiInfo reads WiFi parameters for a single band (0=2.4GHz, 1=5GHz)
// from the full summon parameter map and returns a WiFiInfo struct.
func extractWiFiInfo(bandIdx int, params map[string]string, mapper datamodel.Mapper) device.WiFiInfo {
	parseInt := func(key string) int {
		if key == "" {
			return 0
		}
		v, _ := strconv.Atoi(params[key])
		return v
	}

	parseInt64 := func(key string) int64 {
		if key == "" {
			return 0
		}
		v, _ := strconv.ParseInt(params[key], 10, 64)
		return v
	}

	parseBool := func(key string) bool {
		if key == "" {
			return false
		}
		return strings.EqualFold(params[key], "true") || params[key] == "1"
	}

	band := "2.4GHz"
	if bandIdx == 1 {
		band = "5GHz"
	}

	return device.WiFiInfo{
		Band:             band,
		SSID:             params[mapper.WiFiSSIDPath(bandIdx)],
		Enabled:          parseBool(mapper.WiFiEnabledPath(bandIdx)),
		BSSID:            params[mapper.WiFiBSSIDPath(bandIdx)],
		Channel:          parseInt(mapper.WiFiChannelPath(bandIdx)),
		ChannelWidth:     params[mapper.WiFiChannelWidthPath(bandIdx)],
		Standard:         params[mapper.WiFiStandardPath(bandIdx)],
		SecurityMode:     params[mapper.WiFiSecurityModePath(bandIdx)],
		TXPower:          parseInt(mapper.WiFiTXPowerPath(bandIdx)),
		ConnectedClients: parseInt(mapper.WiFiClientCountPath(bandIdx)),
		BytesSent:        parseInt64(mapper.WiFiBytesSentPath(bandIdx)),
		BytesReceived:    parseInt64(mapper.WiFiBytesReceivedPath(bandIdx)),
		PacketsSent:      parseInt64(mapper.WiFiPacketsSentPath(bandIdx)),
		PacketsReceived:  parseInt64(mapper.WiFiPacketsReceivedPath(bandIdx)),
		ErrorsSent:       parseInt64(mapper.WiFiErrorsSentPath(bandIdx)),
		ErrorsReceived:   parseInt64(mapper.WiFiErrorsReceivedPath(bandIdx)),
	}
}

// extractBandSteeringStatus reads Band Steering status from the parameter map.
// The path is looked up from the mapper so it works for any vendor.
// Returns a pointer to bool if found, nil otherwise.
func extractBandSteeringStatus(params map[string]string, mapper datamodel.Mapper) *bool {
	path := mapper.BandSteeringPath()
	if path == "" {
		return nil
	}
	if val, exists := params[path]; exists && val != "" {
		enabled := strings.EqualFold(val, "true") || val == "1"
		return &enabled
	}
	return nil
}

// extractLANInfo reads LAN/DHCP configuration from the full summon parameter map.
func extractLANInfo(params map[string]string, mapper datamodel.Mapper) device.LANInfo {
	parseBool := func(key string) bool {
		if key == "" {
			return false
		}
		return strings.EqualFold(params[key], "true") || params[key] == "1"
	}

	// Parse and clean up DNS servers: filter out invalid addresses like "0.0.0.0.0.0"
	dnsRaw := params[mapper.LANDNSPath()]
	dnsServers := cleanDNSServers(dnsRaw)

	return device.LANInfo{
		IPAddress:   params[mapper.LANIPAddressPath()],
		SubnetMask:  params[mapper.LANSubnetMaskPath()],
		DHCPEnabled: parseBool(mapper.DHCPServerEnablePath()),
		DHCPStart:   params[mapper.DHCPMinAddressPath()],
		DHCPEnd:     params[mapper.DHCPMaxAddressPath()],
		DNSServers:  dnsServers,
	}
}

// cleanDNSServers parses DNS servers from comma-separated format and filters out
// invalid addresses (e.g., "0.0.0.0", "0.0.0.0.0.0", empty strings).
func cleanDNSServers(dnsStr string) string {
	if dnsStr == "" {
		return ""
	}

	// Split by comma
	parts := strings.Split(dnsStr, ",")
	var valid []string

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "0.0.0.0" || part == "0.0.0.0.0.0" || part == "::" {
			continue
		}
		// Basic validation: should look like an IP address
		if isValidIPString(part) {
			valid = append(valid, part)
		}
	}

	return strings.Join(valid, ",")
}

// isValidIPString returns true if s looks like a valid IP address (v4 or v6).
func isValidIPString(s string) bool {
	// Check if it contains enough dots or colons
	if strings.Count(s, ".") >= 3 || strings.Contains(s, ":") {
		return true
	}
	return false
}
