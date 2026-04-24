package parameter

import (
	"context"
	"time"

	"github.com/raykavin/helix-acs/internal/logger"
)

// Scheduler untuk parameter snapshots
type Scheduler struct {
	repo Repository
	log  logger.Logger
}

// NewScheduler membuat parameter scheduler
func NewScheduler(repo Repository, log logger.Logger) *Scheduler {
	return &Scheduler{
		repo: repo,
		log:  log,
	}
}

// StartDailySnapshot menjalankan scheduler untuk snapshot harian
func (s *Scheduler) StartDailySnapshot(ctx context.Context, scheduleTime string) {
	// Parse schedule time (format: "03:00")
	scheduledTime, err := time.Parse("15:04", scheduleTime)
	if err != nil {
		s.log.WithError(err).Error("invalid schedule time format (use HH:mm)")
		return
	}

	s.log.
		WithField("schedule_time", scheduleTime).
		Info("parameter daily snapshot scheduler started")

	// Calculate initial delay sampai scheduled time
	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(),
		scheduledTime.Hour(), scheduledTime.Minute(), 0, 0, now.Location())

	var nextRun time.Time
	if now.After(today) {
		// Jika waktu sudah lewat hari ini, schedule untuk besok
		nextRun = today.Add(24 * time.Hour)
	} else {
		nextRun = today
	}

	ticker := time.NewTimer(time.Until(nextRun))
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			s.log.Info("parameter scheduler stopped")
			return

		case <-ticker.C:
			s.log.Info("running daily parameter snapshot")
			s.runSnapshot(ctx)

			// Schedule untuk 24 jam kemudian
			ticker.Reset(24 * time.Hour)
		}
	}
}

// StartHourlyCleanup menjalankan cleanup old history setiap jam
func (s *Scheduler) StartHourlyCleanup(ctx context.Context, retentionDays int) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	s.log.
		WithField("retention_days", retentionDays).
		Info("parameter history cleanup scheduler started")

	for {
		select {
		case <-ctx.Done():
			s.log.Info("cleanup scheduler stopped")
			return

		case <-ticker.C:
			s.log.Debug("running parameter history cleanup")
			if err := s.repo.DeleteOldHistory(ctx, retentionDays); err != nil {
				s.log.WithError(err).Error("cleanup failed")
			}
			if err := s.repo.DeleteOldTrafficSamples(ctx, retentionDays); err != nil {
				s.log.WithError(err).Error("wan traffic samples cleanup failed")
			}
		}
	}
}

// runSnapshot executes the snapshot capture
func (s *Scheduler) runSnapshot(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	startTime := time.Now()

	if err := s.repo.CaptureAllDeviceSnapshots(ctx); err != nil {
		s.log.WithError(err).
			WithField("duration_ms", time.Since(startTime).Milliseconds()).
			Error("daily snapshot failed")
		return
	}

	s.log.
		WithField("duration_ms", time.Since(startTime).Milliseconds()).
		Info("daily snapshot completed successfully")
}
