# Helix ACS: Tech Stack & ONU Reset Analysis

## 📋 Tech Stack Overview

### Core Components
- **Language**: Go 1.25.0
- **Protocol**: TR-069 (CWMP - CPE WAN Management Protocol)
- **Database**: MongoDB (via go.mongodb.org/mongo-driver)
- **Cache/Queue**: Redis (via github.com/redis/go-redis)
- **Web Framework**: Gorilla Mux (HTTP routing)
- **Authentication**: JWT + Digest Auth
- **Config**: Viper + YAML

### Key Technologies
| Component | Technology | Purpose |
|-----------|-----------|---------|
| API Server | Gorilla Mux | REST API for device management & task creation |
| CWMP Server | TR-069 SOAP | Device communication & RPC handling |
| Data Storage | MongoDB | Device state, parameters, history |
| Task Queue | Redis | Asynchronous task management |
| Logging | Zerolog | Structured logging |
| Web UI | Vanilla JS | Dashboard & device management interface |

### Supported Data Models
- **TR-181**: Modern, vendor-neutral (Huawei, TP-Link, ZTE variants)
- **TR-098**: Legacy CPE data model with vendor-specific extensions

---

## 🔄 ONU Auto-Reset with Parameter Restoration: Feasibility Analysis

### ✅ YES, It is POSSIBLE

This ACS architecture supports automated device reset with parameter restoration when devices reconnect. Here's how:

### Architecture Flow

```
1. Initial State
   ├─ Device connected to ACS
   ├─ All parameters stored in MongoDB
   └─ Device online and operational

2. Reset Task Created
   ├─ User creates "factory_reset" task via API
   ├─ Task queued in Redis
   └─ Device receives reset command on next Inform

3. Device Resets & Reconnects
   ├─ Device factory resets locally
   ├─ All parameters cleared
   └─ Device re-contacts ACS (new Inform)

4. Parameter Restoration (ACS Handles)
   ├─ ACS recognizes device (by serial/OUI)
   ├─ Loads previous parameters from MongoDB
   ├─ Creates "set_parameters" tasks automatically
   └─ Parameters pushed to device on reconnect

5. New Session State
   ├─ Device has previous configuration
   ├─ All parameters restored
   └─ Device back to pre-reset state
```

### Implementation Components

#### 1. **Device Management** (`internal/device/`)
```go
// Device model stores complete parameter history
type Device struct {
    Serial       string              // Unique identifier
    Parameters   map[string]string   // All TR-069 parameters
    WANs         []WANInfo           // Network configuration
    LAN          *LANInfo            // DHCP/IP settings
    WiFi24/5     *WiFiInfo           // Wireless config
    CreatedAt    time.Time
    UpdatedAt    time.Time
}
```
- **Persistence**: MongoDB stores device state indefinitely
- **Snapshots**: Device parameters auto-captured during every Inform
- **Retrieval**: Can fetch last-known-good configuration anytime

#### 2. **Task System** (`internal/task/`)
```go
type Task struct {
    Type      Type              // factory_reset, reboot, set_parameters, etc.
    Status    Status            // pending → executing → done/failed
    Payload   json.RawMessage   // Task-specific data
    Serial    string            // Target device
}
```

**Available Reset Operations**:
- `TypeReboot`: Device restart without data loss
- `TypeFactoryReset`: Complete reset to factory defaults
- `TypeSetParams`: Push arbitrary TR-069 parameters

**Key Insight**: Tasks are **queued in Redis** and execute only when device connects (Inform RPC).

#### 3. **CWMP Session Handler** (`internal/cwmp/session.go`)
```go
type Session struct {
    ID              string
    DeviceSerial    string
    State           SessionState    // New → Inform → Processing → Done
    pendingTasks    []*task.Task    // Tasks waiting dispatch
    currentTask     *task.Task      // Currently executing task
    summonPhase     int             // Parameter discovery phase
    summonAllParams map[string]string  // Accumulated device params
}
```

**Session Lifecycle**:
1. Device sends **Inform** RPC → Session created
2. ACS retrieves **pending tasks** from Redis queue
3. ACS executes tasks in sequence via **SetParameterValues RPC**
4. Device responses collected → Task marked done

#### 4. **Device Service** (`internal/device/service.go`)
- Retrieves stored parameters from MongoDB
- Can restore any previous configuration
- Auto-upserts device state on Inform

---

## 🛠️ Implementation Approach

### Option A: Manual Reset + Restore (Recommended)

**Step 1**: Create factory reset task
```bash
POST /api/v1/devices/{serial}/tasks/factory-reset
```

**Step 2**: Capture pre-reset configuration
```bash
# Automatically saved in MongoDB during device Inform
# No additional action needed
```

**Step 3**: Auto-restore on reconnect (Requires Custom Logic)
```go
// In CWMP Handler: After Inform, check if device was recently reset
// If yes, compare stored params with current device state
// If mismatch detected → enqueue set_parameters task automatically
```

### Option B: Scripted Reset + Restore

**Workflow**:
1. Query device current parameters via `TypeGetParams` task
2. Store snapshot in MongoDB
3. Execute `TypeFactoryReset` task
4. On device reconnect (Inform), automatically enqueue `TypeSetParams` task with stored values

