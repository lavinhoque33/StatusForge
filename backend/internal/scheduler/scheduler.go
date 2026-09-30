package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/checkwork"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type (
	Clock interface {
		Now() time.Time
		After(time.Duration) <-chan time.Time
	}
	realClock struct{}
)

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

type Persistence interface {
	TickCycle(context.Context, time.Time) (store.TickReport, error)
	SweepOld(context.Context, time.Time) (int, error)
	Claim(context.Context, store.Work, time.Time) (monitor.Monitor, string, error)
	RecordResult(context.Context, monitor.Observation, string) (monitor.Observation, error)
}
type (
	Runner interface {
		Run(context.Context, monitor.Monitor) monitor.Observation
	}
	Scheduler struct {
		Store            Persistence
		Runner           Runner
		Clock            Clock
		Workers          int
		ReminderInterval time.Duration
		LivenessInterval time.Duration
		Logger           *slog.Logger
		// Coverage receives this run's dispatch evidence; nil keeps a private one.
		Coverage *Coverage
	}
)

const (
	// cyclePause separates scheduling passes. Workers do not wait for it: a
	// free worker takes the next eligible candidate at once.
	cyclePause = 2 * time.Second
	// cycleTimeout bounds one pass. Each monitor's share is bounded separately
	// in the store, so a slow monitor fails alone instead of starving the rest.
	cycleTimeout = 15 * time.Second
	// monitorTimeout bounds one monitor's maintenance, reminder, and deadline
	// work within a pass.
	monitorTimeout = 5 * time.Second
	shutdownDrain  = 30 * time.Second
)

// counters aggregate dispatch outcomes between two "scheduler tick" lines.
type counters struct {
	dispatched, completed, expired, notEligible atomic.Int64
}

func (s *Scheduler) Run(ctx context.Context) {
	clock := s.Clock
	if clock == nil {
		clock = realClock{}
	}
	workers := s.Workers
	if workers < 1 {
		workers = 4
	}
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}
	coverage := s.Coverage
	if coverage == nil {
		coverage = NewCoverage(workers, true)
	}
	coverage.start(clock.Now())
	var counts counters
	f := newFeed(func() { counts.expired.Add(1) })
	runner := checkwork.Runner{
		Store:   s.Store,
		Checker: s.Runner,
		Claimed: func(w store.Work, _ monitor.Monitor, now time.Time) {
			counts.dispatched.Add(1)
			if w.Attempts == 0 {
				coverage.record(now, w.DueAt, false)
			}
		},
	}
	process := func(c candidate) {
		w := c.work
		now := clock.Now()
		// In-flight slots finish during the shutdown drain, so they do not
		// inherit the scheduler's context.
		r := runner.RunSlot(context.Background(), w, now)
		claimed := r.Outcome == checkwork.Recorded ||
			(r.Outcome == checkwork.DependencyFailure && r.Stage == "record")
		defer f.finish(w.MonitorID, claimed, now)
		switch {
		case r.Outcome == checkwork.LeaseHeld ||
			(r.Outcome == checkwork.NotEligible && !r.Closed):
			counts.notEligible.Add(1)
		case r.Outcome == checkwork.DependencyFailure && r.Stage == "claim":
			logger.Warn("claim failed", "reason", "dependency_failure")
		case r.Closed:
			// Claim closed the item instead of leasing it: overdue (it
			// expired between hand-off and claim) or cancelled.
			interval := time.Duration(r.Monitor.IntervalSeconds) * time.Second
			if w.State == "pending" && interval > 0 && !now.Before(c.due.Add(interval)) {
				counts.expired.Add(1)
				coverage.record(now, w.DueAt, true)
			} else {
				counts.notEligible.Add(1)
			}
		case r.Outcome == checkwork.DependencyFailure:
			logger.Warn("scheduled result failed", "reason", "dependency_failure")
		case r.Outcome == checkwork.Recorded:
			counts.completed.Add(1)
			checker.Log(logger, r.Observation)
		}
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				c, ok := f.take(ctx, clock)
				if !ok {
					return
				}
				process(c)
			}
		}()
	}
	recoveryDone := false
	lastLiveness := time.Time{}
	lastState := "unknown"
	evaluate := func(now time.Time) {
		snapshot := coverage.Snapshot(now)
		if snapshot.State == lastState {
			return
		}
		attrs := []any{
			"state", snapshot.State,
			"due_checks", snapshot.DueChecks,
			"missed_checks", snapshot.MissedChecks,
			"window_minutes", snapshot.WindowMinutes,
			"workers", snapshot.Workers,
		}
		if snapshot.State == "behind" {
			logger.Warn("scheduler coverage degraded", attrs...)
		} else if lastState == "behind" {
			logger.Info("scheduler coverage recovered", attrs...)
		}
		lastState = snapshot.State
	}
	tick := func() {
		started := time.Now()
		now := clock.Now()
		defer evaluate(now)
		tickCtx, cancel := context.WithTimeout(ctx, cycleTimeout)
		defer cancel()
		if writer, ok := s.Store.(interface {
			WriteLiveness(context.Context, time.Time, time.Duration) (heartbeat.Liveness, error)
		}); ok {
			interval := s.LivenessInterval
			if interval <= 0 {
				interval = 10 * time.Second
			}
			if lastLiveness.IsZero() || now.Sub(lastLiveness) >= interval {
				if _, e := writer.WriteLiveness(tickCtx, now, interval); e != nil {
					logger.Warn("receive liveness write failed", "reason", "dependency_failure")
				} else {
					lastLiveness = now
				}
			}
		}
		sweepGaps := 0
		if !recoveryDone {
			var sweepErr error
			sweepGaps, sweepErr = s.Store.SweepOld(tickCtx, now)
			if sweepErr != nil {
				logger.Warn("scheduler recovery failed", "reason", "dependency_failure")
			} else {
				recoveryDone = true
			}
		}
		report, err := s.Store.TickCycle(tickCtx, now)
		if err != nil {
			logger.Warn("scheduler tick failed", "reason", "dependency_failure")
			return
		}
		if report.Failed > 0 {
			logger.Warn(
				"scheduler tick failed",
				"reason", "dependency_failure",
				"monitors_failed", report.Failed,
			)
		}
		for _, w := range report.Missed {
			coverage.record(now, w.DueAt, true)
		}
		f.install(report.Monitors, report.Open, now)
		s.maintain(tickCtx, report.Monitors, now, logger)
		gaps := report.Gaps + sweepGaps
		dispatched := counts.dispatched.Swap(0)
		completed := counts.completed.Swap(0)
		expired := counts.expired.Swap(0)
		notEligible := counts.notEligible.Swap(0)
		if report.Created > 0 || gaps > 0 || dispatched > 0 || completed > 0 || expired > 0 ||
			notEligible > 0 {
			logger.Info(
				"scheduler tick",
				"monitors", report.Active,
				"work_created", report.Created,
				"gaps_created", gaps,
				"dispatched", dispatched,
				"completed", completed,
				"skipped_expired", expired,
				"not_eligible", notEligible,
				"backlog", f.backlog(),
				"cycle_ms", time.Since(started).Milliseconds(),
			)
		}
	}
	tick()
	for {
		select {
		case <-ctx.Done():
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(shutdownDrain):
			}
			return
		case <-clock.After(cyclePause):
			tick()
		}
	}
}

