package cwmp

// results.go  helpers that parse flat GetParameterValues response maps into
// typed result structs for diagnostic and informational tasks.

import (
	"fmt"
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

	// Find gateway dynamically: scan IPv4Forwarding entries for one pointing
	// to the WAN interface with a non-zero GatewayIPAddress.
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

	// Fallback: if no routing entry matched via Interface field, scan all
	// forwarding entries for a non-zero, non-LAN-subnet gateway.
	if gateway == "" {
		wanIP := params[ipAddrPath]
		for k, v := range params {
			if !strings.HasSuffix(k, ".GatewayIPAddress") || !strings.Contains(k, "IPv4Forwarding") {
				continue
			}
			if v == "" || v == "0.0.0.0" {
				continue
			}
			// Prefer a gateway in the same address range as the WAN IP.
			if wanIP != "" && samePrefix(wanIP, v) {
				gateway = v
				break
			}
			// Otherwise keep the first non-zero candidate.
			if gateway == "" {
				gateway = v
			}
		}
	}

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

	return device.WANInfo{
		ConnectionType: params[mapper.WANConnectionTypePath()],
		IPAddress:      params[mapper.WANIPAddressPath()],
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
