package store

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestIncidentRetainsDeploymentContextAfterArchive(t *testing.T) {
	s := applicationTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	a, _, e := s.CreateApplication(ctx, "incident app", now)
	if e != nil {
		t.Fatal(e)
	}
	m := monitor.New(
		"outage",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
		},
		now.Add(-time.Second),
	)
	if e = s.Create(ctx, m); e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetApplication(ctx, m.ID, a.ID); e != nil {
		t.Fatal(e)
	}
	marker, e := s.PutDeployment(
		ctx,
		a.ID,
		"",
		Marker{
			Version:    "v1",
			ReportedAt: monitor.Stamp(now.Add(-30 * time.Second)),
			Source:     "manual",
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	for i := range 2 {
		at := now.Add(time.Duration(i) * time.Millisecond)
		_, token, e := s.ClaimManual(ctx, m.ID, at)
		if e != nil {
			t.Fatal(e)
		}
		o := monitor.Observation{
			ID:            rand.Text(),
			MonitorID:     m.ID,
			ConfigVersion: 1,
			InitiatedBy:   "manual",
			Request:       m.Check,
			StartedAt:     monitor.Stamp(at),
			CompletedAt:   monitor.Stamp(at),
			Outcome:       "failing",
			Reason:        "wrong_status",
		}
		if _, e = s.RecordResult(ctx, o, token); e != nil {
			t.Fatal(e)
		}
	}
	incidents, e := s.ListIncidents(ctx, m.ID, "open", 10, now)
	if e != nil || len(incidents) != 1 || incidents[0].ApplicationID == nil ||
		*incidents[0].ApplicationID != a.ID {
		t.Fatalf("incident context: %+v %v", incidents, e)
	}
	if _, e = s.ArchiveApplication(ctx, a.ID, now); e != nil {
		t.Fatal(e)
	}
	persisted, _, _, _, e := s.IncidentDetail(ctx, m.ID, incidents[0].ID, now)
	if e != nil || persisted.ApplicationID == nil || *persisted.ApplicationID != a.ID {
		t.Fatalf("archived incident: %+v %v", persisted, e)
	}
	opened, _ := time.Parse(time.RFC3339Nano, persisted.OpenedAt)
	nearby, e := s.NearbyDeployments(
		ctx,
		*persisted.ApplicationID,
		opened,
		nil,
		now.Add(time.Minute),
	)
	if e != nil || len(nearby) != 1 || nearby[0].ID != marker.ID || nearby[0].OffsetSeconds >= 0 {
		t.Fatalf("archived marker context: %+v %v", nearby, e)
	}
}
