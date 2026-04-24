---
name: cwmp-xml
description: Writes, parses, validates, and debugs CWMP XML (TR-069 SOAP messages) including Inform, GetParameterValues, SetParameterValues, Download, Reboot, and Fault envelopes. Use when constructing or reviewing SOAP envelopes exchanged between ACS and CPE, writing test fixtures, or building XML templates for TR-098 and TR-181 data models.
---

# CWMP XML Skill

This skill covers writing correct, well-formed CWMP XML (SOAP) messages used in the TR-069 protocol between ACS and CPE.

---

## Namespace Reference

Always declare these namespaces on the root `Envelope`:

```xml
xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
xmlns:soapenc="http://schemas.xmlsoap.org/soap/encoding/"
xmlns:cwmp="urn:dslforum-org:cwmp-1-2"
xmlns:xsd="http://www.w3.org/2001/XMLSchema"
xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance"
```

CWMP version variants:
| Version | Namespace URN |
|---------|--------------|
| CWMP 1.0 | `urn:dslforum-org:cwmp-1-0` |
| CWMP 1.1 | `urn:dslforum-org:cwmp-1-1` |
| CWMP 1.2 (default) | `urn:dslforum-org:cwmp-1-2` |
| CWMP 1.3 | `urn:dslforum-org:cwmp-1-3` |

---

## SOAP Envelope Skeleton

Every CWMP message MUST follow this structure:

```xml
<?xml version="1.0" encoding="UTF-8"?>
<soapenv:Envelope
  xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"
  xmlns:soapenc="http://schemas.xmlsoap.org/soap/encoding/"
  xmlns:cwmp="urn:dslforum-org:cwmp-1-2"
  xmlns:xsd="http://www.w3.org/2001/XMLSchema"
  xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">

  <soapenv:Header>
    <cwmp:ID soapenv:mustUnderstand="1">ID-001</cwmp:ID>
  </soapenv:Header>

  <soapenv:Body>
    <!-- RPC goes here -->
  </soapenv:Body>

</soapenv:Envelope>
```

Rules:
- `cwmp:ID` is **required** in every envelope. ACS and CPE must echo the same ID.
- `mustUnderstand="1"` is required on `cwmp:ID`.
- Each exchange has exactly **one RPC** per envelope body.

---

## CPE → ACS Messages

### Inform (CPE-initiated session start)

```xml
<cwmp:Inform>
  <DeviceId>
    <Manufacturer>Acme Corp</Manufacturer>
    <OUI>AABBCC</OUI>
    <ProductClass>HomeGateway</ProductClass>
    <SerialNumber>SN-123456</SerialNumber>
  </DeviceId>

  <Event soapenc:arrayType="cwmp:EventStruct[2]">
    <EventStruct>
      <EventCode>0 BOOTSTRAP</EventCode>
      <CommandKey></CommandKey>
    </EventStruct>
    <EventStruct>
      <EventCode>1 BOOT</EventCode>
      <CommandKey></CommandKey>
    </EventStruct>
  </Event>

  <MaxEnvelopes>1</MaxEnvelopes>
  <CurrentTime>2024-01-15T10:30:00+07:00</CurrentTime>
  <RetryCount>0</RetryCount>

  <ParameterList soapenc:arrayType="cwmp:ParameterValueStruct[4]">
    <!-- TR-181 example -->
    <ParameterValueStruct>
      <Name>Device.DeviceInfo.HardwareVersion</Name>
      <Value xsi:type="xsd:string">v1.2</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>Device.DeviceInfo.SoftwareVersion</Name>
      <Value xsi:type="xsd:string">2.1.3</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>Device.DeviceInfo.UpTime</Name>
      <Value xsi:type="xsd:unsignedInt">3600</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>Device.ManagementServer.ConnectionRequestURL</Name>
      <Value xsi:type="xsd:string">http://192.168.1.1:7547/</Value>
    </ParameterValueStruct>
  </ParameterList>
</cwmp:Inform>
```

**Common Event Codes:**
| Code | Meaning |
|------|---------|
| `0 BOOTSTRAP` | First-time contact |
| `1 BOOT` | Device rebooted |
| `2 PERIODIC` | Periodic Inform |
| `3 SCHEDULED` | Scheduled Inform |
| `4 VALUE CHANGE` | Parameter changed |
| `6 CONNECTION REQUEST` | ACS triggered connection |
| `7 TRANSFER COMPLETE` | Download/Upload finished |
| `8 DIAGNOSTICS COMPLETE` | Diagnostics done |
| `M Reboot` | Manual reboot |

---

### ACS → CPE: InformResponse

```xml
<cwmp:InformResponse>
  <MaxEnvelopes>1</MaxEnvelopes>
</cwmp:InformResponse>
```

---

## ACS → CPE RPC Methods

### GetParameterValues

