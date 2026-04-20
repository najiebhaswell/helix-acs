package datamodel

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// InstanceMap holds the discovered instance indices for a CPE's key TR-069 objects.
// Any field left at zero means "not discovered"; the mapper falls back to its
// hardcoded default in that case.
type InstanceMap struct {
	// TR-181: Device.IP.Interface.{WANIPIfaceIdx} carries the public WAN IP.
	WANIPIfaceIdx int
	// TR-181: Device.IP.Interface.{LANIPIfaceIdx} carries the private LAN IP.
	LANIPIfaceIdx int
	// TR-181: Device.PPP.Interface.{PPPIfaceIdx}
	PPPIfaceIdx int
	// TR-181: Device.Ethernet.VLANTermination.{WANVLANTermIdx} linked to WAN/PPPoE
	WANVLANTermIdx int
	// TR-181: Device.Ethernet.Link.{WANEthLinkIdx} linked to WAN/PPPoE (for delete+add provisioning)
	WANEthLinkIdx int
	// TR-181: Current VLAN ID value on WANVLANTermination (for change detection)
	WANCurrentVLAN int
	// TR-181: First unused (disabled) Device.X_TP_GPON.Link.{i} slot.
	FreeGPONLinkIdx int

	// TR-181 WiFi instances indexed by band (0=2.4GHz, 1=5GHz, 2=6GHz).
	// A zero entry means the band was not discovered for that slot.
	WiFiSSIDIndices  []int // Device.WiFi.SSID.{i}
	WiFiRadioIndices []int // Device.WiFi.Radio.{i}
	WiFiAPIndices    []int // Device.WiFi.AccessPoint.{i}

	// TR-098: WANDevice.{WANDeviceIdx}.WANConnectionDevice.{WANConnDevIdx}
	WANDeviceIdx  int
	WANConnDevIdx int
	// TR-098: WANIPConnection.{WANIPConnIdx}
	WANIPConnIdx int
	// TR-098: WANPPPConnection.{WANPPPConnIdx}
	WANPPPConnIdx int
	// TR-098: LANDevice.{LANDeviceIdx}
	LANDeviceIdx int
	// TR-098: WLANConfiguration.{i} per band (0=2.4GHz, 1=5GHz, …).
	WLANIndices []int
}

// DiscoverInstances scans a flat parameter map (e.g. from an Inform or a
// GetParameterValues response) and returns the best-known instance indices for
// WAN, LAN, and WiFi objects.
//
// It detects the data model from the parameter key prefixes and delegates to
// the appropriate scanner. Any indices that cannot be determined from the
// available parameters are left at zero so the caller can apply safe defaults.
func DiscoverInstances(params map[string]string) InstanceMap {
	var im InstanceMap
	if isTR181Params(params) {
		discoverTR181(params, &im)
	} else {
		discoverTR098(params, &im)
	}
	return im
}

// isTR181Params returns true when at least one key starts with "Device.".
func isTR181Params(params map[string]string) bool {
	for k := range params {
		if strings.HasPrefix(k, "Device.") {
			return true
		}
	}
	return false
}

// ---- TR-181 discovery -------------------------------------------------------

var (
	reIPIfaceAddr      = regexp.MustCompile(`^Device\.IP\.Interface\.(\d+)\.IPv4Address\.\d+\.IPAddress$`)
	reIPIfaceAddrType  = regexp.MustCompile(`^Device\.IP\.Interface\.(\d+)\.IPv4Address\.\d+\.AddressingType$`)
	reIPIfaceConnType  = regexp.MustCompile(`^Device\.IP\.Interface\.(\d+)\.X_TP_ConnType$`)
	reIPIfaceLower     = regexp.MustCompile(`^Device\.IP\.Interface\.(\d+)\.LowerLayers$`)
	rePPPIface         = regexp.MustCompile(`^Device\.PPP\.Interface\.(\d+)\.`)
	rePPPIfaceRef      = regexp.MustCompile(`Device\.PPP\.Interface\.(\d+)\.`)
	rePPPIfaceLower    = regexp.MustCompile(`^Device\.PPP\.Interface\.(\d+)\.LowerLayers$`)
	reVLANTermVLANID   = regexp.MustCompile(`^Device\.Ethernet\.VLANTermination\.(\d+)\.VLANID$`)
	reVLANTermAnything = regexp.MustCompile(`^Device\.Ethernet\.VLANTermination\.(\d+)\.`)
	reVLANTermRef      = regexp.MustCompile(`Device\.Ethernet\.VLANTermination\.(\d+)\.`)
	reRadioFreq        = regexp.MustCompile(`^Device\.WiFi\.Radio\.(\d+)\.OperatingFrequencyBand$`)
	reRadioStd         = regexp.MustCompile(`^Device\.WiFi\.Radio\.(\d+)\.OperatingStandards$`)
	reSSIDLower        = regexp.MustCompile(`^Device\.WiFi\.SSID\.(\d+)\.LowerLayers$`)
	reAPRef            = regexp.MustCompile(`^Device\.WiFi\.AccessPoint\.(\d+)\.SSIDReference$`)
	reAPAnything       = regexp.MustCompile(`^Device\.WiFi\.AccessPoint\.(\d+)\.`)
	reSSIDAnything     = regexp.MustCompile(`^Device\.WiFi\.SSID\.(\d+)\.`)
)

