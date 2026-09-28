package store

import (
	"context"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func incidentTestStore(t *testing.T) *Store {
	t.Helper()
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local"),
		"statusforge_incident_test_"+rand.Text(),
		time.Second,
		time.Now,
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := s.db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); e != nil {
			t.Errorf("delete table: %v", e)
		}
	})
	return s
}

func TestIncidentOutboxAndRecovery(t *testing.T) {
	ctx := t.Context()
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
	if e := s.Create(ctx, m); e != nil {
		t.Fatal(e)
	}
	record := func(outcome string, at time.Time) {
		t.Helper()
		_, token, e := s.ClaimManual(ctx, m.ID, at)
		if e != nil {
			t.Fatalf("claim: %v", e)
		}
		o := monitor.Observation{
			ID:            rand.Text(),
			MonitorID:     m.ID,
			ConfigVersion: 1,
			InitiatedBy:   "manual",
			Request:       m.Check,
			StartedAt:     monitor.Stamp(at),
			CompletedAt:   monitor.Stamp(at),
			Outcome:       outcome,
			Reason:        "status",
		}
		saved, e := s.RecordResult(ctx, o, token)
		if e != nil || !saved.Counted {
			t.Fatalf("record: %+v %v", saved, e)
		}
	}
	record("failing", now.Add(100*time.Millisecond))
	record("failing", now.Add(200*time.Millisecond))
	record("failing", now.Add(300*time.Millisecond))
	list, e := s.ListIncidents(ctx, m.ID, "open", 10, time.Now())
	if e != nil || len(list) != 1 || list[0].FailureCount != 3 {
		t.Fatalf("incident open: %+v %v", list, e)
	}
	due, e := s.Due(ctx, time.Now())
	if e != nil || len(due) != 1 || due[0].NoteKey != "opened" {
		t.Fatalf("outbox: %+v %v", due, e)
	}
	n, a, token, e := s.ClaimDelivery(ctx, due[0], time.Now())
	if e != nil {
		t.Fatal(e)
	}
	state, e := s.CompleteDelivery(
		ctx,
		due[0],
		n,
		a,
		token,
		"delivered",
		nil,
		1,
		time.Now(),
		[]time.Duration{time.Second},
	)
	if e != nil || state != "delivered" {
		t.Fatalf("delivery: %s %v", state, e)
	}
	record("healthy", now.Add(400*time.Millisecond))
	record("healthy", now.Add(500*time.Millisecond))
	in, events, _, notes, e := s.IncidentDetail(ctx, m.ID, list[0].ID, time.Now())
	if e != nil || in.State != "resolved" || len(events) != 2 || len(notes) != 2 {
		t.Fatalf("resolution: %+v %+v %+v %v", in, events, notes, e)
	}
	if len(notes[0].Attempts) != 1 ||
		in.NotificationSummary != (Summary{Delivered: 1, Pending: 1}) {
		t.Fatalf("attempts inflated detail summary: %+v %+v", notes, in.NotificationSummary)
	}
	perMonitor, e := s.ListIncidents(ctx, m.ID, "resolved", 10, time.Now())
	if e != nil || len(perMonitor) != 1 ||
		perMonitor[0].NotificationSummary != in.NotificationSummary {
		t.Fatalf(
			"per-monitor list disagrees with detail: %+v %+v %v",
			perMonitor,
			in.NotificationSummary,
			e,
		)
	}
	across, e := s.ListIncidents(ctx, "", "resolved", 10, time.Now())
	if e != nil || len(across) != 1 || across[0].NotificationSummary != in.NotificationSummary {
		t.Fatalf(
			"cross-monitor list disagrees with detail: %+v %+v %v",
			across,
			in.NotificationSummary,
			e,
		)
	}
}

func TestIncidentIDIncludesMilliseconds(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 15, 30, 123000000, time.UTC)
	id := incidentID(now)
	if len(id) != 26 || id[:19] != "20260928T101530123Z" {
		t.Fatalf("unexpected time-ordered ID %q", id)
	}
}
