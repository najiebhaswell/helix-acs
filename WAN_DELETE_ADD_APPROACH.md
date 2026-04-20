# WAN PPPoE Update: Delete+Add Approach

## Overview
Implemented a delete+add approach for updating existing PPPoE configurations. Instead of trying to update parameters in-place, the system now deletes the old configuration and provisions fresh.

## Problem Solved
Previous attempt to update credentials and VLAN in-place had ordering issues where VLAN changes wouldn't take effect because device would reconnect before VLAN parameter was applied.

## Solution
**Delete+Add Flow:**
1. Delete IP.Interface (which cascades delete of NAT and DHCPv6)
2. Delete PPP.Interface  
3. Delete Ethernet.VLANTermination
4. Delete Ethernet.Link
5. Reuse existing GPON.Link
6. Add fresh Ethernet.Link with new configuration
7. Add fresh VLAN.Termination with new VLAN ID
8. Add fresh PPP.Interface with new credentials
9. Add fresh IP.Interface with DHCPv6 and NAT

This ensures clean state and proper initialization order.

## Files Changed

### internal/cwmp/wan_provision.go
- Added `newWANProvisionDeleteAndAdd()` function
- Updated `buildCurrentXML()` to handle wanStepDelete case
- DeleteObject support via existing `BuildDeleteObject()` function

### internal/datamodel/instances.go  
- Added `WANEthLinkIdx` field to InstanceMap
- Extended `discoverTR181VLAN()` to trace:
  - PPP → VLANTermination → Ethernet.Link → GPON.Link
- Auto-reuses discovered GPON Link index

### internal/cwmp/session.go
- Modified WAN task handler (TypeWAN case)
- When PPPoE exists and needs update: uses delete+add approach
- Requires VLAN ID and username for update
- Initiates WANProvision state machine

## Usage Example

**API Request (WAN Update):**
```json
{
  "type": "wan",
  "payload": {
    "connection_type": "pppoe",
    "vlan": 130,
    "username": "newuser@isp.com",
    "password": "newpass123"
  }
}
```

**Flow Generated:**
```
Step 1: DeleteObject Device.IP.Interface.1.
Step 2: DeleteObject Device.PPP.Interface.1.
Step 3: DeleteObject Device.Ethernet.VLANTermination.2.
Step 4: DeleteObject Device.Ethernet.Link.1.
Step 5: SetParameterValues (reset GPON Link)
Step 6: AddObject Device.Ethernet.Link.
Step 7: SetParameterValues (configure Link)
Step 8: AddObject Device.Ethernet.VLANTermination.
Step 9: SetParameterValues (set VLAN ID = 130)
... (continues with PPP, IP, DHCPv6, NAT setup)
```

## Benefits
- **Clean State**: No residual parameters from old configuration
- **Atomic**: Multi-step but well-defined sequence
- **Efficient**: Reuses GPON Link, minimizes device churn
- **Reliable**: No race conditions with parameter ordering
- **Future-proof**: Supports cascading deletes

## Testing
Run unit tests:
```bash
cd /home/well/helix-acs
go test ./internal/cwmp ./internal/datamodel -v
```

## Rollback
To revert to parameter update approach, see git history for:
- Original SetParameterValues-based implementation
- Parameter priority sorting in soap.go
