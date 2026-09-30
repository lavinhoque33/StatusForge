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
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func applicationTestStore(t *testing.T) *Store {
	t.Helper()
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	s := New(
		localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").DynamoDB(),
		"statusforge_test_"+rand.Text(),
		time.Second,
		time.Now,
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.db.(*dynamodb.Client).DeleteTable(
			ctx,
			&dynamodb.DeleteTableInput{TableName: aws.String(s.table)},
		)
	})
	return s
}

func TestApplicationsMembershipArchiveAndMarkers(t *testing.T) {
	s := applicationTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC().Truncate(time.Millisecond)
	a, token, e := s.CreateApplication(ctx, "Service", now)
	if e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.CreateApplication(ctx, "service", now); !errors.Is(e, ErrDuplicateName) {
		t.Fatalf("duplicate name: %v", e)
	}
	other, _, e := s.CreateApplication(ctx, "Other", now)
	if e != nil {
		t.Fatal(e)
	}
	m := monitor.New(
		"monitor",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/healthy",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
		},
		now,
	)
	if e = s.Create(ctx, m); e != nil {
		t.Fatal(e)
	}
	assigned, e := s.SetApplication(ctx, m.ID, a.ID)
	if e != nil || assigned.ConfigVersion != m.ConfigVersion || assigned.ApplicationID != a.ID {
		t.Fatalf("membership: %+v %v", assigned, e)
	}
	assigned, e = s.SetApplication(ctx, m.ID, other.ID)
	if e != nil || assigned.ApplicationID != other.ID {
		t.Fatalf("move: %+v %v", assigned, e)
	}
	if _, e = s.RenameApplication(ctx, a.ID, "OTHER", now); !errors.Is(e, ErrDuplicateName) {
		t.Fatalf("rename to another app's name: %v", e)
	}
	if renamed, err := s.RenameApplication(ctx, a.ID, "SERVICE", now); err != nil ||
		renamed.Name != "SERVICE" {
		t.Fatalf("case-only rename: %+v %v", renamed, err)
	}
	assigned, e = s.SetApplication(ctx, m.ID, a.ID)
	if e != nil {
		t.Fatal(e)
	}
	deploymentID := "build-1"
	marker := Marker{
		Version:      "v1",
		DeploymentID: &deploymentID,
		ReportedAt:   monitor.Stamp(now.Add(-time.Minute)),
		Source:       "ingest",
	}
	first, e := s.PutDeployment(ctx, a.ID, a.Token.Hash, marker)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.PutDeployment(ctx, a.ID, a.Token.Hash, marker); !errors.Is(
		e,
		ErrDuplicateDeployment,
	) {
		t.Fatalf("duplicate guard: %v", e)
	}
	before, e := s.Application(ctx, a.ID)
	if e != nil || len(before.Members) != 1 {
		t.Fatalf("members: %+v %v", before, e)
	}
	if _, _, e = s.ApplicationToken(ctx, a.ID, true, now); e != nil {
		t.Fatalf("revoke token: %v", e)
	}
	if _, e = s.AuthenticateApplication(ctx, a.ID, token); !errors.Is(e, ErrNotEligible) {
		t.Fatalf("revoked token still accepted: %v", e)
	}
	if _, _, e = s.ApplicationToken(ctx, a.ID, false, now); e != nil {
		t.Fatalf("rotate revoked token: %v", e)
	}
	// Simulate an interruption after the atomic archive/revocation but before cleanup:
	// mark archived and remove the name guard; the normal archive retry must drain members.
	_, e = s.db.TransactWriteItems(
		ctx,
		&dynamodb.TransactWriteItemsInput{
			TransactItems: []types.TransactWriteItem{
				{
					Update: &types.Update{
						TableName:                aws.String(s.table),
						Key:                      appKey(a.ID),
						UpdateExpression:         aws.String("SET archivedAt = :at REMOVE #token"),
						ExpressionAttributeNames: map[string]string{"#token": "token"},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":at": mustAV(monitor.Stamp(now)),
						},
					},
				},
				{Delete: &types.Delete{TableName: aws.String(s.table), Key: nameKey(a.Name)}},
			},
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	visible, e := s.Application(ctx, a.ID)
	if e != nil || len(visible.Members) != 0 {
		t.Fatalf("archived app showed members: %+v %v", visible, e)
	}
	invisible, e := s.Get(ctx, m.ID)
	if e != nil || invisible.ApplicationID != "" {
		t.Fatalf("archived membership visible on monitor: %+v %v", invisible, e)
	}
	archived, e := s.ArchiveApplication(ctx, a.ID, now)
	if e != nil || len(archived.Members) != 0 {
		t.Fatalf("archive retry: %+v %v", archived, e)
	}
	cleared, e := s.Get(ctx, m.ID)
	if e != nil || cleared.ApplicationID != "" {
		t.Fatalf("membership not cleared: %+v %v", cleared, e)
	}
	raw, e := s.monitorApplicationRaw(ctx, m.ID)
	if e != nil || raw != "" {
		t.Fatalf("archive retry did not clear persisted membership: %q %v", raw, e)
	}
	if _, e = s.SetApplication(ctx, m.ID, a.ID); !errors.Is(e, ErrArchived) {
		t.Fatalf("assigned archived app: %v", e)
	}
	if _, e = s.AuthenticateApplication(ctx, a.ID, token); !errors.Is(e, ErrNotEligible) {
		t.Fatalf("archived token: %v", e)
	}
	listed, e := s.Deployments(ctx, a.ID, 50)
	if e != nil || len(listed) != 1 || listed[0].ID != first.ID {
		t.Fatalf("archived markers: %+v %v", listed, e)
	}
	if _, _, e = s.CreateApplication(ctx, "Service", now); e != nil {
		t.Fatalf("released name: %v", e)
	}
}

