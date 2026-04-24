package parameter

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/raykavin/helix-acs/internal/logger"
)

// PostgreSQL implementation dari Parameter Repository
type PostgreSQLRepository struct {
	db    *sqlx.DB
	cache Cache
	log   logger.Logger
}

// Cache interface untuk Redis
type Cache interface {
	Get(ctx context.Context, key string) (map[string]string, error)
	Set(ctx context.Context, key string, value map[string]string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}

// New membuat PostgreSQL repository
func NewPostgreSQLRepository(db *sqlx.DB, cache Cache, log logger.Logger) *PostgreSQLRepository {
	return &PostgreSQLRepository{
		db:    db,
		cache: cache,
		log:   log,
	}
}

// ============================================================
// Parameter Management
// ============================================================

// UpdateParameters melakukan batch update semua parameters
func (r *PostgreSQLRepository) UpdateParameters(
	ctx context.Context,
	serial string,
	params map[string]string,
) error {
	if len(params) == 0 {
		return nil
	}

	// Convert ke JSON
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal params: %w", err)
	}

	// Use stored procedure untuk efficient batch insert/update
	var count int
	err = r.db.QueryRowContext(
		ctx,
		"SELECT upsert_device_parameters($1, $2)",
		serial,
		string(paramsJSON),
	).Scan(&count)
	if err != nil {
		return fmt.Errorf("upsert parameters: %w", err)
	}

	r.log.
		WithField("serial", serial).
		WithField("param_count", count).
		Debug("parameters updated")

	// Invalidate cache
	_ = r.cache.Delete(ctx, "params:"+serial)

	return nil
}

// GetParameter mengambil satu parameter by name
func (r *PostgreSQLRepository) GetParameter(
	ctx context.Context,
	serial string,
	paramName string,
) (string, error) {
	var value sql.NullString

	err := r.db.QueryRowContext(
		ctx,
		"SELECT param_value FROM device_parameters WHERE device_serial = $1 AND param_name = $2",
		serial,
		paramName,
	).Scan(&value)

	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("query parameter: %w", err)
	}

	return value.String, nil
}

// GetAllParameters mengambil semua parameters untuk device
// Try cache dulu, jika miss query database
func (r *PostgreSQLRepository) GetAllParameters(
	ctx context.Context,
	serial string,
) (map[string]string, error) {
	cacheKey := "params:" + serial

	// Try cache dulu
	if cached, err := r.cache.Get(ctx, cacheKey); err == nil && cached != nil {
		r.log.WithField("serial", serial).Debug("parameters from cache")
		return cached, nil
	}

	// Cache miss - query database
	rows, err := r.db.QueryContext(
		ctx,
		"SELECT param_name, param_value FROM device_parameters WHERE device_serial = $1 ORDER BY param_name",
		serial,
	)
	if err != nil {
		return nil, fmt.Errorf("query parameters: %w", err)
	}
	defer rows.Close()

	params := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		params[name] = value
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows error: %w", err)
	}

	// Cache untuk 1 jam
	_ = r.cache.Set(ctx, cacheKey, params, 1*time.Hour)

	r.log.
		WithField("serial", serial).
		WithField("param_count", len(params)).
		Debug("parameters from database")

	return params, nil
}

// GetParametersByPrefix mengambil parameters yang match prefix
// Contoh: "Device.WiFi." akan ambil semua WiFi parameters
func (r *PostgreSQLRepository) GetParametersByPrefix(
	ctx context.Context,
	serial string,
	prefix string,
) (map[string]string, error) {
	// In PostgreSQL LIKE, underscore (_) is a single-char wildcard.
	// Escape it so "_helix.provision." matches literally and not any character.
	escapedPrefix := strings.ReplaceAll(prefix, "_", "\\_")
	rows, err := r.db.QueryContext(
		ctx,
		"SELECT param_name, param_value FROM device_parameters WHERE device_serial = $1 AND param_name LIKE $2 ESCAPE '\\' ORDER BY param_name",
		serial,
		escapedPrefix+"%",
	)
	if err != nil {
		return nil, fmt.Errorf("query by prefix: %w", err)
	}
	defer rows.Close()

	params := make(map[string]string)
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		params[name] = value
	}

	return params, rows.Err()
}

// ============================================================
// Snapshots Management
// ============================================================

// SaveSnapshot menyimpan parameter snapshot
func (r *PostgreSQLRepository) SaveSnapshot(
	ctx context.Context,
	serial string,
	snapType string,
	params map[string]string,
) error {
	paramsJSON, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}

	// Use stored procedure
	_, err = r.db.ExecContext(
		ctx,
		"SELECT save_parameter_snapshot($1, $2, $3)",
		serial,
		snapType,
		paramsJSON,
	)
	if err != nil {
		return fmt.Errorf("save snapshot: %w", err)
	}

	r.log.
		WithField("serial", serial).
		WithField("snap_type", snapType).
		WithField("param_count", len(params)).
		Info("parameter snapshot saved")

	return nil
}

