package scheduler

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// feedStore serves a fixed pass (monitors plus open work) and records claims.
type feedStore struct {
	mu       sync.Mutex
	monitors []monitor.Monitor
	open     map[string][]store.Work
	missed   []store.Work
	ticks    int
	claimed  chan string
	tickDone chan int
}

func newFeedStore(ms []monitor.Monitor, open map[string][]store.Work) *feedStore {
	return &feedStore{
		monitors: ms,
		open:     open,
		claimed:  make(chan string, 32),
		tickDone: make(chan int, 32),
	}
}

func (s *feedStore) TickCycle(context.Context, time.Time) (store.TickReport, error) {
	s.mu.Lock()
	s.ticks++
	open := make(map[string][]store.Work, len(s.open))
	for id, ws := range s.open {
		open[id] = append([]store.Work(nil), ws...)
	}
	r := store.TickReport{
		Monitors: append([]monitor.Monitor(nil), s.monitors...),
		Open:     open,
		Active:   len(s.monitors),
		Missed:   s.missed,
	}
	s.missed = nil
	ticks := s.ticks
	s.mu.Unlock()
	s.tickDone <- ticks
	return r, nil
}

func (s *feedStore) SweepOld(context.Context, time.Time) (int, error) { return 0, nil }

func (s *feedStore) Claim(
	_ context.Context,
	w store.Work,
	now time.Time,
) (monitor.Monitor, string, error) {
	s.mu.Lock()
	// The store removes claimed work from the open set.
	ws := s.open[w.MonitorID]
	for i := range ws {
		if ws[i].DueAt == w.DueAt {
			s.open[w.MonitorID] = append(ws[:i:i], ws[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
	s.claimed <- w.MonitorID + "@" + w.DueAt
	return monitor.Monitor{
		ID:              w.MonitorID,
		IntervalSeconds: 10,
		Check:           monitor.Check{DeadlineMs: 60000},
	}, "token", nil
}

func (s *feedStore) RecordResult(
	_ context.Context,
	o monitor.Observation,
	_ string,
) (monitor.Observation, error) {
	return o, nil
}

func (s *feedStore) tickCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ticks
}

func at(t time.Time) string { return t.UTC().Format(stampLayout) }

func active(id, lastClaimAt string) monitor.Monitor {
	return monitor.Monitor{
		ID:              id,
		Lifecycle:       "active",
		IntervalSeconds: 10,
		LastClaimAt:     lastClaimAt,
	}
}

func pending(id string, due time.Time) store.Work {
	return store.Work{MonitorID: id, DueAt: at(due), State: "pending", Trigger: "schedule"}
}

func startScheduler(
	t *testing.T,
	s *Scheduler,
) (cancel func()) {
	t.Helper()
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.Run(ctx)
	}()
	return func() {
		stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("scheduler did not drain")
		}
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func waitClaim(t *testing.T, s *feedStore) string {
	t.Helper()
	select {
	case got := <-s.claimed:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("no claim")
		return ""
	}
}

func noClaim(t *testing.T, s *feedStore) {
	t.Helper()
	select {
	case got := <-s.claimed:
		t.Fatalf("unexpected claim %s", got)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestFreedWorkerTakesNextWorkWithinTheSameCycle(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c := &fakeClock{now: now, wake: make(chan time.Time, 1)}
	f := newFeedStore(
		[]monitor.Monitor{active("a", ""), active("b", ""), active("c", "")},
		map[string][]store.Work{
			"a": {pending("a", now.Add(-3*time.Second))},
			"b": {pending("b", now.Add(-2*time.Second))},
			"c": {pending("c", now.Add(-time.Second))},
		},
	)
	r := blockedRunner{release: make(chan struct{})}
	stop := startScheduler(
		t,
		&Scheduler{Store: f, Runner: r, Clock: c, Workers: 1, Logger: discard()},
	)
	defer stop()
	want := []string{"a", "b", "c"}
	for i, id := range want {
		if got := waitClaim(t, f); !strings.HasPrefix(got, id+"@") {
			t.Fatalf("claim %d = %s, want %s", i, got, id)
		}
		if i < len(want)-1 {
			r.release <- struct{}{}
		}
	}
	if ticks := f.tickCount(); ticks != 1 {
		t.Fatalf("freed worker waited for %d passes", ticks)
	}
	close(r.release)
}

func TestHandOffNeverGivesExpiredOrLeasedWork(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c := &fakeClock{now: now, wake: make(chan time.Time, 1)}
	leased := active("leased", "")
	leased.Lease = &monitor.Lease{Token: "manual", Until: at(now.Add(5 * time.Second))}
	claimedWork := pending("claimed", now.Add(-time.Second))
	claimedWork.State, claimedWork.Attempts = "claimed", 1
	claimedWork.LeaseUntil = at(now.Add(5 * time.Second))
	// "late" is eligible at the pass but expires two seconds later, while the
	// only worker is busy with "fresh" (least recently claimed goes first).
	f := newFeedStore(
		[]monitor.Monitor{
			active("expired", ""),
			leased,
			active("claimed", ""),
			active("fresh", ""),
			active("late", at(now.Add(-time.Second))),
		},
		map[string][]store.Work{
			"expired": {pending("expired", now.Add(-10*time.Second))},
			"leased":  {pending("leased", now.Add(-time.Second))},
			"claimed": {claimedWork},
			"fresh":   {pending("fresh", now.Add(-time.Second))},
			"late":    {pending("late", now.Add(-8*time.Second))},
		},
	)
	var logs bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: &logMu}, nil))
	r := blockedRunner{release: make(chan struct{})}
	stop := startScheduler(t, &Scheduler{Store: f, Runner: r, Clock: c, Workers: 1, Logger: logger})
	if got := waitClaim(t, f); got != "fresh@"+at(now.Add(-time.Second)) {
		t.Fatalf("first claim %s", got)
	}
	<-f.tickDone
	c.mu.Lock()
	c.now = now.Add(3 * time.Second) // past late's dueAt + interval; no new pass
	c.mu.Unlock()
	r.release <- struct{}{}
	noClaim(t, f)
	// The next pass reports the store closing "late" as overdue; the tick line
	// counts the hand-off that was skipped.
	f.mu.Lock()
	f.open = map[string][]store.Work{}
	f.missed = []store.Work{pending("late", now.Add(-8*time.Second))}
	f.mu.Unlock()
	c.Advance(2 * time.Second)
	<-f.tickDone
	noClaim(t, f)
	close(r.release)
	stop()
	logMu.Lock()
	defer logMu.Unlock()
	if !strings.Contains(logs.String(), "skipped_expired=1") {
		t.Fatalf("expired hand-off not counted:\n%s", logs.String())
	}
}

type lockedWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestNotEligibleClaimsAreCounted(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c := &fakeClock{now: now, wake: make(chan time.Time, 1)}
	f := &refusingStore{feedStore: newFeedStore(
		[]monitor.Monitor{active("a", "")},
		map[string][]store.Work{"a": {pending("a", now.Add(-time.Second))}},
	)}
	var logs bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: &logMu}, nil))
	stop := startScheduler(
		t,
		&Scheduler{Store: f, Runner: blockedRunner{}, Clock: c, Workers: 1, Logger: logger},
	)
	waitClaim(t, f.feedStore)
	<-f.tickDone
	c.Advance(2 * time.Second)
	<-f.tickDone
	stop()
	logMu.Lock()
	defer logMu.Unlock()
	if !strings.Contains(logs.String(), "not_eligible=1") {
		t.Fatalf("not-eligible claim not counted:\n%s", logs.String())
	}
}

