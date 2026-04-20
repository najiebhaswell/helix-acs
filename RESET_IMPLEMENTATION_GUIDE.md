# Implementation Guide: Auto-Reset with Parameter Restoration for ONU Devices

## Overview
This guide shows how to implement automatic parameter restoration when ONU devices are factory reset and reconnect to the ACS.

## Architecture

```
┌─────────────────┐
│  Admin Trigger  │
│  Factory Reset  │
└────────┬────────┘
         │
         ▼
┌─────────────────────────────────────┐
│  1. Task Enqueue                    │
│  - Create TypeFactoryReset task     │
│  - Store in Redis queue             │
│  - Capture pre-reset params         │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│  2. Device Reset Execution          │
│  - Device receives Reboot RPC       │
│  - Factory resets locally           │
│  - All parameters cleared           │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│  3. Device Reconnection             │
│  - Sends new Inform RPC             │
│  - ACS recognizes by serial/OUI     │
│  - New session created              │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│  4. Reset Detection & Restore       │
│  - Compare current vs stored params │
│  - Enqueue TypeSetParameters task   │
│  - Push previous config back        │
└────────┬────────────────────────────┘
         │
         ▼
┌─────────────────────────────────────┐
│  5. Parameter Restoration           │
│  - Device receives SetParameterRPC  │
│  - Applies previous configuration   │
│  - Session completes                │
└─────────────────────────────────────┘
```

## Implementation Steps

### Step 1: Add Reset Tracking to Device Model

**File**: `internal/device/model.go`

```go
// Add to Device struct:
type Device struct {
    // ... existing fields ...
    
    // Reset tracking
    LastResetTime    *time.Time            `bson:"last_reset_time" json:"last_reset_time,omitempty"`
    PreResetParams   map[string]string     `bson:"pre_reset_params" json:"pre_reset_params,omitempty"`
    ResetInProgress  bool                  `bson:"reset_in_progress" json:"reset_in_progress,omitempty"`
}
```

### Step 2: Store Pre-Reset Configuration

**File**: `internal/api/handler/task.go`

Modify the `CreateFactoryReset` function to capture current parameters:

```go
// CreateFactoryReset handles POST /api/v1/devices/:serial/tasks/factory-reset
func (h *TaskHandler) CreateFactoryReset(w http.ResponseWriter, r *http.Request) {
    serial := mux.Vars(r)["serial"]
    dev := h.requireDevice(w, r, serial)
    if dev == nil {
        return
    }
    
    // IMPORTANT: Capture pre-reset parameters
    if err := h.deviceSvc.CaptureResetSnapshot(r.Context(), dev); err != nil {
        // Log but don't fail - reset should proceed even if snapshot fails
        h.log.Error().Err(err).Msg("failed to capture reset snapshot")
    }
    
    // Mark reset as in-progress
    dev.ResetInProgress = true
    dev.LastResetTime = ptr(time.Now().UTC())
    if err := h.deviceSvc.Update(r.Context(), dev); err != nil {
        writeError(w, http.StatusInternalServerError, "failed to mark reset")
        return
    }
    
    // Enqueue the factory reset task
    h.enqueueTask(w, r, serial, task.TypeFactoryReset, struct{}{})
}

func ptr[T any](v T) *T {
    return &v
}
```

### Step 3: Implement Reset Detection in CWMP Session

**File**: `internal/cwmp/session.go`

Add detection logic after Inform processing:

```go
import "github.com/raykavin/helix-acs/internal/device"

// Add method to Session struct:
func (s *Session) CheckAndRestoreAfterReset(
    ctx context.Context,
    dev *device.Device,
    deviceRepo device.Repository,
    taskQueue task.Queue,
) error {
    // Check if device was recently reset
    if !dev.ResetInProgress {
        return nil // No reset in progress
    }
    
    // Check if device has been restored already
    if dev.PreResetParams == nil || len(dev.PreResetParams) == 0 {
        return nil // No params to restore
    }
    
    // Get current device parameters
    currentParams := dev.Parameters
    if currentParams == nil {
        currentParams = make(map[string]string)
    }
    
    // Compare: if current params are significantly different from pre-reset,
    // it confirms the reset happened
    if hasSignificantDifference(currentParams, dev.PreResetParams) {
        // Enqueue restore task
        if err := s.enqueueRestoreTask(ctx, dev, taskQueue); err != nil {
            return fmt.Errorf("failed to enqueue restore task: %w", err)
        }
        
        // Mark reset as complete
        dev.ResetInProgress = false
        if err := deviceRepo.Update(ctx, dev); err != nil {
            return fmt.Errorf("failed to update device state: %w", err)
        }
    }
    
    return nil
}

func (s *Session) enqueueRestoreTask(
    ctx context.Context,
    dev *device.Device,
    taskQueue task.Queue,
) error {
    // Create set_parameters task with pre-reset values
    payload := task.SetParamsPayload{
        Parameters: dev.PreResetParams,
    }
    
    raw, _ := json.Marshal(payload)
    restoreTask := &task.Task{
        ID:          uuid.NewString(),
        Serial:      dev.Serial,
        Type:        task.TypeSetParams,
        Payload:     json.RawMessage(raw),
        Status:      task.StatusPending,
        CreatedAt:   time.Now().UTC(),
        MaxAttempts: 3,
    }
    
    return taskQueue.Enqueue(ctx, restoreTask)
}

func hasSignificantDifference(current, previous map[string]string) bool {
    // Check critical parameters that indicate a reset occurred
    criticalParams := []string{
        "Device.WiFi.SSID",           // WiFi changed
        "Device.WANDevice..ConnectionType", // WAN type reset
        "Device.ManagementServer.URL", // ACS URL reset
    }
    
    for _, param := range criticalParams {
        if current[param] != previous[param] {
            return true
        }
    }
    
    // Or check if major parameter count difference
    return len(current) < len(previous)/2 // More than 50% params missing
}
```

### Step 4: Integrate Detection into Inform Handler

**File**: `internal/cwmp/handler.go`

In the HandleInform method, after device state update:

```go
// After device update from Inform:
if err := session.CheckAndRestoreAfterReset(
    r.Context(),
    device,
    h.deviceRepo,
    h.taskQueue,
); err != nil {
    h.log.Error().Err(err).Str("serial", device.Serial).
        Msg("failed to check and restore after reset")
    // Log but continue - don't fail the Inform
}
```

### Step 5: Capture Parameter Snapshots

**File**: `internal/device/service.go`

Add method to capture pre-reset state:

```go
func (s *DeviceService) CaptureResetSnapshot(ctx context.Context, dev *Device) error {
    // Create a copy of current parameters as pre-reset backup
    dev.PreResetParams = make(map[string]string)
    for k, v := range dev.Parameters {
        dev.PreResetParams[k] = v
    }
    
    // Also store important derived values
    if dev.WiFi24 != nil {
        dev.PreResetParams["_snapshot.wifi24.ssid"] = dev.WiFi24.SSID
        dev.PreResetParams["_snapshot.wifi24.enabled"] = strconv.FormatBool(dev.WiFi24.Enabled)
    }
    
    if dev.WiFi5 != nil {
        dev.PreResetParams["_snapshot.wifi5.ssid"] = dev.WiFi5.SSID
        dev.PreResetParams["_snapshot.wifi5.enabled"] = strconv.FormatBool(dev.WiFi5.Enabled)
    }
    
    if len(dev.WANs) > 0 {
        wan := dev.WANs[0]
        dev.PreResetParams["_snapshot.wan.type"] = wan.ConnectionType
        dev.PreResetParams["_snapshot.wan.ip"] = wan.IPAddress
    }
    
    return s.repo.Update(ctx, dev)
}
```

### Step 6: Add Selective Parameter Filtering (Optional)

For cases where you don't want to restore *all* parameters:

```go
// File: internal/task/executor.go

// Define which parameter categories to restore
var restorableParamCategories = map[string]bool{
    "Device.WiFi":       true,
    "Device.WANDevice":  true,
    "Device.LANDevice":  true,
    "Device.ManagementServer": true,
    // Don't restore:
    // - System identifiers (serial, hardware version)
    // - Runtime metrics (uptime, traffic counters)
    // - Vendor-specific diagnostics
}

func filterRestorable(allParams map[string]string) map[string]string {
    filtered := make(map[string]string)
    for param, value := range allParams {
        for category := range restorableParamCategories {
            if strings.HasPrefix(param, category) {
                filtered[param] = value
                break
            }
        }
    }
    return filtered
}
```