```xml
<cwmp:GetParameterValues>
  <ParameterNames soapenc:arrayType="xsd:string[3]">
    <string>Device.DeviceInfo.SoftwareVersion</string>
    <string>Device.DeviceInfo.UpTime</string>
    <string>Device.ManagementServer.PeriodicInformInterval</string>
  </ParameterNames>
</cwmp:GetParameterValues>
```

TR-098 equivalent:
```xml
<cwmp:GetParameterValues>
  <ParameterNames soapenc:arrayType="xsd:string[2]">
    <string>InternetGatewayDevice.DeviceInfo.SoftwareVersion</string>
    <string>InternetGatewayDevice.DeviceInfo.UpTime</string>
  </ParameterNames>
</cwmp:GetParameterValues>
```

### GetParameterValuesResponse (CPE reply)

```xml
<cwmp:GetParameterValuesResponse>
  <ParameterList soapenc:arrayType="cwmp:ParameterValueStruct[2]">
    <ParameterValueStruct>
      <Name>Device.DeviceInfo.SoftwareVersion</Name>
      <Value xsi:type="xsd:string">2.1.3</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>Device.DeviceInfo.UpTime</Name>
      <Value xsi:type="xsd:unsignedInt">7200</Value>
    </ParameterValueStruct>
  </ParameterList>
</cwmp:GetParameterValuesResponse>
```

---

### SetParameterValues

```xml
<cwmp:SetParameterValues>
  <ParameterList soapenc:arrayType="cwmp:ParameterValueStruct[2]">
    <ParameterValueStruct>
      <Name>Device.ManagementServer.PeriodicInformInterval</Name>
      <Value xsi:type="xsd:unsignedInt">300</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>Device.WiFi.SSID.1.SSID</Name>
      <Value xsi:type="xsd:string">MyNetwork</Value>
    </ParameterValueStruct>
  </ParameterList>
  <ParameterKey>key-20240115-001</ParameterKey>
</cwmp:SetParameterValues>
```

### SetParameterValuesResponse

```xml
<cwmp:SetParameterValuesResponse>
  <Status>0</Status>  <!-- 0 = success, 1 = reboot required -->
</cwmp:SetParameterValuesResponse>
```

---

### GetParameterNames

```xml
<cwmp:GetParameterNames>
  <ParameterPath>Device.WiFi.</ParameterPath>
  <NextLevel>false</NextLevel>  <!-- true = immediate children only -->
</cwmp:GetParameterNames>
```

### GetParameterNamesResponse

```xml
<cwmp:GetParameterNamesResponse>
  <ParameterList soapenc:arrayType="cwmp:ParameterInfoStruct[3]">
    <ParameterInfoStruct>
      <Name>Device.WiFi.SSID.1.SSID</Name>
      <Writable>true</Writable>
    </ParameterInfoStruct>
    <ParameterInfoStruct>
      <Name>Device.WiFi.SSID.1.Enable</Name>
      <Writable>true</Writable>
    </ParameterInfoStruct>
    <ParameterInfoStruct>
      <Name>Device.WiFi.Radio.1.Channel</Name>
      <Writable>true</Writable>
    </ParameterInfoStruct>
  </ParameterList>
</cwmp:GetParameterNamesResponse>
```

---

### AddObject

```xml
<cwmp:AddObject>
  <ObjectName>Device.WiFi.SSID.</ObjectName>
  <ParameterKey>key-add-001</ParameterKey>
</cwmp:AddObject>
```

Response:
```xml
<cwmp:AddObjectResponse>
  <InstanceNumber>2</InstanceNumber>
  <Status>0</Status>
</cwmp:AddObjectResponse>
```

### DeleteObject

```xml
<cwmp:DeleteObject>
  <ObjectName>Device.WiFi.SSID.2.</ObjectName>
  <ParameterKey>key-del-001</ParameterKey>
</cwmp:DeleteObject>
```

---

### Download (Firmware / Config file)

```xml
<cwmp:Download>
  <CommandKey>fw-upgrade-20240115</CommandKey>
  <FileType>1 Firmware Upgrade Image</FileType>
  <URL>http://acs.example.com/firmware/v2.2.0.bin</URL>
  <Username>fwuser</Username>
  <Password>fwpass</Password>
  <FileSize>8388608</FileSize>
  <TargetFileName>firmware.bin</TargetFileName>
  <DelaySeconds>0</DelaySeconds>
  <SuccessURL></SuccessURL>
  <FailureURL></FailureURL>
</cwmp:Download>
```

**FileType values:**
| Code | Meaning |
|------|---------|
| `1 Firmware Upgrade Image` | Main firmware |
| `2 Web Content` | Web UI |
| `3 Vendor Configuration File` | Config backup |
| `4 Tone File` | VoIP tones |
| `5 Ringer File` | VoIP ringtones |