// GetSnapshot mengambil parameter snapshot
func (r *PostgreSQLRepository) GetSnapshot(
	ctx context.Context,
	serial string,
	snapType string,
) (map[string]string, error) {
	var paramsJSON []byte

	err := r.db.QueryRowContext(
		ctx,
		"SELECT parameters FROM device_parameter_snapshots WHERE device_serial = $1 AND snapshot_type = $2",
		serial,
		snapType,
	).Scan(&paramsJSON)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query snapshot: %w", err)
	}

	var params map[string]string
	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	return params, nil
}

// GetLatestSnapshot mengambil snapshot terbaru dari tipe apapun
func (r *PostgreSQLRepository) GetLatestSnapshot(
	ctx context.Context,
	serial string,
) (snapType string, params map[string]string, err error) {
	var paramsJSON []byte

	err = r.db.QueryRowContext(
		ctx,
		"SELECT snapshot_type, parameters FROM device_parameter_snapshots WHERE device_serial = $1 ORDER BY captured_at DESC LIMIT 1",
		serial,
	).Scan(&snapType, &paramsJSON)

	if err == sql.ErrNoRows {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("query latest snapshot: %w", err)
	}

	if err := json.Unmarshal(paramsJSON, &params); err != nil {
		return "", nil, fmt.Errorf("unmarshal snapshot: %w", err)
	}

	return snapType, params, nil
}

// ============================================================
// History Management
// ============================================================

// RecordParameterChange mencatat perubahan parameter untuk audit
func (r *PostgreSQLRepository) RecordParameterChange(
	ctx context.Context,
	serial string,
	paramName string,
	oldValue string,
	newValue string,
	changedBy string,
) error {
	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO device_parameter_history 
		(device_serial, param_name, old_value, new_value, changed_by, changed_at)
		VALUES ($1, $2, $3, $4, $5, CURRENT_TIMESTAMP)`,
		serial,
		paramName,
		oldValue,
		newValue,
		changedBy,
	)
	if err != nil {
		return fmt.Errorf("record change: %w", err)
	}

	return nil
}

// GetParameterHistory mengambil history perubahan parameter
func (r *PostgreSQLRepository) GetParameterHistory(
	ctx context.Context,
	serial string,
	paramName string,
	limit int,
) ([]map[string]interface{}, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT param_name, old_value, new_value, changed_at, changed_by
		 FROM device_parameter_history 
		 WHERE device_serial = $1 AND param_name = $2
		 ORDER BY changed_at DESC
		 LIMIT $3`,
		serial,
		paramName,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query history: %w", err)
	}
	defer rows.Close()

	var history []map[string]interface{}
	for rows.Next() {
		var paramName, oldValue, newValue, changedBy string
		var changedAt time.Time

		if err := rows.Scan(&paramName, &oldValue, &newValue, &changedAt, &changedBy); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}

		history = append(history, map[string]interface{}{
			"param_name": paramName,
			"old_value":  oldValue,
			"new_value":  newValue,
			"changed_at": changedAt,
			"changed_by": changedBy,
		})
	}

	return history, rows.Err()
}

// ============================================================
// Statistics & Metadata
// ============================================================

// CountParameters returns the number of stored parameters for a device.
// Uses a fast COUNT(*) query — suitable for calling on every Inform.
func (r *PostgreSQLRepository) CountParameters(ctx context.Context, serial string) (int, error) {
	var count int
	err := r.db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM device_parameters WHERE device_serial = $1",
		serial,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count parameters: %w", err)
	}
	return count, nil
}

// GetDeviceParameterStats mengambil statistik parameters untuk device
func (r *PostgreSQLRepository) GetDeviceParameterStats(
	ctx context.Context,
	serial string,
) (map[string]interface{}, error) {
	var totalParams int
	var lastUpdated sql.NullTime
	var lastSnapshot sql.NullTime

	err := r.db.QueryRowContext(
		ctx,
		`SELECT 
			COALESCE(COUNT(*), 0) as total_params,
			MAX(dp.updated_at) as last_updated,
			dpm.last_snapshot_time
		FROM device_parameters dp
		LEFT JOIN device_parameter_metadata dpm ON dp.device_serial = dpm.device_serial
		WHERE dp.device_serial = $1
		GROUP BY dpm.last_snapshot_time`,
		serial,
	).Scan(&totalParams, &lastUpdated, &lastSnapshot)

	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("query stats: %w", err)
	}

	return map[string]interface{}{
		"total_parameters":   totalParams,
		"last_updated":       lastUpdated.Time,
		"last_snapshot_time": lastSnapshot.Time,
	}, nil
}

// ============================================================
// Batch Operations
// ============================================================

// DeleteDeviceParameters menghapus semua parameters untuk device
func (r *PostgreSQLRepository) DeleteDeviceParameters(
	ctx context.Context,
	serial string,
) error {
	_, err := r.db.ExecContext(
		ctx,
		"DELETE FROM device_parameters WHERE device_serial = $1",
		serial,
	)
	if err != nil {
		return fmt.Errorf("delete parameters: %w", err)
	}

	// Delete from metadata
	_, _ = r.db.ExecContext(
		ctx,
		"DELETE FROM device_parameter_metadata WHERE device_serial = $1",
		serial,
	)

	// Invalidate cache
	_ = r.cache.Delete(ctx, "params:"+serial)

	return nil
}

