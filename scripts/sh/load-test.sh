#!/bin/bash

# Phase 4: Load Testing Script
# Simulates multiple ONU devices with concurrent parameter operations

set -e

PROJECT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${PROJECT_DIR}/../.."

# Configuration
DEVICE_COUNT=${1:-100}
CONCURRENT=${2:-10}
DURATION=${3:-60}

# Colors
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
NC='\033[0m'

log_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

echo "======================================"
echo "Phase 4: Load Testing"
echo "======================================"
echo ""
echo "Configuration:"
echo "  Devices to simulate: $DEVICE_COUNT"
echo "  Concurrent operations: $CONCURRENT"
echo "  Test duration: ${DURATION}s"
echo ""

# Check if PostgreSQL is running
log_info "Checking PostgreSQL connection..."
if ! docker-compose exec -T postgres psql -U helix -d helix_parameters -c "SELECT 1" > /dev/null 2>&1; then
    log_error "PostgreSQL is not running. Start it with: docker-compose up -d"
    exit 1
fi
log_info "✓ PostgreSQL connected"
echo ""

# Generate test data
log_info "Step 1: Generating $DEVICE_COUNT test devices..."
BATCH_SIZE=100
PROCESSED=0

while [ $PROCESSED -lt $DEVICE_COUNT ]; do
    BATCH_END=$((PROCESSED + BATCH_SIZE))
    if [ $BATCH_END -gt $DEVICE_COUNT ]; then
        BATCH_END=$DEVICE_COUNT
    fi

    SQL=""
    for ((i=PROCESSED; i<BATCH_END; i++)); do
        SERIAL="LOAD-TEST-$(printf "%06d" $i)"
        SQL="${SQL}INSERT INTO device_parameters (device_serial, param_name, param_value, updated_at) 
            VALUES ('${SERIAL}', 'Device.DeviceInfo.SerialNumber', '${SERIAL}', NOW()),
                   ('${SERIAL}', 'Device.WiFi.SSID', 'TestNetwork${i}', NOW()),
                   ('${SERIAL}', 'Device.WiFi.PreSharedKey', 'password${i}', NOW());
        "
    done

    docker-compose exec -T postgres psql -U helix -d helix_parameters -c "${SQL}"
    PROCESSED=$BATCH_END
    echo "  Generated $PROCESSED/$DEVICE_COUNT devices"
done

log_info "✓ Test devices created"
echo ""

# Test 1: Parameter Retrieval Throughput
log_info "Step 2: Testing parameter retrieval throughput..."
START_TIME=$(date +%s%N)
QUERY_COUNT=0

for i in $(seq 1 $DEVICE_COUNT); do
    SERIAL="LOAD-TEST-$(printf "%06d" $((i-1)))"
    docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
        "SELECT * FROM device_parameters WHERE device_serial = '${SERIAL}' LIMIT 10;" > /dev/null 2>&1 &
    
    # Limit concurrent queries
    ACTIVE_JOBS=$(jobs -r | wc -l)
    while [ $ACTIVE_JOBS -ge $CONCURRENT ]; do
        sleep 0.1
        ACTIVE_JOBS=$(jobs -r | wc -l)
    done
    
    QUERY_COUNT=$((QUERY_COUNT + 1))
done

wait
END_TIME=$(date +%s%N)
DURATION_MS=$((($END_TIME - $START_TIME) / 1000000))
THROUGHPUT=$((QUERY_COUNT * 1000 / DURATION_MS))

log_info "✓ Retrieved $QUERY_COUNT parameters in ${DURATION_MS}ms ($THROUGHPUT queries/sec)"
echo ""

# Test 2: Parameter Update Throughput
log_info "Step 3: Testing parameter update throughput..."
START_TIME=$(date +%s%N)
UPDATE_COUNT=0

for i in $(seq 1 $DEVICE_COUNT); do
    SERIAL="LOAD-TEST-$(printf "%06d" $((i-1)))"
    docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
        "UPDATE device_parameters SET param_value = 'updated${RANDOM}', updated_at = NOW() 
         WHERE device_serial = '${SERIAL}';" > /dev/null 2>&1 &
    
    ACTIVE_JOBS=$(jobs -r | wc -l)
    while [ $ACTIVE_JOBS -ge $CONCURRENT ]; do
        sleep 0.1
        ACTIVE_JOBS=$(jobs -r | wc -l)
    done
    
    UPDATE_COUNT=$((UPDATE_COUNT + 1))
