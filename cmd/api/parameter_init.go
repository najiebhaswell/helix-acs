package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"

	"github.com/raykavin/helix-acs/internal/config"
	"github.com/raykavin/helix-acs/internal/logger"
	"github.com/raykavin/helix-acs/internal/parameter"
)

// initParameterRepository initializes PostgreSQL connection and parameter repository
func initParameterRepository(
	ctx context.Context,
	cfg config.PostgreSQL,
	redisClient *redis.Client,
	appLogger logger.Logger,
) (parameter.Repository, error) {
	// Build PostgreSQL connection string
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
		return nil, fmt.Errorf("connect to postgresql: %w", err)
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

	appLogger.
		WithField("host", cfg.Host).
		WithField("database", cfg.Database).
		Info("PostgreSQL connection established")

	// Initialize Redis cache
	cache := parameter.NewRedisCache(redisClient)

	// Create parameter repository
	paramRepo := parameter.NewPostgreSQLRepository(db, cache, appLogger)

	// Health check
	if err := paramRepo.HealthCheck(testCtx); err != nil {
		return nil, fmt.Errorf("parameter repository health check failed: %w", err)
	}

	appLogger.Info("Parameter repository initialized successfully")

	return paramRepo, nil
}

// closeParameterRepository closes PostgreSQL connection gracefully
func closeParameterRepository(repo parameter.Repository, appLogger logger.Logger) {
	if err := repo.Close(); err != nil {
		appLogger.WithError(err).Error("error closing parameter repository")
	}
}

// initParameterScheduler starts the daily snapshot and cleanup schedulers
func initParameterScheduler(
	ctx context.Context,
	paramRepo parameter.Repository,
	paramCfg config.Parameters,
	appLogger logger.Logger,
) {
	scheduler := parameter.NewScheduler(paramRepo, appLogger)

	// Start daily snapshot scheduler if enabled
	if paramCfg.DailySnapshot.Enabled {
		go scheduler.StartDailySnapshot(ctx, paramCfg.DailySnapshot.Time)
		appLogger.
			WithField("time", paramCfg.DailySnapshot.Time).
			Info("Parameter daily snapshot scheduler started")
	}

	// Start history cleanup scheduler if enabled
	if paramCfg.History.Enabled {
		go scheduler.StartHourlyCleanup(ctx, paramCfg.History.RetentionDays)
		appLogger.
			WithField("retention_days", paramCfg.History.RetentionDays).
			Info("Parameter history cleanup scheduler started")
	}
}
