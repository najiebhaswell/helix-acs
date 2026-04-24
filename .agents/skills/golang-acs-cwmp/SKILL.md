---
name: golang-acs-cwmp
description: Generates, reviews, and refactors Golang code for ACS (Auto Configuration Server) that handles TR-069 CWMP protocol with TR-098 (DSL/Device) and TR-181 (Device:2) data models. Use when writing ACS server logic, CWMP session handling, RPC methods, parameter management, or CPE communication in Go.
---

# Golang ACS CWMP Skill

This skill guides coding of an ACS (Auto Configuration Server) in Go that implements the TR-069 CWMP protocol, supporting both TR-098 and TR-181 data models.

---

## Context & Terminology

| Term | Meaning |
|------|---------|
| ACS  | Auto Configuration Server — server side of TR-069 |
| CPE  | Customer Premises Equipment — device being managed |
| CWMP | CPE WAN Management Protocol (TR-069) |
| RPC  | Remote Procedure Call defined in CWMP |
| TR-098 | Legacy data model: `InternetGatewayDevice.` root |
| TR-181 | Modern data model: `Device.` root (Device:2) |
| Inform | CPE-initiated session start with event codes |
| Session | HTTP/SOAP exchange between CPE and ACS |

---

## Project Structure Convention

```
acs/
├── cmd/
│   └── server/
│       └── main.go          # Entry point
├── internal/
│   ├── cwmp/
│   │   ├── session.go       # Session state machine
│   │   ├── inform.go        # Inform RPC handler
│   │   ├── rpc.go           # Generic RPC dispatcher
│   │   └── envelope.go      # SOAP envelope parser/builder
│   ├── datamodel/
│   │   ├── tr098/           # TR-098 parameter definitions
│   │   ├── tr181/           # TR-181 parameter definitions
│   │   └── resolver.go      # Detects and routes data model
│   ├── db/
│   │   ├── device.go        # Device/CPE persistence
│   │   └── parameter.go     # Parameter store (key-value)
│   ├── rpc/
│   │   ├── get_param.go     # GetParameterValues
│   │   ├── set_param.go     # SetParameterValues
│   │   ├── get_names.go     # GetParameterNames
│   │   ├── add_object.go    # AddObject
│   │   ├── delete_object.go # DeleteObject
│   │   ├── download.go      # Download (firmware/config)
│   │   ├── upload.go        # Upload
│   │   └── reboot.go        # Reboot
│   └── api/
│       └── handler.go       # HTTP handlers for ACS endpoint
├── pkg/
│   ├── soap/                # SOAP encoding/decoding helpers
│   └── util/
└── configs/
    └── config.yaml
```

---

## Core Coding Patterns

### 1. SOAP Envelope Struct

Always use `encoding/xml` with proper namespace declarations.

```go
package soap

import "encoding/xml"

const (
    NSEnvelope = "http://schemas.xmlsoap.org/soap/envelope/"
    NSEncoding = "http://schemas.xmlsoap.org/soap/encoding/"
    NSCWMP     = "urn:dslforum-org:cwmp-1-2"
)

type Envelope struct {
    XMLName xml.Name `xml:"soapenv:Envelope"`
    XmlnsSoapenv string `xml:"xmlns:soapenv,attr"`
    XmlnsCwmp    string `xml:"xmlns:cwmp,attr"`
    Header  *Header  `xml:"soapenv:Header,omitempty"`
    Body    Body     `xml:"soapenv:Body"`
}

type Header struct {
    ID struct {
        Value         string `xml:",chardata"`
        MustUnderstand string `xml:"soapenv:mustUnderstand,attr"`
    } `xml:"cwmp:ID"`
}

type Body struct {
    Inform            *InformRequest            `xml:",omitempty"`
    InformResponse    *InformResponse           `xml:",omitempty"`
    GetParameterValues *GetParameterValuesRequest `xml:",omitempty"`
    // ... other RPCs
}
```

### 2. Session State Machine

CWMP sessions follow a strict sequence. Model it explicitly.

