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

func TestHeartbeatOutagePauseAndCollapse(t *testing.T) {
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	clock := now
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").DynamoDB(),
		"statusforge_heartbeat_outage_"+rand.Text(),
		time.Second,
		func() time.Time { return clock },
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := s.db.(*dynamodb.Client).DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); e != nil {
			t.Errorf("delete isolated table: %v", e)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	m := monitor.New("job", monitor.Check{}, now)
	m.Kind = "heartbeat"
	m.IntervalSeconds = 0
	m.IncidentPolicy = incident.Policy{OpenAfter: 1, RecoverAfter: 1}
	m.Heartbeat = &heartbeat.Configuration{
		Schedule: heartbeat.Schedule{IntervalSeconds: 15, GraceSeconds: 5},
	}
	expect := heartbeat.Expect(now, m.Heartbeat.Schedule)
	m.Expectation = &expect
	token, e := heartbeat.Generate()
	if e != nil {
		t.Fatal(e)
	}
	m.Heartbeat.Token = &heartbeat.Token{
		Hash:      heartbeat.Hash(token),
		Hint:      heartbeat.Hint(token),
		CreatedAt: monitor.Stamp(now),
	}
	if e = s.Create(ctx, m); e != nil {
		t.Fatal(e)
	}
	if _, e = s.WriteLiveness(ctx, now, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	clock = now.Add(30 * time.Second)
	live, e := s.WriteLiveness(ctx, clock, 2*time.Second)
	if e != nil || len(live.Outages) != 1 {
		t.Fatalf("liveness outage: %+v %v", live, e)
	}
	stored, e := s.Get(ctx, m.ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Deadline(ctx, stored, clock, live); e != nil {
		t.Fatal(e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil || stored.OpenIncident != nil {
		t.Fatalf("outage opened incident: %+v %v", stored.OpenIncident, e)
	}
	gaps, e := s.Gaps(ctx, m.ID, 10)
	if e != nil || len(gaps) != 1 || gaps[0].Reason != "not_observed" {
		t.Fatalf("outage gap: %+v %v", gaps, e)
	}
	clock = now.Add(36 * time.Second)
	stored, e = s.Lifecycle(ctx, m.ID, "pause", clock)
	if e != nil || stored.Expectation != nil {
		t.Fatalf("pause: %+v %v", stored.Expectation, e)
	}
	result, e := s.RecordHeartbeat(
		ctx,
		m.ID,
		heartbeat.Hash(token),
		heartbeat.Report{},
		clock.Add(time.Millisecond),
	)
	if e != nil || result.Counted || result.NotCountedReason == nil ||
		*result.NotCountedReason != "paused" {
		t.Fatalf("paused receipt: %+v %v", result, e)
	}
	clock = now.Add(37 * time.Second)
	stored, e = s.Lifecycle(ctx, m.ID, "resume", clock)
	if e != nil || stored.Expectation == nil ||
		stored.Expectation.DueAt != heartbeat.Stamp(clock.Add(15*time.Second)) {
		t.Fatalf("resume: %+v %v", stored.Expectation, e)
	}
	if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
		t.Fatal(e)
	}
	for elapsed := 39; elapsed <= 83; elapsed += 2 {
		clock = now.Add(time.Duration(elapsed) * time.Second)
		if _, e = s.WriteLiveness(ctx, clock, 2*time.Second); e != nil {
			t.Fatal(e)
		}
	}
	clock = now.Add(84 * time.Second)
	live, e = s.WriteLiveness(ctx, clock, 2*time.Second)
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
	gaps, e = s.Gaps(ctx, m.ID, 10)
	if e != nil || len(gaps) < 2 || gaps[0].Reason != "overdue" {
		t.Fatalf("collapsed: %+v %v", gaps, e)
	}
}
