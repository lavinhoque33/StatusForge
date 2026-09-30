package store

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func maintenanceFixture(t *testing.T) (*Store, monitor.Monitor, time.Time) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local unavailable:", err)
	}
	conn.Close()
	now := time.Now().UTC().Truncate(time.Second)
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").DynamoDB(),
		"statusforge_maintenance_test_"+rand.Text(),
		time.Second,
		func() time.Time { return now },
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.db.(*dynamodb.Client).DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); err != nil {
			t.Errorf("delete test table: %v", err)
		}
	})
	m := monitor.New(
		"maintenance fixture",
		monitor.Check{
			URL:            "http://127.0.0.1:8999/",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
			MaxBodyBytes:   65536,
		},
		now,
	)
	if err := s.Create(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	return s, m, now
}

func TestMaintenanceClaimAndBoundaries(t *testing.T) {
	s, m, now := maintenanceFixture(t)
	ctx := t.Context()
	w, err := s.CreateMaintenance(
		ctx,
		m.ID,
		now.Add(-time.Second),
		now.Add(time.Second),
		"upgrade",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMaintenance(ctx, m.ID, now, now.Add(70*time.Second), "", now); !errors.Is(
		err,
		ErrOverlaps,
	) {
		t.Fatalf("overlap: %v", err)
	}
	claimed, token, err := s.ClaimManual(ctx, m.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Lease.MaintenanceWindowID == nil || *claimed.Lease.MaintenanceWindowID != w.ID {
		t.Fatalf("claim label: %+v", claimed.Lease)
	}
	obs := monitor.Observation{
		ID:            rand.Text(),
		MonitorID:     m.ID,
		ConfigVersion: 1,
		InitiatedBy:   "manual",
		Request:       m.Check,
		StartedAt:     claimed.Lease.StartedAt,
		CompletedAt:   monitor.Stamp(now.Add(2 * time.Second)),
		Outcome:       "failing",
		Reason:        "wrong_status",
	}
	m, err = s.Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	m.Evaluation.FailRun = []incident.Evidence{{Outcome: "failing"}}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.table),
		Key:              key("MONITORS", "MON#"+m.ID),
		UpdateExpression: aws.String("SET evaluation = :evaluation"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":evaluation": mustAV(m.Evaluation),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.Boundary(ctx, m, now) }()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !errors.Is(err, ErrNotEligible) {
			t.Fatalf("boundary: %v", err)
		}
	}
	if success != 1 {
		t.Fatalf("start recorded %d times", success)
	}
	after, err := s.Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Maintenance.ActiveID != w.ID || after.Evaluation.Revision != 2 ||
		len(after.Evaluation.FailRun) != 0 {
		t.Fatalf("start boundary: %+v", after)
	}
	after.Evaluation.HealthyRun = []incident.Evidence{{Outcome: "healthy"}}
	_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName:        aws.String(s.table),
		Key:              key("MONITORS", "MON#"+m.ID),
		UpdateExpression: aws.String("SET evaluation = :evaluation"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":evaluation": mustAV(after.Evaluation),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Boundary(ctx, after, now.Add(time.Second)); err != nil {
		t.Fatalf("end: %v", err)
	}
	after, err = s.Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Evaluation.HealthyRun) != 0 {
		t.Fatalf("end boundary retained healthy run: %+v", after.Evaluation)
	}
	s.now = func() time.Time { return now.Add(2 * time.Second) }
	obs, err = s.RecordResult(ctx, obs, token)
	if err != nil {
		t.Fatalf("late result: %v", err)
	}
	if obs.MaintenanceWindowID == nil || *obs.MaintenanceWindowID != w.ID || !obs.Counted {
		t.Fatalf("late label: %+v", obs)
	}
	after, err = s.Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Evaluation.FailRun) != 0 || after.Maintenance.ActiveID != "" {
		t.Fatalf("late evidence changed run: %+v", after)
	}
}

