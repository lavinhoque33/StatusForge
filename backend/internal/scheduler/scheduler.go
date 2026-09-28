package scheduler

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/checker"
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
	Tick(context.Context, time.Time) (int, int, int, error)
	List(context.Context) ([]monitor.Monitor, error)
	Works(context.Context, monitor.Monitor, time.Time) ([]store.Work, error)
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
	}
)

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
	// Reserve at most one outstanding item per worker, including in-flight
	// checks. Otherwise stale buffered work biases the next dispatch cycle.
	work := make(chan store.Work, workers)
	type dueKey struct{ monitorID, dueAt string }
	queued := make(map[dueKey]struct{})
	var queueMu sync.Mutex
	process := func(w store.Work) {
		defer func() {
			queueMu.Lock()
			delete(queued, dueKey{w.MonitorID, w.DueAt})
			queueMu.Unlock()
		}()
		now := clock.Now()
		claimCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		m, token, err := s.Store.Claim(claimCtx, w, now)
		cancel()
		if err != nil {
			if !errors.Is(err, store.ErrNotEligible) && !errors.Is(err, store.ErrLeaseHeld) {
				logger.Warn("claim failed", "reason", "dependency_failure")
			}
			return
		}
		if token == "" {
			return
		}
		checkCtx, stop := context.WithTimeout(
			context.Background(),
			time.Duration(m.Check.DeadlineMs)*time.Millisecond,
		)
		o := s.Runner.Run(checkCtx, m)
		stop()
		o.InitiatedBy = "scheduled"
		o.Trigger = &w.Trigger
		o.DueAt = &w.DueAt
		saveCtx, saveCancel := context.WithTimeout(context.Background(), 5*time.Second)
		o, err = s.Store.RecordResult(saveCtx, o, token)
		saveCancel()
		if err != nil {
			logger.Warn("scheduled result failed", "reason", "dependency_failure")
			return
		}
		checker.Log(logger, o)
	}
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for w := range work {
				if ctx.Err() != nil {
					return
				}
				process(w)
			}
		}()
	}
	recoveryDone := false
	lastLiveness := time.Time{}
	tick := func() {
		now := clock.Now()
		tickCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
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
		defer cancel()
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
		monitors, created, gaps, err := s.Store.Tick(tickCtx, now)
		if err != nil {
			logger.Warn("scheduler tick failed", "reason", "dependency_failure")
			return
		}
		gaps += sweepGaps
		if created > 0 || gaps > 0 {
			logger.Info(
				"scheduler tick",
				"monitors",
				monitors,
				"work_created",
				created,
				"gaps_created",
				gaps,
			)
		}
		ms, err := s.Store.List(tickCtx)
		if err != nil {
			logger.Warn("scheduler dispatch failed", "reason", "dependency_failure")
			return
		}
		type candidate struct {
			work        store.Work
			lastClaimAt string
		}
		candidates := make([]candidate, 0, len(ms))
		for _, m := range ms {
			if boundaryStore, ok := s.Store.(interface {
				Boundary(context.Context, monitor.Monitor, time.Time) error
			}); ok && m.Lifecycle == "active" {
				if e := boundaryStore.Boundary(tickCtx, m, now); e != nil &&
					!errors.Is(e, store.ErrNotEligible) {
					logger.Warn(
						"scheduler maintenance boundary failed",
						"reason",
						"dependency_failure",
					)
				}
			}
			if reminderStore, ok := s.Store.(interface {
				Reminder(context.Context, monitor.Monitor, time.Time, time.Duration) error
			}); ok && m.OpenIncident != nil && m.Lifecycle == "active" {
				interval := s.ReminderInterval
				if interval <= 0 {
					interval = 6 * time.Hour
				}
				if e := reminderStore.Reminder(tickCtx, m, now, interval); e != nil &&
					!errors.Is(e, store.ErrNotEligible) {
					logger.Warn("scheduler reminder failed", "reason", "dependency_failure")
				}
			}
			if m.Kind == "heartbeat" {
				if deadlines, ok := s.Store.(interface {
					Liveness(context.Context) (heartbeat.Liveness, error)
					Deadline(context.Context, monitor.Monitor, time.Time, heartbeat.Liveness) error
				}); ok && m.Lifecycle == "active" {
					if live, e := deadlines.Liveness(tickCtx); e == nil {
						if e = deadlines.Deadline(tickCtx, m, now, live); e != nil &&
							!errors.Is(e, store.ErrNotEligible) {
							logger.Warn("heartbeat deadline failed", "reason", "dependency_failure")
						}
					}
				}
				continue
			}
			ws, e := s.Store.Works(tickCtx, m, now)
			if e != nil {
				logger.Warn("scheduler dispatch failed", "reason", "dependency_failure")
				continue
			}
			for _, w := range ws {
				if w.State == "claimed" {
					until, parseErr := time.Parse(time.RFC3339Nano, w.LeaseUntil)
					if parseErr == nil && !now.After(until) {
						continue
					}
				}
				candidates = append(candidates, candidate{work: w, lastClaimAt: m.LastClaimAt})
			}
		}
		slices.SortFunc(candidates, func(a, b candidate) int {
			if order := cmp.Compare(a.lastClaimAt, b.lastClaimAt); order != 0 {
				return order
			}
			if order := cmp.Compare(a.work.DueAt, b.work.DueAt); order != 0 {
				return order
			}
			return cmp.Compare(a.work.MonitorID, b.work.MonitorID)
		})
		for _, next := range candidates {
			w := next.work
			key := dueKey{w.MonitorID, w.DueAt}
			queueMu.Lock()
			if _, exists := queued[key]; exists {
				queueMu.Unlock()
				continue
			}
			if len(queued) >= workers {
				queueMu.Unlock()
				break
			}
			select {
			case work <- w:
				queued[key] = struct{}{}
			default:
			}
			queueMu.Unlock()
		}
	}
	tick()
	for {
		select {
		case <-ctx.Done():
			close(work)
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(30 * time.Second):
			}
			return
		case <-clock.After(2 * time.Second):
			tick()
		}
	}
}
