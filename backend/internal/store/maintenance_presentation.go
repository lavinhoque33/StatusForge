package store

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func (s *Store) presentMaintenance(ctx context.Context, m *monitor.Monitor) error {
	now := s.now()
	for _, id := range []string{incident.Active(m.Maintenance.Windows, now), incident.Next(m.Maintenance.Windows, now)} {
		if id == "" {
			continue
		}
		for _, ref := range m.Maintenance.Windows {
			if ref.WindowID != id {
				continue
			}
			w := monitor.MaintenanceWindow{ID: id, StartAt: ref.StartAt}
			out, err := s.db.GetItem(
				ctx,
				&dynamodb.GetItemInput{
					TableName:      aws.String(s.table),
					Key:            windowKey(m.ID, w),
					ConsistentRead: aws.Bool(true),
				},
			)
			if err != nil || len(out.Item) == 0 {
				return ErrUnavailable
			}
			if attributevalue.UnmarshalMap(out.Item, &w) != nil {
				return ErrUnavailable
			}
			w = w.WithState(now)
			if w.State == "active" {
				m.Maintenance.Active = &w
			} else if w.State == "scheduled" {
				m.Maintenance.Next = &w
			}
			break
		}
	}
	return nil
}

func maintenanceActive(m monitor.Monitor, now time.Time) bool {
	return incident.Active(m.Maintenance.Windows, now) != ""
}