### TransferComplete (CPE reports result)

```xml
<cwmp:TransferComplete>
  <CommandKey>fw-upgrade-20240115</CommandKey>
  <FaultStruct>
    <FaultCode>0</FaultCode>
    <FaultString></FaultString>
  </FaultStruct>
  <StartTime>2024-01-15T10:31:00+07:00</StartTime>
  <CompleteTime>2024-01-15T10:33:45+07:00</CompleteTime>
</cwmp:TransferComplete>
```

---

### Reboot

```xml
<cwmp:Reboot>
  <CommandKey>reboot-manual-001</CommandKey>
</cwmp:Reboot>
```

Response:
```xml
<cwmp:RebootResponse/>
```

---

## SOAP Fault (Error Response)

Use this for any CWMP error. Never use HTTP error body.

```xml
<soapenv:Fault>
  <faultcode>Client</faultcode>
  <faultstring>CWMP fault</faultstring>
  <detail>
    <cwmp:Fault>
      <FaultCode>9005</FaultCode>
      <FaultString>Invalid parameter name</FaultString>
    </cwmp:Fault>
  </detail>
</soapenv:Fault>
```

**CWMP Fault Codes:**
| Code | Meaning |
|------|---------|
| 9000 | Method not supported |
| 9001 | Request denied |
| 9002 | Internal error |
| 9003 | Invalid arguments |
| 9004 | Resources exceeded |
| 9005 | Invalid parameter name |
| 9006 | Invalid parameter type |
| 9007 | Invalid parameter value |
| 9008 | Attempt to set non-writable parameter |
| 9009 | Notification request rejected |
| 9010 | Download failure |
| 9011 | Upload failure |
| 9012 | File transfer server authentication failure |
| 9013 | Unsupported protocol for file transfer |

---

## XSD Type Mapping

Always set `xsi:type` correctly on parameter values:

| CWMP / TR-069 type | XML xsi:type |
|--------------------|-------------|
| string | `xsd:string` |
| int | `xsd:int` |
| unsignedInt | `xsd:unsignedInt` |
| boolean | `xsd:boolean` (use `true`/`false` or `0`/`1`) |
| dateTime | `xsd:dateTime` (ISO 8601) |
| base64 | `xsd:base64Binary` |
| hexBinary | `xsd:hexBinary` |
| long | `xsd:long` |

---

## arrayType Counting Rule

Always set `soapenc:arrayType` with the **exact count** of child elements:

```xml
<!-- CORRECT: 3 items declared and present -->
<ParameterNames soapenc:arrayType="xsd:string[3]">
  <string>Device.A</string>
  <string>Device.B</string>
  <string>Device.C</string>
</ParameterNames>

<!-- WRONG: count mismatch causes many CPEs to reject the message -->
<ParameterNames soapenc:arrayType="xsd:string[2]">
  <string>Device.A</string>
  <string>Device.B</string>
  <string>Device.C</string>   <!-- 3rd element but count says 2 -->
</ParameterNames>
```

---

## Empty POST (Session Continuation)

After InformResponse, ACS sends an HTTP 200 with an empty body (or no body) when it has no pending RPCs. CPE must then close the session.

```
HTTP/1.1 200 OK
Content-Type: text/xml
Content-Length: 0
```

Or equivalently, HTTP 204 No Content. Both are valid per TR-069.

---

## Common Mistakes to Avoid

1. **Missing `cwmp:ID`** — Session breaks; CPE may retry infinitely.
2. **Wrong arrayType count** — Many CPE firmwares are strict; count must match exactly.
3. **Missing `xsi:type`** on Value — Some CPEs reject untyped values.
4. **Using HTTP errors for CWMP errors** — Always use SOAP Fault with CWMP fault codes.
5. **Trailing dot missing on object paths** — `Device.WiFi.SSID.` must end with `.` for object operations.
6. **Incorrect namespace prefix** — Don't mix `soap:` and `soapenv:`; pick one and be consistent.
7. **Mixing TR-098 and TR-181 paths** in the same session — detect data model from Inform and stay consistent.

---

## Decision Tree

```
Writing a CWMP XML message?
│
├─ Who sends it?
│   ├─ CPE → ACS: Inform (with Event + ParameterList)
│   └─ ACS → CPE: Pick RPC below
│       ├─ Read params → GetParameterValues
│       ├─ Write params → SetParameterValues
│       ├─ List params → GetParameterNames
│       ├─ Add instance → AddObject
│       ├─ Remove instance → DeleteObject
│       ├─ Push firmware/config → Download
│       └─ Restart device → Reboot
│
├─ Error condition?
│   └─ Always use SOAP Fault + CWMP FaultCode (never HTTP 4xx/5xx body)
│
└─ Session end?
    └─ ACS sends empty HTTP 200 or 204 → CPE closes TCP connection
```