### Step 7: Add Monitoring & Logging

**File**: `internal/cwmp/handler.go`

```go
func (s *Session) enqueueRestoreTask(ctx context.Context, dev *Device, taskQueue task.Queue) error {
    // ... existing code ...
    
    h.log.Info().
        Str("serial", dev.Serial).
        Int("params_count", len(dev.PreResetParams)).
        Time("reset_time", *dev.LastResetTime).
        Msg("enqueued parameter restoration after factory reset")
    
    return nil
}
```

---

## Configuration Options

Add to `config.yml`:

```yaml
# Reset and restoration settings
reset:
  # Enable automatic parameter restoration after factory reset
  enable_auto_restore: true
  
  # Maximum time to wait for device to reconnect after reset (minutes)
  reconnect_timeout: 30
  
  # Parameters to exclude from restoration (regex patterns)
  exclude_patterns:
    - "^Device.DeviceInfo.*"
    - "^Device.ManagementServer.Username"
    - "^Device.ManagementServer.Password"
  
  # Require manual confirmation for factory resets
  require_confirmation: true
  
  # Log all reset operations
  audit_log_enabled: true
```

---

## Testing Checklist

- [ ] Device factory reset task created successfully
- [ ] Pre-reset parameters stored in MongoDB
- [ ] Device reboots and sends new Inform
- [ ] Reset detected by ACS
- [ ] Restore task automatically enqueued
- [ ] Device receives SetParameterValues with previous config
- [ ] Device applies parameters correctly
- [ ] MongoDB state updated with restore status
- [ ] Log contains reset and restore events
- [ ] Handles device reconnect failures gracefully
- [ ] Selectively restores only applicable parameters
- [ ] Two-factor confirmation works if enabled

---

## API Example Usage

### Create Reset Request with Pre-Capture
```bash
POST /api/v1/devices/ABC123/tasks/factory-reset
Content-Type: application/json

{
  "reason": "Device configuration corruption",
  "notify_on_restore": true
}
```

### Check Reset Status
```bash
GET /api/v1/devices/ABC123/tasks?type=factory_reset&limit=1

Response:
{
  "id": "task-uuid",
  "serial": "ABC123",
  "type": "factory_reset",
  "status": "done",
  "created_at": "2024-01-15T10:00:00Z",
  "executed_at": "2024-01-15T10:02:30Z",
  "completed_at": "2024-01-15T10:05:45Z"
}
```

### Monitor Restore Task
```bash
GET /api/v1/devices/ABC123/tasks?type=set_parameters&limit=1

Response:
{
  "id": "restore-task-uuid",
  "serial": "ABC123",
  "type": "set_parameters",
  "status": "done",
  "result": {
    "parameters_set": 47,
    "parameters_failed": 0,
    "restore_duration_seconds": 15
  }
}
```

---

## Troubleshooting

### Device Not Reconnecting
- Check network connectivity
- Verify ACS URL in device configuration
- Check firewall/NAT rules
- Increase `reconnect_timeout` if device is slow to recover

### Partial Parameter Restoration
- Some vendor-specific parameters may require device reboot
- Create follow-up `reboot` task after parameter restoration
- Check parameter value compatibility

### Restore Task Failing
- Verify parameter paths match device's data model (TR-181 vs TR-098)
- Check if device supports all parameters being restored
- Review parameter value compatibility
- Use selective restoration instead of full restoration

---

## Future Enhancements

1. **Scheduled Resets**: Implement periodic device resets for maintenance
2. **Rollback Capability**: Keep multiple parameter snapshots for rollback
3. **Batch Restore**: Restore multiple devices in one operation
4. **Parameter Validation**: Validate parameters before pushing back
5. **Smart Diffing**: Only restore parameters that actually changed
6. **A/B Testing**: Test new config on subset before full rollout
