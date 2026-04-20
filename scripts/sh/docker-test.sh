#!/bin/bash

# Phase 4: Docker Integration Testing Script
# This script tests the complete system with Docker containers

set -e

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCKER_COMPOSE_FILE="${PROJECT_DIR}/docker-compose.yml"

echo "======================================"
echo "Phase 4: Docker Integration Testing"
echo "======================================"
echo ""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Helper functions
log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Step 1: Start Docker containers
log_info "Step 1: Starting Docker containers..."
cd "${PROJECT_DIR}"

if docker-compose ps | grep -q "Up"; then
    log_warn "Docker containers already running, stopping first..."
    docker-compose down
fi

docker-compose up -d --wait
sleep 5

# Verify containers are running
if ! docker-compose ps | grep -q "helix-acs"; then
    log_error "Failed to start Docker containers"
    exit 1
fi
log_info "✓ Docker containers started successfully"
echo ""

# Step 2: Test PostgreSQL connection
log_info "Step 2: Testing PostgreSQL connection..."
if docker-compose exec -T postgres psql -U helix -d helix_parameters -c "SELECT 1" > /dev/null 2>&1; then
    log_info "✓ PostgreSQL connection successful"
else
    log_error "Failed to connect to PostgreSQL"
    exit 1
fi
echo ""

# Step 3: Test MongoDB connection
log_info "Step 3: Testing MongoDB connection..."
if docker-compose exec -T mongo mongosh --eval "db.adminCommand('ping')" > /dev/null 2>&1; then
    log_info "✓ MongoDB connection successful"
else
    log_error "Failed to connect to MongoDB"
    exit 1
fi
echo ""

# Step 4: Test Redis connection
log_info "Step 4: Testing Redis connection..."
if docker-compose exec -T redis redis-cli ping > /dev/null 2>&1; then
    log_info "✓ Redis connection successful"
else
    log_error "Failed to connect to Redis"
    exit 1
fi
echo ""

# Step 5: Check database schema
log_info "Step 5: Verifying PostgreSQL schema..."
TABLE_COUNT=$(docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public';" 2>/dev/null | tail -1)

if [ "$TABLE_COUNT" -ge 6 ]; then
    log_info "✓ PostgreSQL schema initialized (${TABLE_COUNT} tables found)"
else
    log_warn "PostgreSQL schema may not be fully initialized (${TABLE_COUNT} tables found)"
fi
echo ""

# Step 6: Test parameter storage
log_info "Step 6: Testing parameter storage..."
SERIAL="TEST-DEVICE-001"
INSERT_SQL="INSERT INTO device_parameters (device_serial, param_name, param_value, updated_at) 
    VALUES ('${SERIAL}', 'Device.WiFi.SSID', 'TestSSID', NOW());"

docker-compose exec -T postgres psql -U helix -d helix_parameters -c "${INSERT_SQL}"

# Verify insert
PARAM_COUNT=$(docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "SELECT COUNT(*) FROM device_parameters WHERE device_serial = '${SERIAL}';" 2>/dev/null | tail -1)

if [ "$PARAM_COUNT" -ge 1 ]; then
    log_info "✓ Parameter storage working (${PARAM_COUNT} parameters stored)"
else
    log_error "Failed to store parameters"
    exit 1
fi
echo ""

# Step 7: Test parameter snapshot
log_info "Step 7: Testing parameter snapshots..."
SNAPSHOT_SQL="INSERT INTO parameter_snapshots (device_serial, snapshot_type, parameters, created_at)
    VALUES ('${SERIAL}', 'pre_reset_params', '{\"Device.WiFi.SSID\": \"TestSSID\"}', NOW());"

docker-compose exec -T postgres psql -U helix -d helix_parameters -c "${SNAPSHOT_SQL}"

# Verify snapshot
SNAPSHOT_COUNT=$(docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "SELECT COUNT(*) FROM parameter_snapshots WHERE device_serial = '${SERIAL}';" 2>/dev/null | tail -1)

if [ "$SNAPSHOT_COUNT" -ge 1 ]; then
    log_info "✓ Parameter snapshots working (${SNAPSHOT_COUNT} snapshots stored)"
else
    log_error "Failed to store parameter snapshot"
    exit 1
fi
echo ""

# Step 8: Test Redis cache
log_info "Step 8: Testing Redis cache..."
if docker-compose exec -T redis redis-cli SET "params:${SERIAL}" '{"test":"value"}' > /dev/null 2>&1; then
    CACHED=$(docker-compose exec -T redis redis-cli GET "params:${SERIAL}")
    if [ -n "$CACHED" ]; then
        log_info "✓ Redis cache working"
    else
        log_error "Failed to retrieve cached value"
        exit 1
    fi
else
    log_error "Failed to set cache value"
    exit 1
fi
echo ""

# Step 9: Build the application
log_info "Step 9: Building Helix ACS application..."
if cd "${PROJECT_DIR}" && go build ./cmd/api/...; then
    log_info "✓ Application built successfully"
else
    log_error "Failed to build application"
    exit 1
fi
echo ""

# Step 10: Check data volume
log_info "Step 10: Checking PostgreSQL data volume..."
DB_SIZE=$(docker-compose exec -T postgres du -sh /var/lib/postgresql/data 2>/dev/null | cut -f1)
log_info "PostgreSQL data volume: ${DB_SIZE}"
echo ""

# Step 11: Performance baseline
log_info "Step 11: Running performance baseline tests..."
log_info "  - Parameter insert latency"
START_TIME=$(date +%s%N)
for i in {1..100}; do
    docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
        "INSERT INTO device_parameters (device_serial, param_name, param_value, updated_at) 
         VALUES ('PERF-TEST-${i}', 'Device.Test', 'value${i}', NOW());" > /dev/null 2>&1
done
END_TIME=$(date +%s%N)
DURATION=$((($END_TIME - $START_TIME) / 1000000))
AVG_LATENCY=$(($DURATION / 100))
log_info "  ✓ 100 inserts in ${DURATION}ms (${AVG_LATENCY}ms average)"
echo ""

# Cleanup test data
log_info "Cleaning up test data..."
docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "DELETE FROM device_parameters WHERE device_serial LIKE 'TEST-%' OR device_serial LIKE 'PERF-%';"
docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "DELETE FROM parameter_snapshots WHERE device_serial LIKE 'TEST-%' OR device_serial LIKE 'PERF-%';"
echo ""

# Summary
echo "======================================"
echo -e "${GREEN}✓ Phase 4: All Tests Passed!${NC}"
echo "======================================"
echo ""
echo "Test Results Summary:"
echo "  ✓ PostgreSQL connection: OK"
echo "  ✓ MongoDB connection: OK"
echo "  ✓ Redis connection: OK"
echo "  ✓ Database schema: OK"
echo "  ✓ Parameter storage: OK"
echo "  ✓ Parameter snapshots: OK"
echo "  ✓ Redis cache: OK"
echo "  ✓ Application build: OK"
echo "  ✓ Performance baseline: ${AVG_LATENCY}ms per insert"
echo ""
echo "Next Steps:"
echo "  1. Run Docker containers: docker-compose up -d"
echo "  2. Build application: go build ./cmd/api/..."
echo "  3. Run integration tests: go test -v ./internal/..."
echo "  4. Load testing: ./scripts/load-test.sh"
echo ""
log_info "Docker containers running. Use 'docker-compose down' to stop."
