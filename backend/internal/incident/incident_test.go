package incident

import (
	"testing"
	"time"
)

func TestEvaluateTransitions(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	p := Policy{OpenAfter: 2, RecoverAfter: 2}
	e := Evaluation{}
	f := Evidence{
		ObservationID: "one",
		StartedAt:     "2026-09-28T10:00:00.000Z",
		InitiatedBy:   "manual",
		Outcome:       "failing",
		Reason:        "wrong_status",
		ConfigVersion: 1,
	}
	e, tr := Evaluate(p, e, nil, f, now)
	if tr != nil || len(e.FailRun) != 1 {
		t.Fatalf("first failure: %+v %+v", e, tr)
	}
	problem := f
	problem.Outcome = "checker_problem"
	e, tr = Evaluate(p, e, nil, problem, now)
	if tr != nil || len(e.FailRun) != 1 {
		t.Fatalf("checker problem reset run: %+v", e)
	}
	f.ObservationID = "two"
	e, tr = Evaluate(p, e, nil, f, now)
	if tr == nil || tr.Kind != "opened" || len(tr.Evidence) != 2 ||
		tr.Evidence[0].InitiatedBy != "manual" {
		t.Fatalf("opening: %+v", tr)
	}
	op := &Open{ID: "incident"}
	f.ObservationID = "three"
	e, tr = Evaluate(p, e, op, f, now)
	if tr == nil || tr.Kind != "extend" || len(e.FailRun) != 0 {
		t.Fatalf("extension: %+v %+v", e, tr)
	}
	problem.ObservationID = "four"
	e, tr = Evaluate(p, e, op, problem, now)
	if tr == nil || tr.Kind != "checker_problem" {
		t.Fatalf("problem: %+v", tr)
	}
	healthy := f
	healthy.Outcome = "healthy"
	healthy.ObservationID = "five"
	e, tr = Evaluate(p, e, op, healthy, now)
	if tr != nil || len(e.HealthyRun) != 1 {
		t.Fatalf("first healthy: %+v %+v", e, tr)
	}
	revision := e.Revision
	e = Clear(e)
	if len(e.HealthyRun) != 0 || e.Revision != revision+1 {
		t.Fatalf("gap clearing: %+v", e)
	}
	e, tr = Evaluate(p, e, op, healthy, now)
	healthy.ObservationID = "six"
	e, tr = Evaluate(p, e, op, healthy, now)
	if tr == nil || tr.Kind != "resolved" || len(tr.Evidence) != 2 {
		t.Fatalf("recovery: %+v", tr)
	}
}

func TestReminderSlotsSkipDowntime(t *testing.T) {
	start := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct{ after, expected time.Duration }{{0, time.Minute}, {time.Minute, 2 * time.Minute}, {3*time.Minute + time.Second, 4 * time.Minute}} {
		got := NextSlot(start, time.Minute, start.Add(tc.after))
		if want := start.Add(tc.expected); !got.Equal(want) {
			t.Fatalf("after %s: got %s want %s", tc.after, got, want)
		}
	}
}

func TestRetryScheduleBoundaries(t *testing.T) {
	start := time.Now()
	schedule := []time.Duration{time.Second, 2 * time.Second}
	for _, tc := range []struct {
		attempt int
		ok      bool
		delay   time.Duration
	}{{1, true, time.Second}, {2, true, 2 * time.Second}, {3, false, 0}} {
		at, ok := RetryAt(start, tc.attempt, schedule)
		if ok != tc.ok || ok && at.Sub(start) != tc.delay {
			t.Fatalf("attempt %d: %s %t", tc.attempt, at, ok)
		}
	}
}
