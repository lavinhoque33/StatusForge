package store

import (
	"context"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func dailyTestStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").DynamoDB(),
		"statusforge_m5a_test_"+rand.Text(),
		time.Second,
		func() time.Time { return now },
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := s.db.(*dynamodb.Client).DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); e != nil {
			t.Errorf("delete isolated table: %v", e)
		}
	})
	return s
}

func dailyMonitor(now time.Time) monitor.Monitor {
	m := monitor.New(
		"Example",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/healthy",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
		},
		now,
	)
	m.IntervalSeconds = 10
	return m
}

func TestLifecycleHistoryAtomic(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	m := dailyMonitor(now)
	if e := s.Create(t.Context(), m); e != nil {
		t.Fatal(e)
	}
	events, e := s.lifecycleWindow(t.Context(), m.ID, now.Add(-time.Hour), now.Add(time.Hour))
	if e != nil || len(events) != 1 || events[0].Action != "created" {
		t.Fatalf("create events: %+v %v", events, e)
	}
	if _, e = s.Lifecycle(t.Context(), m.ID, "resume", now.Add(time.Second)); e != ErrInvalidTransition {
		t.Fatalf("invalid transition: %v", e)
	}
	events, _ = s.lifecycleWindow(t.Context(), m.ID, now.Add(-time.Hour), now.Add(time.Hour))
	if len(events) != 1 {
		t.Fatalf("invalid transition wrote event: %+v", events)
	}
	next, _ := m.Transition("pause", now.Add(time.Second))
	if e := s.save(t.Context(), next, m, "lifecycle = :from",
		map[string]types.AttributeValue{":from": mustAV("paused")}, "", now.Add(time.Second)); e != ErrVersionConflict {
		t.Fatalf("conditional transition: %v", e)
	}
	events, _ = s.lifecycleWindow(t.Context(), m.ID, now.Add(-time.Hour), now.Add(time.Hour))
	current, e := s.Get(t.Context(), m.ID)
	if e != nil || len(events) != 1 || current.Lifecycle != "active" {
		t.Fatalf(
			"failed conditional change must write neither monitor nor event: %+v %+v %v",
			current,
			events,
			e,
		)
	}
	if _, e = s.Lifecycle(t.Context(), m.ID, "pause", now.Add(2*time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Lifecycle(t.Context(), m.ID, "pause", now.Add(3*time.Second)); e != ErrInvalidTransition {
		t.Fatalf("duplicate pause: %v", e)
	}
	if _, e = s.Lifecycle(t.Context(), m.ID, "resume", now.Add(4*time.Second)); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Lifecycle(t.Context(), m.ID, "archive", now.Add(5*time.Second)); e != nil {
		t.Fatal(e)
	}
	events, e = s.lifecycleWindow(t.Context(), m.ID, now.Add(-time.Hour), now.Add(time.Hour))
	if e != nil || len(events) != 4 {
		t.Fatalf("events: %+v %v", events, e)
	}
	for i, action := range []string{"created", "paused", "resumed", "archived"} {
		if events[i].Action != action {
			t.Fatalf("event %d: %+v", i, events[i])
		}
	}
}

func TestSummaryWindowBound(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	m := dailyMonitor(now.Add(-time.Hour))
	if e := s.Create(t.Context(), m); e != nil {
		t.Fatal(e)
	}
	for i := range 4 {
		due := monitor.Stamp(now.Add(-time.Duration(i+1) * 10 * time.Second))
		o := monitor.Observation{
			ID:          rand.Text(),
			MonitorID:   m.ID,
			DueAt:       &due,
			InitiatedBy: "scheduled",
			StartedAt:   due,
			CompletedAt: due,
			Outcome:     "healthy",
			Reason:      "ok",
			Counted:     true,
		}
		if e := s.PutObservation(t.Context(), o); e != nil {
			t.Fatal(e)
		}
	}
	items, truncated, e := s.windowQuery(
		t.Context(),
		"MON#"+m.ID,
		"OBS#",
		now.Add(-time.Hour),
		now,
		2,
		"dueAt, startedAt, expiresAt, PK, SK",
	)
	if e != nil || !truncated || len(items) != 2 {
		t.Fatalf("bounded result: %d %v %v", len(items), truncated, e)
	}
	result, e := s.Summary(t.Context(), m, "24h", now)
	if e != nil || result.Coverage.Recorded != 4 || result.Truncated {
		t.Fatalf("summary: %+v %v", result.Coverage, e)
	}
	partial, e := s.summaryWithBounds(t.Context(), m, "24h", now, 2, 2)
	if e != nil || !partial.Truncated || partial.Coverage.Recorded != 2 ||
		partial.CoveredFrom != monitor.Stamp(now.Add(-20*time.Second)) {
		t.Fatalf("partial: %+v %v", partial.Coverage, e)
	}
}

func TestHeartbeatSummaryFailureFromPersistedReason(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	m := dailyMonitor(now.Add(-time.Hour))
	m.Kind = "heartbeat"
	m.Heartbeat = &heartbeat.Configuration{
		Schedule: heartbeat.Schedule{IntervalSeconds: 10, GraceSeconds: 5},
	}
	if err := s.Create(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	at := monitor.Stamp(now.Add(-time.Second))
	obs := monitor.Observation{
		ID: "failure", MonitorID: m.ID, Kind: "heartbeat_report",
		StartedAt: at, CompletedAt: at, Counted: true, Outcome: "failing",
		Reason: "reported_failure", Report: &heartbeat.Report{Status: "failure"},
	}
	if err := s.PutObservation(t.Context(), obs); err != nil {
		t.Fatal(err)
	}
	result, err := s.HeartbeatSummary(t.Context(), m, "24h", now)
	if err != nil || result.Coverage.Recorded != 1 ||
		result.Coverage.Outcomes.OnTime != 1 || result.Coverage.Outcomes.FailureReports != 1 {
		t.Fatalf("persisted failure summary: %+v %v", result.Coverage, err)
	}
}
