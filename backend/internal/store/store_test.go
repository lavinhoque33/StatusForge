package store

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestLocalRoundTrip(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local on 127.0.0.1:8000 unreachable:", err)
	}
	conn.Close()
	now := time.Now().UTC()
	s := New(localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local"), "statusforge_test_"+rand.Text(), time.Second, time.Now)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		if _, err := s.db.DeleteTable(cleanupCtx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); err != nil {
			t.Logf("could not remove isolated DynamoDB Local test table: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	m := monitor.New("sample", monitor.Check{URL: "http://127.0.0.1:8090/healthy", Method: "GET", ExpectedStatus: 200, DeadlineMs: 1000, MaxBodyBytes: monitor.MaxBodyBytes}, now)
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Patch(ctx, m.ID, 2, nil, nil, now); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version conflict: %v", err)
	}
	changed := m.Check
	changed.DeadlineMs = 3000
	updated, err := s.Patch(ctx, m.ID, 1, nil, &changed, now)
	if err != nil || updated.ConfigVersion != 2 {
		t.Fatalf("patch: %+v %v", updated, err)
	}
	if _, err := s.Lifecycle(ctx, m.ID, "resume", now); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("invalid transition: %v", err)
	}
	o := monitor.Observation{ID: rand.Text(), MonitorID: m.ID, ConfigVersion: 2, InitiatedBy: "manual", Request: changed, StartedAt: monitor.Stamp(now), CompletedAt: monitor.Stamp(now), Outcome: "healthy", Reason: "ok"}
	if err := s.PutObservation(ctx, o); err != nil {
		t.Fatal(err)
	}
	if err := s.PutObservation(ctx, o); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	later := o
	later.ID = rand.Text()
	later.StartedAt = monitor.Stamp(now.Add(time.Second))
	if err := s.PutObservation(ctx, later); err != nil {
		t.Fatal(err)
	}
	obs, err := s.Observations(ctx, m.ID, 2)
	if err != nil || len(obs) != 2 || obs[0].ID != later.ID {
		t.Fatalf("newest first: %+v %v", obs, err)
	}
	s.now = func() time.Time { return now.Add(91 * 24 * time.Hour) }
	obs, err = s.Observations(ctx, m.ID, 2)
	if err != nil || len(obs) != 0 {
		t.Fatalf("expired: %+v %v", obs, err)
	}
	archived, err := s.Lifecycle(ctx, m.ID, "archive", now)
	if err != nil || archived.Lifecycle != "archived" {
		t.Fatalf("archive: %+v %v", archived, err)
	}
	if _, err := s.Patch(ctx, m.ID, 2, nil, nil, now); !errors.Is(err, ErrArchived) {
		t.Fatalf("edit archived: %v", err)
	}
	listed, err := s.List(ctx)
	if err != nil || len(listed) != 1 || listed[0].ID != m.ID {
		t.Fatalf("list: %+v %v", listed, err)
	}
}
