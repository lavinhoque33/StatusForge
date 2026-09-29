package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func isolatedStore(t *testing.T, now time.Time) *Store {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local unavailable:", err)
	}
	conn.Close()
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local"),
		"statusforge_test_"+rand.Text(),
		time.Second,
		func() time.Time { return now },
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := s.db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(s.table)}); err != nil {
			t.Errorf("delete isolated table: %v", err)
		}
	})
	return s
}

func TestScheduledTransactionsAndEligibility(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	m := monitor.New(
		"sample",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/healthy",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
			MaxBodyBytes:   monitor.MaxBodyBytes,
		},
		now,
	)
	m.IntervalSeconds = 10
	if err := s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	ms, err := s.List(ctx)
	if err != nil || len(ms) != 1 {
		t.Fatalf("monitors %v %v", ms, err)
	}
	works, err := s.Works(ctx, m, now.Add(time.Second))
	if err != nil || len(works) != 1 || works[0].Trigger != "create" {
		t.Fatalf("create work %v %v", works, err)
	}
	claim, token, err := s.Claim(ctx, works[0], now.Add(time.Second))
	if err != nil || token == "" {
		t.Fatalf("claim %v", err)
	}
	leased, err := s.Get(ctx, m.ID)
	if err != nil || leased.LastClaimAt != workStamp(now.Add(time.Second)) {
		t.Fatalf("scheduled claim recency %+v %v", leased, err)
	}
	if _, _, err = s.ClaimManual(ctx, m.ID, now.Add(time.Second)); !errors.Is(err, ErrLeaseHeld) {
		t.Fatalf("manual vs scheduled lease %v", err)
	}
	if _, _, err = s.Claim(ctx, works[0], now.Add(time.Second)); err == nil {
		t.Fatal("second claim won")
	}
	obs := monitor.Observation{
		ID:            rand.Text(),
		MonitorID:     m.ID,
		ConfigVersion: claim.ConfigVersion,
		InitiatedBy:   "scheduled",
		Trigger:       &works[0].Trigger,
		DueAt:         &works[0].DueAt,
		Request:       claim.Check,
		StartedAt:     monitor.Stamp(now.Add(time.Second)),
		CompletedAt:   monitor.Stamp(now.Add(time.Second)),
		Outcome:       "healthy",
		Reason:        "ok",
	}
	obs, err = s.RecordResult(ctx, obs, token)
	if err != nil || !obs.Counted {
		t.Fatalf("counted %v %+v", err, obs)
	}
	_, created, gaps, err := s.Tick(ctx, now.Add(35*time.Second))
	if err != nil || created != 1 || gaps != 1 {
		t.Fatalf("catchup %d %d %v", created, gaps, err)
	}
	_, created, gaps, err = s.Tick(ctx, now.Add(35*time.Second))
	if err != nil || created != 0 || gaps != 0 {
		t.Fatalf("duplicate tick %d %d %v", created, gaps, err)
	}
	gapRows, err := s.Gaps(ctx, m.ID, 10)
	if err != nil || len(gapRows) != 1 || gapRows[0].MissedCount < 1 {
		t.Fatalf("gaps %v %v", gapRows, err)
	}
	_, token, err = s.ClaimManual(ctx, m.ID, now.Add(36*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	leased, err = s.Get(ctx, m.ID)
	if err != nil || leased.LastClaimAt != workStamp(now.Add(36*time.Second)) {
		t.Fatalf("manual claim recency %+v %v", leased, err)
	}
	fresh := obs
	fresh.ID = rand.Text()
	fresh.InitiatedBy = "manual"
	fresh.Trigger = nil
	fresh.DueAt = nil
	fresh.StartedAt = monitor.Stamp(now.Add(36 * time.Second))
	fresh.CompletedAt = fresh.StartedAt
	fresh, err = s.RecordResult(ctx, fresh, token)
	if err != nil || !fresh.Counted {
		t.Fatalf("newer %v %+v", err, fresh)
	}
	_, token, err = s.ClaimManual(ctx, m.ID, now.Add(37*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	older := fresh
	older.ID = rand.Text()
	older.StartedAt = monitor.Stamp(now.Add(2 * time.Second))
	older.CompletedAt = monitor.Stamp(now.Add(37 * time.Second))
	older, err = s.RecordResult(ctx, older, token)
	if err != nil || older.Counted || older.NotCountedReason == nil ||
		*older.NotCountedReason != "older_than_current" {
		t.Fatalf("older %v %+v", err, older)
	}
	read, err := s.Get(ctx, m.ID)
	if err != nil || read.Evidence.ObservationID != fresh.ID {
		t.Fatalf("status overwritten %v %+v", err, read.Evidence)
	}
	_, token, err = s.ClaimManual(ctx, m.ID, now.Add(38*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Lifecycle(ctx, m.ID, "pause", now.Add(38*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	paused := fresh
	paused.ID = rand.Text()
	paused.StartedAt = monitor.Stamp(now.Add(38 * time.Second))
	paused, err = s.RecordResult(ctx, paused, token)
	if err != nil || paused.NotCountedReason == nil || *paused.NotCountedReason != "paused" {
		t.Fatalf("paused %v %+v", err, paused)
	}
	_, err = s.Lifecycle(ctx, m.ID, "resume", now.Add(39*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	_, token, err = s.ClaimManual(ctx, m.ID, now.Add(40*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	changed := m.Check
	changed.DeadlineMs = 2000
	updated, err := s.Patch(ctx, m.ID, 1, nil, &changed, now.Add(40*time.Second))
	if err != nil || updated.ConfigVersion != 2 {
		t.Fatalf("patch %v %+v", err, updated)
	}
	obsolete := fresh
	obsolete.ID = rand.Text()
	obsolete.StartedAt = monitor.Stamp(now.Add(40 * time.Second))
	obsolete.ConfigVersion = 1
	obsolete, err = s.RecordResult(ctx, obsolete, token)
	if err != nil || obsolete.Counted || obsolete.NotCountedReason == nil ||
		*obsolete.NotCountedReason != "config_changed" {
		t.Fatalf("obsolete %v %+v", err, obsolete)
	}
	newInterval := 15
	updated, err = s.PatchInterval(ctx, m.ID, 2, nil, nil, &newInterval, now.Add(41*time.Second))
	if err != nil || updated.ConfigVersion != 2 || updated.IntervalSeconds != 15 ||
		updated.ScheduledThrough != slotStamp(m.ID, 15, now.Add(41*time.Second)) {
		t.Fatalf("interval change %v %+v", err, updated)
	}
	_, _, gapCount, err := s.Tick(ctx, now.Add(42*time.Second))
	if err != nil || gapCount != 0 {
		t.Fatalf("interval change created gap %d %v", gapCount, err)
	}
}

func TestTickCycleClosesExpiredWorkOnceAndReportsOpenWork(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	ids := make([]string, 0, 12)
	for i := range 12 {
		m := monitor.New(
			fmt.Sprintf("parallel %d", i),
			monitor.Check{Method: "GET", DeadlineMs: 1000},
			now,
		)
		m.IntervalSeconds = 10
		if err := s.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, m.ID)
	}
	r, err := s.TickCycle(ctx, now.Add(time.Second))
	if err != nil || r.Failed != 0 || len(r.Monitors) != 12 || r.Active != 12 {
		t.Fatalf("first pass %+v %v", r, err)
	}
	for _, id := range ids {
		// The create run is still open next to the newest scheduled slot.
		if len(r.Open[id]) == 0 || r.Open[id][0].Trigger != "create" {
			t.Fatalf("open work for %s: %+v", id, r.Open[id])
		}
	}
	// Twenty seconds on, every create run is past dueAt + interval: each is
	// closed as exactly one overdue gap, reported once as missed, and no
	// longer offered as open work.
	later := now.Add(20 * time.Second)
	r, err = s.TickCycle(ctx, later)
	if err != nil || r.Failed != 0 {
		t.Fatalf("expiry pass %+v %v", r, err)
	}
	missedCreate := map[string]int{}
	for _, w := range r.Missed {
		if w.Trigger == "create" {
			missedCreate[w.MonitorID]++
		}
	}
	for _, id := range ids {
		if missedCreate[id] != 1 {
			t.Fatalf("missed create runs %v", missedCreate)
		}
	}
	for _, id := range ids {
		for _, w := range r.Open[id] {
			if w.Trigger == "create" {
				t.Fatalf("expired work still open: %+v", w)
			}
		}
	}
	r, err = s.TickCycle(ctx, later)
	if err != nil || len(r.Missed) != 0 || r.Gaps != 0 {
		t.Fatalf("repeat pass %+v %v", r, err)
	}
	for _, id := range ids {
		gaps, err := s.Gaps(ctx, id, 10)
		overdue := 0
		for _, g := range gaps {
			if g.Reason == "overdue" && g.FromDueAt == workStamp(now) {
				overdue++
			}
		}
		if err != nil || overdue != 1 {
			t.Fatalf("overdue gaps for %s: %+v %v", id, gaps, err)
		}
	}
}