func TestNearbyDeploymentsWindow(t *testing.T) {
	s := applicationTestStore(t)
	ctx := t.Context()
	opened := time.Now().UTC().Truncate(time.Millisecond)
	a, _, e := s.CreateApplication(ctx, "window", opened)
	if e != nil {
		t.Fatal(e)
	}
	times := []time.Time{
		opened.Add(-2*time.Hour - time.Millisecond),
		opened.Add(-2 * time.Hour),
		opened.Add(-time.Minute),
		opened.Add(time.Minute),
		opened.Add(time.Minute + time.Millisecond),
	}
	for i, at := range times {
		m := Marker{Version: string(rune('a' + i)), ReportedAt: monitor.Stamp(at), Source: "manual"}
		if _, e = s.PutDeployment(ctx, a.ID, "", m); e != nil {
			t.Fatal(e)
		}
	}
	resolved := opened.Add(time.Minute)
	near, e := s.NearbyDeployments(ctx, a.ID, opened, &resolved, opened.Add(time.Hour))
	if e != nil || len(near) != 3 {
		t.Fatalf("resolved incident range: %+v %v", near, e)
	}
	for i, want := range []struct {
		version string
		offset  int64
	}{{"b", -7200}, {"c", -60}, {"d", 60}} {
		if near[i].Version != want.version || near[i].OffsetSeconds != want.offset {
			t.Fatalf("nearby[%d]: %+v, want %s offset %d", i, near[i], want.version, want.offset)
		}
	}
}

func TestNearbyDeploymentsOldestTwenty(t *testing.T) {
	s := applicationTestStore(t)
	ctx := t.Context()
	opened := time.Now().UTC().Truncate(time.Millisecond)
	app, _, err := s.CreateApplication(ctx, "many markers", opened)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 22 {
		m := Marker{
			Version:    fmt.Sprintf("v%02d", i),
			ReportedAt: monitor.Stamp(opened.Add(time.Duration(i-21) * time.Minute)),
			Source:     "manual",
		}
		if _, err = s.PutDeployment(ctx, app.ID, "", m); err != nil {
			t.Fatal(err)
		}
	}
	near, err := s.NearbyDeployments(ctx, app.ID, opened, nil, opened.Add(time.Minute))
	if err != nil || len(near) != 20 {
		t.Fatalf("nearby cap: %d %v", len(near), err)
	}
	for i, marker := range near {
		want := fmt.Sprintf("v%02d", i)
		if marker.Version != want || marker.OffsetSeconds != int64(i-21)*60 {
			t.Fatalf("nearby[%d]: %+v, want %s", i, marker, want)
		}
	}
}

func TestArchivedMonitorCannotJoinApplication(t *testing.T) {
	s := applicationTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	app, _, err := s.CreateApplication(ctx, "archived path", now)
	if err != nil {
		t.Fatal(err)
	}
	m := monitor.New("archived monitor", monitor.Check{
		URL: "http://127.0.0.1:8090/healthy", Method: "GET", ExpectedStatus: 200, DeadlineMs: 1000,
	}, now)
	if err = s.Create(ctx, m); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Lifecycle(ctx, m.ID, "archive", now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetApplication(ctx, m.ID, app.ID); !errors.Is(err, ErrArchived) {
		t.Fatalf("archived monitor accepted membership: %v", err)
	}
}
