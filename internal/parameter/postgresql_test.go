package parameter

import (
	"context"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/raykavin/helix-acs/internal/logger"
)

// TestLogger is a simple no-op logger for testing
type TestLogger struct{}

var _ logger.Logger = (*TestLogger)(nil)

func (t *TestLogger) WithField(key string, value any) logger.Logger  { return t }
func (t *TestLogger) WithFields(fields map[string]any) logger.Logger { return t }
func (t *TestLogger) WithError(err error) logger.Logger              { return t }
func (t *TestLogger) Print(args ...any)                              {}
func (t *TestLogger) Printf(format string, args ...any)              {}
func (t *TestLogger) Debug(args ...any)                              {}
func (t *TestLogger) Debugf(format string, args ...any)              {}
func (t *TestLogger) Info(args ...any)                               {}
func (t *TestLogger) Infof(format string, args ...any)               {}
func (t *TestLogger) Warn(args ...any)                               {}
func (t *TestLogger) Warnf(format string, args ...any)               {}
func (t *TestLogger) Error(args ...any)                              {}
func (t *TestLogger) Errorf(format string, args ...any)              {}
func (t *TestLogger) Panic(args ...any)                              {}
func (t *TestLogger) Panicf(format string, args ...any)              {}
func (t *TestLogger) Fatal(args ...any)                              {}
func (t *TestLogger) Fatalf(format string, args ...any)              {}

// TestPostgreSQLRepository tests the PostgreSQL repository implementation.
// These tests require a running PostgreSQL instance.
// Run with: go test -v -run TestPostgreSQL ./internal/parameter/...

func TestUpdateAndGetParameters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, cleanup := setupTestRepository(t, ctx)
	defer cleanup()

	serial := "TEST-ONU-001"
	params := map[string]string{
		"Device.WiFi.SSID":              "TestNetwork",
		"Device.WiFi.PreSharedKey":      "password123",
		"Device.ManagementServer.URL":   "http://acs.example.com",
		"Device.Ethernet.Interface":     "eth0",
		"InternetGatewayDevice.X_Model": "TestONU",
	}

	// Update parameters
	err := repo.UpdateParameters(ctx, serial, params)
	if err != nil {
		t.Fatalf("UpdateParameters failed: %v", err)
	}

	// Retrieve all parameters
	retrieved, err := repo.GetAllParameters(ctx, serial)
	if err != nil {
		t.Fatalf("GetAllParameters failed: %v", err)
	}

	if len(retrieved) != len(params) {
		t.Errorf("Expected %d parameters, got %d", len(params), len(retrieved))
	}

	for key, expected := range params {
		actual, exists := retrieved[key]
		if !exists {
			t.Errorf("Parameter %s not found", key)
		}
		if actual != expected {
			t.Errorf("Parameter %s: expected %q, got %q", key, expected, actual)
		}
	}

	// Test single parameter retrieval
	val, err := repo.GetParameter(ctx, serial, "Device.WiFi.SSID")
	if err != nil {
		t.Fatalf("GetParameter failed: %v", err)
	}
	if val != "TestNetwork" {
		t.Errorf("Expected SSID 'TestNetwork', got %q", val)
	}
}

func TestParameterSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, cleanup := setupTestRepository(t, ctx)
	defer cleanup()

	serial := "TEST-ONU-002"
	params := map[string]string{
		"Device.WiFi.SSID":         "OriginalSSID",
		"Device.WiFi.PreSharedKey": "originalPassword",
	}

	// Save snapshot
	err := repo.SaveSnapshot(ctx, serial, "pre_reset_params", params)
	if err != nil {
		t.Fatalf("SaveSnapshot failed: %v", err)
	}

	// Retrieve snapshot
	retrieved, err := repo.GetSnapshot(ctx, serial, "pre_reset_params")
	if err != nil {
		t.Fatalf("GetSnapshot failed: %v", err)
	}

	if len(retrieved) != len(params) {
		t.Errorf("Expected %d parameters in snapshot, got %d", len(params), len(retrieved))
	}

	for key, expected := range params {
		actual, exists := retrieved[key]
		if !exists {
			t.Errorf("Parameter %s not found in snapshot", key)
		}
		if actual != expected {
			t.Errorf("Parameter %s: expected %q, got %q", key, expected, actual)
		}
	}
}

func TestParameterPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, cleanup := setupTestRepository(t, ctx)
	defer cleanup()

	serial := "TEST-ONU-003"
	params := map[string]string{
		"Device.WiFi.SSID":       "TestSSID",
		"Device.WiFi.Channel":    "6",
		"Device.Ethernet.Status": "Up",
		"Device.IP.DNSServer1":   "8.8.8.8",
		"Device.IP.DNSServer2":   "8.8.4.4",
	}

	err := repo.UpdateParameters(ctx, serial, params)
	if err != nil {
		t.Fatalf("UpdateParameters failed: %v", err)
	}

	// Get WiFi parameters by prefix
	wifiParams, err := repo.GetParametersByPrefix(ctx, serial, "Device.WiFi")
	if err != nil {
		t.Fatalf("GetParametersByPrefix failed: %v", err)
	}

	if len(wifiParams) != 2 {
		t.Errorf("Expected 2 WiFi parameters, got %d", len(wifiParams))
	}

	if _, exists := wifiParams["Device.WiFi.SSID"]; !exists {
		t.Errorf("SSID parameter not found in WiFi prefix results")
	}

	// Get DNS parameters
	dnsParams, err := repo.GetParametersByPrefix(ctx, serial, "Device.IP.DNS")
	if err != nil {
		t.Fatalf("GetParametersByPrefix for DNS failed: %v", err)
	}

	if len(dnsParams) != 2 {
		t.Errorf("Expected 2 DNS parameters, got %d", len(dnsParams))
	}
}

func TestRecordParameterChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, cleanup := setupTestRepository(t, ctx)
	defer cleanup()

	serial := "TEST-ONU-004"

	// Record a parameter change
	err := repo.RecordParameterChange(ctx, serial, "Device.WiFi.SSID", "OldSSID", "NewSSID", "acs_command")
	if err != nil {
		t.Fatalf("RecordParameterChange failed: %v", err)
	}

	// Get history
	history, err := repo.GetParameterHistory(ctx, serial, "Device.WiFi.SSID", 10)
	if err != nil {
		t.Fatalf("GetParameterHistory failed: %v", err)
	}

	if len(history) == 0 {
		t.Errorf("Expected at least 1 history entry, got 0")
	}

	// Verify the change was recorded
	firstEntry := history[0]
	if newVal, ok := firstEntry["new_value"]; ok {
		if newVal != "NewSSID" {
			t.Errorf("Expected new_value 'NewSSID', got %q", newVal)
		}
	}
}

func TestDeleteDeviceParameters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, cleanup := setupTestRepository(t, ctx)
	defer cleanup()

	serial := "TEST-ONU-005"
	params := map[string]string{
		"Device.WiFi.SSID": "TestSSID",
	}

	// Insert parameters
	err := repo.UpdateParameters(ctx, serial, params)
	if err != nil {
		t.Fatalf("UpdateParameters failed: %v", err)
	}

	// Verify they exist
	retrieved, err := repo.GetAllParameters(ctx, serial)
	if err != nil {
		t.Fatalf("GetAllParameters failed: %v", err)
	}

	if len(retrieved) == 0 {
		t.Fatalf("Expected parameters to exist before deletion")
	}

	// Delete parameters
	err = repo.DeleteDeviceParameters(ctx, serial)
	if err != nil {
		t.Fatalf("DeleteDeviceParameters failed: %v", err)
	}

	// Verify they're gone
	retrieved, err = repo.GetAllParameters(ctx, serial)
	if err != nil {
		t.Fatalf("GetAllParameters failed: %v", err)
	}

	if len(retrieved) > 0 {
		t.Errorf("Expected 0 parameters after deletion, got %d", len(retrieved))
	}
}

func TestHealthCheck(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	repo, cleanup := setupTestRepository(t, ctx)
	defer cleanup()

	err := repo.HealthCheck(ctx)
	if err != nil {
		t.Fatalf("HealthCheck failed: %v", err)
	}
}

// setupTestRepository creates a test repository with a temporary database.
func setupTestRepository(t *testing.T, ctx context.Context) (*PostgreSQLRepository, func()) {
	// Connect to test database
	connStr := "postgres://helix:helix_password@localhost:5432/helix_parameters?sslmode=disable"
	db, err := sqlx.Connect("postgres", connStr)
	if err != nil {
		t.Skipf("Could not connect to test PostgreSQL database: %v", err)
	}

	// Create test cache
	cache := &TestCache{data: make(map[string]map[string]string)}

	// Create test logger
	testLogger := &TestLogger{}

	// Create repository
	repo := &PostgreSQLRepository{
		db:    db,
		cache: cache,
		log:   testLogger,
	}

	// Clean up test data
	cleanup := func() {
		// Delete test data
		_, _ = db.ExecContext(ctx, "DELETE FROM device_parameters WHERE device_serial LIKE 'TEST-ONU-%'")
		_, _ = db.ExecContext(ctx, "DELETE FROM parameter_snapshots WHERE device_serial LIKE 'TEST-ONU-%'")
		_, _ = db.ExecContext(ctx, "DELETE FROM parameter_history WHERE device_serial LIKE 'TEST-ONU-%'")
		db.Close()
	}

	return repo, cleanup
}

// TestCache is a simple in-memory cache for testing.
type TestCache struct {
	data map[string]map[string]string
}

func (tc *TestCache) Get(ctx context.Context, key string) (map[string]string, error) {
	return tc.data[key], nil
}

func (tc *TestCache) Set(ctx context.Context, key string, value map[string]string, ttl time.Duration) error {
	tc.data[key] = value
	return nil
}

func (tc *TestCache) Delete(ctx context.Context, key string) error {
	delete(tc.data, key)
	return nil
}
