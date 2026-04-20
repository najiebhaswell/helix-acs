-- scripts/schema-postgresql.sql
-- PostgreSQL Schema untuk Helix ACS Parameter Storage

-- ============================================================
-- 1. Main Parameters Table
-- ============================================================
CREATE TABLE IF NOT EXISTS device_parameters (
    id BIGSERIAL PRIMARY KEY,
    device_serial VARCHAR(64) NOT NULL,
    param_name VARCHAR(512) NOT NULL,
    param_value TEXT,
    data_type VARCHAR(32) DEFAULT 'string',  -- string, int, boolean, datetime
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uk_device_param UNIQUE(device_serial, param_name)
);

CREATE INDEX IF NOT EXISTS idx_params_device_serial ON device_parameters(device_serial);
CREATE INDEX IF NOT EXISTS idx_params_updated_at ON device_parameters(updated_at);
CREATE INDEX IF NOT EXISTS idx_params_param_name ON device_parameters(param_name);

-- ============================================================
-- 2. Parameter Snapshots Table (untuk restore)
-- ============================================================
CREATE TABLE IF NOT EXISTS device_parameter_snapshots (
    id BIGSERIAL PRIMARY KEY,
    device_serial VARCHAR(64) NOT NULL,
    snapshot_type VARCHAR(32) NOT NULL,  -- "last_known_good", "pre_reset"
    parameters JSONB NOT NULL,  -- Semua parameters dalam satu JSON
    captured_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT uk_device_snapshot UNIQUE(device_serial, snapshot_type)
);

CREATE INDEX IF NOT EXISTS idx_snapshots_device_serial ON device_parameter_snapshots(device_serial);
CREATE INDEX IF NOT EXISTS idx_snapshots_type ON device_parameter_snapshots(snapshot_type);
CREATE INDEX IF NOT EXISTS idx_snapshots_captured_at ON device_parameter_snapshots(captured_at);

-- ============================================================
-- 3. Parameter History Table (audit trail)
-- ============================================================
CREATE TABLE IF NOT EXISTS device_parameter_history (
    id BIGSERIAL PRIMARY KEY,
    device_serial VARCHAR(64) NOT NULL,
    param_name VARCHAR(512) NOT NULL,
    old_value TEXT,
    new_value TEXT,
    changed_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    changed_by VARCHAR(64) DEFAULT 'inform'  -- "inform", "task", "admin", "restore"
);

CREATE INDEX IF NOT EXISTS idx_history_device_serial ON device_parameter_history(device_serial);
CREATE INDEX IF NOT EXISTS idx_history_changed_at ON device_parameter_history(changed_at);
CREATE INDEX IF NOT EXISTS idx_history_param_name ON device_parameter_history(param_name);

