package incident

import (
	"testing"
	"time"
)

func TestMaintenanceClassification(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	windows := []WindowRef{
		{
			WindowID: "past",
			StartAt:  now.Add(-time.Hour).Format(time.RFC3339),
			EndAt:    now.Format(time.RFC3339),
		},
		{
			WindowID: "current",
			StartAt:  now.Format(time.RFC3339),
			EndAt:    now.Add(time.Hour).Format(time.RFC3339),
		},
		{
			WindowID: "future",
			StartAt:  now.Add(time.Hour).Format(time.RFC3339),
			EndAt:    now.Add(2 * time.Hour).Format(time.RFC3339),
		},
	}
	if Active(windows, now) != "current" || Active(windows, now.Add(time.Hour)) != "future" ||
		Active(windows, now.Add(2*time.Hour)) != "" {
		t.Fatal("half-open active classification")
	}
	if Next(windows, now) != "future" || Next(windows, now.Add(time.Hour)) != "" {
		t.Fatal("future classification")
	}
	live := Prune(windows, now)
	if len(live) != 2 || Overlaps(live, now.Add(-time.Hour), now) ||
		!Overlaps(live, now.Add(time.Hour-time.Nanosecond), now.Add(time.Hour)) {
		t.Fatal("expired/adjacent overlap classification")
	}
	if len(Prune(windows, now.Add(2*time.Hour))) != 0 {
		t.Fatal("ended windows count toward limit")
	}
}

func TestMaintenanceEvidenceDoesNotTransition(t *testing.T) {
	at := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	id := "window"
	p := Policy{OpenAfter: 2, RecoverAfter: 2}
	failing := Evidence{Outcome: "failing"}
	healthy := Evidence{Outcome: "healthy"}
	labelled := Evidence{Outcome: "failing", MaintenanceWindowID: &id}
	e, _ := Evaluate(p, Evaluation{}, nil, failing, at)
	n, tr := Evaluate(p, e, nil, labelled, at)
	if tr != nil || len(n.FailRun) != 1 {
		t.Fatal("labelled failure opened or changed run")
	}
	open := &Open{ID: "incident"}
	e, _ = Evaluate(p, Evaluation{}, open, healthy, at)
	labelled.Outcome = "healthy"
	n, tr = Evaluate(p, e, open, labelled, at)
	if tr == nil || tr.Kind != "maintenance" || len(n.HealthyRun) != 1 {
		t.Fatal("labelled health resolved or changed run")
	}
}