func discoverTR181(params map[string]string, im *InstanceMap) {
	discoverTR181WAN(params, im)
	discoverTR181PPP(params, im)
	discoverTR181VLAN(params, im)
	discoverTR181WiFi(params, im)
	discoverTR181FreeGPON(params, im)
}

var reGPONLinkEnable = regexp.MustCompile(`^Device\.X_TP_GPON\.Link\.(\d+)\.Enable$`)

// discoverTR181FreeGPON finds the first disabled (Enable=0 or empty) GPON Link slot.
func discoverTR181FreeGPON(params map[string]string, im *InstanceMap) {
	var candidates []int
	for name, val := range params {
		m := reGPONLinkEnable.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		if val == "0" || val == "" {
			idx, _ := strconv.Atoi(m[1])
			candidates = append(candidates, idx)
		}
	}
	sort.Ints(candidates)
	if len(candidates) > 0 {
		im.FreeGPONLinkIdx = candidates[0]
	}
}

func discoverTR181WAN(params map[string]string, im *InstanceMap) {
	// Pass 0: check TP-Link specific X_TP_ConnType for robust mapping
	for name, val := range params {
		if strings.HasSuffix(name, ".X_TP_ConnType") {
			m := reIPIfaceConnType.FindStringSubmatch(name)
			if m != nil {
				idx, _ := strconv.Atoi(m[1])
				if val == "LAN" && im.LANIPIfaceIdx == 0 {
					im.LANIPIfaceIdx = idx
				} else if val == "PPPoE" && im.WANIPIfaceIdx == 0 {
					im.WANIPIfaceIdx = idx
				}
			}
		}
	}

	// First pass: find public-IP interface (best case) and private LAN interface.
	for name, val := range params {
		if val == "" {
			continue
		}
		m := reIPIfaceAddr.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		if isPublicIP(val) && im.WANIPIfaceIdx == 0 {
			im.WANIPIfaceIdx = idx
		} else if isPrivateIP(val) && im.LANIPIfaceIdx == 0 {
			// Avoid setting a WAN management IP as LAN
			if connType := params[fmt.Sprintf("Device.IP.Interface.%d.X_TP_ConnType", idx)]; connType != "" && connType != "LAN" {
				continue
			}
			im.LANIPIfaceIdx = idx
		}
	}

	// Second pass: fallback to IPCP for PPPoE behind CGNAT.
	if im.WANIPIfaceIdx == 0 {
		for name, val := range params {
			if val != "IPCP" {
				continue
			}
			m := reIPIfaceAddrType.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			idx, _ := strconv.Atoi(m[1])
			if im.WANIPIfaceIdx == 0 || idx < im.WANIPIfaceIdx {
				im.WANIPIfaceIdx = idx
			}
		}
	}
}

