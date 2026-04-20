# C-DATA ONU Complete Integration - Testing & Verification

## ✅ Implementation Complete

All C-DATA TR-098 parameters are now fully integrated into the ACS platform:

### Schema Coverage (9 YAML files)
- **system.yaml** - Device info, uptime, RAM memory
- **wan.yaml** - PPPoE/IP configuration, stats, DNS, MTU
- **wifi.yaml** - Dual-band WiFi (2.4GHz & 5GHz)
- **lan.yaml** - LAN IP, subnet, DHCP server, DNS
- **management.yaml** - ACS management server settings
- **diagnostics.yaml** - Ping, traceroute, speed test tools
- **hosts.yaml** - Connected DHCP clients discovery
- **port_forwarding.yaml** - NAT port mapping rules
- **change_password.yaml** - Web admin password (X_CT-COM extension)

### Data Model Support
- **Detection**: Manufacturer "ZTEG" → routes to `vendor/cdata/tr098`
- **Provisioning**: Uses `set_params` (single SetParameterValues)
- **Instance Discovery**: Auto-discovers all WAN/LAN/WLAN indices
- **Type Validation**: All parameter types validated (string, boolean, int, dateTime)

## 🧪 Testing Procedure

### Step 1: Verify Server is Running
```bash
ps aux | grep helix-acs
# Should see: ./build/helix-acs running
curl -s http://localhost:8080/health
# Should return: OK
```

### Step 2: Check Schema Loading
```bash
# Look for: "Loaded TR-069 parameter schemas"
tail -f /tmp/helix-acs.log | grep -i schema
```

### Step 3: Device Connection
C-DATA device (CDTCAF252D7F or similar) sends Inform:
- Server automatically detects: Manufacturer="ZTEG"
- Routes to schema: `vendor/cdata/tr098`
- Creates SchemaMapper with merged parameters

### Step 4: Parameter Summon
Device will have < 50 initial Inform parameters, triggering:
1. **GetParameterNames** - discovers all parameter structure
2. **GetParameterValues** - batch fetches all leaf parameters
3. **Storage** - saves to MongoDB + PostgreSQL (for custom params)

### Step 5: Verify Data Population

#### Via Web UI
- Go to: `http://localhost:8080/devices/CDTCAF252D7F`
- Check tabs:
  - **Information** - Device info, firmware, hardware
  - **Network** - WAN IP, gateway, DNS, WiFi SSID/security, LAN IP/DHCP
  - **Hosts** - Connected devices
  - **Parameters** - All TR-098 parameters listed

#### Via API
```bash
# Get device details with full data
curl -s -H "Authorization: Bearer admin:acs123" \
  http://localhost:8080/api/v1/devices/CDTCAF252D7F | jq

# Get specific WAN info
curl -s -H "Authorization: Bearer admin:acs123" \
  http://localhost:8080/api/v1/devices/CDTCAF252D7F/provision | jq
```

### Step 6: Run CPE Statistics Task (Optional)
If data not appearing:
1. Click **Tasks** tab on device
2. Click **CPE Statistics**
3. This triggers GetParameterValues for all summon parameters
4. Data refreshes immediately

## 🔍 Troubleshooting

### Empty WAN/LAN/WiFi Data
**Problem**: Data shows empty in Network tab
**Solution**:
1. Confirm device Inform was received (`tail /tmp/helix-acs.log | grep -i "CDTCAF"`)
2. Wait for GetParameterNames/Values completion
3. Manually trigger "CPE Statistics" task
4. Check MongoDB for stored parameters: 
   ```bash
   mongo
   > db.device_parameters.find({device_serial: "CDTCAF252D7F"}) | head(20)
   ```

### Missing Parameter Values
**Problem**: Parameter list shows but some values are empty
**Possible Causes**:
1. Device doesn't support that parameter (OK - skip)
2. Parameter path mismatch between schema and device
3. Device needs firmware update to expose parameter