```go
type SessionState int

const (
    StateInit SessionState = iota
    StateInformReceived
    StateACSRequesting
    StateComplete
)

type Session struct {
    ID        string
    DeviceID  string
    State     SessionState
    DataModel DataModelType // TR098 or TR181
    mu        sync.Mutex
}

type DataModelType int

const (
    DataModelUnknown DataModelType = iota
    DataModelTR098
    DataModelTR181
)

func (s *Session) DetectDataModel(rootElement string) {
    s.mu.Lock()
    defer s.mu.Unlock()
    switch {
    case strings.HasPrefix(rootElement, "InternetGatewayDevice."):
        s.DataModel = DataModelTR098
    case strings.HasPrefix(rootElement, "Device."):
        s.DataModel = DataModelTR181
    }
}
```

### 3. Inform Handler

```go
type InformRequest struct {
    DeviceID     DeviceID      `xml:"DeviceId"`
    Event        []EventStruct `xml:"Event>EventStruct"`
    MaxEnvelopes int           `xml:"MaxEnvelopes"`
    CurrentTime  string        `xml:"CurrentTime"`
    RetryCount   int           `xml:"RetryCount"`
    ParameterList []ParameterValueStruct `xml:"ParameterList>ParameterValueStruct"`
}

type DeviceID struct {
    Manufacturer string `xml:"Manufacturer"`
    OUI          string `xml:"OUI"`
    ProductClass string `xml:"ProductClass"`
    SerialNumber string `xml:"SerialNumber"`
}

func HandleInform(ctx context.Context, req *InformRequest, sess *Session) (*InformResponse, error) {
    // 1. Upsert device in DB
    // 2. Detect data model from ParameterList root key
    for _, pv := range req.ParameterList {
        sess.DetectDataModel(pv.Name)
        break
    }
    // 3. Store parameters
    // 4. Return InformResponse with MaxEnvelopes=1
    return &InformResponse{MaxEnvelopes: 1}, nil
}
```

### 4. Data Model Resolver

TR-098 and TR-181 have different parameter paths for the same concept. Always normalize.

```go
// resolver.go
type ParamPath struct {
    TR098 string
    TR181 string
}

var commonPaths = map[string]ParamPath{
    "wan_ip": {
        TR098: "InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANIPConnection.1.ExternalIPAddress",
        TR181: "Device.IP.Interface.1.IPv4Address.1.IPAddress",
    },
    "firmware_version": {
        TR098: "InternetGatewayDevice.DeviceInfo.SoftwareVersion",
        TR181: "Device.DeviceInfo.SoftwareVersion",
    },
    "uptime": {
        TR098: "InternetGatewayDevice.DeviceInfo.UpTime",
        TR181: "Device.DeviceInfo.UpTime",
    },
}

func Resolve(key string, model DataModelType) string {
    p, ok := commonPaths[key]
    if !ok {
        return ""
    }
    if model == DataModelTR098 {
        return p.TR098
    }
    return p.TR181
}
```

### 5. GetParameterValues RPC

```go
type GetParameterValuesRequest struct {
    ParameterNames []string `xml:"ParameterNames>string"`
}

type ParameterValueStruct struct {
    Name  string `xml:"Name"`
    Value struct {
        Type  string `xml:"type,attr"`
        Value string `xml:",chardata"`
    } `xml:"Value"`
}

func HandleGetParameterValues(ctx context.Context, req *GetParameterValuesRequest, sess *Session, store ParameterStore) (*GetParameterValuesResponse, error) {
    var results []ParameterValueStruct
    for _, name := range req.ParameterNames {
        val, err := store.Get(sess.DeviceID, name)
        if err != nil {
            return nil, fmt.Errorf("parameter %q not found: %w", name, err)
        }
        results = append(results, ParameterValueStruct{
            Name: name,
            // infer xsd type based on value or schema
        })
        _ = val
    }
    return &GetParameterValuesResponse{ParameterList: results}, nil
}
```

### 6. HTTP Handler (ACS Endpoint)