func discoverTR181PPP(params map[string]string, im *InstanceMap) {
	// Collect all existing PPP interface indices first.
	allPPP := map[int]bool{}
	for name := range params {
		m := rePPPIface.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		allPPP[idx] = true
		if im.PPPIfaceIdx == 0 || idx < im.PPPIfaceIdx {
			im.PPPIfaceIdx = idx
		}
	}

	// Prefer the PPP interface referenced by the discovered WAN IP interface
	// LowerLayers (typical TR-181 PPPoE topology on TP-Link).
	if im.WANIPIfaceIdx == 0 {
		return
	}
	for name, val := range params {
		m := reIPIfaceLower.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		ipIdx, _ := strconv.Atoi(m[1])
		if ipIdx != im.WANIPIfaceIdx {
			continue
		}
		pm := rePPPIfaceRef.FindStringSubmatch(val)
		if pm == nil {
			continue
		}
		pppIdx, _ := strconv.Atoi(pm[1])
		if allPPP[pppIdx] {
			im.PPPIfaceIdx = pppIdx
			return
		}
	}
}

// discoverTR181VLAN finds the VLAN Termination linked to PPPoE and extracts current VLAN ID.
// Also traces back to find the Ethernet Link and GPON Link for delete+add provisioning.
// Traces: Device.PPP.Interface.{pppIdx}.LowerLayers → Device.Ethernet.VLANTermination.{vlanIdx}
// Traces: Device.Ethernet.VLANTermination.{vlanIdx}.LowerLayers → Device.Ethernet.Link.{ethIdx}
// Traces: Device.Ethernet.Link.{ethIdx}.LowerLayers → Device.X_TP_GPON.Link.{gponIdx}
func discoverTR181VLAN(params map[string]string, im *InstanceMap) {
	if im.PPPIfaceIdx == 0 {
		// No PPP interface discovered, cannot find linked VLAN
		return
	}

	// Find PPP LowerLayers to discover VLAN Termination index
	pppLowerKey := fmt.Sprintf("Device.PPP.Interface.%d.LowerLayers", im.PPPIfaceIdx)
	pppLowerVal, exists := params[pppLowerKey]
	if !exists || pppLowerVal == "" {
		// No LowerLayers found - cannot trace to VLAN
		return
	}

	// Extract VLAN Termination index from reference (e.g., "Device.Ethernet.VLANTermination.1.")
	m := reVLANTermRef.FindStringSubmatch(pppLowerVal)
	if m == nil {
		// Could not parse VLAN Termination reference
		return
	}
	vlanIdx, _ := strconv.Atoi(m[1])
	im.WANVLANTermIdx = vlanIdx

	// Extract current VLAN ID from Device.Ethernet.VLANTermination.{vlanIdx}.VLANID
	vlanIDKey := fmt.Sprintf("Device.Ethernet.VLANTermination.%d.VLANID", vlanIdx)
	if vlanIDVal, ok := params[vlanIDKey]; ok && vlanIDVal != "" {
		vlanID, err := strconv.Atoi(vlanIDVal)
		if err == nil {
			im.WANCurrentVLAN = vlanID
		}
	}

	// Find Ethernet Link by tracing VLAN Termination LowerLayers
	vlanLowerKey := fmt.Sprintf("Device.Ethernet.VLANTermination.%d.LowerLayers", vlanIdx)
	vlanLowerVal, exists := params[vlanLowerKey]
	if exists && vlanLowerVal != "" {
		// Extract Ethernet Link index from reference (e.g., "Device.Ethernet.Link.2.")
		reEthLinkRef := regexp.MustCompile(`Device\.Ethernet\.Link\.(\d+)\.`)
		m := reEthLinkRef.FindStringSubmatch(vlanLowerVal)
		if m != nil {
			ethIdx, _ := strconv.Atoi(m[1])
			im.WANEthLinkIdx = ethIdx

			// Find GPON Link by tracing Ethernet Link LowerLayers
			ethLowerKey := fmt.Sprintf("Device.Ethernet.Link.%d.LowerLayers", ethIdx)
			if ethLowerVal, ok := params[ethLowerKey]; ok && ethLowerVal != "" {
				// Extract GPON index from reference (e.g., "Device.X_TP_GPON.Link.3.")
				reGPONRef := regexp.MustCompile(`Device\.X_TP_GPON\.Link\.(\d+)\.`)
				if m := reGPONRef.FindStringSubmatch(ethLowerVal); m != nil {
					gponIdx, _ := strconv.Atoi(m[1])
					if gponIdx > 0 {
						im.FreeGPONLinkIdx = gponIdx // reuse this GPON Link
					}
				}
			}
		}
	}
}

