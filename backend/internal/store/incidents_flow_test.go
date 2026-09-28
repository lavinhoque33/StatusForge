package store

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func incidentFixture(t *testing.T) (*Store, monitor.Monitor, time.Time) {
	t.Helper()
	s := incidentTestStore(t)
	now := time.Now().UTC().Add(-time.Second)
	m := monitor.New(
		"fixture",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
			MaxBodyBytes:   monitor.MaxBodyBytes,
		},
		now,
	)
	if e := s.Create(t.Context(), m); e != nil {
		t.Fatal(e)
	}
	for i := range 2 {
		at := now.Add(time.Duration(i+1) * 100 * time.Millisecond)
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
			Outcome:       "failing",
			Reason:        "wrong_status",
		}
		if _, e = s.RecordResult(t.Context(), o, token); e != nil {
			t.Fatal(e)
		}
	}
	return s, m, now
}

func TestDeliveryTakeoverAndManualRetry(t *testing.T) {
	s, m, _ := incidentFixture(t)
	ctx := context.Background()
	due, e := s.Due(ctx, time.Now())
	if e != nil || len(due) != 1 {
		t.Fatalf("due: %+v %v", due, e)
	}
	n, a, token, e := s.ClaimDelivery(ctx, due[0], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, _, _, e = s.ClaimDelivery(ctx, due[0], time.Now()); !errors.Is(e, ErrNotEligible) {
		t.Fatalf("overlapping claim: %v", e)
	}
	later := time.Now().Add(16 * time.Second)
	n, a2, token2, e := s.ClaimDelivery(ctx, due[0], later)
	if e != nil || a2.Number != 2 || token == token2 {
		t.Fatalf("takeover: %+v %v", a2, e)
	}
	if _, e = s.CompleteDelivery(ctx, due[0], n, a, token, "delivered", nil, 1, later, nil); !errors.Is(
		e,
		ErrNotEligible,
	) {
		t.Fatalf("stale completion: %v", e)
	}
	state, e := s.CompleteDelivery(ctx, due[0], n, a2, token2, "rejected", nil, 1, later, nil)
	if e != nil || state != "failed" {
		t.Fatalf("rejected: %s %v", state, e)
	}
	detail, _, _, notes, e := s.IncidentDetail(ctx, m.ID, due[0].IncidentID, later)
	if e != nil || detail.State != "open" || len(notes) != 1 || len(notes[0].Attempts) != 2 ||
		notes[0].Attempts[0].Result != "process_stopped" {
		t.Fatalf("takeover evidence: %+v %+v %v", detail, notes, e)
	}
	attention, e := s.Attention(ctx, 10)
	if e != nil || len(attention) != 1 {
		t.Fatalf("attention: %+v %v", attention, e)
	}
	retry, e := s.RetryNotification(ctx, m.ID, due[0].IncidentID, "opened", later)
	if e != nil || retry.State != "pending" {
		t.Fatalf("retry: %+v %v", retry, e)
	}
	if _, e = s.RetryNotification(ctx, m.ID, due[0].IncidentID, "opened", later); !errors.Is(
		e,
		ErrNotFailed,
	) {
		t.Fatalf("duplicate retry: %v", e)
	}
	due, e = s.Due(ctx, later)
	if e != nil || len(due) != 1 {
		t.Fatalf("manual due: %+v %v", due, e)
	}
	n, a, token, e = s.ClaimDelivery(ctx, due[0], later)
	if e != nil || !a.Manual {
		t.Fatalf("manual claim: %+v %v", a, e)
	}
	state, e = s.CompleteDelivery(
		ctx,
		due[0],
		n,
		a,
		token,
		"http_error",
		nil,
		1,
		later,
		[]time.Duration{time.Second, time.Second, time.Second},
	)
	if e != nil || state != "failed" {
		t.Fatalf("one manual attempt: %s %v", state, e)
	}
}

func TestReminderCollapsesDowntime(t *testing.T) {
	s, m, now := incidentFixture(t)
	s.SetReminderInterval(time.Minute)
	current, e := s.Get(t.Context(), m.ID)
	if e != nil {
		t.Fatal(e)
	}
	dueAt := now.Add(7 * time.Hour)
	if e = s.Reminder(t.Context(), current, dueAt, time.Minute); e != nil {
		t.Fatal(e)
	}
	if e = s.Reminder(t.Context(), current, dueAt, time.Minute); !errors.Is(e, ErrNotEligible) {
		t.Fatalf("duplicate reminder: %v", e)
	}
	updated, e := s.Get(t.Context(), m.ID)
	if e != nil || updated.OpenIncident.ReminderSeq != 1 {
		t.Fatalf("reminder seq: %+v %v", updated.OpenIncident, e)
	}
	next, _ := time.Parse(time.RFC3339Nano, updated.OpenIncident.NextReminderAt)
	if !next.After(dueAt) {
		t.Fatalf("next slot %s is not after %s", next, dueAt)
	}
}

func TestResolvedDeliveryWaitsForOpened(t *testing.T) {
	s, m, _ := incidentFixture(t)
	for i := range 2 {
		at := time.Now().UTC().Add(time.Duration(i) * time.Millisecond)
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
	}
	due, e := s.Due(t.Context(), time.Now().Add(time.Second))
	if e != nil || len(due) != 2 {
		t.Fatalf("delivery obligations: %+v %v", due, e)
	}
	if _, _, _, e = s.ClaimDelivery(t.Context(), due[1], time.Now().Add(time.Second)); !errors.Is(
		e,
		ErrNotEligible,
	) {
		t.Fatalf("resolved delivered before opened: %v", e)
	}
	n, a, token, e := s.ClaimDelivery(t.Context(), due[0], time.Now().Add(time.Second))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.CompleteDelivery(t.Context(), due[0], n, a, token, "delivered", nil, 1, time.Now(), nil); e != nil {
		t.Fatal(e)
	}
	_, _, _, e = s.ClaimDelivery(t.Context(), due[1], time.Now().Add(time.Second))
	if e != nil {
		t.Fatalf("resolved blocked after opened delivered: %v", e)
	}
}