// maintain runs the per-monitor maintenance boundary, incident reminder, and
// heartbeat deadline passes with the store's bounded concurrency. It runs after
// candidates are installed, so it never delays dispatch.
func (s *Scheduler) maintain(
	ctx context.Context,
	ms []monitor.Monitor,
	now time.Time,
	logger *slog.Logger,
) {
	boundaryStore, hasBoundary := s.Store.(interface {
		Boundary(context.Context, monitor.Monitor, time.Time) error
	})
	reminderStore, hasReminder := s.Store.(interface {
		Reminder(context.Context, monitor.Monitor, time.Time, time.Duration) error
	})
	deadlines, hasDeadlines := s.Store.(interface {
		Liveness(context.Context) (heartbeat.Liveness, error)
		Deadline(context.Context, monitor.Monitor, time.Time, heartbeat.Liveness) error
	})
	if !hasBoundary && !hasReminder && !hasDeadlines {
		return
	}
	var live heartbeat.Liveness
	liveOK := false
	if hasDeadlines {
		for _, m := range ms {
			if m.Kind == "heartbeat" && m.Lifecycle == "active" {
				var e error
				live, e = deadlines.Liveness(ctx)
				liveOK = e == nil
				break
			}
		}
	}
	reminder := s.ReminderInterval
	if reminder <= 0 {
		reminder = 6 * time.Hour
	}
	store.ForEachMonitor(ms, func(m monitor.Monitor) {
		if m.Lifecycle != "active" {
			return
		}
		monitorCtx, cancel := context.WithTimeout(ctx, monitorTimeout)
		defer cancel()
		if hasBoundary {
			if e := boundaryStore.Boundary(monitorCtx, m, now); e != nil &&
				!errors.Is(e, store.ErrNotEligible) {
				logger.Warn("scheduler maintenance boundary failed", "reason", "dependency_failure")
			}
		}
		if hasReminder && m.OpenIncident != nil {
			if e := reminderStore.Reminder(monitorCtx, m, now, reminder); e != nil &&
				!errors.Is(e, store.ErrNotEligible) {
				logger.Warn("scheduler reminder failed", "reason", "dependency_failure")
			}
		}
		if m.Kind == "heartbeat" && liveOK {
			if e := deadlines.Deadline(monitorCtx, m, now, live); e != nil &&
				!errors.Is(e, store.ErrNotEligible) {
				logger.Warn("heartbeat deadline failed", "reason", "dependency_failure")
			}
		}
	})
}