func discoverTR181WiFi(params map[string]string, im *InstanceMap) {
	// Step 1: map Radio instance → band via OperatingFrequencyBand or OperatingStandards.
	radioToBand := map[int]int{}

	// Try OperatingFrequencyBand first (standard parameter)
	for name, val := range params {
		m := reRadioFreq.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		radioIdx, _ := strconv.Atoi(m[1])
		switch val {
		case "2.4GHz":
			radioToBand[radioIdx] = 0
		case "5GHz":
			radioToBand[radioIdx] = 1
		case "6GHz":
			radioToBand[radioIdx] = 2
		}
	}

	// Fallback: detect band from OperatingStandards for TP-Link devices
	// that don't set OperatingFrequencyBand
	if len(radioToBand) == 0 {
		for name, val := range params {
			m := reRadioStd.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			radioIdx, _ := strconv.Atoi(m[1])
			// 802.11b/g/n are 2.4GHz; 802.11a/ac are 5GHz; 802.11ax can be 2.4/5/6GHz; 802.11ad/ay are 60GHz
			if strings.Contains(val, "b") || strings.Contains(val, "g") ||
				(strings.Contains(val, "n") && !strings.Contains(val, "a")) {
				radioToBand[radioIdx] = 0 // 2.4GHz
			} else if strings.Contains(val, "a") && !strings.Contains(val, "n") {
				radioToBand[radioIdx] = 1 // 5GHz (pure 802.11a)
			} else if strings.Contains(val, "ac") ||
				(strings.Contains(val, "a") && strings.Contains(val, "n")) {
				radioToBand[radioIdx] = 1 // 5GHz (802.11a/n/ac)
			} else if strings.Contains(val, "ax") {
				// 802.11ax (WiFi 6) can operate on 2.4GHz, 5GHz, or 6GHz.
				// Detect band from explicit band indicators in OperatingStandards.
				// TP-Link and other vendors may report values like "2.4ax", "5ax", or "6ax".
				if strings.Contains(val, "2.4") {
					radioToBand[radioIdx] = 0 // 2.4GHz
				} else if strings.Contains(val, "5") {
					radioToBand[radioIdx] = 1 // 5GHz
				} else if strings.Contains(val, "6") {
					radioToBand[radioIdx] = 2 // 6GHz
				}
				// Otherwise band cannot be determined from value; leave unset
			}
		}
	}

	// Step 2: map SSID instance → band via LowerLayers → Radio reference.
	ssidToBand := map[int]int{}
	if len(radioToBand) > 0 {
		for name, val := range params {
			m := reSSIDLower.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			ssidIdx, _ := strconv.Atoi(m[1])
			for radioIdx, band := range radioToBand {
				if strings.Contains(val, "Device.WiFi.Radio."+strconv.Itoa(radioIdx)+".") {
					ssidToBand[ssidIdx] = band
				}
			}
		}
	}

	// Fallback: if LowerLayers are absent, use heuristic to assign bands.
	// For TP-Link Band Steering with detected Radio bands:
	// - Map SSIDs to bands based on detected Radio band info
	// For TP-Link Band Steering (2 radio, 4 SSIDs):
	// - SSID 1,2 → band 0 (2.4GHz)
	// - SSID 3,4 → band 1 (5GHz)
	// For TP-Link Band Steering with many SSIDs (8+ SSIDs, 2 radios):
	// - SSID 1,3,5,7... → band 0 (2.4GHz)
	// - SSID 2,4,6,8... → band 1 (5GHz)
	if len(ssidToBand) == 0 && len(radioToBand) > 0 {
		// We have Radio band info but SSIDs lack LowerLayers
		// Use alternating pattern for multi-SSID TP-Link devices
		var indices []int
		seen := map[int]bool{}
		for name := range params {
			m := reSSIDAnything.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			idx, _ := strconv.Atoi(m[1])
			if !seen[idx] {
				seen[idx] = true
				indices = append(indices, idx)
			}
		}
		sort.Ints(indices)

		// For TP-Link with 2 radios and band steering, alternate odd/even or pattern
		if len(indices) >= 4 && len(radioToBand) == 2 {
			// TP-Link pattern: SSIDs interleave between bands in blocks of 2 usually
			for _, ssidIdx := range indices {
				switch ssidIdx {
				case 1, 2, 5, 6, 9, 11, 13:
					ssidToBand[ssidIdx] = 0 // 2.4GHz
				case 3, 4, 7, 8, 10, 12, 14:
					ssidToBand[ssidIdx] = 1 // 5GHz
				default:
					if ssidIdx%2 == 1 {
						ssidToBand[ssidIdx] = 0
					} else {
						ssidToBand[ssidIdx] = 1
					}
				}
			}
		} else {
			// Fallback: assign each index to a unique band in round-robin
			for i, idx := range indices {
				band := i % len(radioToBand)
				ssidToBand[idx] = band
			}
		}
	}

	// Final fallback: if we still have no band detection
	if len(ssidToBand) == 0 {
		var indices []int
		seen := map[int]bool{}
		for name := range params {
			m := reSSIDAnything.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			idx, _ := strconv.Atoi(m[1])
			if !seen[idx] {
				seen[idx] = true
				indices = append(indices, idx)
			}
		}
		sort.Ints(indices)

		// Special case: TP-Link Band Steering with 4 SSIDs (1,2,3,4)
		if len(indices) == 4 && indices[0] == 1 && indices[1] == 2 && indices[2] == 3 && indices[3] == 4 {
			// SSID 1,2 → band 0 (2.4GHz)
			// SSID 3,4 → band 1 (5GHz)
			ssidToBand[1] = 0
			ssidToBand[2] = 0
			ssidToBand[3] = 1
			ssidToBand[4] = 1
		} else {
			// Fallback: assign each index to a unique band
			for band, idx := range indices {
				ssidToBand[idx] = band
			}
		}
	}

	if len(ssidToBand) == 0 {
		return // no WiFi parameters found
	}

	// Calculate maxBand early for use in fallback logic
	maxBand := 0
	for _, b := range ssidToBand {
		if b > maxBand {
			maxBand = b
		}
	}
	for _, b := range radioToBand {
		if b > maxBand {
			maxBand = b
		}
	}

	// Step 3: map AccessPoint instance → band via SSIDReference → SSID reference.
	apToBand := map[int]int{}
	for name, val := range params {
		m := reAPRef.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		apIdx, _ := strconv.Atoi(m[1])
		for ssidIdx, band := range ssidToBand {
			if strings.Contains(val, "Device.WiFi.SSID."+strconv.Itoa(ssidIdx)+".") {
				apToBand[apIdx] = band
			}
		}
	}

	// Fallback: if no AccessPoint-SSID references found (common on TP-Link),
	// assume AccessPoint indices match SSID indices or infer from available data
	if len(apToBand) == 0 && len(ssidToBand) > 0 {
		// Check for AccessPoint parameters with same indices as SSIDs
		for name := range params {
			m := reAPAnything.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			apIdx, _ := strconv.Atoi(m[1])
			// If this AP index has a corresponding SSID with same index, use same band
			if band, ok := ssidToBand[apIdx]; ok {
				apToBand[apIdx] = band
				fmt.Fprintf(os.Stderr, "DEBUG: AP.%d matched to SSID.%d band=%d\n", apIdx, apIdx, band)
			}
		}
	}

	// Further fallback: if still no AP-to-band mapping, map first AP to first band, etc.
	if len(apToBand) == 0 && len(ssidToBand) > 0 {
		// Collect all discovered AP indices
		var apIndices []int
		seen := map[int]bool{}
		for name := range params {
			m := reAPAnything.FindStringSubmatch(name)
			if m == nil {
				continue
			}
			apIdx, _ := strconv.Atoi(m[1])
			if !seen[apIdx] {
				seen[apIdx] = true
				apIndices = append(apIndices, apIdx)
			}
		}
		sort.Ints(apIndices)

		// Assign AP indices to bands in order
		if len(apIndices) > 0 {
			bandIdx := 0
			for _, apIdx := range apIndices {
				if bandIdx < len(ssidToBand) {
					apToBand[apIdx] = bandIdx
					bandIdx++
				} else {
					apToBand[apIdx] = maxBand
				}
			}
		}
	}

	// Build index slices from the band maps.

	im.WiFiSSIDIndices = make([]int, maxBand+1)
	im.WiFiRadioIndices = make([]int, maxBand+1)
	im.WiFiAPIndices = make([]int, maxBand+1)

	for ssidIdx, band := range ssidToBand {
		if band < len(im.WiFiSSIDIndices) {
			// Keep the smallest (primary) SSID index for each band when multiple SSIDs per band exist
			if im.WiFiSSIDIndices[band] == 0 || ssidIdx < im.WiFiSSIDIndices[band] {
				im.WiFiSSIDIndices[band] = ssidIdx
			}
		}
	}
	for radioIdx, band := range radioToBand {
		if band < len(im.WiFiRadioIndices) {
			im.WiFiRadioIndices[band] = radioIdx
		}
	}
	for apIdx, band := range apToBand {
		if band < len(im.WiFiAPIndices) {
			// Keep the smallest (primary) AP index for each band when multiple APs per band exist
			if im.WiFiAPIndices[band] == 0 || apIdx < im.WiFiAPIndices[band] {
				im.WiFiAPIndices[band] = apIdx
			}
		}
	}
}