done

wait
END_TIME=$(date +%s%N)
DURATION_MS=$((($END_TIME - $START_TIME) / 1000000))
THROUGHPUT=$((UPDATE_COUNT * 1000 / DURATION_MS))

log_info "✓ Updated $UPDATE_COUNT parameters in ${DURATION_MS}ms ($THROUGHPUT updates/sec)"
echo ""

# Test 3: Snapshot Operations
log_info "Step 4: Testing snapshot operations..."
START_TIME=$(date +%s%N)
SNAPSHOT_COUNT=0

for i in $(seq 1 $DEVICE_COUNT); do
    SERIAL="LOAD-TEST-$(printf "%06d" $((i-1)))"
    PARAMS="{\"Device.WiFi.SSID\": \"TestNetwork${i}\", \"Device.WiFi.PreSharedKey\": \"password${i}\"}"
    
    docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
        "INSERT INTO parameter_snapshots (device_serial, snapshot_type, parameters, created_at)
         VALUES ('${SERIAL}', 'pre_reset_params', '${PARAMS}', NOW());" > /dev/null 2>&1 &
    
    ACTIVE_JOBS=$(jobs -r | wc -l)
    while [ $ACTIVE_JOBS -ge $CONCURRENT ]; do
        sleep 0.1
        ACTIVE_JOBS=$(jobs -r | wc -l)
    done
    
    SNAPSHOT_COUNT=$((SNAPSHOT_COUNT + 1))
done

wait
END_TIME=$(date +%s%N)
DURATION_MS=$((($END_TIME - $START_TIME) / 1000000))
THROUGHPUT=$((SNAPSHOT_COUNT * 1000 / DURATION_MS))

log_info "✓ Created $SNAPSHOT_COUNT snapshots in ${DURATION_MS}ms ($THROUGHPUT snapshots/sec)"
echo ""

# Test 4: Database Statistics
log_info "Step 5: Collecting database statistics..."
PARAM_COUNT=$(docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "SELECT COUNT(*) FROM device_parameters WHERE device_serial LIKE 'LOAD-TEST-%';" 2>/dev/null | tail -1)

SNAPSHOT_COUNT=$(docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "SELECT COUNT(*) FROM parameter_snapshots WHERE device_serial LIKE 'LOAD-TEST-%';" 2>/dev/null | tail -1)

TABLE_SIZE=$(docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "SELECT pg_size_pretty(pg_total_relation_size('device_parameters'));" 2>/dev/null | tail -1)

log_info "Database Statistics:"
echo "  Parameters stored: $PARAM_COUNT"
echo "  Snapshots created: $SNAPSHOT_COUNT"
echo "  device_parameters table size: $TABLE_SIZE"
echo ""

# Cleanup
log_info "Cleaning up test data..."
docker-compose exec -T postgres psql -U helix -d helix_parameters -c \
    "DELETE FROM parameter_history WHERE device_serial LIKE 'LOAD-TEST-%';
     DELETE FROM parameter_snapshots WHERE device_serial LIKE 'LOAD-TEST-%';
     DELETE FROM device_parameters WHERE device_serial LIKE 'LOAD-TEST-%';"

log_info "✓ Test data cleaned up"
echo ""

# Summary
echo "======================================"
echo -e "${GREEN}✓ Phase 4: Load Testing Complete${NC}"
echo "======================================"
echo ""
echo "Performance Summary:"
echo "  Parameter retrieval: $THROUGHPUT queries/sec"
echo "  Parameter updates: $THROUGHPUT updates/sec"  
echo "  Snapshot creation: $THROUGHPUT snapshots/sec"
echo ""
echo "Scaling Analysis:"
SCALED_THROUGHPUT=$((THROUGHPUT * 300 / DEVICE_COUNT))
log_warn "Estimated throughput at 30K devices: ~$SCALED_THROUGHPUT ops/sec"
echo ""
echo "Recommendation:"
if [ $THROUGHPUT -gt 1000 ]; then
    echo -e "${GREEN}✓ Performance is excellent - ready for 30K+ ONU devices${NC}"
elif [ $THROUGHPUT -gt 500 ]; then
    echo -e "${YELLOW}✓ Performance is good - suitable for 30K ONU devices${NC}"
else
    echo -e "${RED}⚠ Performance may need optimization for 30K ONU devices${NC}"
fi
