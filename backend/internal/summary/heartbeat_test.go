package summary

import (
	"testing"
	"time"
)

func TestHeartbeatDeadlineAccounting(t *testing.T) {
	to := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	from := to.Add(-24 * time.Hour)
	at := func(d time.Duration) string { return stamp(from.Add(d)) }
	ptr := func(v string) *string { return &v }
	maint := "window"
	paused := "paused"
	old := "older_than_current"
	cases := []struct {
		name                                                                        string
		input                                                                       Input
		expected, recorded, missed, late, onTime, failures, maintenance, notCounted int
	}{
		{
			name: "report resets deadline; late boundary and failure",
			input: Input{Observations: []Observation{
				{Kind: "heartbeat_report", StartedAt: at(time.Minute), Counted: true},
				{
					Kind:          "heartbeat_report",
					StartedAt:     at(2 * time.Minute),
					Counted:       true,
					Late:          true,
					FailureReport: true,
					Reason:        "reported_failure",
					Outcome:       "failing",
				},
				{
					Kind:      "heartbeat_missed",
					DueAt:     ptr(at(3 * time.Minute)),
					StartedAt: at(3*time.Minute + 5*time.Second),
					Counted:   true,
				},
				{
					Kind:      "heartbeat_report",
					StartedAt: at(3*time.Minute + 6*time.Second),
					Counted:   true,
				},
			}},
			expected: 4,
			recorded: 4,
			missed:   1,
			late:     1,
			onTime:   2,
			failures: 1,
		},
		{
			name: "not counted reports are evidence but not deadline slots",
			input: Input{Observations: []Observation{
				{Kind: "heartbeat_report", StartedAt: at(time.Minute), NotCountedReason: &paused},
				{Kind: "heartbeat_report", StartedAt: at(2 * time.Minute), NotCountedReason: &old},
				{
					Kind:                "heartbeat_report",
					StartedAt:           at(3 * time.Minute),
					Counted:             true,
					MaintenanceWindowID: &maint,
				},
				{
					Kind:      "heartbeat_missed",
					DueAt:     ptr(at(4 * time.Minute)),
					StartedAt: at(4*time.Minute + 5*time.Second),
					Counted:   true,
				},
			}},
			expected:    2,
			recorded:    2,
			maintenance: 1,
			missed:      1,
			notCounted:  2,
		},
		{
			name: "gap straddles window and covered edge",
			input: Input{
				Gaps: []Gap{
					{FromDueAt: at(-time.Minute), ToDueAt: at(time.Minute), MissedCount: 3},
				},
				Observations: []Observation{
					{Kind: "heartbeat_report", StartedAt: at(2 * time.Minute), Counted: true},
				},
			},
			expected: 3,
			recorded: 1,
			onTime:   1,
		},
		{
			name: "truncated gap and paused span",
			input: Input{
				Truncated:   true,
				CoveredFrom: from.Add(time.Hour),
				Lifecycle:   "paused",
				PausedAt:    at(30 * time.Minute),
				Gaps: []Gap{
					{
						FromDueAt:   at(30 * time.Minute),
						ToDueAt:     at(90 * time.Minute),
						MissedCount: 3,
					},
				},
			},
			expected: 2,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := tc.input
			in.To = to
			in.Window = "24h"
			in.MonitorID = "heartbeat"
			r := ComputeHeartbeat(in)
			c := r.Coverage
			if c.Expected != tc.expected || c.Recorded != tc.recorded ||
				c.Outcomes.Missed != tc.missed ||
				c.Outcomes.Late != tc.late ||
				c.Outcomes.OnTime != tc.onTime ||
				c.Outcomes.FailureReports != tc.failures ||
				c.Maintenance != tc.maintenance ||
				c.NotCounted != tc.notCounted {
				t.Fatalf("counts %+v expected %+v", c, tc)
			}
			if c.Expected != c.Recorded+c.NotObserved ||
				c.Outcomes.OnTime+c.Outcomes.Late+c.Outcomes.Missed != c.Recorded-c.Maintenance ||
				c.Outcomes.FailureReports > c.Outcomes.OnTime+c.Outcomes.Late {
				t.Fatalf("invariant %+v", c)
			}
			var sum HeartbeatBucket
			for _, b := range r.Buckets {
				sum.Expected += b.Expected
				sum.Recorded += b.Recorded
				sum.NotObserved += b.NotObserved
				sum.OnTime += b.OnTime
				sum.Late += b.Late
				sum.Missed += b.Missed
				sum.FailureReports += b.FailureReports
				sum.Maintenance += b.Maintenance
				sum.NotCounted += b.NotCounted
			}
			if sum.Expected != c.Expected || sum.Recorded != c.Recorded ||
				sum.NotObserved != c.NotObserved ||
				sum.OnTime != c.Outcomes.OnTime ||
				sum.Late != c.Outcomes.Late ||
				sum.Missed != c.Outcomes.Missed ||
				sum.FailureReports != c.Outcomes.FailureReports ||
				sum.Maintenance != c.Maintenance ||
				sum.NotCounted != c.NotCounted {
				t.Fatalf("buckets %+v coverage %+v", sum, c)
			}
			if r.Latency != nil || r.Kind != "heartbeat" {
				t.Fatalf("envelope %+v", r)
			}
		})
	}
}