// ---- TR-098 discovery -------------------------------------------------------

var (
	reWANIPConn   = regexp.MustCompile(`^InternetGatewayDevice\.WANDevice\.(\d+)\.WANConnectionDevice\.(\d+)\.WANIPConnection\.(\d+)\.`)
	reWANPPPConn  = regexp.MustCompile(`^InternetGatewayDevice\.WANDevice\.(\d+)\.WANConnectionDevice\.(\d+)\.WANPPPConnection\.(\d+)\.`)
	reLANDevice   = regexp.MustCompile(`^InternetGatewayDevice\.LANDevice\.(\d+)\.`)
	reWLANCfg     = regexp.MustCompile(`^InternetGatewayDevice\.LANDevice\.\d+\.WLANConfiguration\.(\d+)\.`)
	reWLANChannel = regexp.MustCompile(`^InternetGatewayDevice\.LANDevice\.\d+\.WLANConfiguration\.(\d+)\.Channel$`)
	reWLANStd     = regexp.MustCompile(`^InternetGatewayDevice\.LANDevice\.\d+\.WLANConfiguration\.(\d+)\.Standard$`)
)

func discoverTR098(params map[string]string, im *InstanceMap) {
	discoverTR098WAN(params, im)
	discoverTR098LAN(params, im)
	discoverTR098WLAN(params, im)
}

