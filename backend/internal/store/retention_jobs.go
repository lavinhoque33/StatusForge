package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
)

func retentionJobItem(mid, id string) map[string]types.AttributeValue {
	item := key("JOBS", "JOB#retain#"+mid+"#"+id)
	item["token"] = mustAV(rand.Text())
	return item
}

func (s *Store) retentionJob(mid, id string) types.TransactWriteItem {
	return types.TransactWriteItem{
		Put: &types.Put{TableName: aws.String(s.table), Item: retentionJobItem(mid, id)},
	}
}

func (s *Store) queryPage(
	ctx context.Context,
	pk, prefix string,
	limit int32,
) ([]map[string]types.AttributeValue, error) {
	condition := "PK = :pk AND begins_with(SK, :prefix)"
	values := map[string]types.AttributeValue{":pk": mustAV(pk), ":prefix": mustAV(prefix)}
	if prefix == "" {
		condition = "PK = :pk"
		delete(values, ":prefix")
	}
	out, err := s.db.Query(
		ctx,
		&dynamodb.QueryInput{
			TableName:                 aws.String(s.table),
			KeyConditionExpression:    aws.String(condition),
			ExpressionAttributeValues: values,
			ConsistentRead:            aws.Bool(true),
			Limit:                     aws.Int32(limit),
		},
	)
	if err != nil {
		return nil, ErrUnavailable
	}
	return out.Items, nil
}

func (s *Store) stampRow(
	ctx context.Context,
	pk, sk string,
	expires int64,
	notification bool,
) (bool, error) {
	condition := "attribute_exists(PK)"
	values := map[string]types.AttributeValue{":at": mustAV(expires)}
	var names map[string]string
	if notification {
		condition += " AND #state IN (:delivered,:failed,:cancelled)"
		names = map[string]string{"#state": "state"}
		values[":delivered"], values[":failed"], values[":cancelled"] =
			mustAV("delivered"), mustAV("failed"), mustAV("cancelled")
	}
	_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
		TableName: aws.String(s.table), Key: key(pk, sk),
		UpdateExpression:         aws.String("SET expiresAt = :at"),
		ConditionExpression:      aws.String(condition),
		ExpressionAttributeNames: names, ExpressionAttributeValues: values,
	})
	if err != nil {
		var failed *types.ConditionalCheckFailedException
		if errors.As(err, &failed) {
			return false, nil
		}
		return false, fmt.Errorf("%w: stamp %s/%s: %v", ErrUnavailable, pk, sk, err)
	}
	return true, nil
}

// RetainJob processes at most 25 children or pointers per invocation. The incident is stamped last.
func (s *Store) RetainJob(ctx context.Context, mid, id string) error {
	jobSK := "JOB#retain#" + mid + "#" + id
	job, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table), Key: key("JOBS", jobSK), ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return ErrUnavailable
	}
	if len(job.Item) == 0 {
		return nil
	}
	token := avString(job.Item["token"])
	m, e := s.Get(ctx, mid)
	if e == ErrNotFound {
		return s.removeJob(ctx, "JOB#retain#"+mid+"#"+id)
	}
	if e != nil {
		return e
	}
	if m.Deletion != nil {
		return nil
	}
	in, e := s.incident(ctx, mid, id)
	if e == ErrIncidentNotFound {
		return s.removeJob(ctx, "JOB#retain#"+mid+"#"+id)
	}
	if e != nil {
		return e
	}
	if in.ResolvedAt == nil {
		return nil
	}
	expires := retention.History(parseStamp(*in.ResolvedAt))
	// Check every notification, not just the first page, before stamping.
	var noteCursor map[string]types.AttributeValue
	for {
		out, e := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk": mustAV("MON#" + mid),
					":sk": mustAV("INCX#" + id + "#NOTE#"),
				},
				Limit:             aws.Int32(100),
				ConsistentRead:    aws.Bool(true),
				ExclusiveStartKey: noteCursor,
			},
		)
		if e != nil {
			return ErrUnavailable
		}
		for _, it := range out.Items {
			if avString(it["entityType"]) != "notification" {
				continue
			}
			switch avString(it["state"]) {
			case "pending", "retry_wait", "sending":
				return nil
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		noteCursor = out.LastEvaluatedKey
	}
	for _, prefix := range []struct{ pk, prefix string }{{"MON#" + mid, "INCX#" + id + "#"}, {"DELIVERY", "ATTENTION#" + mid + "#" + id + "#"}} {
		rows, complete, e := s.unstamped(ctx, prefix.pk, prefix.prefix, expires)
		if e != nil {
			return e
		}
		for _, it := range rows {
			notification := avString(it["entityType"]) == "notification"
			ok, stampErr := s.stampRow(ctx, prefix.pk, avString(it["SK"]), expires, notification)
			if stampErr != nil {
				return stampErr
			}
			if !ok {
				return nil
			}
		}
		if !complete {
			return nil
		}
	}
	return s.finalizeRetention(ctx, mid, id, expires, token)
}