```go
func ACSHandler(sessions SessionStore, devices DeviceStore) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        if r.Method != http.MethodPost && r.ContentLength != 0 {
            // Empty POST = CPE waiting for ACS request
        }

        body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1MB max
        if err != nil {
            http.Error(w, "bad request", http.StatusBadRequest)
            return
        }

        var env soap.Envelope
        if err := xml.Unmarshal(body, &env); err != nil {
            http.Error(w, "invalid SOAP", http.StatusBadRequest)
            return
        }

        // Dispatch to correct RPC handler
        // Return SOAP response with correct Content-Type
        w.Header().Set("Content-Type", "text/xml; charset=utf-8")
        // Write response envelope
    }
}
```

---

## TR-098 vs TR-181 Cheat Sheet

| Feature | TR-098 Root | TR-181 Root |
|---------|-------------|-------------|
| Root | `InternetGatewayDevice.` | `Device.` |
| Firmware | `DeviceInfo.SoftwareVersion` | `DeviceInfo.SoftwareVersion` |
| WAN IP | `WANDevice.{i}.WANConnectionDevice.{i}.WANIPConnection.{i}.ExternalIPAddress` | `IP.Interface.{i}.IPv4Address.{i}.IPAddress` |
| LAN IP | `LANDevice.{i}.LANHostConfigManagement.IPInterface.{i}.IPInterfaceIPAddress` | `Ethernet.Interface.{i}.` |
| WiFi SSID | `LANDevice.{i}.WLANConfiguration.{i}.SSID` | `WiFi.SSID.{i}.SSID` |
| DNS | `WANDevice.{i}...DNSServers` | `DNS.Client.Server.{i}.DNSServer` |

---

## Error Handling Rules

- Always return a SOAP Fault for CWMP errors, never an HTTP error body.
- Use CWMP fault codes (9000–9019) as defined in TR-069 spec.
- Wrap errors with context using `fmt.Errorf("context: %w", err)`.
- Log device OUI+SerialNumber in every log line for traceability.

```go
func CWMPFault(code int, msg string) *soap.Fault {
    return &soap.Fault{
        FaultCode:   "Client",
        FaultString: "CWMP fault",
        Detail: &soap.FaultDetail{
            CWMPFault: &soap.CWMPFault{
                FaultCode:   code,
                FaultString: msg,
            },
        },
    }
}

// Common fault codes
const (
    FaultMethodNotSupported    = 9000
    FaultRequestDenied         = 9001
    FaultInternalError         = 9002
    FaultInvalidArguments      = 9003
    FaultResourcesExceeded     = 9004
    FaultInvalidParameterName  = 9005
    FaultInvalidParameterType  = 9006
    FaultInvalidParameterValue = 9007
    FaultAttemptSetNonWritable = 9008
)
```

---

## Concurrency Guidelines

- Each CPE session runs in its own goroutine.
- Use `sync.Map` or a keyed mutex map for per-device locking.
- Never hold a lock across an I/O or DB call.
- Use `context.Context` for cancellation throughout the call chain.

---

## Testing Approach

- Unit test each RPC handler with mock `ParameterStore`.
- Integration test: spin up ACS HTTP server + simulated CPE Inform.
- Use table-driven tests for TR-098 vs TR-181 parameter resolution.
- Mock SOAP envelopes from real CPE captures whenever possible.

```go
func TestHandleInform_TR181(t *testing.T) {
    req := &InformRequest{
        DeviceID: DeviceID{OUI: "AABBCC", SerialNumber: "12345"},
        ParameterList: []ParameterValueStruct{
            {Name: "Device.DeviceInfo.SoftwareVersion"},
        },
    }
    sess := &Session{}
    _, err := HandleInform(context.Background(), req, sess)
    assert.NoError(t, err)
    assert.Equal(t, DataModelTR181, sess.DataModel)
}
```

---

## Decision Tree

```
Incoming HTTP POST to ACS endpoint
│
├─ Empty body?
│   └─ CPE is waiting → send queued ACS RPC or empty response (204/200 empty)
│
├─ Has SOAP body?
│   ├─ Inform → HandleInform → detect TR-098 or TR-181 → persist → InformResponse
│   ├─ GetParameterValuesResponse → process response from previous ACS RPC
│   ├─ SetParameterValuesResponse → mark task complete
│   ├─ Fault → log + mark task failed
│   └─ Unknown → return CWMP Fault 9000
│
└─ Auth required? → check HTTP Digest/Basic first
```
