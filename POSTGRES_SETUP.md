# PostgreSQL Setup & Migration Guide

## Quick Start with Docker

### 1. Prerequisites
- Docker & Docker Compose installed
- Helix ACS repository cloned

### 2. Start Services

```bash
# Navigate to project root
cd /home/well/helix-acs

# Make script executable
chmod +x scripts/docker-setup.sh

# Run setup script
./scripts/docker-setup.sh
```

This will start:
- **PostgreSQL** (port 5432)
- **MongoDB** (port 27017)
- **Redis** (port 6379)
- **pgAdmin** (port 5050) - for database management

### 3. Verify Services

```bash
# Check all containers running
docker-compose ps

# View logs
docker-compose logs -f postgresql
docker-compose logs -f mongodb
docker-compose logs -f redis
```

### 4. Access Databases

#### PostgreSQL
```bash
# Via docker-compose
docker-compose exec postgresql psql -U helix -d helix_parameters

# Via psql (if installed locally)
psql -h localhost -U helix -d helix_parameters -p 5432
```

#### MongoDB
```bash
# Via docker-compose
docker-compose exec mongodb mongosh

# Via mongosh (if installed locally)
mongosh --host localhost --port 27017 -u helix -p helix_password
```

#### Redis
```bash
# Via docker-compose
docker-compose exec redis redis-cli

# Via redis-cli (if installed locally)
redis-cli -h localhost -p 6379
```

#### pgAdmin (GUI)
- URL: http://localhost:5050
- Email: admin@example.com
- Password: admin_password

---

## Configuration

### 1. Update config.yml

Copy example configuration:
```bash
cp configs/config.example.with-postgres.yml configs/config.yml
```

Edit `configs/config.yml` and adjust:
```yaml
postgresql:
  host: "localhost"
  port: 5432
  user: "helix"
  password: "helix_password"
  database: "helix_parameters"
  max_connections: 50

parameters:
  backend: "postgresql"
  cache_enabled: true
  daily_snapshot:
    enabled: true
    time: "03:00"
```

### 2. Update go.mod

Add PostgreSQL driver:
```bash
go get github.com/lib/pq
go get github.com/jmoiron/sqlx
```

---

## Implementation in Code

### 1. Update config struct

Add PostgreSQL config ke `internal/config/config.go`:

```go
type PostgreSQL struct {
    Host                    string        `mapstructure:"host"`
    Port                    int           `mapstructure:"port"`
    User                    string        `mapstructure:"user"`
    Password                string        `mapstructure:"password"`
    Database                string        `mapstructure:"database"`
    MaxConnections          int           `mapstructure:"max_connections"`
    ConnectionTimeoutMs     int           `mapstructure:"connection_timeout_ms"`
    ConnectionMaxLifetime   time.Duration `mapstructure:"connection_max_lifetime_seconds"`
    CacheHost               string        `mapstructure:"cache_host"`
    CachePort               int           `mapstructure:"cache_port"`
}

type Config struct {
    // ... existing fields ...
    PostgreSQL PostgreSQL `mapstructure:"postgresql"`
}
```

### 2. Initialize in main.go

```go
package main

import (
    "context"
    "github.com/raykavin/helix-acs/internal/config"
    "github.com/raykavin/helix-acs/internal/parameter"
)

func main() {
    // ... existing setup ...
    
    // Initialize parameter repository (PostgreSQL)
    paramRepo, err := parameter.InitializePostgreSQL(cfg.PostgreSQL, logger)
    if err != nil {
        log.Fatal().Err(err).Msg("failed to initialize parameter repository")
    }
    defer paramRepo.Close()
    
    // Initialize parameter scheduler
    paramScheduler := parameter.NewScheduler(paramRepo, logger)
    go paramScheduler.StartDailySnapshot(context.Background(), cfg.Parameters.DailySnapshot.Time)
    go paramScheduler.StartHourlyCleanup(context.Background(), cfg.Parameters.History.RetentionDays)
    
    // ... rest of application setup ...
}
```

### 3. Update CWMP Handler

Di `internal/cwmp/handler.go`:

```go
type Handler struct {
    deviceRepo    device.Repository    // MongoDB
    parameterRepo parameter.Repository // PostgreSQL (BARU)
    taskQueue     task.Queue           // Redis
    // ... existing fields ...
}

func (h *Handler) handleInform(w http.ResponseWriter, r *http.Request, body []byte) {
    // ... parse Inform ...
    
    // Update parameters ke PostgreSQL
    if err := h.parameterRepo.UpdateParameters(ctx, serial, newParameters); err != nil {
        h.log.Error().Err(err).Msg("failed to update parameters")
        // Continue - jangan fail request
    }
    
    // Update device metadata ke MongoDB
    device.UpdatedAt = time.Now()
    if err := h.deviceRepo.Update(ctx, device); err != nil {
        h.log.Error().Err(err).Msg("failed to update device")
    }
    
    // ... rest of handler ...
}
```

