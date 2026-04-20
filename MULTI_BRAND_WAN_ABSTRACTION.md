# Multi-Brand WAN Provisioning Abstraction

## Objective
Abstract WAN provisioning logic to support multiple device vendors (TP-Link, Huawei, ZTE, etc.) instead of hardcoding TP-Link-specific paths and flows.

## Completed Changes

### 1. Mapper Interface Extensions (`internal/datamodel/model.go`)
Added 3 new methods to the `Mapper` interface:
- `WANServiceTypePath()` — returns path for service label (e.g., `Device.IP.Interface.X.X_TP_ServiceType` for TP-Link, empty for generic)
- `BandSteeringPath()` — returns path for band steering enable/disable (empty for devices without this feature)
- `WANProvisioningType()` — returns `"add_object"` for TP-Link multi-step flow, `"set_params"` for generic devices

### 2. Mapper Implementations
- **TR181Mapper** (`internal/datamodel/tr181.go`): Returns empty defaults for vendor-specific fields, `"set_params"` for provisioning type
- **TR098Mapper** (`internal/datamodel/tr098.go`): Same defaults as TR-181
- **SchemaMapper** (`internal/schema/mapper.go`): Resolves paths from YAML schema:
  - `wan.service_type` → `WANServiceTypePath()`
  - `wifi.band_steering` → `BandSteeringPath()`
  - `wan.provisioning_type` → `WANProvisioningType()` (defaults to `"set_params"`)

### 3. Schema YAML Updates
- **`schemas/tr181/wan.yaml`**: Added `wan.service_type` (empty) and `wan.provisioning_type` (`set_params`)
- **`schemas/vendors/tplink/tr181/wan.yaml`**: Added:
  - `wan.service_type` → `Device.IP.Interface.{wan}.X_TP_ServiceType`
  - `wan.provisioning_type` → `add_object`
- **`schemas/vendors/tplink/tr181/wifi.yaml`**: Added `wifi.band_steering` alias pointing to `Device.WiFi.X_TP_BandSteering.Enable`

### 4. WAN Extraction Abstraction (`internal/cwmp/results.go`)
- **`extractWANInfo()`**: Now uses `mapper.WANServiceTypePath()` to read service type label
- **`extractWANInfos()`**: Replaced hardcoded `X_TP_ConnType` loop with:
  - `buildConnTypePattern()` — derives regex from `mapper.WANConnectionTypePath()`
  - `serviceTypeSuffixFromMapper()` — extracts field suffix from `mapper.WANServiceTypePath()`
  - Scans all IP interfaces for connection-type parameters regardless of vendor

### 5. Band Steering Abstraction (`internal/cwmp/results.go`, `internal/cwmp/session.go`)
- **`extractBandSteeringStatus()`**: Now takes `mapper` parameter and uses `mapper.BandSteeringPath()`
- Returns `nil` for devices without band steering (generic TR-181, TR-098)
- Updated both call sites in `session.go` to pass the mapper

### 6. WAN Provisioning Strategy (`internal/cwmp/wan_provisioner.go`)
Created new file with generic provisioner helpers:
- **`buildGenericWANParams()`**: Builds SetParameterValues for devices using `set_params` provisioning type
  - Uses `mapper.WANPPPoEUserPath()` and `mapper.WANPPPoEPassPath()` for credential paths
- **`buildGenericVLANUpdate()`**: Builds VLAN change parameters for generic TR-181 devices

### 7. Session Task Execution (`internal/cwmp/session.go`)
Updated `executeTask()` TypeWAN case to branch based on `mapper.WANProvisioningType()`:
- **`add_object`** (TP-Link): Uses existing `WANProvision` state machine with multi-step AddObject flow
- **`set_params`** (Generic): Uses `buildGenericWANParams()` for single-shot SetParameterValues

### 8. Bug Fixes (Previous Session)
- Fixed `onSetParams()` in `wan_provision.go` to return success when last step completes instead of calling `buildCurrentXML()` which returns error
- Fixed `extractWANInfo()` to include `ServiceType` from `wanIface + "X_TP_ServiceType"`

## Architecture Summary