### Option C: Vendor-Specific Reset APIs

**For TP-Link ONU Devices**:
- TP-Link TR-181 implementation supports parameterized resets
- Can reset specific subsystems (WiFi, WAN, LAN) without full factory reset
- Parameters remain intact for non-reset areas

---

## 📊 Current Capabilities

| Capability | Status | Details |
|-----------|--------|---------|
| **Store Parameters** | ✅ Full | MongoDB persistence, automatic snapshots |
| **Execute Reset** | ✅ Full | Factory reset & reboot tasks |
| **Device Reconnection** | ✅ Full | Automatic Inform handling |
| **Push Parameters** | ✅ Full | SetParameterValues RPC via task system |
| **Auto-Restore on Reset** | ⚠️ Partial | Requires custom logic implementation |
| **Selective Reset** | ✅ Full | Can reset specific subsystems via parameters |
| **Diagnostic Recovery** | ✅ Full | Ping, traceroute, speed test during recovery |

---

## 🎯 What Needs Implementation

### 1. **Reset Detection Logic**
```go
// In session.go or session handler
func (s *Session) DetectResetAndRestore() {
    // If device just rebooted after factory reset:
    // 1. Get stored pre-reset config from MongoDB
    // 2. Compare with current device state
    // 3. If major mismatch detected → enqueue restore task
}
```

### 2. **Automatic Parameter Restore Task**
```go
// Create composite task in task executor
func RestoreDeviceConfig(serial string, storedParams map[string]string) *task.Task {
    // Build set_parameters task with stored values
    // Enqueue automatically on Inform if reset detected
}
```

### 3. **Reset State Tracking**
```go
// In Device model
type ResetHistory struct {
    LastResetTime   *time.Time
    PreResetParams  map[string]string
    PostResetParams map[string]string
    RestoreStatus   string  // pending, completed, failed
}
```

---

## 🔌 Example Workflow (ONU Device)

### Scenario: TP-Link ONU Factory Reset + Auto-Restore

```
TimeT0: Device configured
├─ WiFi: SSID="MyNetwork", Password="secure123"
├─ WAN: PPPoE user="customer@isp.com", password="pass456"
├─ LAN: DHCP enabled, IP 192.168.1.1
└─ All parameters stored in MongoDB

TimeT1: Admin triggers reset
├─ POST /api/v1/devices/ABC123/tasks/factory-reset
├─ Task queued in Redis
└─ Awaiting device Inform

TimeT2: Device performs factory reset
├─ All config cleared
├─ Device reboots
└─ Contacts ACS with new Inform

TimeT3: ACS receives Inform after reset
├─ Recognizes device by serial (ABC123)
├─ Detects parameters differ from stored snapshot
├─ Automatically enqueues restore task with previous values
└─ Device receives SetParameterValues RPC

TimeT4: Device restored
├─ WiFi: SSID="MyNetwork", Password="secure123" ✅
├─ WAN: PPPoE user="customer@isp.com", password="pass456" ✅
├─ LAN: DHCP enabled, IP 192.168.1.1 ✅
└─ Full configuration restored
```

---

## 📝 Limitations & Considerations

1. **Timing**: Parameter restoration happens asynchronously after device reconnects
2. **WiFi Clients**: May experience brief disconnect during reset
3. **Secrets**: Passwords stored in MongoDB (should use encryption at rest)
4. **Partial Reset**: Cannot selectively reset without vendor-specific APIs
5. **Device State**: Some devices may require multiple Inform cycles before fully restoring parameters

---

## ✨ Recommendations

### For Production ONU Reset + Restore:

1. **Enable MongoDB Encryption**: Encrypt stored passwords in database
2. **Implement Reset Confirmation**: Add two-factor confirmation for factory resets
3. **Custom Recovery Task**: Create `TypeAutoRestore` task type for efficiency
4. **Device-Specific Handling**: 
   - TP-Link: Use `X_TP_*` vendor parameters for selective resets
   - Huawei: Leverage HuaweiTR181 schema for recovery
5. **Audit Logging**: Track all resets and restoration attempts
6. **Timeout Handling**: Define max wait for reconnect before declaring failure

---

## 📚 Related Code Locations

| Functionality | File | Lines |
|--------------|------|-------|
| Task Types | [internal/task/model.go](internal/task/model.go) | 1-50 |
| Reset Endpoints | [internal/api/handler/task.go](internal/api/handler/task.go) | 120-140 |
| CWMP Session | [internal/cwmp/session.go](internal/cwmp/session.go) | 1-100 |
| Device Model | [internal/device/model.go](internal/device/model.go) | 1-100 |
| Task Executor | [internal/task/executor.go](internal/task/executor.go) | 1-200 |

---

## 🎓 Conclusion

**The Helix ACS architecture is well-suited for automated ONU reset with parameter restoration.** The system already has:
- ✅ Persistent parameter storage (MongoDB)
- ✅ Task-based remote execution (Redis queue)
- ✅ Device reconnection handling (CWMP sessions)
- ✅ Parameter provisioning capabilities (SetParameterValues RPC)

**What's missing**: Custom orchestration logic to detect resets and trigger automatic parameter restoration. This can be implemented in 1-2 weeks as a feature addition to the CWMP session handler.
