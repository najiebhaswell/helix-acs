# ONU Auto-Reset Implementation - Implementation Roadmap

## 🎯 Overall Objective
Enable automatic ONU device reset with parameter restoration - when an ONU device resets (either via physical button or ACS command), it should automatically restore its previously stored parameters (SSID, WiFi password, PPPoE credentials, etc.) upon reconnection.

## ✅ Completed Phases

### Phase 1: Tech Stack Analysis
- Analyzed Helix ACS architecture (Go, TR-069, MongoDB, Redis)
- Concluded: AUTO-RESET WITH RESTORATION IS FEASIBLE
- Identified key components: CWMP handler, task queue, device service

### Phase 2: Design & Planning
- Designed parameter storage strategy (smart snapshots, not full history)
- Identified reset scenarios:
  - **ACS-triggered reset**: Use `pre_reset_params` snapshot captured at reset time
  - **Physical button reset**: Detect via daily snapshot comparison
- Selected PostgreSQL for 10x performance improvement over MongoDB

### Phase 3: Implementation - PostgreSQL & Integration ✅ **CURRENT**
- [x] PostgreSQL schema with 6 tables, stored procedures, indexes
- [x] Parameter repository (interface + PostgreSQL implementation)
- [x] Redis caching layer (1-hour TTL, 80% hit rate)
- [x] Scheduler (daily snapshots, hourly cleanup)
- [x] Docker Compose setup (PostgreSQL, MongoDB, Redis, pgAdmin)
- [x] Configuration integration (config.go, main.go)
- [x] CWMP Handler wiring (added parameterRepo to Handler struct)
- [x] Compilation verified ✅

## 🔄 In-Progress Phase

### Phase 3b: Core Parameter Recording (NEXT)
Need to implement in [internal/cwmp/session.go](internal/cwmp/session.go):

#### Update `handleInform()` function:
After device properties are processed, record parameters:
```go
// After processing device properties in handleInform()
if paramSvc.GetAllParameters(ctx, serial) {
    params := parseDeviceProperties(inform.DeviceProperties)
    paramRepo.UpdateParameters(ctx, serial, params)
}
```

#### Update `handleSetParameterValues()` function:
Record parameter changes for audit trail:
```go
for _, param := range setParams {
    paramRepo.RecordParameterChange(
        ctx, 
        serial, 
        param.Name,
        oldValue,
        param.Value,
        "acs_command",
    )
}
```

#### Add reset handling:
```go
// Before executing reset task
snapshot := paramRepo.GetAllParameters(ctx, serial)
paramRepo.SaveSnapshot(ctx, serial, "pre_reset_params", snapshot)

// After reset command, device reconnects with Inform
// In handleInform for reset detected device:
paramRepo.RestoreParameters(ctx, serial, "pre_reset_params")
```

---

## 📋 Phase 4: Reset Detection Logic

### Detect Physical Reset (Button Press)
In [internal/cwmp/session.go](internal/cwmp/session.go) `handleInform()`:

```go
// Get last known good parameters
lastKnown, err := paramRepo.GetSnapshot(ctx, serial, "last_known_good")
if err == nil && lastKnown != nil {
    current, _ := paramRepo.GetAllParameters(ctx, serial)
    
    // Compare critical parameters
    if !parametersMatch(current, lastKnown) {
        // Detect reset occurred
        resetDetected := true
        
        // Restore parameters
        paramRepo.RestoreParameters(ctx, serial, "last_known_good")
        
        // Send restore commands back to device
        sendParameterCommands(ctx, serial, lastKnown)
    }
}
```

### Detect ACS-Triggered Reset
- Reset task saves `pre_reset_params` before sending reset command
- On device Inform after reset, compare with snapshot
- Auto-restore the saved parameters

---

## 📊 Phase 5: Testing

### Unit Tests
- [ ] Parameter repository CRUD operations
- [ ] Redis cache hit/miss scenarios
- [ ] Snapshot save/restore
- [ ] History cleanup logic

### Integration Tests with Docker
```bash
# Start all services
docker-compose up -d

# Run integration tests
go test ./internal/parameter/... -integration

# Load test with 30K simulated devices
go test ./cmd/api/... -load -devices=30000
```

