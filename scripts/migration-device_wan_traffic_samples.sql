-- Run once on existing PostgreSQL databases (new installs get this from schema-postgresql.sql).

CREATE TABLE IF NOT EXISTS device_wan_traffic_samples (
    id BIGSERIAL PRIMARY KEY,
    device_serial VARCHAR(64) NOT NULL,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    bytes_sent BIGINT NOT NULL DEFAULT 0,
    bytes_received BIGINT NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_wan_traffic_serial_time ON device_wan_traffic_samples (device_serial, recorded_at DESC);
