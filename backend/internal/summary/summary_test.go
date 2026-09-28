package summary

import (
	"testing"
	"time"
)

func TestWindowSlotAccounting(t *testing.T) {
	to := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	at := func(offset time.Duration) string { return stamp(from.Add(offset)) }
	ptr := func(s string) *string { return &s }
	maint := "maintenance"
	reason := "config_changed"
	cases := []struct {
		name                                                                                 string
		input                                                                                Input
		expected, recorded, missing, maintenance, notCounted, healthy, fail, checker, manual int
	}{
		{
			"boundaries and gap straddle",
			Input{
				Observations: []Observation{
					{DueAt: ptr(at(0)), Outcome: "healthy", Counted: true, Reason: "ok"},
					{
						DueAt:   ptr(at(24 * time.Hour)),
						Outcome: "failing",
						Counted: true,
						Reason:  "wrong_status",
					},
				},
				Gaps: []Gap{
					{
						FromDueAt:   at(-10 * time.Second),
						ToDueAt:     at(10 * time.Second),
						MissedCount: 3,
					},
				},
			},
			4,
			2,
			2,
			0,
			0,
			1,
			1,
			0,
			0,
		},
		{
			"maintenance precedes not counted",
			Input{
				Observations: []Observation{
					{
						DueAt:               ptr(at(time.Hour)),
						Outcome:             "failing",
						Reason:              "timeout",
						MaintenanceWindowID: &maint,
						NotCountedReason:    &reason,
					},
					{
						DueAt:   ptr(at(2 * time.Hour)),
						Outcome: "checker_problem",
						Counted: true,
						Reason:  "internal",
					},
					{DueAt: ptr(at(3 * time.Hour)), Outcome: "healthy", NotCountedReason: &reason},
					{StartedAt: at(4 * time.Hour), Reason: "ok", Outcome: "healthy"},
				},
			},
			3,
			3,
			0,
			1,
			1,
			0,
			0,
			1,
			1,
		},
		{
			"changed interval gap uses original due grid",
			Input{
				Gaps: []Gap{
					{FromDueAt: at(5 * time.Second), ToDueAt: at(25 * time.Second), MissedCount: 3},
					{
						FromDueAt:   at(60 * time.Second),
						ToDueAt:     at(120 * time.Second),
						MissedCount: 2,
					},
				},
			},
			5,
			0,
			5,
			0,
			0,
			0,
			0,
			0,
			0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input
			in.MonitorID = "id"
			in.Window = "24h"
			in.Lifecycle = "active"
			in.To = to
			r := Compute(in)
			c := r.Coverage
			if c.Expected != tc.expected || c.Recorded != tc.recorded ||
				c.NotObserved != tc.missing ||
				c.Maintenance != tc.maintenance ||
				c.NotCounted != tc.notCounted ||
				c.Outcomes.Healthy != tc.healthy ||
				c.Outcomes.Failing != tc.fail ||
				c.Outcomes.CheckerProblem != tc.checker ||
				c.ManualChecks != tc.manual {
				t.Fatalf("coverage %+v; want %+v", c, tc)
			}
			if c.Expected != c.Recorded+c.NotObserved ||
				c.Outcomes.Healthy+c.Outcomes.Failing+c.Outcomes.CheckerProblem != c.Recorded-c.Maintenance-c.NotCounted {
				t.Fatalf("broken invariant %+v", c)
			}
			var expected, recorded, missing, maint, nc, healthy, fail, checker int
			for _, b := range r.Buckets {
				expected += b.Expected
				recorded += b.Recorded
				missing += b.NotObserved
				maint += b.Maintenance
				nc += b.NotCounted
				healthy += b.Healthy
				fail += b.Failing
				checker += b.CheckerProblem
			}
			if expected != c.Expected || recorded != c.Recorded || missing != c.NotObserved ||
				maint != c.Maintenance ||
				nc != c.NotCounted ||
				healthy != c.Outcomes.Healthy ||
				fail != c.Outcomes.Failing ||
				checker != c.Outcomes.CheckerProblem {
				t.Fatalf("buckets do not sum: %+v", r.Buckets)
			}
		})
	}
}

func TestPercentileNearestRanks(t *testing.T) {
	for _, n := range []int{4, 5, 20} {
		t.Run(string(rune('A'+n)), func(t *testing.T) {
			samples := make([]int64, n)
			for i := range n {
				samples[i] = int64(i + 1)
			}
			r := statistics(samples)
			if r.MaxMs == nil || *r.MaxMs != int64(n) {
				t.Fatalf("max: %+v", r)
			}
			if n < 5 {
				if r.MedianMs != nil || r.P95Ms != nil {
					t.Fatalf("too few: %+v", r)
				}
				return
			}
			median := (n + 1) / 2
			if *r.MedianMs != int64(median) {
				t.Fatalf("median: %+v", r)
			}
			p95 := 5
			if n == 20 {
				p95 = 19
			}
			if *r.P95Ms != int64(p95) {
				t.Fatalf("p95: %+v", r)
			}
		})
	}
}

func TestPauseAndTruncation(t *testing.T) {
	to := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	r := Compute(
		Input{
			Window:      "24h",
			To:          to,
			Lifecycle:   "paused",
			PausedAt:    stamp(from.Add(-time.Hour)),
			Truncated:   true,
			CoveredFrom: from.Add(2 * time.Hour),
			Events:      []LifecycleEvent{{Action: "created", At: stamp(from.Add(-time.Hour))}},
			Gaps: []Gap{
				{
					FromDueAt:   stamp(from.Add(time.Hour)),
					ToDueAt:     stamp(from.Add(3 * time.Hour)),
					MissedCount: 3,
				},
			},
		},
	)
	if r.Coverage.PausedSeconds != 22*3600 || r.Coverage.NotObserved != 2 ||
		r.Buckets[0].From != r.CoveredFrom ||
		r.LifecycleHistoryFrom == nil {
		t.Fatalf("pause/truncation: %+v", r)
	}
}

func TestRetainedResumeClosesPauseStartedBeforeHistory(t *testing.T) {
	to := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	resumed := from.Add(3 * time.Hour)
	r := Compute(Input{
		Window: "24h", To: to, Lifecycle: "active",
		Events: []LifecycleEvent{{Action: "resumed", At: stamp(resumed)}},
	})
	if r.Coverage.PausedSeconds != 3*3600 || r.LifecycleHistoryFrom == nil ||
		*r.LifecycleHistoryFrom != stamp(resumed) {
		t.Fatalf("retained resume must preserve window's paused time: %+v", r)
	}
}