func (s *Store) finalizeRetention(
	ctx context.Context,
	mid, id string,
	expires int64,
	token string,
) error {
	jobSK := "JOB#retain#" + mid + "#" + id
	_, e := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{
		TransactItems: []types.TransactWriteItem{
			{Update: &types.Update{
				TableName: aws.String(s.table), Key: key("MON#"+mid, "INC#"+id),
				ConditionExpression:       aws.String("attribute_exists(PK)"),
				UpdateExpression:          aws.String("SET expiresAt = :at"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":at": mustAV(expires)},
			}},
			{Delete: &types.Delete{
				TableName: aws.String(s.table), Key: key("JOBS", jobSK),
				ConditionExpression:       aws.String("#token = :token"),
				ExpressionAttributeNames:  map[string]string{"#token": "token"},
				ExpressionAttributeValues: map[string]types.AttributeValue{":token": mustAV(token)},
			}},
		},
	})
	if e != nil {
		if cancelled(e, 0) || cancelled(e, 1) {
			return nil
		}
		return fmt.Errorf("%w: retention final transaction: %v", ErrUnavailable, e)
	}
	return nil
}

func expiredOrStamped(it map[string]types.AttributeValue, at int64) bool {
	v, ok := it["expiresAt"].(*types.AttributeValueMemberN)
	return ok && v.Value == strconv.FormatInt(at, 10)
}

func (s *Store) unstamped(
	ctx context.Context,
	pk, prefix string,
	expires int64,
) ([]map[string]types.AttributeValue, bool, error) {
	rows := make([]map[string]types.AttributeValue, 0, 25)
	var cursor map[string]types.AttributeValue
	for {
		out, e := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":     mustAV(pk),
					":prefix": mustAV(prefix),
				},
				ConsistentRead:    aws.Bool(true),
				ExclusiveStartKey: cursor,
				Limit:             aws.Int32(100),
			},
		)
		if e != nil {
			return nil, false, ErrUnavailable
		}
		for _, it := range out.Items {
			if !expiredOrStamped(it, expires) {
				rows = append(rows, it)
				if len(rows) == 25 {
					return rows, false, nil
				}
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return rows, true, nil
		}
		cursor = out.LastEvaluatedKey
	}
}

func (s *Store) removeJob(ctx context.Context, sk string) error {
	_, err := s.db.DeleteItem(
		ctx,
		&dynamodb.DeleteItemInput{TableName: aws.String(s.table), Key: key("JOBS", sk)},
	)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) RetentionJobs(ctx context.Context) (int, error) {
	items, e := s.queryPage(ctx, "JOBS", "JOB#retain#", 10000)
	return len(items), e
}

func (s *Store) processRetention(ctx context.Context) (int, error) {
	out, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName:              aws.String(s.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": mustAV("JOBS"), ":sk": mustAV("JOB#retain#"),
		},
		Limit: aws.Int32(10), ConsistentRead: aws.Bool(true),
		ExclusiveStartKey: s.retentionJobCursor,
	})
	if err != nil {
		return 0, ErrUnavailable
	}
	s.retentionJobCursor = out.LastEvaluatedKey
	var failures []error
	for _, job := range out.Items {
		parts := strings.SplitN(strings.TrimPrefix(avString(job["SK"]), "JOB#retain#"), "#", 2)
		if len(parts) != 2 {
			continue
		}
		if jobErr := s.RetainJob(ctx, parts[0], parts[1]); jobErr != nil {
			failures = append(failures, jobErr)
		}
	}
	return len(out.Items), errors.Join(failures...)
}
