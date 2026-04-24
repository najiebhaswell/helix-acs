package parameter

import (
	"context"
	"time"
)

// Repository interface mendefinisikan operasi parameter storage
type Repository interface {
	// Basic operations
	UpdateParameters(ctx context.Context, serial string, params map[string]string) error
	GetParameter(ctx context.Context, serial string, paramName string) (string, error)
	GetAllParameters(ctx context.Context, serial string) (map[string]string, error)
	GetParametersByPrefix(ctx context.Context, serial string, prefix string) (map[string]string, error)

	// Snapshot operations
	SaveSnapshot(ctx context.Context, serial string, snapType string, params map[string]string) error
	GetSnapshot(ctx context.Context, serial string, snapType string) (map[string]string, error)
	GetLatestSnapshot(ctx context.Context, serial string) (snapType string, params map[string]string, err error)

	// History operations
	RecordParameterChange(ctx context.Context, serial string, paramName string, oldValue string, newValue string, changedBy string) error
	GetParameterHistory(ctx context.Context, serial string, paramName string, limit int) ([]map[string]interface{}, error)

	// Stats operations
	CountParameters(ctx context.Context, serial string) (int, error)
	GetDeviceParameterStats(ctx context.Context, serial string) (map[string]interface{}, error)

	// Batch operations
	DeleteDeviceParameters(ctx context.Context, serial string) error
	DeleteSnapshot(ctx context.Context, serial string, snapType string) error
	DeleteOldHistory(ctx context.Context, daysOld int) error
	CaptureAllDeviceSnapshots(ctx context.Context) error

	// WAN traffic samples (cumulative counters for rate graphs)
	RecordWANTrafficSample(ctx context.Context, serial string, recordedAt time.Time, bytesSent, bytesReceived int64) error
	ListWANTrafficSamples(ctx context.Context, serial string, since time.Time, limit int) ([]TrafficSample, error)
	DeleteOldTrafficSamples(ctx context.Context, daysOld int) error

	// Connection
	HealthCheck(ctx context.Context) error
	Close() error
}

// CacheRepository interface untuk Redis cache
type CacheRepository interface {
	Get(ctx context.Context, key string) (map[string]string, error)
	Set(ctx context.Context, key string, value map[string]string, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
	Flush(ctx context.Context) error
}