type refusingStore struct{ *feedStore }

func (s *refusingStore) Claim(
	ctx context.Context,
	w store.Work,
	now time.Time,
) (monitor.Monitor, string, error) {
	_, _, _ = s.feedStore.Claim(ctx, w, now)
	return monitor.Monitor{}, "", store.ErrNotEligible
}

func TestCoverageStateAndLogsFollowMissedWork(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	c := &fakeClock{now: now, wake: make(chan time.Time, 1)}
	f := newFeedStore([]monitor.Monitor{active("a", "")}, map[string][]store.Work{})
	f.missed = []store.Work{
		pending("a", now.Add(-time.Hour)), // before this run: not evidence
		pending("a", now),
	}
	var logs bytes.Buffer
	var logMu sync.Mutex
	logger := slog.New(slog.NewTextHandler(&lockedWriter{w: &logs, mu: &logMu}, nil))
	coverage := NewCoverage(1, true)
	if got := coverage.Snapshot(now).State; got != "unknown" {
		t.Fatalf("before start %s", got)
	}
	r := blockedRunner{release: make(chan struct{})}
	close(r.release)
	stop := startScheduler(
		t,
		&Scheduler{Store: f, Runner: r, Clock: c, Workers: 1, Logger: logger, Coverage: coverage},
	)
	<-f.tickDone
	c.Advance(2 * time.Second)
	<-f.tickDone
	if got := coverage.Snapshot(c.Now()); got.State != "behind" || got.DueChecks != 1 ||
		got.MissedChecks != 1 {
		t.Fatalf("after missed work %+v", got)
	}
	// The window slides past the miss; the next slot is dispatched.
	f.mu.Lock()
	f.open = map[string][]store.Work{"a": {pending("a", now.Add(6*time.Minute))}}
	f.mu.Unlock()
	c.Advance(6 * time.Minute)
	<-f.tickDone
	waitClaim(t, f)
	c.Advance(2 * time.Second)
	<-f.tickDone
	if got := coverage.Snapshot(c.Now()); got.State != "ok" || got.DueChecks != 1 ||
		got.MissedChecks != 0 {
		t.Fatalf("after recovery %+v", got)
	}
	stop()
	logMu.Lock()
	defer logMu.Unlock()
	out := logs.String()
	if strings.Count(out, "scheduler coverage degraded") != 1 ||
		!strings.Contains(out, "level=WARN msg=\"scheduler coverage degraded\"") ||
		strings.Count(out, "scheduler coverage recovered") != 1 {
		t.Fatalf("transition logs:\n%s", out)
	}
}
