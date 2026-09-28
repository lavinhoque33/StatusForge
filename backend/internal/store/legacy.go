package store

import (
	"context"
	"errors"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func (s *Store) initializeLegacyCursor(
	ctx context.Context,
	m monitor.Monitor,
) (monitor.Monitor, error) {
	if m.Kind == "heartbeat" {
		return monitor.WithStatus(m, s.now()), nil
	}
	if m.IntervalSeconds == 0 {
		m.IntervalSeconds = monitor.DefaultIntervalSeconds
	}
	if m.ScheduledThrough == "" {
		cursor := slotStamp(m.ID, m.IntervalSeconds, s.now())
		_, err := s.db.UpdateItem(
			ctx,
			&dynamodb.UpdateItemInput{
				TableName: aws.String(s.table),
				Key:       key("MONITORS", "MON#"+m.ID),
				UpdateExpression: aws.String(
					"SET scheduledThrough = :cursor, intervalSeconds = if_not_exists(intervalSeconds, :interval)",
				),
				ConditionExpression: aws.String("attribute_not_exists(scheduledThrough)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":cursor":   mustAV(cursor),
					":interval": mustAV(m.IntervalSeconds),
				},
			},
		)
		if err != nil {
			var condition *types.ConditionalCheckFailedException
			if errors.As(err, &condition) {
				return s.Get(ctx, m.ID)
			}
			return m, ErrUnavailable
		}
		m.ScheduledThrough = cursor
	}
	return monitor.WithStatus(m, s.now()), nil
}
