# Analisis TR-069 CWMP — Provisioning WAN PPPoE
**File PCAP:** `tr069_cdatatr098.pcap`  
**Data Model:** TR-098 (InternetGatewayDevice)  
**Protokol:** CWMP 1.0 over HTTP

---

## 1. Identitas Perangkat & Topologi

| Item | Nilai |
|---|---|
| CPE IP (WAN) | `10.201.78.47` |
| ACS IP:Port | `10.69.69.1:14501` |
| ACS URL | `http://10.201.78.47:7547/tr069` |
| CPE Manufacturer | ZTEG |
| CPE Model | FD514GD-R460 |
| Serial Number | CDTCAF252D7F |
| OUI | D05FAF |
| Firmware | V3.2.18_P396001 |
| Hardware | R550.1B |
| ProvisioningCode Awal | `sOLTinit` |
| ProvisioningCode Akhir | `sOLT.rPPP` |

---

## 2. Ringkasan Alur Provisioning

Seluruh sesi terjadi dalam **satu TCP connection** (CPE `36187` → ACS `14501`). ACS tidak membuat instance PPPoE baru (`AddObject` tidak ditemukan). Instance `WANPPPConnection.1` sudah ada di CPE secara default; ACS cukup membersihkan duplikat, mengisi parameter, lalu mengaktifkan.

```
CPE  →  Inform (BOOT + LONGRESET)
ACS  ←→  GetParameterNames / GetParameterValues  (discovery)
ACS  →   DeleteObject WANPPPConnection.2 s/d .6
ACS  →   SetParameterValues (Username, Password, ConnectionType)
ACS  →   SetParameterValues (X_CT-COM_IPMode)
ACS  →   SetParameterValues (Enable, Name, ProvisioningCode)
ACS  →   SetParameterValues (ManagementServer)
CPE  →   Inform (VALUE CHANGE) × 3  ← konfirmasi PPPoE aktif
```

---

## 3. Detail Setiap Fase

### Fase 1 — CPE Boot Inform (Frame 12)

CPE mengirim `Inform` ke ACS setelah reboot dengan dua event:
- `1 BOOT`
- `X CT-COM LONGRESET`

Parameter yang dilaporkan di Inform:
| Parameter | Nilai |
|---|---|
| `DeviceInfo.ProvisioningCode` | `sOLTinit` |
| `ManagementServer.ConnectionRequestURL` | `http://10.201.78.47:7547/tr069` |
| `ManagementServer.mac` | `d0:5f:af:25:2d:7f` |
| `WANIPConnection.1.ExternalIPAddress` | `10.201.78.47` ← WAN IP sudah up |
| `WANPPPConnection.1.ExternalIPAddress` | *(kosong)* ← PPPoE belum aktif |
| `WANPPPConnection.1.MACAddress` | `d0:5f:af:25:2d:83` |

---

### Fase 2 — Discovery / Inventaris (Frame 20–115)

ACS melakukan serangkaian `GetParameterNames` dan `GetParameterValues` untuk memetakan state CPE:

1. `GetParameterNames` pada `WANConnectionDevice.1.` (NextLevel=1)  
   → Ditemukan: `WANIPConnection.1` dan `WANPPPConnection.1` s/d `.6`

2. `GetParameterValues` untuk tiap instance `WANPPPConnection.N`:
   - Cek `ServiceList` → semua bernilai `INTERNET`
   - Cek `ConnectionType` → semua `PPPoE_Routed`

3. `GetParameterValues` pada `WANIPConnection.1`:
   - 56 parameter dibaca, termasuk status, NAT, DNS, dsb.

---

### Fase 3 — Cleanup: DeleteObject (Frame 88–156)

ACS menghapus semua instance PPPoE yang tidak diperlukan, menyisakan hanya `.1`:

| RPC | Object | Response Status |
|---|---|---|
| `DeleteObject` | `WANPPPConnection.2.` | 0 (sukses) |
| `DeleteObject` | `WANPPPConnection.3.` | 0 (sukses) |
| `DeleteObject` | `WANPPPConnection.4.` | 0 (sukses) |
| `DeleteObject` | `WANPPPConnection.5.` | 0 (sukses) |
| `DeleteObject` | `WANPPPConnection.6.` | 0 (sukses) |

---

### Fase 4 — Re-read WANPPPConnection.1 (Frame 159–173)

