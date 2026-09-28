package housekeeping

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

type failingPendingStore struct {
	events chan<- string
}

func (s failingPendingStore) RunHousekeeping(context.Context) error {
	s.events <- "run"
	return nil
}

func (s failingPendingStore) PendingHousekeeping(context.Context) (bool, error) {
	s.events <- "pending"
	return false, errors.New("DynamoDB unavailable")
}
func (failingPendingStore) SetHousekeepingRun(time.Time) {}

func TestFailedPendingCheckWaitsForConfiguredInterval(t *testing.T) {
	base := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	var current atomic.Int64
	current.Store(base.UnixNano())
	events := make(chan string, 20)
	ticks := make(chan time.Time)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	worker := Worker{
		Store:    failingPendingStore{events: events},
		Interval: 3 * time.Second,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time {
			events <- "now"
			return time.Unix(0, current.Load())
		},
		ticks: ticks,
	}
	go func() { worker.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("housekeeping worker did not stop")
		}
	})
	wantEvent := func(want string) {
		t.Helper()
		select {
		case got := <-events:
			if got != want {
				t.Fatalf("worker event %q, want %q", got, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("worker did not report %q", want)
		}
	}
	wantEvent("run")
	wantEvent("now")
	wantEvent("now")
	for second := 1; second <= 6; second++ {
		at := base.Add(time.Duration(second) * time.Second)
		current.Store(at.UnixNano())
		ticks <- at
		if second%3 == 1 {
			wantEvent("pending")
		}
		wantEvent("now")
		if second%3 == 0 {
			wantEvent("run")
			wantEvent("now")
			wantEvent("now")
		}
	}
}
