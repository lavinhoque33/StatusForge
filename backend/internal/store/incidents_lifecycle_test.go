package store

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestGapClearsEvaluationRuns(t *testing.T) {
	s, m, _ := incidentFixture(t)
	at := time.Now().UTC()
	_, token, e := s.ClaimManual(t.Context(), m.ID, at)
	if e != nil {
		t.Fatal(e)
	}
	o := monitor.Observation{
		ID:            rand.Text(),
		MonitorID:     m.ID,
		ConfigVersion: 1,
		InitiatedBy:   "manual",
		Request:       m.Check,
		StartedAt:     monitor.Stamp(at),
		CompletedAt:   monitor.Stamp(at),
		Outcome:       "healthy",
		Reason:        "ok",
	}
	if _, e = s.RecordResult(t.Context(), o, token); e != nil {
		t.Fatal(e)
	}
	before, e := s.Get(t.Context(), m.ID)
	if e != nil || len(before.Evaluation.HealthyRun) != 1 {
		t.Fatalf("pre-gap: %+v %v", before.Evaluation, e)
	}
	_, _, gaps, e := s.Tick(t.Context(), at.Add(30*time.Minute))
	if e != nil || gaps == 0 {
		t.Fatalf("tick gap: %d %v", gaps, e)
	}
	after, e := s.Get(t.Context(), m.ID)
	if e != nil || len(after.Evaluation.HealthyRun) != 0 ||
		after.Evaluation.Revision <= before.Evaluation.Revision {
		t.Fatalf("gap run reset: %+v %v", after.Evaluation, e)
	}
}

func TestArchiveClosesIncidentWithResolvedIntent(t *testing.T) {
	s, m, opened := incidentFixture(t)
	current, e := s.Get(t.Context(), m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Reminder(t.Context(), current, opened.Add(7*time.Hour), time.Hour); e != nil {
		t.Fatal(e)
	}
	archived, e := s.Lifecycle(t.Context(), m.ID, "archive", time.Now())
	if e != nil || archived.Lifecycle != "archived" || archived.OpenIncident != nil {
		t.Fatalf("archive: %+v %v", archived, e)
	}
	items, e := s.ListIncidents(t.Context(), m.ID, "resolved", 10, time.Now())
	if e != nil || len(items) != 1 || items[0].Resolution == nil ||
		*items[0].Resolution != "archived" {
		t.Fatalf("archive resolution: %+v %v", items, e)
	}
	in, events, _, notes, e := s.IncidentDetail(t.Context(), m.ID, items[0].ID, time.Now())
	if e != nil || in.State != "resolved" || len(events) != 2 || len(notes) != 3 ||
		notes[2].Kind != "resolved" || notes[1].State != "cancelled" {
		t.Fatalf("archive evidence: %+v %+v %+v %v", in, events, notes, e)
	}
}

func TestPauseResumeRecordsIncidentEvents(t *testing.T) {
	s, m, _ := incidentFixture(t)
	opened, e := s.Get(t.Context(), m.ID)
	if e != nil || opened.OpenIncident == nil {
		t.Fatalf("open incident: %+v %v", opened, e)
	}
	at := time.Now().UTC()
	paused, e := s.Lifecycle(t.Context(), m.ID, "pause", at)
	if e != nil || paused.OpenIncident == nil {
		t.Fatalf("pause lost incident: %+v %v", paused, e)
	}
	resumed, e := s.Lifecycle(t.Context(), m.ID, "resume", at.Add(time.Millisecond))
	if e != nil || resumed.OpenIncident == nil {
		t.Fatalf("resume lost incident: %+v %v", resumed, e)
	}
	in, events, _, _, e := s.IncidentDetail(
		t.Context(),
		m.ID,
		opened.OpenIncident.ID,
		at.Add(time.Second),
	)
	if e != nil || in.State != "open" || len(events) != 3 || events[1].Type != "paused" ||
		events[2].Type != "resumed" {
		t.Fatalf("pause/resume timeline: %+v %+v %v", in, events, e)
	}
}

func TestUndeliveredReminderCancelledAfterResolution(t *testing.T) {
	s, m, opened := incidentFixture(t)
	current, e := s.Get(t.Context(), m.ID)
	if e != nil {
		t.Fatal(e)
	}
	dueAt := opened.Add(7 * time.Hour)
	if e = s.Reminder(t.Context(), current, dueAt, time.Hour); e != nil {
		t.Fatal(e)
	}
	// Leave one reminder waiting for a far-future retry and another pending.
	openedDue, e := s.Due(t.Context(), time.Now())
	if e != nil || len(openedDue) != 1 {
		t.Fatalf("opened due: %+v %v", openedDue, e)
	}
	openedNote, openedAttempt, openedToken, e := s.ClaimDelivery(
		t.Context(),
		openedDue[0],
		time.Now(),
	)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteDelivery(t.Context(), openedDue[0], openedNote, openedAttempt, openedToken, "delivered", nil, 1, time.Now(), nil); e != nil {
		t.Fatal(e)
	}
	reminderDue, e := s.Due(t.Context(), dueAt.Add(time.Second))
	if e != nil || len(reminderDue) != 1 {
		t.Fatalf("first reminder due: %+v %v", reminderDue, e)
	}
	first, attempt, token, e := s.ClaimDelivery(t.Context(), reminderDue[0], dueAt.Add(time.Second))
	if e != nil {
		t.Fatal(e)
	}
	if state, completeErr := s.CompleteDelivery(t.Context(), reminderDue[0], first, attempt, token, "http_error", nil, 1, dueAt.Add(time.Second), []time.Duration{30 * time.Minute}); completeErr != nil ||
		state != "retry_wait" {
		t.Fatalf("retry_wait: %s %v", state, completeErr)
	}
	next, e := s.Get(t.Context(), m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Reminder(t.Context(), next, dueAt.Add(2*time.Hour), time.Hour); e != nil {
		t.Fatal(e)
	}
	retry, e := s.notification(t.Context(), m.ID, current.OpenIncident.ID, "reminder#0001")
	if e != nil || retry.NextAttemptAt == nil || *retry.NextAttemptAt <= monitor.Stamp(dueAt) {
		t.Fatalf("future retry: %+v %v", retry, e)
	}
	for i := range 2 {
		at := time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
		_, token, claimErr := s.ClaimManual(t.Context(), m.ID, at)
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		o := monitor.Observation{
			ID:            rand.Text(),
			MonitorID:     m.ID,
			ConfigVersion: 1,
			InitiatedBy:   "manual",
			Request:       m.Check,
			StartedAt:     monitor.Stamp(at),
			CompletedAt:   monitor.Stamp(at),
			Outcome:       "healthy",
			Reason:        "ok",
		}
		if _, claimErr = s.RecordResult(t.Context(), o, token); claimErr != nil {
			t.Fatal(claimErr)
		}
	}
	for _, nk := range []string{"reminder#0001", "reminder#0002"} {
		n, readErr := s.notification(t.Context(), m.ID, current.OpenIncident.ID, nk)
		if readErr != nil || n.State != "cancelled" || n.CancelledReason == nil ||
			*n.CancelledReason != "incident_resolved" ||
			n.NextAttemptAt != nil {
			t.Fatalf("%s not cancelled immediately after resolution: %+v %v", nk, n, readErr)
		}
	}
	due, e := s.Due(t.Context(), dueAt.Add(3*time.Hour))
	if e != nil {
		t.Fatal(e)
	}
	for _, d := range due {
		if d.NoteKey == "reminder#0001" || d.NoteKey == "reminder#0002" {
			t.Fatalf("cancelled reminder retained due pointer: %+v", due)
		}
	}
}