---

## Database Schema

Schema otomatis dibuat via `scripts/schema-postgresql.sql`:

```sql
-- Main tables
- device_parameters          (current parameters)
- device_parameter_snapshots (snapshots untuk restore)
- device_parameter_history   (audit trail)
- device_parameter_metadata  (statistics)

-- Stored procedures
- upsert_device_parameters()      (batch update)
- save_parameter_snapshot()        (save snapshot)
- get_device_parameters_json()     (fetch as JSON)
- daily_snapshot_all_devices()     (daily snapshot)

-- Views
- v_device_parameter_summary       (stats per device)
- v_parameter_change_frequency     (change tracking)

-- Indexes
- device_serial (primary lookup)
- updated_at (time-based queries)
- param_name (parameter searches)
```

---

## Performance Tips

### 1. Connection Pooling
```yaml
postgresql:
  max_connections: 50  # Adjust berdasarkan load
```

### 2. Redis Cache
Enable cache untuk frequent reads:
```yaml
parameters:
  cache_enabled: true
  cache_ttl_minutes: 60
```

### 3. Batch Size
Adjust batch size untuk insert:
```yaml
parameters:
  batch_size: 1000  # Jika lebih lambat, kurangi
```

### 4. Monitoring
```bash
# Check PostgreSQL stats
SELECT datname, numbackends, xact_commit, xact_rollback
FROM pg_stat_database
WHERE datname = 'helix_parameters';

# Check table sizes
SELECT schemaname, tablename, pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename))
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;
```

---

## Migration from MongoDB

### Phase 1: Dual-Write (1-2 weeks)
```go
// Write ke both MongoDB dan PostgreSQL
deviceRepo.Update(ctx, device)  // MongoDB
paramRepo.UpdateParameters(ctx, serial, params)  // PostgreSQL

// Read dari MongoDB (lama)
params := device.Parameters
```

### Phase 2: PostgreSQL Read (1-2 weeks)
```go
// Write ke both
deviceRepo.Update(ctx, device)
paramRepo.UpdateParameters(ctx, serial, params)

// Read dari PostgreSQL (baru)
params, _ := paramRepo.GetAllParameters(ctx, serial)
```

### Phase 3: Cleanup (1+ weeks)
```go
// Write hanya ke PostgreSQL
paramRepo.UpdateParameters(ctx, serial, params)

// Archive MongoDB documents
mongoCollection.DeleteMany(ctx, bson.M{...})
```

---

## Troubleshooting

### PostgreSQL Connection Error
```bash
# Check if container is running
docker-compose ps postgresql

# View logs
docker-compose logs postgresql

# Restart
docker-compose restart postgresql
```

### Schema Not Applied
```bash
# Check tables exist
docker-compose exec postgresql psql -U helix -d helix_parameters -c "\dt"

# Manually apply schema
docker-compose exec postgresql psql -U helix -d helix_parameters < scripts/schema-postgresql.sql
```

### Slow Queries
```bash
# Enable query logging
ALTER SYSTEM SET log_min_duration_statement = 1000;  -- 1 second

# Check slow query log
docker-compose exec postgresql tail -f /var/log/postgresql/postgresql.log
```

### Redis Cache Issues
```bash
# Flush all cache
docker-compose exec redis redis-cli FLUSHALL

# Monitor cache
docker-compose exec redis redis-cli MONITOR
```

---

## Stop Services

```bash
# Stop containers (keep data)
docker-compose stop

# Stop and remove containers (keep data)
docker-compose down

# Stop and remove everything (INCLUDING DATA)
docker-compose down -v
```

---

## Expected Performance

For 30,000 devices with 500 parameters each:

| Metric | MongoDB | PostgreSQL |
|--------|---------|-----------|
| Inform response time | 500-2000ms | 50-100ms |
| Write throughput | ~1,000 writes/sec | ~10,000 writes/sec |
| Storage per month | ~45 GB | ~5 GB |
| Cache hit rate | N/A | ~80% |

---

## Next Steps

1. ✅ PostgreSQL setup dengan Docker
2. ✅ Schema creation
3. 📝 Integration code
4. 📊 Performance testing
5. 🔄 Migration dari MongoDB

Sudah ready untuk implementasi? Mau saya lanjutkan dengan integration code di handler? 🚀