Sebelum konfigurasi, ACS membaca ulang seluruh parameter `WANPPPConnection.1` (86 parameter) untuk memverifikasi kondisi awal. Parameter yang diperhatikan antara lain:

| Parameter | Nilai Saat Ini |
|---|---|
| `NATEnabled` | `true` |
| `Password` | *(kosong)* |
| `X_CT-COM_IPMode` | `1` |
| `ConnectionType` | `PPPoE_Routed` |

---

### Fase 5 — SetParameterValues: Kredensial PPPoE (Frame 180)

**Arah: ACS → CPE**

```xml
<cwmp:SetParameterValues>
  <ParameterList>
    <ParameterValueStruct>
      <Name>...WANPPPConnection.1.ConnectionType</Name>
      <Value>IP_Routed</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>...WANPPPConnection.1.Password</Name>
      <Value>gmedia</Value>
    </ParameterValueStruct>
    <ParameterValueStruct>
      <Name>...WANPPPConnection.1.Username</Name>
      <Value>gmedia746</Value>
    </ParameterValueStruct>
  </ParameterList>
</cwmp:SetParameterValues>
```

**CPE Response:** `Status = 0` ✓

---

### Fase 6 — SetParameterValues: Vendor Extension (Frame 184)

**Arah: ACS → CPE**

```
WANPPPConnection.1.X_CT-COM_IPMode = 3
```

| Nilai `X_CT-COM_IPMode` | Keterangan |
|---|---|
| `1` | IPv4 only |
| `2` | IPv6 only |
| `3` | Dual-stack (IPv4 + IPv6) |

**CPE Response:** `Status = 0` ✓

---

### Fase 7 — Verifikasi Default Route (Frame 200–203)

ACS membaca `Layer3Forwarding.DefaultConnectionService`:

```
→ Nilai: InternetGatewayDevice.WANDevice.1.WANConnectionDevice.1.WANPPPConnection.1
```

PPPoE sudah terdaftar sebagai default connection service.

---

### Fase 8 — SetParameterValues: Enable + Name + ProvisioningCode (Frame 244)

**Arah: ACS → CPE** — ini adalah langkah kritis yang mengaktifkan PPPoE:

```
DeviceInfo.ProvisioningCode                     = sOLT.rPPP
WANPPPConnection.1.Enable                       = 1           ← PPPoE AKTIF
WANPPPConnection.1.Name                         = Internet_PPPoE
```

**CPE Response:** `Status = 0` ✓

> ProvisioningCode berubah dari `sOLTinit` → `sOLT.rPPP`, menandakan provisioning PPPoE berhasil diselesaikan.

---

### Fase 9 — SetParameterValues: ManagementServer (Frame 254)

```
ManagementServer.ConnectionRequestPassword = pdkfuj9jk9
ManagementServer.PeriodicInformInterval    = 3600
```

---

### Fase 10 — Konfirmasi: CPE Inform VALUE CHANGE (Frame 351, 371, 438)

CPE mengirim tiga kali Inform dengan event `4 VALUE CHANGE`, melaporkan parameter yang berubah:

| Parameter | Nilai Baru |
|---|---|
| `WANPPPConnection.1.Username` | `gmedia746` |
| `WANPPPConnection.1.MACAddress` | `d0:5f:af:25:2d:83` |
| `DeviceInfo.ProvisioningCode` | `sOLT.rPPP` |

PPPoE berhasil aktif dan dilaporkan ke ACS.

---

## 4. Parameter Konfigurasi WAN PPPoE (Final)

| Parameter TR-098 | Nilai |
|---|---|
| `WANPPPConnection.1.ConnectionType` | `IP_Routed` |
| `WANPPPConnection.1.Username` | `gmedia746` |
| `WANPPPConnection.1.Password` | `gmedia` |
| `WANPPPConnection.1.Name` | `Internet_PPPoE` |
| `WANPPPConnection.1.Enable` | `1` |
| `WANPPPConnection.1.NATEnabled` | `true` |
| `WANPPPConnection.1.X_CT-COM_IPMode` | `3` (dual-stack) |
| `WANPPPConnection.1.X_CT-COM_ServiceList` | `INTERNET` |
| `WANPPPConnection.1.DNSEnabled` | `true` |
| `WANPPPConnection.1.DNSOverrideAllowed` | `true` |
| `WANPPPConnection.1.PPPoEACName` | *(vendor configured)* |
| `WANPPPConnection.1.PPPoEServiceName` | *(vendor configured)* |
| `WANPPPConnection.1.ConnectionTrigger` | `AlwaysOn` |

