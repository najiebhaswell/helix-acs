// cdata_simulator simulates a C-DATA GPON ONU (FD514GD-R460) TR-098 device
// connecting to the Helix ACS with full parameter support.
//
// It handles the complete TR-069 session lifecycle:
//  1. POST Inform → receive InformResponse
//  2. Receive GetParameterNames → respond with parameter list
//  3. Receive GetParameterValues → respond with parameter values
//  4. POST empty body → receive 204 (session end)
//
// Digest Authentication (RFC 2617) is handled automatically.
//
// Usage:
//
//	go run ./examples/cdata_simulator \
//	  -acs http://localhost:7547/acs \
//	  -serial CDTCAF252D7F
package main

import (
	"bytes"
	"crypto/md5"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SOAP envelope and response structs (minimal - matching ACS expectations)

type SoapEnvelope struct {
	XMLName xml.Name   `xml:"soap:Envelope"`
	NSsoap  string     `xml:"xmlns:soap,attr"`
	NScwmp  string     `xml:"xmlns:cwmp,attr"`
	Header  SoapHeader `xml:"soap:Header"`
	Body    SoapBody   `xml:"soap:Body"`
}

type SoapHeader struct {
	ID *SoapID `xml:"cwmp:ID,omitempty"`
}

type SoapID struct {
	Value string `xml:",chardata"`
}

type SoapBody struct {
	GetParameterNamesResponse  *GetParameterNamesResponse  `xml:"cwmp:GetParameterNamesResponse,omitempty"`
	GetParameterValuesResponse *GetParameterValuesResponse `xml:"cwmp:GetParameterValuesResponse,omitempty"`
}

type GetParameterNamesResponse struct {
	ParameterList ParameterInfoList `xml:"ParameterList"`
}

type ParameterInfoList struct {
	ParameterInfoStructs []ParameterInfoStruct `xml:"ParameterInfoStruct"`
}

type ParameterInfoStruct struct {
	Name     string `xml:"Name"`
	Writable bool   `xml:"Writable"`
}

type GetParameterValuesResponse struct {
	ParameterList ParameterValueList `xml:"ParameterList"`
}

type ParameterValueList struct {
	ParameterValueStructs []ParameterValueStruct `xml:"ParameterValueStruct"`
}

type ParameterValueStruct struct {
	Name  string `xml:"Name"`
	Value string `xml:"Value"`
}

var (
	flagACS      = flag.String("acs", "http://localhost:7547/acs", "ACS URL")
	flagUsername = flag.String("username", "acs", "ACS username (Digest)")
	flagPassword = flag.String("password", "acs123", "ACS password (Digest)")
	flagSerial   = flag.String("serial", "CDTCAF252D7F", "CPE serial number")
	flagInterval = flag.Int("interval", 0, "Inform interval in seconds (0 = single shot)")
)

func main() {
	flag.Parse()

	logf("C-DATA CPE Simulator starting")
	logf("  Serial  : %s", *flagSerial)
	logf("  ACS URL : %s", *flagACS)

	if *flagInterval > 0 {
		logf("  Mode: periodic, every %ds", *flagInterval)
		for {
			runSession()
			logf("Waiting %ds before next Inform...", *flagInterval)
			time.Sleep(time.Duration(*flagInterval) * time.Second)
		}
	} else {
		logf("  Mode: single-shot")
		runSession()
	}
}

func runSession() {
	sessionID := uuid.NewString()
	logf("\n=== Session %s ===", sessionID[:8])

	// Step 1: Send Inform
	informBody := buildInform(sessionID)
	logf("[1] Sending Inform...")

	resp, body, cookies, err := doRequestWithDigest("POST", *flagACS, *flagUsername, *flagPassword, []byte(informBody), nil)
	if err != nil {
		logf("ERROR: Inform failed: %v", err)
		return
	}
	logf("    → HTTP %d (InformResponse)", resp.StatusCode)

	// Step 2: Handle CWMP operations until session end
	paramValuesBatch := 0

	for {
		// POST response body (or empty to wait for next RPC)
		var postBody []byte
		resp, body, cookies, err = doRequestWithDigest("POST", *flagACS, *flagUsername, *flagPassword, postBody, cookies)
		if err != nil {
			logf("ERROR: POST failed: %v", err)
			break
		}

		logf("    → HTTP %d", resp.StatusCode)

		if resp.StatusCode == http.StatusNoContent {
			logf("    ✓ Session closed (204 No Content)")
			break
		}

		if resp.StatusCode != http.StatusOK {
			logf("    WARNING: unexpected status %d", resp.StatusCode)
			break
		}

		respStr := string(body)

		// Determine what RPC this is and respond
		if strings.Contains(respStr, "<cwmp:GetParameterNames") {
			logf("    ← GetParameterNames, responding...")
			response := buildGetParameterNamesResponse(sessionID, respStr)
			logf("    [DEBUG] Response length: %d bytes", len(response))
			postBody = []byte(response)

		} else if strings.Contains(respStr, "<cwmp:GetParameterValues") {
			paramValuesBatch++
			logf("    ← GetParameterValues batch %d, responding...", paramValuesBatch)
			response := buildGetParameterValuesResponse(sessionID, respStr)
			postBody = []byte(response)

		} else if strings.Contains(respStr, "InformResponse") {
			logf("    ← InformResponse (skip)")
			continue

		} else if len(respStr) > 0 {
			logf("    WARNING: Unknown CWMP RPC")
			break
		}
	}

	logf("=== Session complete ===")
}

// HTTP + Digest Auth

func doRequestWithDigest(method, url, username, password string, body []byte, cookies []*http.Cookie) (*http.Response, []byte, []*http.Cookie, error) {
	client := &http.Client{Timeout: 30 * time.Second}

	req, err := newReq(method, url, body)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		b, _ := io.ReadAll(resp.Body)
		return resp, b, resp.Cookies(), nil
	}

	// Parse Digest challenge
	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Digest ") {
		return nil, nil, nil, fmt.Errorf("unexpected auth scheme: %s", challenge)
	}

	params := parseDigestChallenge(challenge[7:])
	realm := params["realm"]
	nonce := params["nonce"]
	qop := params["qop"]
	algorithm := params["algorithm"]
	if algorithm == "" {
		algorithm = "MD5"
	}

	cnonce := uuid.NewString()[:8]
	nc := "00000001"
	uri := extractPath(url)

	ha1 := md5hex(username + ":" + realm + ":" + password)
	ha2 := md5hex(method + ":" + uri)

	var digestResp string
	if strings.Contains(qop, "auth") {
		digestResp = md5hex(ha1 + ":" + nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
	} else {
		digestResp = md5hex(ha1 + ":" + nonce + ":" + ha2)
	}

	authHeader := fmt.Sprintf(
		`Digest username=%q, realm=%q, nonce=%q, uri=%q, algorithm=%s, qop=auth, nc=%s, cnonce=%q, response=%q`,
		username, realm, nonce, uri, algorithm, nc, cnonce, digestResp,
	)

	req2, err := newReq(method, url, body)
	if err != nil {
		return nil, nil, nil, err
	}
	req2.Header.Set("Authorization", authHeader)
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	for _, c := range resp.Cookies() {
		req2.AddCookie(c)
	}

	resp2, err := client.Do(req2)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("http (digest): %w", err)
	}
	defer resp2.Body.Close()

	b, _ := io.ReadAll(resp2.Body)
	return resp2, b, resp2.Cookies(), nil
}