func discoverTR098WAN(params map[string]string, im *InstanceMap) {
	for name := range params {
		if m := reWANIPConn.FindStringSubmatch(name); m != nil {
			wanDev, _ := strconv.Atoi(m[1])
			wanConn, _ := strconv.Atoi(m[2])
			wanIP, _ := strconv.Atoi(m[3])
			if im.WANDeviceIdx == 0 {
				im.WANDeviceIdx = wanDev
				im.WANConnDevIdx = wanConn
				im.WANIPConnIdx = wanIP
			}
		}
		if m := reWANPPPConn.FindStringSubmatch(name); m != nil {
			wanDev, _ := strconv.Atoi(m[1])
			wanConn, _ := strconv.Atoi(m[2])
			wanPPP, _ := strconv.Atoi(m[3])
			if im.WANDeviceIdx == 0 {
				im.WANDeviceIdx = wanDev
				im.WANConnDevIdx = wanConn
			}
			if im.WANPPPConnIdx == 0 {
				im.WANPPPConnIdx = wanPPP
			}
		}
	}
}

func discoverTR098LAN(params map[string]string, im *InstanceMap) {
	for name := range params {
		m := reLANDevice.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		if im.LANDeviceIdx == 0 || idx < im.LANDeviceIdx {
			im.LANDeviceIdx = idx
		}
	}
}

