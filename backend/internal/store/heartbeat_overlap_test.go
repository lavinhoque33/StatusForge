package store

import (
	"context"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestHeartbeatWindowOverlapRestartsDeadline(t *testing.T) {
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	start := time.Now().UTC().Truncate(time.Millisecond)
	clock := start
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local"),
		"statusforge_heartbeat_window_"+rand.Text(),
		time.Second,
		func() time.Time { return clock },
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := s.db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); e != nil {
			t.Errorf("delete isolated table: %v", e)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	create := func(name string, reference time.Time) monitor.Monitor {
		m := monitor.New(name, monitor.Check{}, reference)
		m.Kind = "heartbeat"
		m.IntervalSeconds = 0
		m.IncidentPolicy = incident.Policy{OpenAfter: 1, RecoverAfter: 1}
		m.Heartbeat = &heartbeat.Configuration{
			Schedule: heartbeat.Schedule{IntervalSeconds: 15, GraceSeconds: 5},
		}
		expectation := heartbeat.Expect(reference, m.Heartbeat.Schedule)
		m.Expectation = &expectation
		if e := s.Create(ctx, m); e != nil {
			t.Fatal(e)
		}
		return m
	}
	m := create("outage crosses early window", start)
	clock = start.Add(3 * time.Second)
	if _, e := s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	clock = start.Add(16 * time.Second)
	live, e := s.WriteLiveness(ctx, clock, 2*time.Second)
	if e != nil || len(live.Outages) != 1 {
		t.Fatalf("expected outage: %+v %v", live, e)
	}
	for elapsed := 18; elapsed <= 20; elapsed += 2 {
		clock = start.Add(time.Duration(elapsed) * time.Second)
		if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
			t.Fatal(e)
		}
	}
	clock = start.Add(21 * time.Second)
	live, e = s.WriteLiveness(ctx, clock, 2*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := s.Get(ctx, m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Deadline(ctx, stored, clock, live); e != nil {
		t.Fatal(e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil || stored.OpenIncident != nil ||
		stored.Expectation.Reference != heartbeat.Stamp(start.Add(16*time.Second)) ||
		stored.Expectation.DueAt != heartbeat.Stamp(start.Add(31*time.Second)) {
		t.Fatalf(
			"overlap wrongly judged: incident=%+v expectation=%+v err=%v",
			stored.OpenIncident,
			stored.Expectation,
			e,
		)
	}
	gaps, e := s.Gaps(ctx, m.ID, 10)
	if e != nil || len(gaps) != 1 || gaps[0].Reason != "not_observed" || gaps[0].MissedCount != 1 {
		t.Fatalf("overlap gap: %+v %v", gaps, e)
	}
	observations, e := s.Observations(ctx, m.ID, 10)
	if e != nil || len(observations) != 0 {
		t.Fatalf("outage created missed evidence: %+v %v", observations, e)
	}
	before := create("outage before window", start.Add(30*time.Second))
	for elapsed := 23; elapsed <= 35; elapsed += 2 {
		clock = start.Add(time.Duration(elapsed) * time.Second)
		if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
			t.Fatal(e)
		}
	}
	clock = start.Add(36 * time.Second)
	if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	live, e = s.Liveness(ctx)
	if e != nil {
		t.Fatal(e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Deadline(ctx, stored, clock, live); e != nil {
		t.Fatal(e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil || stored.OpenIncident == nil ||
		stored.OpenIncident.OpenedAt != heartbeat.Stamp(start.Add(36*time.Second)) {
		t.Fatalf("post-restart skip not missed: %+v %v", stored.OpenIncident, e)
	}
	for elapsed := 38; elapsed <= 50; elapsed += 2 {
		clock = start.Add(time.Duration(elapsed) * time.Second)
		if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
			t.Fatal(e)
		}
	}
	clock = start.Add(51 * time.Second)
	if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	live, e = s.Liveness(ctx)
	if e != nil {
		t.Fatal(e)
	}
	before, e = s.Get(ctx, before.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Deadline(ctx, before, clock, live); e != nil {
		t.Fatal(e)
	}
	before, e = s.Get(ctx, before.ID)
	if e != nil || before.OpenIncident == nil ||
		before.OpenIncident.OpenedAt != heartbeat.Stamp(start.Add(50*time.Second)) {
		t.Fatalf("outage before window hid miss: %+v %v", before.OpenIncident, e)
	}
}
