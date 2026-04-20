# Helix‑ACS Progress Summary (2026‑04‑20)

## 🎯 Objective
Persist WAN PPPoE credentials (username, password, VLAN ID) in PostgreSQL and expose them via the API/web UI. Add support for a new C‑DATA GPON device (`serial CDTCAF252D7F`) and fix TR‑098 summon errors.

---

## 📦 Completed Tasks

### 1. Persistent WAN Credentials (Backend)
| File | Change |
|------|--------|
| `internal/cwmp/session.go` | Added `pendingWANCredentials`, persisted creds after successful WAN tasks. |
| `internal/cwmp/wan_provisioner.go` | Helper `buildWANCredentialMap` + `persistWANCredentials`. |
| `internal/api/handler/device.go` | New endpoint **GET `/api/v1/devices/:serial/provision`** to read `_helix.provision.*` from PostgreSQL. |
| `internal/api/router.go` | Registered the new provision route. |
| `internal/parameter/postgresql.go` | Fixed `LIKE` query: escaped underscore (`\_helix.provision.`) to avoid wildcard matches & added `strings` import. |
| `web/app.js` | - UI now fetches `/provision` and displays PPPoE **Username** & **Password**. <br> - Password row always visible with hide/show toggle. <br> - Shows a helpful “Belum tersimpan — jalankan task WAN Configuration” hint when password is not yet stored. <br> - Updated `passRow` signature and call sites. |
| **Build & Deploy** | Re‑built binary, restarted server, verified token‑based API access (`admin`/`acs123`). |

### 2. C‑DATA Device Support (TR‑098)
| File | Change |
|------|--------|
| `schemas/vendors/cdata/tr098/wan.yaml` | Full WAN schema for C‑DATA GPON ONUs (PPPoE, VLAN, stats, etc.). |
| `schemas/vendors/cdata/tr098/wifi.yaml` | Dual‑band Wi‑Fi schema (2.4 GHz + 5 GHz). |
| `internal/schema/resolver.go` | Added vendor mapping: `"zteg" → "cdata"`, also `"c-data"` & `"cdata"`. |
| `internal/cwmp/session.go` | Fixed full‑parameter summon root path: uses `InternetGatewayDevice.` for TR‑098 devices (detected via mapper). |
| **Verification** | Device `CDTCAF252D7F` now resolves to schema `vendor/cdata/tr098`, summon succeeds, parameters are stored, and UI shows PPPoE credentials after a WAN task. |

### 3. Miscellaneous
- Added proper import of `strings` (SQL fix).  
- Updated UI components for better UX (badge, info messages).  
- Ensured all new code compiles (`go build` passes).  
- Restarted server and confirmed endpoints return expected JSON.

---

## 📊 Current Data Flow (Web UI)
```
Device Inform (TR‑098) → ACS (MongoDB + PostgreSQL) → UI
   │                                 │
   └─> Mapper resolves vendor → `vendor/cdata/tr098`
   │                                 │
   └─> Full summon (GetParameterNames) → stores all TR‑098 params in MongoDB
   │
   └─> WAN task (set_params) → persisting
       `_helix.provision.pppoe_username`
       `_helix.provision.pppoe_password`
       `_helix.provision.vlan_id`
```
The UI now:
- Calls **GET `/api/v1/devices/:serial/provision`** to retrieve stored PPPoE creds.  
- Shows password masked with an eye‑toggle.  
- Displays a gray hint when the password isn’t captured yet.

---

## ✅ Next Steps (optional)
1. **Encrypt** stored PPPoE passwords (future enhancement).  
2. Add UI for editing VLAN ID directly from the dashboard.  
3. Write unit tests for the new C‑DATA schemas and summon logic.

All changes are live on the running server (PID 3710760).