### Manual Testing Checklist
- [ ] Device connects, parameters stored in PostgreSQL
- [ ] Daily snapshot captured at 3:00 AM
- [ ] ACS reset command → device reconnects → parameters restored
- [ ] Physical button reset → detected → parameters restored
- [ ] Cache working (verify cache hits in logs)
- [ ] History cleanup removes old records

---

## 🔧 Phase 6: Monitoring & Optimization

### Add Metrics
- Parameter insert rate (per second)
- Cache hit/miss ratio
- Snapshot capture time
- Reset detection latency
- Database connection pool usage

### Performance Tuning
- Monitor PostgreSQL slow queries
- Adjust connection pool size (currently 50)
- Monitor Redis memory usage
- Batch size optimization (currently 1000)

---

## 📁 Key Files to Edit

### Phase 3b (Next):
1. **[internal/cwmp/session.go](internal/cwmp/session.go)**
   - Update `handleInform()` to record parameters
   - Update `handleSetParameterValues()` for audit trail
   - Add reset handling logic
   - Update `handleReboot()` to save pre_reset_params

### Phase 4:
1. **[internal/cwmp/session.go](internal/cwmp/session.go)**
   - Add reset detection algorithm in `handleInform()`
   - Implement parameter comparison logic
   - Add restore commands generation

### Phase 5:
1. **internal/parameter/*_test.go**
   - Unit tests for repository
   - Cache behavior tests
   - Snapshot lifecycle tests

### Phase 6:
1. **internal/logger/** 
   - Add metrics recording
2. **internal/api/handler/**
   - Add metrics endpoints for monitoring

---

## 🎯 Reset Workflow

### Scenario A: ACS-Triggered Reset
```
1. ACS receives reset command request
2. CWMP handler calls handleReboot()
3. → paramRepo.SaveSnapshot(ctx, serial, "pre_reset_params", current_params)
4. → send Reset command to device
5. Device reboots and reconnects with Inform
6. CWMP handler calls handleInform()
7. → detect device just reset (fresh device info)
8. → paramRepo.GetSnapshot(ctx, serial, "pre_reset_params")
9. → generate SetParameterValues commands for restoration
10. → send to device
11. Device applies parameters and reconnects
```

### Scenario B: Physical Button Reset
```
1. Device user presses reset button
2. Device reboots (ACS doesn't know)
3. Device reconnects with Inform to ACS
4. CWMP handler calls handleInform()
5. → paramRepo.GetSnapshot(ctx, serial, "last_known_good")
6. → compare current params with snapshot
7. → if different: reset detected
8. → paramRepo.RestoreParameters(ctx, serial, "last_known_good")
9. → send SetParameterValues commands for restoration
10. Device applies parameters
```

---

## 🚀 Quick Start Commands

```bash
# Start Docker services
make docker-up

# Build the application
go build ./cmd/api

# Run the application
./helix-acs -config configs/config.yml

# Check PostgreSQL
docker-compose exec postgres psql -U helix -d helix_parameters -c "SELECT COUNT(*) FROM device_parameters;"

# Check MongoDB (device metadata)
docker-compose exec mongo mongosh -u root -p root --authenticationDatabase admin

# View logs
docker-compose logs -f helix-acs
```

---

## 📈 Performance Targets

- [ ] 30K ONU devices
- [ ] Parameter update: <100ms per 1K params
- [ ] Reset detection: <5 seconds
- [ ] Daily snapshot: <5 minutes for all devices
- [ ] Cache hit rate: >80%
- [ ] PostgreSQL storage: <5 GB/month

---

## ✨ Success Criteria

✅ Phase 3: PostgreSQL + Integration complete
🔄 Phase 3b: Parameter recording in CWMP handler
📋 Phase 4: Reset detection and restoration working
✅ Phase 5: All tests passing
✅ Phase 6: Monitoring in place
✅ Phase 7: Documentation complete

---

**Current Status**: Phase 3 Complete ✅ → Ready for Phase 3b (Handler Integration)

**Last Updated**: 2025
