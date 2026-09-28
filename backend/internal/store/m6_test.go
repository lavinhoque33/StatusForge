package store

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
)

func m6Monitor(t *testing.T, s *Store, now time.Time) monitor.Monitor {
	t.Helper()
	m := monitor.New(
		"delete me",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/healthy",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
		},
		now,
	)
	if e := s.Create(t.Context(), m); e != nil {
		t.Fatal(e)
	}
	return m
}

func TestRetentionWaitsForNotificationAndRetryProtectsIncident(t *testing.T) {
	s := incidentTestStore(t)
	ctx := t.Context()
	fixed := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	m := m6Monitor(t, s, fixed)
	id := "resolved-test"
	at := monitor.Stamp(fixed)
	expiry := retention.History(fixed)
	for _, it := range []map[string]types.AttributeValue{
		{"PK": mustAV("MON#" + m.ID), "SK": mustAV("INC#" + id), "incidentId": mustAV(id), "monitorId": mustAV(m.ID), "state": mustAV("resolved"), "resolvedAt": mustAV(at)},
		{"PK": mustAV("MON#" + m.ID), "SK": mustAV(noteSK(id, "opened")), "entityType": mustAV("notification"), "state": mustAV("pending"), "noteKey": mustAV("opened"), "incidentId": mustAV(id), "monitorId": mustAV(m.ID)},
		pointer("ATTENTION#" + m.ID + "#" + id + "#opened"), retentionJobItem(m.ID, id),
	} {
		if _, e := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: it}); e != nil {
			t.Fatal(e)
		}
	}
	if e := s.RetainJob(ctx, m.ID, id); e != nil {
		t.Fatal(e)
	}
	if _, ok := rawM6(t, s, "MON#"+m.ID, "INC#"+id)["expiresAt"]; ok {
		t.Fatal("non-final notification did not protect incident")
	}
	_, e := s.db.UpdateItem(
		ctx,
		&dynamodb.UpdateItemInput{
			TableName:                 aws.String(s.table),
			Key:                       key("MON#"+m.ID, noteSK(id, "opened")),
			UpdateExpression:          aws.String("SET #state = :failed"),
			ExpressionAttributeNames:  map[string]string{"#state": "state"},
			ExpressionAttributeValues: map[string]types.AttributeValue{":failed": mustAV("failed")},
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	for range 4 {
		if e = s.RetainJob(ctx, m.ID, id); e != nil {
			t.Fatal(e)
		}
	}
	if got := rawM6(t, s, "MON#"+m.ID, "INC#"+id)["expiresAt"]; got == nil ||
		got.(*types.AttributeValueMemberN).Value != mustAV(expiry).(*types.AttributeValueMemberN).Value {
		t.Fatalf("incident expiry %v", got)
	}
	if rawM6(t, s, "DELIVERY", "ATTENTION#"+m.ID+"#"+id+"#opened")["expiresAt"] == nil {
		t.Fatal("attention pointer not stamped")
	}
	if _, e = s.RetryNotification(ctx, m.ID, id, "opened", fixed.Add(time.Second)); e != nil {
		t.Fatal(e)
	}
	for _, sk := range []string{"INC#" + id, noteSK(id, "opened")} {
		if _, ok := rawM6(t, s, "MON#"+m.ID, sk)["expiresAt"]; ok {
			t.Fatalf("manual retry left %s expiring", sk)
		}
	}
	if e = s.RetainJob(ctx, m.ID, id); e != nil {
		t.Fatal(e)
	}
	if _, ok := rawM6(t, s, "MON#"+m.ID, "INC#"+id)["expiresAt"]; ok {
		t.Fatal("pending retry not protected")
	}
}

func TestRetentionRetryTokenPreventsStaleFinalization(t *testing.T) {
	s := incidentTestStore(t)
	ctx := t.Context()
	fixed := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	m := m6Monitor(t, s, fixed)
	id := "retry-race"
	for _, row := range []map[string]types.AttributeValue{
		{
			"PK": mustAV("MON#" + m.ID), "SK": mustAV("INC#" + id), "incidentId": mustAV(id),
			"monitorId": mustAV(m.ID), "state": mustAV("resolved"), "resolvedAt": mustAV(monitor.Stamp(fixed)),
		},
		retentionJobItem(m.ID, id),
	} {
		if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: row}); err != nil {
			t.Fatal(err)
		}
	}
	old := avString(rawM6(t, s, "JOBS", "JOB#retain#"+m.ID+"#"+id)["token"])
	if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(s.table), Item: retentionJobItem(m.ID, id),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.finalizeRetention(ctx, m.ID, id, retention.History(fixed), old); err != nil {
		t.Fatal(err)
	}
	if rawM6(t, s, "MON#"+m.ID, "INC#"+id)["expiresAt"] != nil {
		t.Fatal("stale retention run stamped the incident after retry")
	}
	if rawM6(t, s, "JOBS", "JOB#retain#"+m.ID+"#"+id)["token"] == nil {
		t.Fatal("stale retention run deleted the renewed job")
	}
	if err := s.RetainJob(ctx, m.ID, id); err != nil {
		t.Fatal(err)
	}
	if rawM6(t, s, "MON#"+m.ID, "INC#"+id)["expiresAt"] == nil {
		t.Fatal("renewed retention job did not complete")
	}
}

