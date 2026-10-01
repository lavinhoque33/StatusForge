package scheduler

import (
	"cmp"
	"context"
	"sync"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// stampLayout is the store's fixed-width time format (lastClaimAt, dueAt,
// lease until), so stamps order as strings.
const stampLayout = "2006-01-02T15:04:05.000000000Z"

type (
	candidate struct {
		work        store.Work
		due         time.Time
		interval    time.Duration
		lastClaimAt string
	}
	dueKey  struct{ monitorID, dueAt string }
	handoff struct {
		attempts int
		expires  time.Time
	}
	// feed hands work to workers as they become free. It holds at most one
	// candidate per monitor (the freshest eligible slot), so memory is bounded
	// by the monitor count and there is no queue of stale work.
	feed struct {
		mu        sync.Mutex
		next      map[string]candidate
		inFlight  map[string]bool
		handed    map[dueKey]handoff
		claimedAt map[string]string
		changed   chan struct{}
		expired   func()
	}
)

func newFeed(expired func()) *feed {
	return &feed{
		next:      map[string]candidate{},
		inFlight:  map[string]bool{},
		handed:    map[dueKey]handoff{},
		claimedAt: map[string]string{},
		changed:   make(chan struct{}),
		expired:   expired,
	}
}

func (f *feed) broadcast() {
	close(f.changed)
	f.changed = make(chan struct{})
}

func stamp(t time.Time) string { return t.UTC().Format(stampLayout) }

func leaseLive(until string, now time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, until)
	return err == nil && !now.After(t)
}

// install replaces the candidates with a fresh pass. Per monitor it keeps the
// freshest slot that is still eligible: older slots of the same monitor are
// closer to expiry and would spend a worker on a result that is already
// superseded; they end as overdue gaps through the store instead.
func (f *feed) install(ms []monitor.Monitor, open map[string][]store.Work, now time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for key, h := range f.handed {
		if !now.Before(h.expires) {
			delete(f.handed, key)
		}
	}
	next := make(map[string]candidate, len(open))
	listed := make(map[string]bool, len(ms))
	for _, m := range ms {
		listed[m.ID] = true
		works := open[m.ID]
		if len(works) == 0 || m.Lifecycle != "active" || m.Deletion != nil {
			continue
		}
		if m.Lease != nil && leaseLive(m.Lease.Until, now) {
			continue
		}
		interval := time.Duration(m.IntervalSeconds) * time.Second
		lastClaimAt := max(m.LastClaimAt, f.claimedAt[m.ID])
		var best *candidate
		for _, w := range works {
			if h, ok := f.handed[dueKey{w.MonitorID, w.DueAt}]; ok && w.Attempts <= h.attempts {
				continue
			}
			due, err := time.Parse(time.RFC3339Nano, w.DueAt)
			if err != nil || !now.Before(due.Add(interval)) {
				continue
			}
			if w.State == "claimed" && (w.Attempts >= 2 || leaseLive(w.LeaseUntil, now)) {
				continue
			}
			if best == nil || w.DueAt > best.work.DueAt {
				best = &candidate{work: w, due: due, interval: interval, lastClaimAt: lastClaimAt}
			}
		}
		if best != nil {
			next[m.ID] = *best
		}
	}
	for id := range f.claimedAt {
		if !listed[id] {
			delete(f.claimedAt, id)
		}
	}
	f.next = next
	f.broadcast()
}

// backlog is the number of eligible candidates waiting for a worker.
func (f *feed) backlog() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for id := range f.next {
		if !f.inFlight[id] {
			n++
		}
	}
	return n
}

// take blocks until it can hand out work or ctx ends. Eligibility is
// rechecked against the clock at hand-off: a slot already past dueAt +
// interval is dropped (the store's next pass closes it as one overdue gap).
// Order: least recently claimed monitor first (fairness), then oldest
// dueAt, then monitor ID.
func (f *feed) take(ctx context.Context, clock Clock) (candidate, bool) {
	for {
		if ctx.Err() != nil {
			return candidate{}, false
		}
		f.mu.Lock()
		now := clock.Now()
		var best candidate
		found := false
		for id, c := range f.next {
			if f.inFlight[id] {
				continue
			}
			if !now.Before(c.due.Add(c.interval)) {
				delete(f.next, id)
				f.expired()
				continue
			}
			if !found || before(c, best) {
				best, found = c, true
			}
		}
		if found {
			id := best.work.MonitorID
			delete(f.next, id)
			f.inFlight[id] = true
			f.handed[dueKey{id, best.work.DueAt}] = handoff{
				attempts: best.work.Attempts,
				expires:  best.due.Add(best.interval),
			}
			f.mu.Unlock()
			return best, true
		}
		changed := f.changed
		f.mu.Unlock()
		select {
		case <-ctx.Done():
			return candidate{}, false
		case <-changed:
		}
	}
}

func before(a, b candidate) bool {
	if order := cmp.Compare(a.lastClaimAt, b.lastClaimAt); order != 0 {
		return order < 0
	}
	if order := cmp.Compare(a.work.DueAt, b.work.DueAt); order != 0 {
		return order < 0
	}
	return a.work.MonitorID < b.work.MonitorID
}

// finish frees the monitor for its next slot. A successful claim moves the
// monitor to the back of the fairness order at once, before the store's next
// monitor list reflects it.
func (f *feed) finish(id string, claimed bool, at time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.inFlight, id)
	if claimed {
		f.claimedAt[id] = stamp(at)
	}
	f.broadcast()
}