func TestMaintenanceCancelAndLimit(t *testing.T) {
	s, m, now := maintenanceFixture(t)
	ctx := t.Context()
	active, err := s.CreateMaintenance(
		ctx,
		m.ID,
		now.Add(-time.Second),
		now.Add(time.Minute),
		"",
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	ended, err := s.CancelMaintenance(ctx, m.ID, active.ID, now.Add(time.Second))
	if err != nil || ended.EndAt != monitor.Stamp(now.Add(time.Second)) ||
		ended.State != "cancelled" {
		t.Fatalf("end active: %+v %v", ended, err)
	}
	if _, err = s.CancelMaintenance(ctx, m.ID, active.ID, now.Add(2*time.Second)); !errors.Is(
		err,
		ErrWindowClosed,
	) {
		t.Fatalf("closed: %v", err)
	}
	for i := range 10 {
		start := now.Add(time.Duration(i+1) * time.Hour)
		if _, err = s.CreateMaintenance(ctx, m.ID, start, start.Add(30*time.Minute), "", now.Add(2*time.Second)); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err = s.CreateMaintenance(ctx, m.ID, now.Add(12*time.Hour), now.Add(13*time.Hour), "", now.Add(2*time.Second)); !errors.Is(
		err,
		ErrTooManyWindows,
	) {
		t.Fatalf("limit: %v", err)
	}
}

func TestMaintenanceCreateRaceAndScheduledCancellation(t *testing.T) {
	s, m, now := maintenanceFixture(t)
	ctx := t.Context()
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.CreateMaintenance(
				ctx, m.ID, now.Add(time.Hour), now.Add(2*time.Hour), "", now,
			)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	wins, overlaps := 0, 0
	for err := range results {
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrOverlaps):
			overlaps++
		default:
			t.Fatalf("race returned %v", err)
		}
	}
	if wins != 1 || overlaps != 1 {
		t.Fatalf("one overlapping create should win, wins=%d overlaps=%d", wins, overlaps)
	}
	windows, err := s.ListMaintenance(ctx, m.ID, 10, now)
	if err != nil || len(windows) != 1 {
		t.Fatalf("windows: %+v %v", windows, err)
	}
	cancelled, err := s.CancelMaintenance(ctx, m.ID, windows[0].ID, now)
	if err != nil || cancelled.State != "cancelled" || cancelled.EndAt != windows[0].EndAt {
		t.Fatalf("cancel scheduled: %+v %v", cancelled, err)
	}
}

func TestMaintenanceReminderSuppression(t *testing.T) {
	s, m, now := maintenanceFixture(t)
	ctx := t.Context()
	opened := now.Add(-2 * time.Minute)
	open := incident.Open{
		ID: "test-open", OpenedAt: monitor.Stamp(opened),
		NextReminderAt: monitor.Stamp(now.Add(-time.Minute)),
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table), Key: key("MONITORS", "MON#"+m.ID),
		UpdateExpression:          aws.String("SET openIncident = :open"),
		ExpressionAttributeValues: map[string]types.AttributeValue{":open": mustAV(open)},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := Incident{
		ID:             open.ID,
		MonitorID:      m.ID,
		MonitorName:    m.Name,
		State:          "open",
		OpenedAt:       open.OpenedAt,
		FailureCount:   2,
		FirstFailureAt: open.OpenedAt,
		LastFailure:    incident.Evidence{Outcome: "failing"},
	}
	item, err := attributevalue.MarshalMap(in)
	if err != nil {
		t.Fatal(err)
	}
	item["PK"] = mustAV(incidentPK(m.ID))
	item["SK"] = mustAV("INC#" + in.ID)
	if _, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMaintenance(ctx, m.ID, now.Add(-time.Second), now.Add(5*time.Minute), "", now); err != nil {
		t.Fatal(err)
	}
	for _, at := range []time.Time{now, now.Add(3 * time.Minute)} {
		m, err = s.Get(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err = s.Reminder(ctx, m, at, time.Minute); err != nil {
			t.Fatalf("suppressed reminder: %v", err)
		}
	}
	m, err = s.Get(ctx, m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.OpenIncident.ReminderSeq != 0 ||
		m.OpenIncident.NextReminderAt != monitor.Stamp(now.Add(4*time.Minute)) {
		t.Fatalf("slot not advanced without intent: %+v", m.OpenIncident)
	}
	items, err := s.query(ctx, incidentPK(m.ID), "INCX#"+open.ID+"#NOTE#", 10, false)
	if err != nil || len(items) != 0 {
		t.Fatalf("reminder created during maintenance: %d %v", len(items), err)
	}
}

func TestMaintenanceDefersQueuedOpeningDelivery(t *testing.T) {
	s, m, now := maintenanceFixture(t)
	ctx := t.Context()
	in := Incident{
		ID: "queued", MonitorID: m.ID, MonitorName: m.Name, State: "open",
		OpenedAt: monitor.Stamp(now.Add(-time.Minute)), FailureCount: 2,
	}
	tx, err := s.intent(m, in, "opened", "opened", nil, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateMaintenance(ctx, m.ID, now.Add(-time.Second), now.Add(time.Minute), "", now); err != nil {
		t.Fatal(err)
	}
	due := Due{MonitorID: m.ID, IncidentID: in.ID, NoteKey: "opened", At: monitor.Stamp(now)}
	if _, _, _, err = s.ClaimDelivery(ctx, due, now); !errors.Is(err, ErrNotEligible) {
		t.Fatalf("queued opening should wait until maintenance ends: %v", err)
	}
	n, err := s.notification(ctx, m.ID, in.ID, "opened")
	if err != nil || n.State != "pending" || n.AttemptCount != 0 {
		t.Fatalf("claim changed deferred notification: %+v %v", n, err)
	}
	if _, _, _, err = s.ClaimDelivery(ctx, due, now.Add(time.Minute)); err != nil {
		t.Fatalf("delivery did not resume after window: %v", err)
	}
}