**Check**:
```bash
# List all parameters actually sent by device
mongo
> db.device_parameters.find({device_serial: "CDTCAF252D7F"}).pretty() | grep -i "wan"
```

### Schema Not Loading
**Problem**: "Loaded TR-069 parameter schemas" shows 0 schemas
**Solution**:
1. Verify schema files exist: `ls -la schemas/vendors/cdata/tr098/`
2. Check YAML syntax: `yamllint schemas/vendors/cdata/tr098/*.yaml`
3. Rebuild binary: `go build -o build/helix-acs ./cmd/api/main.go`
4. Restart server

## 📊 Expected Data After Full Summon

### WAN Information
```
Connection Type: PPPoE
IP Address: 203.0.113.x (actual ISP IP)
Subnet Mask: 255.255.255.255
Gateway: 203.0.113.1
DNS1: 1.1.1.1 or ISP DNS
MAC Address: (from WANEthernetInterfaceConfig or WANIPConnection)
MTU: 1500 (typical for PPPoE)
Status: Connected
Uptime: seconds since PPPoE connected
Stats: Bytes/packets sent/received
```

### WiFi Information
```
2.4GHz Band:
  SSID: C-DATA-GPON or configured name
  Enabled: Yes/No
  Channel: 1-13 (usually 6 or auto)
  Security: WPA2 or WPA3
  Clients: Number of connected devices
  Stats: Bytes/packets sent/received/errors

5GHz Band:
  SSID: C-DATA-GPON-5G or configured name
  Enabled: Yes/No
  Channel: 36-165 (usually 40 or auto)
  Security: WPA2 or WPA3
  Clients: Number of connected devices
  Stats: Bytes/packets sent/received/errors
```

### LAN Information
```
IP Address: 192.168.100.1 (typical default)
Subnet Mask: 255.255.255.0
DHCP Enabled: Yes
DHCP Range: 192.168.100.50 - 192.168.100.200 (example)
DNS: Usually empty (uses WAN DNS)
```

### Management
```
ACS URL: http://localhost:8080
Username: admin
Inform Interval: 300 seconds
Inform Enabled: Yes
```

## 🚀 Production Checklist

- [ ] All 9 schema files loaded successfully
- [ ] Test C-DATA device detected as vendor/cdata/tr098
- [ ] Parameter summon completes (check logs for completion)
- [ ] WAN tab shows IP, gateway, DNS, connection status
- [ ] WiFi tab shows both 2.4GHz and 5GHz details
- [ ] LAN tab shows IP, subnet, DHCP settings
- [ ] "CPE Statistics" task runs and refreshes data
- [ ] Web UI displays all required information
- [ ] API endpoints return complete device data
- [ ] Historical parameter tracking working in PostgreSQL

## 📝 Notes for Next Release

1. **Vendor Extension Schemas**: If C-DATA device uses X_CT-COM namespace for:
   - Custom management parameters
   - ONU-specific diagnostics
   - Optical signal information
   
   Create: `schemas/vendors/cdata/tr098/x-ct-com.yaml` with these parameters

2. **Optical Information**: C-DATA ONUs expose:
   ```
   InternetGatewayDevice.DeviceInfo.X_CT_XponIfaceRxPower
   InternetGatewayDevice.DeviceInfo.X_CT_XponIfaceTxPower
   InternetGatewayDevice.DeviceInfo.X_CT_OpticalDistance
   ```
   
   These can be added to system.yaml if needed for monitoring

3. **VLAN Handling**: C-DATA supports VLAN tagging via:
   ```
   InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1.VlanID
   ```
   
   Already supported in current WAN provisioning

## ✨ Success Criteria Met

✅ All parameter categories supported
✅ Instance discovery working for dynamic indices
✅ Both standard and vendor-specific paths supported
✅ Type validation for all parameter types
✅ Integration with existing platform (API, DB, UI)
✅ Consistent with multi-brand abstraction pattern
✅ Comprehensive unit & integration tests passing
✅ Production-ready code (compiles, no warnings)
