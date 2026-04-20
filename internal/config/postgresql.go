package config

import "time"

// PostgreSQL represents PostgreSQL-specific configuration
type PostgreSQL struct {
	Host                  string        `mapstructure:"host"`
	Port                  int           `mapstructure:"port"`
	User                  string        `mapstructure:"user"`
	Password              string        `mapstructure:"password"`
	Database              string        `mapstructure:"database"`
	MaxConnections        int           `mapstructure:"max_connections"`
	ConnectionTimeoutMs   int           `mapstructure:"connection_timeout_ms"`
	ConnectionMaxLifetime time.Duration `mapstructure:"connection_max_lifetime_seconds"`
	CacheHost             string        `mapstructure:"cache_host"`
	CachePort             int           `mapstructure:"cache_port"`
}

// Parameters represents parameter storage configuration
type Parameters struct {
	Backend         string `mapstructure:"backend"` // "postgresql" or "mongodb"
	CacheEnabled    bool   `mapstructure:"cache_enabled"`
	CacheTTLMinutes int    `mapstructure:"cache_ttl_minutes"`
	DailySnapshot   DailySnapshot
	History         HistoryConfig
	BatchSize       int `mapstructure:"batch_size"`
}

// DailySnapshot represents daily snapshot scheduler configuration
type DailySnapshot struct {
	Enabled bool   `mapstructure:"enabled"`
	Time    string `mapstructure:"time"` // Format: "03:00" (UTC)
}

// HistoryConfig represents parameter history configuration
type HistoryConfig struct {
	Enabled       bool `mapstructure:"enabled"`
	RetentionDays int  `mapstructure:"retention_days"`
}

// ApplicationConfigExtended extends ApplicationConfigProvider with PostgreSQL settings
type ApplicationConfigExtended interface {
	ApplicationConfigProvider
	GetPostgreSQL() PostgreSQL
	GetParameters() Parameters
}
