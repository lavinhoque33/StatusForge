package store

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestExpiredLeaseRetriesOnceThenGaps(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	m := monitor.New(
		"interrupted",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/slow",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
			MaxBodyBytes:   monitor.MaxBodyBytes,
		},
		now,
	)
	m.IntervalSeconds = 60
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	work, err := s.Works(ctx, m, now)
	if err != nil || len(work) != 1 {
		t.Fatalf("work %v %v", work, err)
	}
	_, first, err := s.Claim(ctx, work[0], now)
	if err != nil {
		t.Fatal(err)
	}
	work, err = s.Works(ctx, m, now.Add(12*time.Second))
	if err != nil || len(work) != 1 || work[0].Attempts != 1 {
		t.Fatalf("claimed %v %v", work, err)
	}
	_, second, err := s.Claim(ctx, work[0], now.Add(12*time.Second))
	if err != nil || second == first {
		t.Fatalf("retry %s %s %v", first, second, err)
	}
	work, err = s.Works(ctx, m, now.Add(24*time.Second))
	if err != nil || len(work) != 1 || work[0].Attempts != 2 {
		t.Fatalf("second claim %v %v", work, err)
	}
	_, token, err := s.Claim(ctx, work[0], now.Add(24*time.Second))
	if err != nil || token != "" {
		t.Fatalf("close exhausted %q %v", token, err)
	}
	gaps, err := s.Gaps(ctx, m.ID, 10)
	if err != nil || len(gaps) != 1 || gaps[0].Reason != "lease_expired" {
		t.Fatalf("gap %+v %v", gaps, err)
	}
	work, err = s.Works(ctx, m, now.Add(25*time.Second))
	if err != nil || len(work) != 0 {
		t.Fatalf("closed work %v %v", work, err)
	}
}

func TestTickWindowAndRestartSweep(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	m := monitor.New("downtime", monitor.Check{Method: "GET", DeadlineMs: 1000}, now)
	m.IntervalSeconds = 10
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	for i := range 220 {
		if i == 180 || i == 190 {
			continue
		}
		due := now.Add(time.Duration(i-360) * time.Second)
		item, err := workItem(
			Work{MonitorID: m.ID, DueAt: workStamp(due), State: "done", Attempts: 1},
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item}); err != nil {
			t.Fatal(err)
		}
	}
	orphanDue := workStamp(now.Add(-180 * time.Second))
	old, err := workItem(
		Work{MonitorID: m.ID, DueAt: orphanDue, State: "pending", ConfigVersion: 1},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: old}); err != nil {
		t.Fatal(err)
	}
	expiredDue := workStamp(now.Add(-170 * time.Second))
	claimed, err := workItem(Work{
		MonitorID: m.ID, DueAt: expiredDue, State: "claimed", ConfigVersion: 1,
		Attempts: 2, ClaimToken: "interrupted", LeaseUntil: workStamp(now.Add(-150 * time.Second)),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: claimed}); err != nil {
		t.Fatal(err)
	}
	recent, err := s.Works(ctx, m, now)
	if err != nil || len(recent) != 1 || recent[0].Trigger != "create" {
		t.Fatalf("tick window should exclude old rows: %v %v", recent, err)
	}
	gaps, err := s.SweepOld(ctx, now)
	if err != nil || gaps != 2 {
		t.Fatalf("restart sweep gaps %d %v", gaps, err)
	}
	records, err := s.Gaps(ctx, m.ID, 10)
	if err != nil || len(records) != 2 || records[0].Reason != "lease_expired" ||
		records[0].FromDueAt != expiredDue || records[1].Reason != "overdue" ||
		records[1].FromDueAt != orphanDue {
		t.Fatalf("old work was lost: %v %v", records, err)
	}
	gaps, err = s.SweepOld(ctx, now)
	if err != nil || gaps != 0 {
		t.Fatalf("repeated sweep %d %v", gaps, err)
	}
}

func TestSaturatedTickClosesPendingWithinWindow(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	m := monitor.New("busy", monitor.Check{Method: "GET", DeadlineMs: 1000}, now)
	m.IntervalSeconds = 10
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	// No worker claims any item: the bounded dispatch channel may be full.
	for elapsed := 0; elapsed <= 14; elapsed += 2 {
		if _, _, _, err := s.Tick(ctx, now.Add(time.Duration(elapsed)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	gaps, err := s.Gaps(ctx, m.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range gaps {
		if gap.FromDueAt == workStamp(now) && gap.Reason == "overdue" && gap.MissedCount == 1 {
			return
		}
	}
	t.Fatalf("unclaimed create work was not closed overdue: %+v", gaps)
}

func TestFailedTickRecoversAgedPendingWork(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	m := monitor.New("outage", monitor.Check{Method: "GET", DeadlineMs: 1000}, now)
	m.IntervalSeconds = 10
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	failed, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, _, err := s.Tick(failed, now.Add(2*time.Second)); err == nil {
		t.Fatal("cancelled dependency operation should fail")
	}
	if _, _, _, err := s.Tick(ctx, now.Add(70*time.Second)); err != nil {
		t.Fatal(err)
	}
	gaps, err := s.Gaps(ctx, m.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, gap := range gaps {
		if gap.FromDueAt == workStamp(now) && gap.Reason == "overdue" {
			return
		}
	}
	t.Fatalf("failed tick silently stranded old work: %+v", gaps)
}
