package monitor

import (
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
)

func TestHeartbeatStatusBoundary(t *testing.T) {
	heartbeat.SetLivenessInterval(2)
	t.Cleanup(func() { heartbeat.SetLivenessInterval(10) })
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	m := New("job", Check{}, start)
	m.Kind = "heartbeat"
	m.Heartbeat = &heartbeat.Configuration{
		Schedule: heartbeat.Schedule{IntervalSeconds: 15, GraceSeconds: 5},
	}
	expect := heartbeat.Expect(start, m.Heartbeat.Schedule)
	m.Expectation = &expect
	due := start.Add(15 * time.Second)
	missing := due.Add(5 * time.Second)
	cases := []struct {
		now           time.Time
		state, reason string
	}{{due, "unknown", "waiting_for_first_report"}, {due.Add(time.Millisecond), "late", "waiting_for_first_report"}, {missing, "late", "waiting_for_first_report"}, {missing.Add(time.Millisecond), "late", ""}, {missing.Add(10*time.Second + 999*time.Millisecond), "late", ""}, {missing.Add(11 * time.Second), "stale", ""}}
	for _, tt := range cases {
		status := Evaluate(m, tt.now)
		reason := ""
		if status.Reason != nil {
			reason = *status.Reason
		}
		if status.State != tt.state || reason != tt.reason {
			t.Errorf("%v: state %s reason %s", tt.now, status.State, reason)
		}
	}
	m.Evidence = &Evidence{
		Outcome:     "failing",
		Reason:      "missing",
		Observation: Observation{Kind: "heartbeat_missed"},
	}
	if state := Evaluate(m, missing.Add(time.Hour)).State; state != "failing" {
		t.Errorf("missing evidence became %s", state)
	}
	m.Lifecycle = "paused"
	if state := Evaluate(m, missing.Add(time.Hour)).State; state != "paused" {
		t.Errorf("paused became %s", state)
	}
}
