package server

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/ohchanwu/jobcron/internal/storage"
)

// SchedulerConfig contains the dependencies for the daily scrape scheduler.
type SchedulerConfig struct {
	Server          *Server
	DailyScrapeTime string

	// Now and Sleep are injectable so tests can drive the loop without waiting
	// for wall-clock time. Production callers leave them nil.
	Now   func() time.Time
	Sleep func(context.Context, time.Duration) error
}

// StartScheduler starts the daily scheduled scrape loop.
func StartScheduler(ctx context.Context, cfg SchedulerConfig) error {
	if cfg.Server == nil {
		return fmt.Errorf("scheduler: server is required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}
	if _, err := nextScheduledRun(cfg.Now(), cfg.DailyScrapeTime); err != nil {
		return err
	}

	go func() {
		for {
			now := cfg.Now()
			next, err := nextScheduledRun(now, cfg.DailyScrapeTime)
			if err != nil {
				return
			}
			delay := next.Sub(now.In(kstLocation()))
			if delay < 0 {
				delay = 0
			}
			if err := cfg.Sleep(ctx, delay); err != nil {
				return
			}
			cfg.Server.runScheduledScrape(ctx)
		}
	}()
	return nil
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (s *Server) runScheduledScrape(ctx context.Context) {
	// Serialize the complete cohort/runtime snapshot with profile saves and all
	// scrape/rerate work. Resolving before this lock could pair an old credential
	// and budget configuration with a profile committed while waiting.
	lease := s.flight.tryAcquire(scrapeAllKey)
	if lease == nil {
		s.recordSkippedScheduledRun(ctx, "skipped: scrape already running")
		return
	}
	defer lease.release()
	scrapeCtx, cancel := context.WithTimeout(ctx, scrapeMaxDuration)
	defer cancel()
	_, _ = s.runScheduledScrapeWithHistory(scrapeCtx)
}

func (s *Server) runScheduledScrapeWithHistory(
	ctx context.Context,
) (result ScrapeResult, err error) {
	run, err := s.store.StartScrapeRun(ctx, storage.ScrapeTriggerScheduled)
	if err != nil {
		return ScrapeResult{}, err
	}
	status := storage.ScrapeRunStatusSuccess
	summary := ""
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("server: scrape panic: %v", recovered)
		}
		if err != nil {
			status = storage.ScrapeRunStatusFailure
			summary = err.Error()
		}
		finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		finishErr := s.store.FinishScrapeRun(
			finishCtx, run.ID, result, status, truncateScrapeRunError(summary),
		)
		if finishErr != nil && err == nil {
			err = finishErr
		}
	}()

	collection, err := s.collectPostings(ctx, noopSchedulerEmit, func(string) bool { return true })
	result = collection.result
	if err != nil {
		return result, err
	}
	if s.store.Dialect() == storage.DialectSQLite {
		prof, ok, profileErr := s.loadProfile(ctx, 0)
		if profileErr != nil || !ok {
			return result, profileErr
		}
		result.Scored, err = s.analyzeUserCollection(
			ctx, storage.ScrapeTriggerScheduled, noopSchedulerEmit,
			0, prof, nil, nil, collection,
		)
		return result, err
	}

	userIDs, err := s.store.UserIDs(ctx)
	if err != nil {
		return result, err
	}
	warnings := 0
	for _, userID := range userIDs {
		prof, ok, profileErr := s.loadProfile(ctx, userID)
		if profileErr != nil {
			warnings++
			continue
		}
		if !ok {
			continue
		}
		runtime, runtimeErr := s.aiRuntimeForUser(ctx, userID)
		if runtimeErr != nil {
			log.Printf(
				"jobcron: user %d scheduled AI runtime unavailable; using rule scoring",
				userID,
			)
			warnings++
			runtime = nil
		}
		budget := s.newAIBudget(ctx, userID, runtime)
		scored, analysisErr := s.analyzeUserCollection(
			ctx, storage.ScrapeTriggerScheduled, noopSchedulerEmit,
			userID, prof, runtime, budget, collection,
		)
		result.Scored += scored
		if analysisErr != nil {
			warnings++
			continue
		}
	}
	if warnings > 0 {
		summary = fmt.Sprintf("%d user analysis warning(s)", warnings)
	}
	return result, nil
}

func (s *Server) recordSkippedScheduledRun(ctx context.Context, reason string) {
	s.recordSkippedScheduledRunAfterStart(ctx, reason, nil)
}

func (s *Server) recordSkippedScheduledRunAfterStart(ctx context.Context, reason string, afterStart func()) {
	run, err := s.store.StartScrapeRun(ctx, storage.ScrapeTriggerScheduled)
	if err != nil {
		return
	}
	if afterStart != nil {
		afterStart()
	}
	finishCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = s.store.FinishScrapeRun(finishCtx, run.ID, storage.ScrapeResult{}, storage.ScrapeRunStatusFailure, reason)
}

func noopSchedulerEmit(event, data string) {}

func kstLocation() *time.Location {
	return time.FixedZone("KST", 9*60*60)
}

func nextScheduledRun(now time.Time, dailyTime string) (time.Time, error) {
	hour, minute, err := parseDailyScrapeTime(dailyTime)
	if err != nil {
		return time.Time{}, err
	}
	loc := kstLocation()
	kstNow := now.In(loc)
	next := time.Date(kstNow.Year(), kstNow.Month(), kstNow.Day(), hour, minute, 0, 0, loc)
	if !next.After(kstNow) {
		next = next.Add(24 * time.Hour)
	}
	return next, nil
}

func parseDailyScrapeTime(s string) (int, int, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("daily scrape time %q must use HH:MM", s)
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("daily scrape time %q must use HH:MM", s)
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("daily scrape time %q must use HH:MM", s)
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, 0, fmt.Errorf("daily scrape time %q must use HH:MM", s)
	}
	return hour, minute, nil
}