---

## 5. Urutan RPC Lengkap

```
Frame  12  CPE → ACS  Inform (1 BOOT, X CT-COM LONGRESET)
Frame  18  ACS → CPE  InformResponse
Frame  20  ACS → CPE  GetParameterValues (WANConnectionDevice discovery)
Frame  22  ACS → CPE  GetParameterNames (WANConnectionDevice.1 NextLevel=1)
Frame  24  CPE → ACS  GetParameterNamesResponse → 1 writable item WANPPPConn.1.
Frame  26  ACS → CPE  GetParameterNames (WANIPConnection.1 NextLevel=1)
Frame  28  CPE → ACS  GetParameterNamesResponse → 1 writable item WANIPConn.1.
Frame  39  ACS → CPE  GetParameterNames (WANIPConnection.1. full)
Frame  43  ACS → CPE  GetParameterNames (WANPPPConnection.)
Frame  47-87           GetParameterValues loop per instance PPPoE (1-6)
Frame  88  ACS → CPE  DeleteObject WANPPPConnection.2.
Frame  90  CPE → ACS  DeleteObjectResponse Status=0
Frame  97  ACS → CPE  DeleteObject WANPPPConnection.3.
Frame  99  CPE → ACS  DeleteObjectResponse Status=0
Frame 106  ACS → CPE  DeleteObject WANPPPConnection.4.
Frame 108  CPE → ACS  DeleteObjectResponse Status=0
Frame 112  ACS → CPE  DeleteObject WANPPPConnection.5.
Frame 114  CPE → ACS  DeleteObjectResponse Status=0
Frame 156  ACS → CPE  DeleteObject WANPPPConnection.6.
Frame 158  CPE → ACS  DeleteObjectResponse Status=0
Frame 159  ACS → CPE  GetParameterNames (WANPPPConnection. NextLevel=0)
Frame 173  CPE → ACS  GetParameterNamesResponse (86 params)
Frame 180  ACS → CPE  SetParameterValues (ConnectionType, Username, Password)
Frame 183  CPE → ACS  SetParameterValuesResponse Status=0
Frame 184  ACS → CPE  SetParameterValues (X_CT-COM_IPMode=3)
Frame 200  CPE → ACS  SetParameterValuesResponse Status=0
Frame 201  ACS → CPE  GetParameterValues (Layer3Forwarding.DefaultConnectionService)
Frame 203  CPE → ACS  GetParameterValuesResponse → WANPPPConnection.1
Frame 244  ACS → CPE  SetParameterValues (Enable=1, Name, ProvisioningCode)
Frame 246  CPE → ACS  SetParameterValuesResponse Status=0
Frame 254  ACS → CPE  SetParameterValues (ManagementServer Password, Interval)
Frame 256  CPE → ACS  SetParameterValuesResponse Status=0
Frame 351  CPE → ACS  Inform (4 VALUE CHANGE) — PPPoE konfirmasi #1
Frame 355  ACS → CPE  InformResponse
Frame 371  CPE → ACS  Inform (4 VALUE CHANGE) — PPPoE konfirmasi #2
Frame 375  ACS → CPE  InformResponse
Frame 438  CPE → ACS  Inform (4 VALUE CHANGE) — PPPoE konfirmasi #3
Frame 441  ACS → CPE  InformResponse
```

---

## 6. Catatan Penting

- **Tidak ada AddObject** — ACS menggunakan instance `WANPPPConnection.1` yang sudah ada secara default di CPE, bukan membuat baru.
- **Instance PPPoE .2 s/d .6 dihapus** terlebih dahulu untuk memastikan konfigurasi bersih sebelum set parameter.
- **`X_CT-COM_IPMode = 3`** adalah parameter vendor China Telecom (CT-COM) untuk mengaktifkan dual-stack IPv4+IPv6 pada koneksi PPPoE.
- **ProvisioningCode** digunakan sebagai marker state provisioning: `sOLTinit` (belum diprovisioning) → `sOLT.rPPP` (PPPoE sudah diprovisioning).
- CPE mengirim **3× Inform VALUE CHANGE** secara periodik setelah konfigurasi, mengindikasikan PPPoE berhasil terkoneksi dan melaporkan state terkini ke ACS.