func TestAttentionSkipsOrphanPointer(t *testing.T) {
	s := incidentTestStore(t)
	if err := s.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.PutItem(t.Context(), &dynamodb.PutItemInput{
		TableName: aws.String(s.table), Item: pointer("ATTENTION#missing#incident#opened"),
	}); err != nil {
		t.Fatal(err)
	}
	notes, err := s.Attention(t.Context(), 10)
	if err != nil || len(notes) != 0 {
		t.Fatalf("orphan attention pointer: notes=%v err=%v", notes, err)
	}
}

func TestRetentionStampRejectsNotificationChangedToPending(t *testing.T) {
	s := incidentTestStore(t)
	if err := s.Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	pk, sk := "MON#race", noteSK("incident", "opened")
	row := map[string]types.AttributeValue{
		"PK": mustAV(pk), "SK": mustAV(sk), "entityType": mustAV("notification"),
		"state": mustAV("pending"),
	}
	if _, err := s.db.PutItem(t.Context(), &dynamodb.PutItemInput{
		TableName: aws.String(s.table), Item: row,
	}); err != nil {
		t.Fatal(err)
	}
	stamped, err := s.stampRow(
		t.Context(),
		pk,
		sk,
		retention.History(time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)),
		true,
	)
	if err != nil || stamped || rawM6(t, s, pk, sk)["expiresAt"] != nil {
		t.Fatalf("pending notification was stamped: success=%v error=%v", stamped, err)
	}
}

