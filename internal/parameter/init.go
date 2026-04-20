package parameter

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/raykavin/helix-acs/internal/config"
	"github.com/raykavin/helix-acs/internal/logger"
)

// InitializePostgreSQL initializes PostgreSQL connection and repository
func InitializePostgreSQL(ctx context.Context, cfg config.PostgreSQL, log logger.Logger) (*PostgreSQLRepository, error) {
	// Build connection string
	connStr := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=disable&application_name=helix-acs",
		cfg.User,
		cfg.Password,
		cfg.Host,
		cfg.Port,
		cfg.Database,
	)

	// Connect to database
	db, err := sqlx.Connect("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("connect postgresql: %w", err)
	}

	// Configure connection pool
	if cfg.MaxConnections > 0 {
		db.SetMaxOpenConns(cfg.MaxConnections)
		db.SetMaxIdleConns(cfg.MaxConnections / 2)
	}
	if cfg.ConnectionMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnectionMaxLifetime)
	}

	// Test connection
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(testCtx); err != nil {
		return nil, fmt.Errorf("ping postgresql: %w", err)
	}

	log.
		WithField("host", cfg.Host).
		WithField("port", cfg.Port).
		WithField("database", cfg.Database).
		Info("PostgreSQL connected successfully")

	// Initialize Redis cache
	redisClient := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", cfg.CacheHost, cfg.CachePort),
		Password: "",
		DB:       0,
	})

	cache := NewRedisCache(redisClient)

	// Create repository
	repo := NewPostgreSQLRepository(db, cache, log)

	// Health check
	if err := repo.HealthCheck(testCtx); err != nil {
		return nil, fmt.Errorf("health check failed: %w", err)
	}

	log.Info("parameter repository initialized")

	return repo, nil
}