func discoverTR098WLAN(params map[string]string, im *InstanceMap) {
	// Step 1: collect all discovered WLAN indices.
	seen := map[int]bool{}
	for name := range params {
		m := reWLANCfg.FindStringSubmatch(name)
		if m == nil {
			continue
		}
		idx, _ := strconv.Atoi(m[1])
		seen[idx] = true
	}
	if len(seen) == 0 {
		return
	}

	// Step 2: detect band per index using Channel value (most reliable).
	// Channel 1-13 → 2.4 GHz (band 0); Channel 36+ → 5 GHz (band 1).
	bandForIdx := map[int]int{}
	for name, val := range params {
		if val == "" {
			continue
		}
		if m := reWLANChannel.FindStringSubmatch(name); m != nil {
			ch, err := strconv.Atoi(val)
			if err != nil {
				continue
			}
			idx, _ := strconv.Atoi(m[1])
			if ch >= 1 && ch <= 13 {
				bandForIdx[idx] = 0
			} else if ch >= 36 {
				bandForIdx[idx] = 1
			}
		}
	}

	// Step 3: fallback — use Standard parameter for indices still unresolved.
	for name, val := range params {
		if val == "" {
			continue
		}
		if m := reWLANStd.FindStringSubmatch(name); m != nil {
			idx, _ := strconv.Atoi(m[1])
			if _, already := bandForIdx[idx]; already {
				continue
			}
			v := strings.ToLower(val)
			if strings.Contains(v, "ac") || (strings.Contains(v, "a") && !strings.Contains(v, "n")) {
				bandForIdx[idx] = 1
			} else if strings.ContainsAny(v, "bgn") {
				bandForIdx[idx] = 0
			}
		}
	}

	// Step 4: if band info was detected, build WLANIndices ordered by band
	// (smallest/primary index per band). This handles both the common CDATA/ZTE
	// layout (2.4 GHz on indices 1-4, 5 GHz on 5-8) and the FD514GD-R460 variant
	// (5 GHz on indices 1-5, 2.4 GHz on 6-10) automatically from real channel data.
	if len(bandForIdx) > 0 {
		primary := map[int]int{} // band → smallest WLAN index
		for idx := range seen {
			band, ok := bandForIdx[idx]
			if !ok {
				continue
			}
			if cur, exists := primary[band]; !exists || idx < cur {
				primary[band] = idx
			}
		}
		if len(primary) > 0 {
			maxBand := 0
			for band := range primary {
				if band > maxBand {
					maxBand = band
				}
			}
			im.WLANIndices = make([]int, maxBand+1)
			for band, idx := range primary {
				im.WLANIndices[band] = idx
			}
			return
		}
	}

	// Step 5: no channel/standard info available — apply CDATA/ZTE index heuristic:
	// indices 1-4 → 2.4 GHz (band 0), indices 5-8 → 5 GHz (band 1).
	// Falls back to sorted order for devices not matching this pattern.
	indices := make([]int, 0, len(seen))
	for idx := range seen {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	have2G, have5G := false, false
	for _, idx := range indices {
		if idx >= 1 && idx <= 4 {
			have2G = true
		} else if idx >= 5 && idx <= 8 {
			have5G = true
		}
	}
	if have2G && have5G {
		primary := map[int]int{}
		for _, idx := range indices {
			if idx >= 1 && idx <= 4 {
				if _, exists := primary[0]; !exists {
					primary[0] = idx
				}
			} else if idx >= 5 && idx <= 8 {
				if _, exists := primary[1]; !exists {
					primary[1] = idx
				}
			}
		}
		if len(primary) > 0 {
			maxBand := 0
			for band := range primary {
				if band > maxBand {
					maxBand = band
				}
			}
			im.WLANIndices = make([]int, maxBand+1)
			for band, idx := range primary {
				im.WLANIndices[band] = idx
			}
			return
		}
	}

	// Final fallback: sorted order (original behaviour for unknown devices).
	im.WLANIndices = indices
}

// ---- IP classification helpers ----------------------------------------------

var privateRanges []*net.IPNet

func init() {
	for _, cidr := range []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"100.64.0.0/10", // CGNAT
	} {
		_, network, _ := net.ParseCIDR(cidr)
		if network != nil {
			privateRanges = append(privateRanges, network)
		}
	}
}

// isPrivateIP returns true when s is a valid IPv4 address in a private or
// special-use range (RFC 1918, loopback, link-local, CGNAT).
func isPrivateIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	ip = ip.To4()
	if ip == nil {
		return false
	}
	for _, r := range privateRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

// isPublicIP returns true when s is a routable, non-private IPv4 address.
func isPublicIP(s string) bool {
	ip := net.ParseIP(s)
	if ip == nil {
		return false
	}
	ip = ip.To4()
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
		return false
	}
	return !isPrivateIP(s)
}