### Before (TP-Link Hardcoded)
```
wan_provision.go → multi-step AddObject (GPON → Ethernet → VLAN → PPP → IP → NAT)
extractWANInfos → hardcoded X_TP_ConnType loop
extractBandSteeringStatus → hardcoded X_TP_BandSteering.Enable
```

### After (Multi-Brand)
```
Mapper.WANProvisioningType() → "add_object" | "set_params"
  ├─ "add_object" → WANProvision state machine (TP-Link)
  └─ "set_params" → buildGenericWANParams() (Generic)

Mapper.WANServiceTypePath() → vendor-specific or empty
Mapper.BandSteeringPath() → vendor-specific or empty

Schema YAMLs define:
  - wan.service_type
  - wan.provisioning_type
  - wifi.band_steering
```

## What's Still Vendor-Specific

The following still assume TR-181/TP-Link paths:
1. **VLAN change flow** (`session.go` lines 1101-1132): Uses `Device.Ethernet.VLANTermination.*` paths
2. **PPPoE credential update** (`session.go` lines 1139-1176): Uses `Device.PPP.Interface.*` paths

These are acceptable because:
- Most non-TP-Link ONTs have pre-provisioned WAN interfaces and only need credential updates
- The credential update paths (`WANPPPoEUserPath`, `WANPPPoEPassPath`) are already abstracted via mapper
- VLAN change is rare for non-TP-Link devices (OLT typically handles VLAN assignment)

## Next Steps (Optional)

To fully support Huawei/ZTE TR-098 or other vendors:

1. **Add vendor schema directories**:
   - `schemas/vendors/huawei/tr181/wan.yaml` — override with Huawei-specific paths
   - `schemas/vendors/zte/tr098/wan.yaml` — ZTE TR-098 paths

2. **Add vendor-specific WAN provisioning flows** if needed:
   - Huawei TR-181 may use different object creation pattern
   - ZTE TR-098 uses `WANConnectionDevice` hierarchy

3. **Extend `buildGenericVLANUpdate()`** to support TR-098 VLAN paths if different from TR-181

## Testing

- Build: `go build ./...` ✅ passes
- MockMapper in tests updated with new interface methods ✅
- Existing WAN provisioning tests should continue to work with `WANProvisioningType() == "add_object"` ✅
- All tests pass: `go test ./internal/cwmp/... ./internal/datamodel/... ./internal/schema/...` ✅

## Files Modified

| File | Changes |
|------|---------|
| `internal/datamodel/model.go` | Added 3 Mapper interface methods |
| `internal/datamodel/tr181.go` | Implemented 3 new methods (generic defaults) |
| `internal/datamodel/tr098.go` | Implemented 3 new methods (generic defaults) |
| `internal/schema/mapper.go` | Implemented 3 new methods (schema-driven) |
| `internal/cwmp/session.go` | Updated executeTask TypeWAN branching, band steering calls |
| `internal/cwmp/results.go` | Abstracted extractWANInfos, extractBandSteeringStatus, **fixed extractWANInfo ServiceType to use mapper** |
| `internal/cwmp/wan_provision.go` | Fixed onSetParams() bug |
| `internal/cwmp/wan_provisioner.go` | New file with generic provisioner helpers |
| `internal/cwmp/session_test.go` | Updated mockMapper with new methods |
| `schemas/tr181/wan.yaml` | Added service_type, provisioning_type |
| `schemas/tr098/wan.yaml` | **Added service_type, provisioning_type** |
| `schemas/vendors/tplink/tr181/wan.yaml` | Added service_type, provisioning_type |
| `schemas/vendors/tplink/tr181/wifi.yaml` | Added wifi.band_steering alias |
| `schemas/vendors/huawei/tr181/wan.yaml` | **NEW: Huawei TR-181 WAN schema (set_params)** |
| `schemas/vendors/huawei/tr181/wifi.yaml` | **NEW: Huawei TR-181 WiFi schema (standard paths, no band steering)** |
| `schemas/vendors/zte/tr098/wan.yaml` | **NEW: ZTE TR-098 WAN schema (set_params, WANPPPConnection)** |
| `build/helix-acs` | **Rebuilt binary (Apr 20 2026)** |