func TestWaitingRetentionJobsDoNotStarveLaterIncident(t *testing.T) {
	s := incidentTestStore(t)
	ctx := t.Context()
	fixed := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return fixed }
	m := m6Monitor(t, s, fixed)
	for i := range 11 {
		id := fmt.Sprintf("incident-%02d", i)
		items := []map[string]types.AttributeValue{
			{
				"PK": mustAV("MON#" + m.ID), "SK": mustAV("INC#" + id),
				"incidentId": mustAV(id), "monitorId": mustAV(m.ID),
				"state": mustAV("resolved"), "resolvedAt": mustAV(monitor.Stamp(fixed)),
			},
			retentionJobItem(m.ID, id),
		}
		if i < 10 {
			items = append(items, map[string]types.AttributeValue{
				"PK": mustAV("MON#" + m.ID), "SK": mustAV(noteSK(id, "opened")),
				"entityType": mustAV("notification"), "state": mustAV("pending"),
			})
		}
		for _, item := range items {
			if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
				TableName: aws.String(s.table), Item: item,
			}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for range 2 {
		if _, err := s.processRetention(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if rawM6(t, s, "MON#"+m.ID, "INC#incident-10")["expiresAt"] == nil {
		t.Fatal("ready incident starved behind waiting retention jobs")
	}
}

func rawM6(t *testing.T, s *Store, pk, sk string) map[string]types.AttributeValue {
	t.Helper()
	out, e := s.db.GetItem(
		t.Context(),
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            key(pk, sk),
			ConsistentRead: aws.Bool(true),
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	return out.Item
}

func TestDeletionCompletesWithoutRecreatingMonitorOrApplication(t *testing.T) {
	s := incidentTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	m := m6Monitor(t, s, now)
	if _, e := s.StartMonitorDeletion(ctx, m.ID, m.Name, now); !errors.Is(e, ErrNotArchived) {
		t.Fatalf("active monitor deletion: %v", e)
	}
	if _, e := s.Lifecycle(ctx, m.ID, "archive", now); e != nil {
		t.Fatal(e)
	}
	if _, e := s.StartMonitorDeletion(ctx, m.ID, "wrong", now); !errors.Is(e, ErrNameMismatch) {
		t.Fatalf("mismatch: %v", e)
	}
	status, e := s.StartMonitorDeletion(ctx, m.ID, m.Name, now)
	if e != nil || status.State != "deleting" {
		t.Fatalf("start monitor deletion %+v %v", status, e)
	}
	for range 10 {
		if e = s.RunHousekeeping(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Get(ctx, m.ID); errors.Is(e, ErrNotFound) {
			break
		}
	}
	if _, e = s.Get(ctx, m.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("monitor survived: %v", e)
	}
	if e = s.PutObservation(ctx, monitor.Observation{ID: "late", MonitorID: m.ID, StartedAt: monitor.Stamp(now)}); e == nil {
		t.Fatal("deleted monitor accepted a late observation")
	}
	rows, e := s.queryPage(ctx, "MON#"+m.ID, "", 100)
	if e != nil || len(rows) != 0 {
		t.Fatalf("partition recreated: %d %v", len(rows), e)
	}
	app, _, e := s.CreateApplication(ctx, "archived app", now)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ArchiveApplication(ctx, app.ID, now); e != nil {
		t.Fatal(e)
	}
	if _, e = s.StartApplicationDeletion(ctx, app.ID, app.Name, now); e != nil {
		t.Fatal(e)
	}
	for range 10 {
		if e = s.RunHousekeeping(ctx); e != nil {
			t.Fatal(e)
		}
		if _, e = s.Application(ctx, app.ID); errors.Is(e, ErrNotFound) {
			break
		}
	}
	if _, e = s.Application(ctx, app.ID); !errors.Is(e, ErrNotFound) {
		t.Fatalf("application survived: %v", e)
	}
	rows, e = s.queryPage(ctx, "APP#"+app.ID, "", 100)
	if e != nil || len(rows) != 0 {
		t.Fatalf("application partition survives: %d %v", len(rows), e)
	}
}

func TestDeletionCountsParallelBatchRows(t *testing.T) {
	s := incidentTestStore(t)
	ctx := t.Context()
	fixed := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	m := m6Monitor(t, s, fixed)
	if _, err := s.Lifecycle(ctx, m.ID, "archive", fixed); err != nil {
		t.Fatal(err)
	}
	for i := range 175 {
		item := key("MON#"+m.ID, fmt.Sprintf("OBS#bulk-%03d", i))
		if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(s.table), Item: item,
		}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.queryPage(ctx, "MON#"+m.ID, "", 250)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartMonitorDeletion(ctx, m.ID, m.Name, fixed); err != nil {
		t.Fatal(err)
	}
	complete, err := s.deleteBatch(ctx, "monitor", m.ID, "MON#"+m.ID, "", nil)
	if err != nil || complete {
		t.Fatalf("first batch: complete=%v error=%v", complete, err)
	}
	status, err := s.MonitorDeletion(ctx, m.ID)
	if err != nil || status.RemovedItems != len(rows) {
		t.Fatalf("removed rows=%d, want %d: %v", status.RemovedItems, len(rows), err)
	}
	remaining, err := s.queryPage(ctx, "MON#"+m.ID, "", 250)
	if err != nil || len(remaining) != 0 {
		t.Fatalf("partition after bulk delete: %d remaining, error=%v", len(remaining), err)
	}
}

func TestArchivedPendingNotificationCompletesBeforeMonitorDeletion(t *testing.T) {
	fixed := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	s := incidentTestStore(t)
	ctx := t.Context()
	s.now = func() time.Time { return fixed.Add(5 * time.Second) }
	m := m6Monitor(t, s, fixed)
	id, at := "pending-opened", monitor.Stamp(fixed.Add(time.Second))
	for _, row := range []map[string]types.AttributeValue{
		{
			"PK": mustAV("MON#" + m.ID), "SK": mustAV("INC#" + id),
			"incidentId": mustAV(id), "monitorId": mustAV(m.ID), "state": mustAV("open"),
			"openedAt": mustAV(monitor.Stamp(fixed)),
		},
		{
			"PK": mustAV("MON#" + m.ID), "SK": mustAV(noteSK(id, "opened")),
			"entityType": mustAV("notification"), "id": mustAV(id + ":opened"),
			"monitorId": mustAV(m.ID), "incidentId": mustAV(id), "noteKey": mustAV("opened"),
			"kind": mustAV("opened"), "state": mustAV("pending"), "attempts": mustAV(0),
			"nextAttemptAt": mustAV(at), "createdAt": mustAV(at), "payload": mustAV("{}"),
		},
		pointer(dueSK(at, m.ID, id, "opened")),
	} {
		if _, err := s.db.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(s.table), Item: row,
		}); err != nil {
			t.Fatal(err)
		}
	}
	due, err := s.Due(ctx, fixed.Add(time.Second))
	if err != nil || len(due) != 1 || due[0].NoteKey != "opened" {
		t.Fatalf("pending opened delivery: %+v %v", due, err)
	}
	if _, err := s.Lifecycle(ctx, m.ID, "archive", fixed.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	status, err := s.StartMonitorDeletion(ctx, m.ID, m.Name, fixed.Add(2*time.Second))
	if err != nil || status.State != "waiting_for_notifications" {
		t.Fatalf("deletion should await opened notification: %+v %v", status, err)
	}
	if err := s.RunHousekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err = s.MonitorDeletion(ctx, m.ID); err != nil ||
		status.State != "waiting_for_notifications" {
		t.Fatalf("pending notification lost deletion barrier: %+v %v", status, err)
	}
	n, attempt, token, err := s.ClaimDelivery(ctx, due[0], fixed.Add(3*time.Second))
	if err != nil {
		t.Fatalf("archived deletion blocked delivery: %v", err)
	}
	httpStatus := 204
	state, err := s.CompleteDelivery(ctx, due[0], n, attempt, token, "delivered",
		&httpStatus, 1, fixed.Add(4*time.Second), nil)
	if err != nil || state != "delivered" {
		t.Fatalf("complete delivery: state=%s err=%v", state, err)
	}
	if _, err := s.processDeletions(ctx); err != nil {
		t.Fatal(err)
	}
	if status, err = s.MonitorDeletion(ctx, m.ID); err != nil ||
		status.State != "deleting" || status.RemovedItems == 0 {
		t.Fatalf("deletion did not leave waiting state: %+v %v", status, err)
	}
	if err := s.RunHousekeeping(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("monitor survived deletion: %v", err)
	}
	children, err := s.queryPage(ctx, "MON#"+m.ID, "", 100)
	if err != nil || len(children) != 0 {
		t.Fatalf("monitor partition survives: %d rows err=%v", len(children), err)
	}
	pointers, err := s.queryPage(ctx, "DELIVERY", "", 100)
	if err != nil || len(pointers) != 0 {
		t.Fatalf("delivery pointers survive: %d rows err=%v", len(pointers), err)
	}
}
