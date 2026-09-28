// Package housekeeping drives the table's resumable retention and deletion work.
package housekeeping

import (
	"context"
	"log/slog"
	"time"
)

type (
	Store interface {
		RunHousekeeping(context.Context) error
		PendingHousekeeping(context.Context) (bool, error)
		SetHousekeepingRun(time.Time)
	}
	Worker struct {
		Store    Store
		Interval time.Duration
		Logger   *slog.Logger
		Now      func() time.Time
		Fatal    func(error) bool
		ticks    <-chan time.Time
	}
)

func (w Worker) Run(ctx context.Context) {
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Interval <= 0 {
		w.Interval = time.Minute
	}
	if w.Logger == nil {
		w.Logger = slog.Default()
	}
	ticks := w.ticks
	if ticks == nil {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		ticks = ticker.C
	}
	for {
		if w.tick(ctx) {
			return
		}
		nextIdlePass := w.Now().Add(w.Interval)
		pendingCheckFailed := false
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticks:
			}
			if !pendingCheckFailed {
				pending, err := w.Store.PendingHousekeeping(ctx)
				if err != nil {
					w.Logger.Warn("housekeeping_pending_failed", "error", err)
					pendingCheckFailed = true
				} else if pending {
					break
				}
			}
			if !w.Now().Before(nextIdlePass) {
				break
			}
		}
	}
}

func (w Worker) tick(ctx context.Context) bool {
	if e := w.Store.RunHousekeeping(ctx); e != nil {
		if ctx.Err() != nil {
			return true
		}
		if w.Fatal != nil && w.Fatal(e) {
			return true
		}
		w.Logger.Warn("housekeeping_failed", "error", e)
		return false
	}
	w.Store.SetHousekeepingRun(w.Now())
	return false
}
