package store

import (
	"context"
	"crypto/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestHeartbeatReceiptAndDeadline(t *testing.T) {
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	clock := now
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").DynamoDB(),
		"statusforge_heartbeat_test_"+rand.Text(),
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
	m := monitor.New("heartbeat", monitor.Check{}, now)
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
	stored, e := s.Get(ctx, m.ID)
	if e != nil || stored.Heartbeat == nil || stored.Heartbeat.IntervalSeconds != 15 ||
		stored.Expectation == nil {
		t.Fatalf("hydration: %+v %v", stored, e)
	}
	run := "one"
	finished := heartbeat.Stamp(now)
	report := heartbeat.Report{RunID: &run, FinishedAt: &finished}
	clock = now.Add(2 * time.Second)
	res, e := s.RecordHeartbeat(ctx, m.ID, heartbeat.Hash(token), report, clock)
	if e != nil || !res.Counted || res.NextDueAt != heartbeat.Stamp(clock.Add(15*time.Second)) {
		t.Fatalf("first report: %+v %v", res, e)
	}
	res, e = s.RecordHeartbeat(ctx, m.ID, heartbeat.Hash(token), report, clock)
	if e != nil || !res.Duplicate {
		t.Fatalf("duplicate: %+v %v", res, e)
	}
	older := heartbeat.Stamp(now.Add(-time.Second))
	report.RunID = nil
	report.FinishedAt = &older
	res, e = s.RecordHeartbeat(ctx, m.ID, heartbeat.Hash(token), report, clock.Add(time.Second))
	if e != nil || res.Counted || res.NotCountedReason == nil ||
		*res.NotCountedReason != "older_than_current" {
		t.Fatalf("older: %+v %v", res, e)
	}
	clock = now.Add(22 * time.Second)
	live, e := s.WriteLiveness(ctx, clock, 2*time.Second)
	if e != nil {
		t.Fatal(e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			err := s.Deadline(ctx, stored, clock, live)
			if err != nil && err != ErrNotEligible {
				t.Errorf("deadline: %v", err)
			}
		}()
	}
	wg.Wait()
	obs, e := s.Observations(ctx, m.ID, 10)
	if e != nil {
		t.Fatal(e)
	}
	misses := 0
	for _, o := range obs {
		if o.Kind == "heartbeat_missed" {
			misses++
		}
	}
	if misses != 1 {
		t.Fatalf("missed observations: %d", misses)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil || stored.OpenIncident == nil {
		t.Fatalf("incident not opened: %+v %v", stored.OpenIncident, e)
	}
	changed := heartbeat.Schedule{IntervalSeconds: 30, GraceSeconds: 10}
	stored, e = s.PatchHeartbeat(ctx, m.ID, 1, nil, &changed, nil, clock)
	if e != nil || stored.ConfigVersion != 1 || stored.Expectation == nil ||
		stored.Expectation.DueAt != heartbeat.Stamp(now.Add(32*time.Second)) {
		t.Fatalf("schedule change: %+v %v", stored.Expectation, e)
	}
	clock = now.Add(23 * time.Second)
	res, e = s.RecordHeartbeat(ctx, m.ID, heartbeat.Hash(token), heartbeat.Report{}, clock)
	if e != nil || !res.Counted {
		t.Fatalf("recovery: %+v %v", res, e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil || stored.OpenIncident != nil {
		t.Fatalf("incident not resolved: %+v %v", stored.OpenIncident, e)
	}
	if stored.Heartbeat.LastReportAt == nil ||
		*stored.Heartbeat.LastReportAt != heartbeat.Stamp(clock) {
		t.Fatalf("last counted receipt: %+v", stored.Heartbeat.LastReportAt)
	}
	clock = now.Add(54 * time.Second)
	res, e = s.RecordHeartbeat(ctx, m.ID, heartbeat.Hash(token), heartbeat.Report{}, clock)
	if e != nil || !res.Counted || !res.Late {
		t.Fatalf("late grace receipt: %+v %v", res, e)
	}
	stored, e = s.Get(ctx, m.ID)
	if e != nil || stored.Status.State != "healthy" || stored.OpenIncident != nil {
		t.Fatalf("late report health: %s %v", stored.Status.State, e)
	}
	newToken, _, e := s.HeartbeatToken(ctx, m.ID, false, clock)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthenticateHeartbeat(ctx, m.ID, token); e == nil {
		t.Fatal("old token valid")
	}
	if _, e = s.AuthenticateHeartbeat(ctx, m.ID, newToken); e != nil {
		t.Fatalf("new token: %v", e)
	}
	if _, _, e = s.HeartbeatToken(ctx, m.ID, true, clock); e != nil {
		t.Fatal(e)
	}
	if _, e = s.AuthenticateHeartbeat(ctx, m.ID, newToken); e == nil {
		t.Fatal("revoked token valid")
	}
}