// DeleteSnapshot menghapus satu parameter snapshot berdasarkan serial dan tipe
func (r *PostgreSQLRepository) DeleteSnapshot(ctx context.Context, serial string, snapType string) error {
	_, err := r.db.ExecContext(
		ctx,
		"DELETE FROM device_parameter_snapshots WHERE device_serial = $1 AND snapshot_type = $2",
		serial,
		snapType,
	)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	r.log.
		WithField("serial", serial).
		WithField("snap_type", snapType).
		Debug("parameter snapshot deleted")
	return nil
}

// DeleteOldHistory menghapus history yang lebih lama dari n days
func (r *PostgreSQLRepository) DeleteOldHistory(
	ctx context.Context,
	daysOld int,
) error {
	_, err := r.db.ExecContext(
		ctx,
		`DELETE FROM device_parameter_history 
		 WHERE changed_at < CURRENT_TIMESTAMP - INTERVAL '1 day' * $1`,
		daysOld,
	)
	if err != nil {
		return fmt.Errorf("delete old history: %w", err)
	}

	return nil
}

// CaptureAllDeviceSnapshots mengambil snapshot untuk semua devices yang online
// Biasanya dipanggil dari scheduler
func (r *PostgreSQLRepository) CaptureAllDeviceSnapshots(ctx context.Context) error {
	// Get semua unique devices
	rows, err := r.db.QueryContext(
		ctx,
		"SELECT DISTINCT device_serial FROM device_parameters",
	)
	if err != nil {
		return fmt.Errorf("query devices: %w", err)
	}
	defer rows.Close()

	var successCount, failCount int

	for rows.Next() {
		var serial string
		if err := rows.Scan(&serial); err != nil {
			failCount++
			continue
		}

		// Get current parameters
		params, err := r.GetAllParameters(ctx, serial)
		if err != nil {
			r.log.WithError(err).
				WithField("serial", serial).
				Error("failed to get parameters for snapshot")
			failCount++
			continue
		}

		// Save as last_known_good
		if err := r.SaveSnapshot(ctx, serial, "last_known_good", params); err != nil {
			r.log.WithError(err).
				WithField("serial", serial).
				Error("failed to save snapshot")
			failCount++
			continue
		}

		successCount++
	}

	r.log.
		WithField("success", successCount).
		WithField("failed", failCount).
		Info("daily snapshots completed")

	return rows.Err()
}

// ============================================================
// WAN traffic samples
// ============================================================

// RecordWANTrafficSample inserts cumulative WAN byte counters for graphing.
func (r *PostgreSQLRepository) RecordWANTrafficSample(
	ctx context.Context,
	serial string,
	recordedAt time.Time,
	bytesSent, bytesReceived int64,
) error {
	if serial == "" {
		return nil
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO device_wan_traffic_samples (device_serial, recorded_at, bytes_sent, bytes_received)
		 VALUES ($1, $2, $3, $4)`,
		serial, recordedAt.UTC(), bytesSent, bytesReceived,
	)
	if err != nil {
		return fmt.Errorf("insert wan traffic sample: %w", err)
	}
	return nil
}

// ListWANTrafficSamples returns samples with recorded_at >= since, oldest first.
func (r *PostgreSQLRepository) ListWANTrafficSamples(
	ctx context.Context,
	serial string,
	since time.Time,
	limit int,
) ([]TrafficSample, error) {
	if limit < 1 {
		limit = 500
	}
	if limit > 5000 {
		limit = 5000
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT recorded_at, bytes_sent, bytes_received
		 FROM device_wan_traffic_samples
		 WHERE device_serial = $1 AND recorded_at >= $2
		 ORDER BY recorded_at ASC
		 LIMIT $3`,
		serial, since.UTC(), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list wan traffic samples: %w", err)
	}
	defer rows.Close()

	var out []TrafficSample
	for rows.Next() {
		var s TrafficSample
		if err := rows.Scan(&s.RecordedAt, &s.BytesSent, &s.BytesReceived); err != nil {
			return nil, fmt.Errorf("scan wan traffic sample: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteOldTrafficSamples removes samples older than daysOld (UTC).
func (r *PostgreSQLRepository) DeleteOldTrafficSamples(ctx context.Context, daysOld int) error {
	if daysOld < 1 {
		daysOld = 30
	}
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM device_wan_traffic_samples WHERE recorded_at < NOW() - $1 * INTERVAL '1 day'`,
		daysOld,
	)
	if err != nil {
		return fmt.Errorf("delete old wan traffic samples: %w", err)
	}
	return nil
}

// ============================================================
// Connection Management
// ============================================================

// HealthCheck memeriksa koneksi ke PostgreSQL
func (r *PostgreSQLRepository) HealthCheck(ctx context.Context) error {
	return r.db.PingContext(ctx)
}

// Close menutup database connection
func (r *PostgreSQLRepository) Close() error {
	return r.db.Close()
}
