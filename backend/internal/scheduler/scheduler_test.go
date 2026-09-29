package scheduler

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	wake chan time.Time
}

func (c *fakeClock) Now() time.Time                       { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) After(time.Duration) <-chan time.Time { return c.wake }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	now := c.now
	c.mu.Unlock()
	c.wake <- now
}

type fakePersistence struct {
	mu      sync.Mutex
	ticks   int
	claims  int
	work    []store.Work
	started chan struct{}
}

// passParts is the old three-call shape the fakes model; pass combines it into
// one store.TickReport the way the store's TickCycle does.
type passParts interface {
	Tick(context.Context, time.Time) (int, int, int, error)
	List(context.Context) ([]monitor.Monitor, error)
	Works(context.Context, monitor.Monitor, time.Time) ([]store.Work, error)
}

func pass(ctx context.Context, p passParts, now time.Time) (store.TickReport, error) {
	active, created, gaps, err := p.Tick(ctx, now)
	if err != nil {
		return store.TickReport{}, err
	}
	ms, err := p.List(ctx)
	if err != nil {
		return store.TickReport{}, err
	}
	r := store.TickReport{
		Monitors: ms,
		Open:     map[string][]store.Work{},
		Active:   active,
		Created:  created,
		Gaps:     gaps,
	}
	for _, m := range ms {
		if r.Open[m.ID], err = p.Works(ctx, m, now); err != nil {
			return store.TickReport{}, err
		}
	}
	return r, nil
}

func (f *fakePersistence) TickCycle(ctx context.Context, now time.Time) (store.TickReport, error) {
	return pass(ctx, f, now)
}

func (f *fakePersistence) Tick(context.Context, time.Time) (int, int, int, error) {
	f.mu.Lock()
	f.ticks++
	f.mu.Unlock()
	return 3, 1, 1, nil
}

func (f *fakePersistence) List(context.Context) ([]monitor.Monitor, error) {
	return []monitor.Monitor{
		{ID: "a", Lifecycle: "active", IntervalSeconds: 10},
		{ID: "b", Lifecycle: "active", IntervalSeconds: 10},
		{ID: "c", Lifecycle: "active", IntervalSeconds: 10},
	}, nil
}

func (f *fakePersistence) SweepOld(context.Context, time.Time) (int, error) { return 0, nil }

func (f *fakePersistence) Works(
	_ context.Context,
	m monitor.Monitor,
	_ time.Time,
) ([]store.Work, error) {
	return []store.Work{
		{
			MonitorID: m.ID,
			DueAt:     "2026-09-27T00:00:00.000000000Z",
			State:     "pending",
			Trigger:   "schedule",
		},
	}, nil
}

func (f *fakePersistence) Claim(
	_ context.Context,
	w store.Work,
	_ time.Time,
) (monitor.Monitor, string, error) {
	f.mu.Lock()
	f.claims++
	f.mu.Unlock()
	select {
	case f.started <- struct{}{}:
	default:
	}
	return monitor.Monitor{ID: w.MonitorID, Check: monitor.Check{DeadlineMs: 1000}}, "token", nil
}

func (f *fakePersistence) RecordResult(
	_ context.Context,
	o monitor.Observation,
	_ string,
) (monitor.Observation, error) {
	return o, nil
}

type blockedRunner struct{ release chan struct{} }

func (r blockedRunner) Run(ctx context.Context, m monitor.Monitor) monitor.Observation {
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return monitor.Observation{MonitorID: m.ID, Outcome: "healthy"}
}

func TestSaturatedPoolLeavesPendingWork(t *testing.T) {
	c := &fakeClock{
		now:  time.Date(2026, 9, 27, 0, 0, 0, 0, time.UTC),
		wake: make(chan time.Time, 1),
	}
	f := &fakePersistence{started: make(chan struct{}, 10)}
	r := blockedRunner{release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Scheduler{Store: f, Runner: r, Clock: c, Workers: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Run(
			ctx,
		)
	}()
	select {
	case <-f.started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not claim")
	}
	c.Advance(2 * time.Second)
	deadline := time.After(2 * time.Second)
	for {
		f.mu.Lock()
		ticks := f.ticks
		claims := f.claims
		f.mu.Unlock()
		if ticks >= 2 {
			if claims != 1 {
				t.Fatalf("saturated pool claimed %d", claims)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("fake clock tick not handled")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	close(r.release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler did not drain")
	}
}
