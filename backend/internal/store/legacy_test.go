package store

import (
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func TestLegacyReadStartsAtCurrentGrid(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	s := isolatedStore(t, now)
	ctx := t.Context()
	if err := s.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	m := monitor.New("old", monitor.Check{Method: "GET", DeadlineMs: 1000}, now.Add(-time.Hour))
	item, err := monitorItem(m)
	if err != nil {
		t.Fatal(err)
	}
	delete(item, "intervalSeconds")
	delete(item, "scheduledThrough")
	if _, err = s.db.PutItem(ctx, &dynamodb.PutItemInput{TableName: aws.String(s.table), Item: item}); err != nil {
		t.Fatal(err)
	}
	read, err := s.Get(ctx, m.ID)
	if err != nil || read.IntervalSeconds != 300 ||
		read.ScheduledThrough != slotStamp(m.ID, 300, now) {
		t.Fatalf("legacy default %+v %v", read, err)
	}
	_, created, gaps, err := s.Tick(ctx, now)
	if err != nil || created != 0 || gaps != 0 {
		t.Fatalf("historical gaps %d %d %v", created, gaps, err)
	}
	_, created, gaps, err = s.Tick(ctx, now.Add(301*time.Second))
	if err != nil || created != 1 || gaps != 0 {
		t.Fatalf("next schedule %d %d %v", created, gaps, err)
	}
}
