# Device Driver Architecture — Task Tracker

## Phase 1: Driver Schema & Engine (new files)
- [x] `internal/schema/driver.go` — DeviceDriver struct, YAML types, loader, registry
- [x] `internal/schema/provision.go` — ProvisionFlow, ProvisionStep, ProvisionExecutor

## Phase 2: Driver YAML Files (TP-Link migration)
- [x] `schemas/vendors/tplink/tr181/driver.yaml` — TP-Link device config
- [x] `schemas/vendors/tplink/tr181/provision_wan.yaml` — PPPoE provisioning steps (fresh)
- [x] `schemas/vendors/tplink/tr181/provision_wan_delete_add.yaml` — Delete+Add provisioning

## Phase 3: Resolver Enhancement
- [x] `driver.go` — Model-level + vendor-level resolution built into DeviceDriverRegistry

## Phase 4: Session & Executor Refactoring 
- [x] `session.go` — Added `driver` to Session, `driverRegistry` to Handler
- [x] `session.go` — handleInform resolves driver from Manufacturer/ProductClass
- [x] `session.go` — executeTask TypeWAN uses driver-based provisioning flow
- [x] `session.go` — executeTask TypeWifi uses driver.MapSecurityMode()
- [x] `session.go` — handleDeleteObjectResponse handles WAN provision delete steps
- [x] `wan_provision.go` — Rewritten to wrap schema.ProvisionExecutor
- [x] `wan_provision.go` — newWANProvisionFromDriver() for YAML-driven flows
- [x] `wan_provision.go` — Legacy constructors kept for backward compatibility
- [x] `executor.go` — Added DriverHints (BandSteeringPath, SecurityModeMapper)
- [x] `executor.go` — Removed hardcoded X_TP_BandSteering path
- [x] `executor.go` — Removed hardcoded security mode mapping
- [x] `cmd/api/main.go` — Loads driver registry at startup, passes to Handler

## Phase 5: Remove Old Mappers (deferred — separate PR)
- [ ] Remove `TR181Mapper` / `TR098Mapper` — requires full YAML schemas for all paths
- [ ] Ensure `SchemaMapper` always used
- [ ] Update `model.go` — remove `NewMapper()` fallback
> Note: deferred to avoid breaking existing functionality until all vendors have drivers

## Phase 6: Verification
- [x] `go build ./...` — ✅ passes
- [x] `go vet ./internal/schema/... ./internal/cwmp/... ./internal/task/...` — ✅ passes  
- [x] `TestWANProvision_OnSetParams_LastStepReturnsNil` — ✅ passes
- [x] `TestBuildSetParamsWifi24/5/TR098/Empty` — ✅ all pass
- [ ] `TestBuildSetParamsWAN` — ❌ pre-existing failure (not caused by this change)
- [ ] `TestExtractWANInfos_TR098_MultiWAN` — ❌ pre-existing failure (not caused by this change)
