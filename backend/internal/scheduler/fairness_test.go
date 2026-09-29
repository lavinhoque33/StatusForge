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

type cycleStore struct {
	mu      sync.Mutex
	base    time.Time
	cycle   int
	last    map[string]string
	claimed map[string]bool
	runs    map[string]int
	misses  map[string]int
	claims  chan string
	stale   chan struct{}
	ticks   chan int
}

func (c *cycleStore) SweepOld(context.Context, time.Time) (int, error) { return 0, nil }
func (c *cycleStore) TickCycle(ctx context.Context, now time.Time) (store.TickReport, error) {
	return pass(ctx, c, now)
}

func (c *cycleStore) Tick(_ context.Context, now time.Time) (int, int, int, error) {
	c.mu.Lock()
	next := int(now.Sub(c.base) / (10 * time.Second))
	for c.cycle < next {
		for _, id := range []string{"a", "b", "c"} {
			if !c.claimed[id] {
				c.misses[id]++
			}
			c.claimed[id] = false
		}
		c.cycle++
	}
	current := c.cycle
	c.mu.Unlock()
	select {
	case c.ticks <- current:
	default:
	}
	return 3, 0, 0, nil
}

func (c *cycleStore) List(context.Context) ([]monitor.Monitor, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ms := make([]monitor.Monitor, 0, 3)
	for _, id := range []string{"a", "b", "c"} {
		ms = append(
			ms,
			monitor.Monitor{
				ID:              id,
				Lifecycle:       "active",
				IntervalSeconds: 10,
				LastClaimAt:     c.last[id],
			},
		)
	}
	return ms, nil
}

func (c *cycleStore) due(id string, cycle int) string {
	offset := map[string]int{"a": 1, "b": 2, "c": 3}[id]
	return c.base.Add(time.Duration(cycle*10+offset-4) * time.Second).
		UTC().
		Format("2006-01-02T15:04:05.000000000Z")
}

func (c *cycleStore) Works(
	_ context.Context,
	m monitor.Monitor,
	_ time.Time,
) ([]store.Work, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.claimed[m.ID] {
		return nil, nil
	}
	return []store.Work{
		{MonitorID: m.ID, DueAt: c.due(m.ID, c.cycle), State: "pending", Trigger: "schedule"},
	}, nil
}

func (c *cycleStore) Claim(
	_ context.Context,
	w store.Work,
	now time.Time,
) (monitor.Monitor, string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if w.DueAt != c.due(w.MonitorID, c.cycle) || c.claimed[w.MonitorID] {
		select {
		case c.stale <- struct{}{}:
		default:
		}
		return monitor.Monitor{}, "", store.ErrNotEligible
	}
	c.claimed[w.MonitorID] = true
	c.last[w.MonitorID] = now.UTC().Format("2006-01-02T15:04:05.000000000Z")
	c.runs[w.MonitorID]++
	c.claims <- w.MonitorID
	return monitor.Monitor{ID: w.MonitorID, Check: monitor.Check{DeadlineMs: 1000}}, "token", nil
}

func (c *cycleStore) RecordResult(
	_ context.Context,
	o monitor.Observation,
	_ string,
) (monitor.Observation, error) {
	return o, nil
}

func TestSaturationRotatesClaimsAcrossMonitors(t *testing.T) {
	base := time.Date(2026, 9, 27, 0, 0, 4, 0, time.UTC)
	c := &fakeClock{now: base, wake: make(chan time.Time, 1)}
	f := &cycleStore{
		base:    base,
		last:    map[string]string{},
		claimed: map[string]bool{},
		runs:    map[string]int{},
		misses:  map[string]int{},
		claims:  make(chan string, 12),
		stale:   make(chan struct{}, 12),
		ticks:   make(chan int, 12),
	}
	r := blockedRunner{release: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		(&Scheduler{Store: f, Runner: r, Clock: c, Workers: 1, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}).Run(
			ctx,
		)
	}()
	waitClaim := func() string {
		t.Helper()
		select {
		case id := <-f.claims:
			return id
		case <-time.After(2 * time.Second):
			t.Fatal("worker starved")
			return ""
		}
	}
	waitTick := func(want int) {
		t.Helper()
		for {
			select {
			case got := <-f.ticks:
				if got >= want {
					return
				}
			case <-time.After(2 * time.Second):
				t.Fatal("tick stalled")
			}
		}
	}
	waitTick(0)
	nextID := waitClaim()
	for round := range 9 {
		id := nextID
		if round == 0 && id != "a" {
			t.Fatalf("first claim %q, want oldest dueAt monitor a", id)
		}
		c.Advance(10 * time.Second)
		waitTick(round + 1)
		r.release <- struct{}{}
		if round == 8 {
			continue
		}
		found := false
		for range 6 {
			c.Advance(2 * time.Second)
			waitTick(round + 1)
			select {
			case nextID = <-f.claims:
				found = true
			case <-time.After(5 * time.Millisecond):
			}
			if found {
				break
			}
		}
		if !found {
			t.Fatal("no claim after advancing the fake clock")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker failed to drain")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	minRuns, maxRuns := 9, 0
	minMisses, maxMisses := 9, 0
	for _, id := range []string{"a", "b", "c"} {
		if f.runs[id] < minRuns {
			minRuns = f.runs[id]
		}
		if f.runs[id] > maxRuns {
			maxRuns = f.runs[id]
		}
		if f.misses[id] < minMisses {
			minMisses = f.misses[id]
		}
		if f.misses[id] > maxMisses {
			maxMisses = f.misses[id]
		}
	}
	if minRuns == 0 || maxRuns-minRuns > 1 || maxMisses-minMisses > 1 {
		t.Fatalf("starved runs=%v misses=%v", f.runs, f.misses)
	}
}
