package scheduler

import (
	"sync"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// CoverageWindow is the evidence window of the coverage state. Five minutes
// holds at least five slots of every monitor at the default 60 s minimum
// interval, and a "behind" state clears within five minutes of the backlog
// ending.
const CoverageWindow = 5 * time.Minute

// behindPercent: more than this share of due checks missed is "behind". It
// matches the capacity target of completing at least 95% of due slots.
const behindPercent = 5

const windowSeconds = int64(CoverageWindow / time.Second)

type coverageBucket struct {
	second      int64
	due, missed int
}

// Coverage is this process's evidence that due checks reach a worker. Only
// slots due after the scheduler started count, so slots that expired while
// StatusForge was stopped (or seeded history) never read as "behind", and a
// restart starts from "unknown" rather than "ok".
type Coverage struct {
	mu      sync.Mutex
	enabled bool
	workers int
	since   time.Time
	buckets [windowSeconds]coverageBucket
}

// NewCoverage returns the tracker for a process. With enabled false the state
// is always "disabled".
func NewCoverage(workers int, enabled bool) *Coverage {
	return &Coverage{enabled: enabled, workers: workers}
}

func (c *Coverage) start(now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.since = now
}

// record adds one due check resolved at time at: dispatched to a worker, or
// missed (closed as overdue).
func (c *Coverage) record(at time.Time, dueAt string, missed bool) {
	due, err := time.Parse(time.RFC3339Nano, dueAt)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || c.since.IsZero() || due.Before(c.since) {
		return
	}
	second := at.Unix()
	b := &c.buckets[second%windowSeconds]
	if b.second != second {
		*b = coverageBucket{second: second}
	}
	b.due++
	if missed {
		b.missed++
	}
}

// Snapshot evaluates the state at now.
func (c *Coverage) Snapshot(now time.Time) store.SchedulerCoverage {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := store.SchedulerCoverage{
		State:         "unknown",
		WindowMinutes: int(CoverageWindow / time.Minute),
		Workers:       c.workers,
	}
	if !c.enabled {
		result.State = "disabled"
		return result
	}
	current := now.Unix()
	for _, b := range c.buckets {
		if b.second > current-windowSeconds && b.second <= current {
			result.DueChecks += b.due
			result.MissedChecks += b.missed
		}
	}
	switch {
	case result.DueChecks == 0:
	case result.MissedChecks*100 > result.DueChecks*behindPercent:
		result.State = "behind"
	default:
		result.State = "ok"
	}
	return result
}