func newReq(method, url string, body []byte) (*http.Request, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "text/xml; charset=utf-8")
	req.Header.Set("User-Agent", "C-DATA-CPE/1.0")
	return req, nil
}

// SOAP Response Builders

func buildInform(sessionID string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<soap:Envelope xmlns:soap="http://schemas.xmlsoap.org/soap/envelope/" xmlns:cwmp="urn:dslforum-org:cwmp-1-0">
  <soap:Header>
    <cwmp:ID mustUnderstand="1">%s</cwmp:ID>
  </soap:Header>
  <soap:Body>
    <cwmp:Inform>
      <DeviceId>
        <Manufacturer>ZTEG</Manufacturer>
        <OUI>0060A9</OUI>
        <ProductClass>FD514GD-R460</ProductClass>
        <SerialNumber>%s</SerialNumber>
      </DeviceId>
      <Event>
        <EventStruct>
          <EventCode>0 BOOTSTRAP</EventCode>
          <CommandKey></CommandKey>
        </EventStruct>
      </Event>
      <MaxEnvelopes>1</MaxEnvelopes>
      <CurrentTime>%s</CurrentTime>
      <RetryCount>0</RetryCount>
      <ParameterList>
        <ParameterValueStruct>
          <Name>InternetGatewayDevice.DeviceInfo.Manufacturer</Name>
          <Value>ZTEG</Value>
        </ParameterValueStruct>
        <ParameterValueStruct>
          <Name>InternetGatewayDevice.DeviceInfo.ModelName</Name>
          <Value>FD514GD-R460</Value>
        </ParameterValueStruct>
        <ParameterValueStruct>
          <Name>InternetGatewayDevice.DeviceInfo.SerialNumber</Name>
          <Value>%s</Value>
        </ParameterValueStruct>
        <ParameterValueStruct>
          <Name>InternetGatewayDevice.DeviceInfo.SoftwareVersion</Name>
          <Value>V3.2.18_P396001</Value>
        </ParameterValueStruct>
        <ParameterValueStruct>
          <Name>InternetGatewayDevice.DeviceInfo.HardwareVersion</Name>
          <Value>RS50.1B</Value>
        </ParameterValueStruct>
      </ParameterList>
    </cwmp:Inform>
  </soap:Body>
</soap:Envelope>`, sessionID, *flagSerial, now, *flagSerial)
}

func buildGetParameterNamesResponse(sessionID, request string) string {
	// Extract PathName from GetParameterNames request
	pathName := "InternetGatewayDevice."
	if strings.Contains(request, "<ParameterPath>") {
		start := strings.Index(request, "<ParameterPath>") + len("<ParameterPath>")
		end := strings.Index(request[start:], "</ParameterPath>") + start
		if end > start {
			pathName = request[start:end]
		}
	}

	// Get parameter names for the requested path
	paramNames := getCDataParameterNames(pathName)

	// Build response struct
	var infos []ParameterInfoStruct
	for _, name := range paramNames {
		infos = append(infos, ParameterInfoStruct{
			Name:     name,
			Writable: false,
		})
	}

	resp := &SoapEnvelope{
		XMLName: xml.Name{Local: "soap:Envelope", Space: "http://schemas.xmlsoap.org/soap/envelope/"},
		NSsoap:  "http://schemas.xmlsoap.org/soap/envelope/",
		NScwmp:  "urn:dslforum-org:cwmp-1-0",
		Header: SoapHeader{
			ID: &SoapID{Value: sessionID},
		},
		Body: SoapBody{
			GetParameterNamesResponse: &GetParameterNamesResponse{
				ParameterList: ParameterInfoList{
					ParameterInfoStructs: infos,
				},
			},
		},
	}

	// Marshal to XML
	out, _ := xml.MarshalIndent(resp, "", "  ")
	return xml.Header + string(out)
}

func buildGetParameterValuesResponse(sessionID, request string) string {
	// Parse parameter names from request
	paramNames := parseParameterNames(request)

	// Build response struct
	var values []ParameterValueStruct
	for _, name := range paramNames {
		value := getCDataParameterValue(name)
		values = append(values, ParameterValueStruct{
			Name:  name,
			Value: value,
		})
	}

	resp := &SoapEnvelope{
		XMLName: xml.Name{Local: "soap:Envelope", Space: "http://schemas.xmlsoap.org/soap/envelope/"},
		NSsoap:  "http://schemas.xmlsoap.org/soap/envelope/",
		NScwmp:  "urn:dslforum-org:cwmp-1-0",
		Header: SoapHeader{
			ID: &SoapID{Value: sessionID},
		},
		Body: SoapBody{
			GetParameterValuesResponse: &GetParameterValuesResponse{
				ParameterList: ParameterValueList{
					ParameterValueStructs: values,
				},
			},
		},
	}

	// Marshal to XML
	out, _ := xml.MarshalIndent(resp, "", "  ")
	return xml.Header + string(out)
}

// C-DATA Parameter Database

func getCDataParameterNames(pathName string) []string {
	// All C-DATA TR-098 parameters from schemas
	allParams := []string{
		// System
		"InternetGatewayDevice.DeviceInfo.Manufacturer",
		"InternetGatewayDevice.DeviceInfo.ModelName",
		"InternetGatewayDevice.DeviceInfo.SerialNumber",
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion",
		"InternetGatewayDevice.DeviceInfo.HardwareVersion",
		"InternetGatewayDevice.DeviceInfo.UpTime",
		"InternetGatewayDevice.DeviceInfo.SpecVersion",

		// WAN
		"InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.WANAccessType",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionType",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DefaultGateway",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DNSServers",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MACAddress",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MTU",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionStatus",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Uptime",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.X_CT-COM_BundledBytes",

		// WiFi 2.4G
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Enable",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BSSID",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Standard",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.KeyPassphrase",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BeaconType",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BasicEncryptionModes",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BasicAuthenticationMode",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.ChannelWidth",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.TransmitPower",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.X_CT-COM_ClientCount",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.BytesSent",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.BytesReceived",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.PacketsSent",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.PacketsReceived",

		// WiFi 5G
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Enable",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Channel",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BSSID",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Standard",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.KeyPassphrase",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BeaconType",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BasicEncryptionModes",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BasicAuthenticationMode",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.ChannelWidth",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.TransmitPower",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.X_CT-COM_ClientCount",

		// LAN
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPAddress",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.SubnetMask",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerEnable",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerMinAddress",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerMaxAddress",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DNSServers",

		// Management
		"InternetGatewayDevice.ManagementServer.URL",
		"InternetGatewayDevice.ManagementServer.Username",
		"InternetGatewayDevice.ManagementServer.Password",
		"InternetGatewayDevice.ManagementServer.PeriodicInformInterval",
		"InternetGatewayDevice.ManagementServer.PeriodicInformEnable",
	}

	// Filter by pathName
	var result []string
	for _, param := range allParams {
		if strings.HasPrefix(param, pathName) {
			result = append(result, param)
		}
	}
	return result
}

func getCDataParameterValue(paramName string) string {
	values := map[string]string{
		// System
		"InternetGatewayDevice.DeviceInfo.Manufacturer":    "ZTEG",
		"InternetGatewayDevice.DeviceInfo.ModelName":       "FD514GD-R460",
		"InternetGatewayDevice.DeviceInfo.SerialNumber":    *flagSerial,
		"InternetGatewayDevice.DeviceInfo.SoftwareVersion": "V3.2.18_P396001",
		"InternetGatewayDevice.DeviceInfo.HardwareVersion": "RS50.1B",
		"InternetGatewayDevice.DeviceInfo.UpTime":          "3600",
		"InternetGatewayDevice.DeviceInfo.SpecVersion":     "1.0",

		// WAN
		"InternetGatewayDevice.WANDevice.1.WANCommonInterfaceConfig.WANAccessType":                         "PPP",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionType":        "IP_Routed",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Username":              "testuser",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Password":              "testpass123",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ExternalIPAddress":     "10.201.77.80",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DefaultGateway":        "10.201.77.1",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.DNSServers":            "8.8.8.8,8.8.4.4",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MACAddress":            "d0:5f:af:25:2d:7f",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.MTU":                   "1492",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.ConnectionStatus":      "Connected",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.Uptime":                "86400",
		"InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.X_CT-COM_BundledBytes": "1048576000",

		// WiFi 2.4G
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.SSID":                    "C-DATA-2.4G",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Enable":                  "true",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Channel":                 "6",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BSSID":                   "d0:5f:af:00:00:01",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Standard":                "b,g,n",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.KeyPassphrase":           "cdata12345",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BeaconType":              "WPA2",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BasicEncryptionModes":    "TKIP,AES",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.BasicAuthenticationMode": "WPA2PSK",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.ChannelWidth":            "20",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.TransmitPower":           "100",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.X_CT-COM_ClientCount":    "5",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.BytesSent":         "536870912",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.BytesReceived":     "1073741824",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.PacketsSent":       "1024000",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.1.Stats.PacketsReceived":   "2048000",

		// WiFi 5G
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.SSID":                    "C-DATA-5G",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Enable":                  "true",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Channel":                 "36",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BSSID":                   "d0:5f:af:00:00:02",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.Standard":                "a,n,ac",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.KeyPassphrase":           "cdata12345",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BeaconType":              "WPA2",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BasicEncryptionModes":    "AES",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.BasicAuthenticationMode": "WPA2PSK",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.ChannelWidth":            "80",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.TransmitPower":           "100",
		"InternetGatewayDevice.LANDevice.1.WLANConfiguration.2.X_CT-COM_ClientCount":    "8",

		// LAN
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.IPAddress":  "192.168.1.1",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.IPInterface.1.SubnetMask": "255.255.255.0",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerEnable":         "true",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerMinAddress":     "192.168.1.100",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DHCPServerMaxAddress":     "192.168.1.250",
		"InternetGatewayDevice.LANDevice.1.LANHostConfigManagement.DNSServers":               "8.8.8.8,8.8.4.4",

		// Management
		"InternetGatewayDevice.ManagementServer.URL":                    "http://192.167.77.60:7547/acs",
		"InternetGatewayDevice.ManagementServer.Username":               "acs",
		"InternetGatewayDevice.ManagementServer.Password":               "acs123",
		"InternetGatewayDevice.ManagementServer.PeriodicInformInterval": "300",
		"InternetGatewayDevice.ManagementServer.PeriodicInformEnable":   "true",
	}

	if val, ok := values[paramName]; ok {
		return val
	}
	return "unknown"
}

func parseParameterNames(request string) []string {
	// Extract parameter names from GetParameterValues request
	var names []string

	lines := strings.Split(request, "\n")
	for _, line := range lines {
		if strings.Contains(line, "<Name>") && strings.Contains(line, "</Name>") {
			start := strings.Index(line, "<Name>") + 6
			end := strings.Index(line[start:], "</Name>") + start
			if end > start {
				names = append(names, line[start:end])
			}
		}
	}
	return names
}

// Digest Auth Helpers

func parseDigestChallenge(challenge string) map[string]string {
	result := make(map[string]string)
	parts := strings.Split(challenge, ",")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if idx := strings.Index(part, "="); idx > -1 {
			key := strings.TrimSpace(part[:idx])
			val := strings.TrimSpace(part[idx+1:])
			val = strings.Trim(val, `"`)
			result[key] = val
		}
	}
	return result
}

func extractPath(url string) string {
	if idx := strings.LastIndex(url, "/"); idx > -1 {
		return url[idx:]
	}
	return "/"
}

func md5hex(s string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(s)))
}

func logf(format string, args ...interface{}) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
}
