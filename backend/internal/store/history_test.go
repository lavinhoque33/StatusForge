package store

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestHistoryFilteredCursorSurvivesNewHead(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	m := dailyMonitor(now.Add(-time.Hour))
	if err := s.Create(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	insert := func(i int, outcome string) {
		t.Helper()
		stamp := monitor.Stamp(now.Add(time.Duration(i) * time.Second))
		o := monitor.Observation{
			ID:          rand.Text(),
			MonitorID:   m.ID,
			StartedAt:   stamp,
			CompletedAt: stamp,
			Kind:        "heartbeat_report",
			InitiatedBy: "job",
			Outcome:     outcome,
			Counted:     true,
		}
		if err := s.PutObservation(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 8; i++ {
		outcome := "healthy"
		if i%2 == 0 {
			outcome = "failing"
		}
		insert(-i, outcome)
	}
	filter := HistoryFilter{Outcomes: []string{"failing"}}
	first, err := s.HistoryObservations(t.Context(), m.ID, 2, filter)
	if err != nil || len(first.Items) != 2 || first.NextCursor == nil ||
		first.SearchedThrough == nil ||
		first.Items[0].StartedAt != monitor.Stamp(now.Add(-2*time.Second)) ||
		first.Items[1].StartedAt != monitor.Stamp(now.Add(-4*time.Second)) {
		t.Fatalf("first %+v %v", first, err)
	}
	insert(1, "failing")
	filter.Before = *first.NextCursor
	second, err := s.HistoryObservations(t.Context(), m.ID, 2, filter)
	if err != nil || len(second.Items) != 2 ||
		second.Items[0].StartedAt != monitor.Stamp(now.Add(-6*time.Second)) ||
		second.Items[1].StartedAt != monitor.Stamp(now.Add(-8*time.Second)) {
		t.Fatalf("second %+v %v", second, err)
	}
	if _, err = s.HistoryObservations(t.Context(), "another", 2, filter); err != ErrInvalidCursor {
		t.Fatalf("foreign cursor %v", err)
	}
	if _, err = s.HistoryGaps(t.Context(), m.ID, 2, filter.Before); err != ErrInvalidCursor {
		t.Fatalf("wrong prefix %v", err)
	}
	if _, err = s.HistoryObservations(t.Context(), m.ID, 2, HistoryFilter{Before: "not-base64"}); err != ErrInvalidCursor {
		t.Fatalf("malformed cursor %v", err)
	}
}

func TestGapHistoryCursorSurvivesNewHead(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	m := dailyMonitor(now.Add(-time.Hour))
	if err := s.Create(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	insert := func(i int) {
		t.Helper()
		at := monitor.Stamp(now.Add(time.Duration(i) * time.Second))
		g := monitor.Gap{
			ID: rand.Text(), MonitorID: m.ID, FromDueAt: at,
			ToDueAt: at, MissedCount: 1, Reason: "not_observed", RecordedAt: monitor.Stamp(now),
		}
		item, err := gapItem(g)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.PutItem(t.Context(), &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 3; i++ {
		insert(-i)
	}
	first, err := s.HistoryGaps(t.Context(), m.ID, 1, "")
	if err != nil || len(first.Items) != 1 || first.NextCursor == nil {
		t.Fatalf("first %+v %v", first, err)
	}
	insert(1)
	second, err := s.HistoryGaps(t.Context(), m.ID, 2, *first.NextCursor)
	if err != nil || len(second.Items) != 2 ||
		second.Items[0].FromDueAt != monitor.Stamp(now.Add(-2*time.Second)) ||
		second.Items[1].FromDueAt != monitor.Stamp(now.Add(-3*time.Second)) {
		t.Fatalf("second %+v %v", second, err)
	}
}

func TestSparseHistoryScanBound(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := dailyTestStore(t, now)
	m := dailyMonitor(now.Add(-time.Hour))
	if err := s.Create(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	for i := range 100 {
		stamp := monitor.Stamp(now.Add(-time.Duration(i+1) * time.Second))
		outcome := "healthy"
		if i == 49 || i == 99 {
			outcome = "failing"
		}
		o := monitor.Observation{
			ID:          rand.Text(),
			MonitorID:   m.ID,
			StartedAt:   stamp,
			CompletedAt: stamp,
			Counted:     true,
			Outcome:     outcome,
		}
		if err := s.PutObservation(t.Context(), o); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.HistoryObservations(
		t.Context(),
		m.ID,
		1,
		HistoryFilter{Outcomes: []string{"failing"}},
	)
	if err != nil || len(page.Items) != 1 || page.NextCursor == nil ||
		page.SearchedThrough == nil || *page.SearchedThrough != monitor.Stamp(now.Add(-50*time.Second)) {
		t.Fatalf("sparse first %+v %v", page, err)
	}
	older, err := s.HistoryObservations(t.Context(), m.ID, 1,
		HistoryFilter{Outcomes: []string{"failing"}, Before: *page.NextCursor})
	if err != nil || len(older.Items) != 1 || older.SearchedThrough == nil ||
		*older.SearchedThrough != monitor.Stamp(now.Add(-100*time.Second)) {
		t.Fatalf("sparse second %+v %v", older, err)
	}
}
