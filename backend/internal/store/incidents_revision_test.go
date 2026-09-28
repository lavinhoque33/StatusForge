package store

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestRevisionConflictReevaluatesAfterGap(t *testing.T) {
	s := incidentTestStore(t)
	ctx := t.Context()
	base := time.Now().UTC().Add(-time.Second)
	m := monitor.New(
		"fixture",
		monitor.Check{
			URL:            "http://127.0.0.1:8090/",
			Method:         "GET",
			ExpectedStatus: 200,
			DeadlineMs:     1000,
			MaxBodyBytes:   monitor.MaxBodyBytes,
		},
		base,
	)
	if e := s.Create(ctx, m); e != nil {
		t.Fatal(e)
	}
	record := func(at time.Time) (monitor.Observation, string) {
		t.Helper()
		_, token, e := s.ClaimManual(ctx, m.ID, at)
		if e != nil {
			t.Fatal(e)
		}
		return monitor.Observation{
			ID:            rand.Text(),
			MonitorID:     m.ID,
			ConfigVersion: 1,
			InitiatedBy:   "manual",
			Request:       m.Check,
			StartedAt:     monitor.Stamp(at),
			CompletedAt:   monitor.Stamp(at),
			Outcome:       "failing",
			Reason:        "wrong_status",
		}, token
	}
	first, token := record(base.Add(100 * time.Millisecond))
	if _, e := s.RecordResult(ctx, first, token); e != nil {
		t.Fatal(e)
	}
	second, token := record(base.Add(200 * time.Millisecond))
	current, e := s.Get(ctx, m.ID)
	if e != nil {
		t.Fatal(e)
	}
	injected := false
	s.now = func() time.Time {
		if !injected {
			injected = true
			_, e = s.db.UpdateItem(
				context.Background(),
				&dynamodb.UpdateItemInput{
					TableName:           aws.String(s.table),
					Key:                 key("MONITORS", "MON#"+m.ID),
					ConditionExpression: aws.String("evaluation.revision = :revision"),
					UpdateExpression:    aws.String("SET evaluation = :next"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":revision": mustAV(current.Evaluation.Revision),
						":next":     mustAV(incident.Clear(current.Evaluation)),
					},
				},
			)
			if e != nil {
				t.Fatalf("race injection: %v", e)
			}
		}
		return time.Now()
	}
	saved, e := s.RecordResult(ctx, second, token)
	if e != nil || !saved.Counted || !injected {
		t.Fatalf("revised result: %+v %v injected=%t", saved, e, injected)
	}
	after, e := s.Get(ctx, m.ID)
	if e != nil || after.OpenIncident != nil || len(after.Evaluation.FailRun) != 1 ||
		after.Evaluation.FailRun[0].ObservationID != second.ID {
		t.Fatalf("gap won revision race: %+v %v", after.Evaluation, e)
	}
}