-- ============================================================
-- 4. Device Parameter Metadata
-- ============================================================
CREATE TABLE IF NOT EXISTS device_parameter_metadata (
    device_serial VARCHAR(64) PRIMARY KEY,
    total_parameters INT DEFAULT 0,
    last_updated TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_informed TIMESTAMP,
    last_snapshot_time TIMESTAMP,
    total_snapshot_count INT DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_metadata_last_updated ON device_parameter_metadata(last_updated);

-- ============================================================
-- 5. Create Helper Functions
-- ============================================================

-- Function untuk batch update parameters
CREATE OR REPLACE FUNCTION upsert_device_parameters(
    p_serial VARCHAR(64),
    p_params JSON
) RETURNS TABLE(updated_count INT) AS $$
DECLARE
    v_param_count INT := 0;
    v_key TEXT;
    v_value TEXT;
BEGIN
    -- Iterate JSON objects
    FOR v_key, v_value IN SELECT key, value FROM json_each_text(p_params) LOOP
        INSERT INTO device_parameters (device_serial, param_name, param_value, updated_at)
        VALUES (p_serial, v_key, v_value, CURRENT_TIMESTAMP)
        ON CONFLICT (device_serial, param_name) DO UPDATE SET
            param_value = EXCLUDED.param_value,
            updated_at = CURRENT_TIMESTAMP;
        v_param_count := v_param_count + 1;
    END LOOP;
    
    -- Update metadata
    INSERT INTO device_parameter_metadata (device_serial, total_parameters, last_updated)
    VALUES (p_serial, v_param_count, CURRENT_TIMESTAMP)
    ON CONFLICT (device_serial) DO UPDATE SET
        total_parameters = v_param_count,
        last_updated = CURRENT_TIMESTAMP;
    
    RETURN QUERY SELECT v_param_count;
END;
$$ LANGUAGE plpgsql;

-- Function untuk save snapshot
CREATE OR REPLACE FUNCTION save_parameter_snapshot(
    p_serial VARCHAR(64),
    p_snap_type VARCHAR(32),
    p_params JSONB
) RETURNS VOID AS $$
BEGIN
    INSERT INTO device_parameter_snapshots (device_serial, snapshot_type, parameters, captured_at)
    VALUES (p_serial, p_snap_type, p_params, CURRENT_TIMESTAMP)
    ON CONFLICT (device_serial, snapshot_type) DO UPDATE SET
        parameters = p_params,
        captured_at = CURRENT_TIMESTAMP;
    
    -- Update metadata
    UPDATE device_parameter_metadata
    SET last_snapshot_time = CURRENT_TIMESTAMP,
        total_snapshot_count = total_snapshot_count + 1
    WHERE device_serial = p_serial;
END;
$$ LANGUAGE plpgsql;

-- Function untuk get all parameters as JSON
CREATE OR REPLACE FUNCTION get_device_parameters_json(p_serial VARCHAR(64))
RETURNS JSONB AS $$
    SELECT jsonb_object_agg(param_name, param_value)
    FROM device_parameters
    WHERE device_serial = p_serial;
$$ LANGUAGE SQL;

-- ============================================================
-- 6. Views untuk kemudahan query
-- ============================================================

-- View untuk parameter summary per device
CREATE OR REPLACE VIEW v_device_parameter_summary AS
SELECT 
    dp.device_serial,
    COUNT(*) as parameter_count,
    MAX(dp.updated_at) as last_updated,
    dpm.last_snapshot_time,
    dpm.total_snapshot_count
FROM device_parameters dp
LEFT JOIN device_parameter_metadata dpm ON dp.device_serial = dpm.device_serial
GROUP BY dp.device_serial, dpm.last_snapshot_time, dpm.total_snapshot_count;

-- View untuk parameter yang sering berubah
CREATE OR REPLACE VIEW v_parameter_change_frequency AS
SELECT 
    param_name,
    device_serial,
    COUNT(*) as change_count,
    MAX(changed_at) as last_changed
FROM device_parameter_history
WHERE changed_at > CURRENT_TIMESTAMP - INTERVAL '7 days'
GROUP BY param_name, device_serial
ORDER BY change_count DESC;

-- ============================================================
-- 7. Stored Procedures untuk Daily Snapshots
-- ============================================================

-- Procedure untuk capture daily snapshot untuk semua devices
CREATE OR REPLACE PROCEDURE daily_snapshot_all_devices()
LANGUAGE plpgsql
AS $$
DECLARE
    v_device RECORD;
    v_params JSONB;
BEGIN
    -- Iterate semua devices yang punya parameters
    FOR v_device IN SELECT DISTINCT device_serial FROM device_parameters LOOP
        -- Get current parameters as JSON
        SELECT get_device_parameters_json(v_device.device_serial) INTO v_params;
        
        IF v_params IS NOT NULL THEN
            -- Save sebagai last_known_good snapshot
            PERFORM save_parameter_snapshot(
                v_device.device_serial,
                'last_known_good',
                v_params
            );
        END IF;
    END LOOP;
    
    RAISE NOTICE 'Daily snapshot completed';
END;
$$;

-- ============================================================
-- 8. Create Indexes untuk Performance
-- ============================================================

-- Composite index untuk common queries
CREATE INDEX IF NOT EXISTS idx_params_serial_name ON device_parameters(device_serial, param_name);
CREATE INDEX IF NOT EXISTS idx_params_serial_updated ON device_parameters(device_serial, updated_at DESC);

-- Index untuk recent changes
CREATE INDEX IF NOT EXISTS idx_params_recent ON device_parameters(device_serial, updated_at DESC);

-- Index untuk history
CREATE INDEX IF NOT EXISTS idx_history_recent ON device_parameter_history(device_serial, changed_at DESC);

-- ============================================================
-- 9. Enable Extensions
-- ============================================================

CREATE EXTENSION IF NOT EXISTS pg_trgm;  -- For text search
CREATE EXTENSION IF NOT EXISTS btree_gin;  -- For multi-column indexes
