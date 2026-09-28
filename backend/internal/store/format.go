package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/buildinfo"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
)

const DataFormat = 6

var (
	ErrIncompatibleTTL = errors.New("incompatible_ttl")
	ErrNewerFormat     = errors.New("newer_data_format")
)

type Backfill struct {
	State      string                          `json:"state"      dynamodbav:"state"`
	Cursor     map[string]types.AttributeValue `json:"-"          dynamodbav:"-"`
	Stamped    int                             `json:"stamped"    dynamodbav:"stamped"`
	StartedAt  *string                         `json:"startedAt"  dynamodbav:"startedAt,omitempty"`
	FinishedAt *string                         `json:"finishedAt" dynamodbav:"finishedAt,omitempty"`
}
type formatRecord struct {
	DataFormat     int      `dynamodbav:"dataFormat"`
	WrittenBy      string   `dynamodbav:"writtenBy"`
	FirstWrittenAt string   `dynamodbav:"firstWrittenAt"`
	UpdatedAt      string   `dynamodbav:"updatedAt"`
	UpgradedFrom   *string  `dynamodbav:"upgradedFrom,omitempty"`
	Backfill       Backfill `dynamodbav:"backfill"`
}

func (s *Store) ttl(ctx context.Context) (string, string, error) {
	out, err := s.db.DescribeTimeToLive(
		ctx,
		&dynamodb.DescribeTimeToLiveInput{TableName: aws.String(s.table)},
	)
	if err != nil {
		return "", "", ErrUnavailable
	}
	desc := out.TimeToLiveDescription
	if desc == nil {
		return "unknown", "", ErrUnavailable
	}
	attr := aws.ToString(desc.AttributeName)
	status := string(desc.TimeToLiveStatus)
	if attr != "" && attr != "expiresAt" && status != string(types.TimeToLiveStatusDisabled) {
		return status, attr, fmt.Errorf("%w: table %s has TTL on incompatible attribute %s",
			ErrIncompatibleTTL, s.table, attr)
	}
	if desc.TimeToLiveStatus == types.TimeToLiveStatusDisabled {
		_, err = s.db.UpdateTimeToLive(
			ctx,
			&dynamodb.UpdateTimeToLiveInput{
				TableName: aws.String(s.table),
				TimeToLiveSpecification: &types.TimeToLiveSpecification{
					AttributeName: aws.String("expiresAt"),
					Enabled:       aws.Bool(true),
				},
			},
		)
		if err != nil {
			return status, attr, fmt.Errorf("enable TTL for table %s: %w", s.table, ErrUnavailable)
		}
		status, attr = "ENABLING", "expiresAt"
	}
	return status, attr, nil
}

func (s *Store) format(ctx context.Context) (formatRecord, error) {
	out, err := s.db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            key("SYSTEM", "FORMAT"),
			ConsistentRead: aws.Bool(true),
		},
	)
	if err != nil {
		return formatRecord{}, ErrUnavailable
	}
	var f formatRecord
	if len(out.Item) > 0 && attributevalue.UnmarshalMap(out.Item, &f) != nil {
		return f, ErrUnavailable
	}
	if cursor, ok := out.Item["cursor"].(*types.AttributeValueMemberM); ok {
		f.Backfill.Cursor = cursor.Value
	}
	return f, nil
}

func (s *Store) marker(ctx context.Context) error {
	f, err := s.format(ctx)
	if err != nil {
		return err
	}
	at := s.now().UTC().Format(time.RFC3339Nano)
	if f.DataFormat > DataFormat {
		return fmt.Errorf("%w: table %s data format %d is newer than supported format %d",
			ErrNewerFormat, s.table, f.DataFormat, DataFormat)
	}
	if f.DataFormat == 0 {
		out, e := s.db.Scan(
			ctx,
			&dynamodb.ScanInput{
				TableName:            aws.String(s.table),
				Limit:                aws.Int32(1),
				ProjectionExpression: aws.String("PK,SK"),
			},
		)
		if e != nil {
			return ErrUnavailable
		}
		state := "done"
		var from *string
		var started, finished *string
		if len(out.Items) > 0 {
			state = "running"
			unmarked := "unmarked"
			from = &unmarked
			started = &at
		} else {
			finished = &at
		}
		f = formatRecord{
			DataFormat:     DataFormat,
			WrittenBy:      buildinfo.Current(),
			FirstWrittenAt: at,
			UpdatedAt:      at,
			UpgradedFrom:   from,
			Backfill:       Backfill{State: state, StartedAt: started, FinishedAt: finished},
		}
		item, _ := attributevalue.MarshalMap(f)
		item["PK"] = mustAV("SYSTEM")
		item["SK"] = mustAV("FORMAT")
		_, e = s.db.PutItem(
			ctx,
			&dynamodb.PutItemInput{
				TableName:           aws.String(s.table),
				Item:                item,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		)
		if e != nil {
			var conditional *types.ConditionalCheckFailedException
			if errors.As(e, &conditional) {
				return s.marker(ctx)
			}
			return ErrUnavailable
		}
		return nil
	}
	_, err = s.db.UpdateItem(
		ctx,
		&dynamodb.UpdateItemInput{
			TableName:           aws.String(s.table),
			Key:                 key("SYSTEM", "FORMAT"),
			UpdateExpression:    aws.String("SET writtenBy = :v, updatedAt = :at"),
			ConditionExpression: aws.String("attribute_exists(PK)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":v":  mustAV(buildinfo.Current()),
				":at": mustAV(at),
			},
		},
	)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

// BackfillPage processes at most 25 scanned items and saves the cursor only after their writes.
func (s *Store) BackfillPage(ctx context.Context) (bool, error) {
	f, err := s.format(ctx)
	if err != nil {
		return false, err
	}
	if f.Backfill.State != "running" {
		return true, nil
	}
	out, err := s.db.Scan(
		ctx,
		&dynamodb.ScanInput{
			TableName:         aws.String(s.table),
			Limit:             aws.Int32(25),
			ExclusiveStartKey: f.Backfill.Cursor,
			ConsistentRead:    aws.Bool(true),
		},
	)
	if err != nil {
		return false, ErrUnavailable
	}
	stamped := 0
	for _, item := range out.Items {
		pk, sk := avString(item["PK"]), avString(item["SK"])
		if _, exists := item["expiresAt"]; exists {
			continue
		}
		if strings.HasPrefix(pk, "MON#") {
			m, e := s.Get(ctx, strings.TrimPrefix(pk, "MON#"))
			if e == ErrNotFound {
				continue
			}
			if e != nil {
				return false, fmt.Errorf("backfill monitor: %w", ErrUnavailable)
			}
			if m.Deletion != nil {
				continue
			}
		}
		var expiry int64
		switch {
		case len(pk) > 4 && pk[:4] == "MON#" && len(sk) > 5 && sk[:5] == "LIFE#":
			expiry = retention.History(parseStamp(avString(item["at"])))
		case len(pk) > 4 && pk[:4] == "MON#" && len(sk) > 6 && sk[:6] == "MAINT#":
			expiry = retention.History(parseStamp(avString(item["endAt"])))
		case strings.HasPrefix(pk, "APP#") && strings.HasPrefix(sk, "DEP#"):
			expiry = retention.History(parseStamp(avString(item["reportedAt"])))
		case strings.HasPrefix(pk, "APP#") && strings.HasPrefix(sk, "DEPID#"):
			reported := avString(item["reportedAt"])
			if reported == "" {
				reported, err = s.markerReportedAt(ctx, pk, avString(item["markerId"]))
				if err != nil {
					return false, err
				}
			}
			expiry = retention.History(parseStamp(reported))
		case len(pk) > 4 && pk[:4] == "MON#" && len(sk) > 4 && sk[:4] == "INC#" && avString(item["state"]) == "resolved":
			_, err = s.db.PutItem(
				ctx,
				&dynamodb.PutItemInput{
					TableName: aws.String(s.table),
					Item:      retentionJobItem(pk[4:], sk[4:]),
				},
			)
			if err != nil {
				return false, fmt.Errorf("backfill retention job: %w", ErrUnavailable)
			}
			stamped++
			continue
		default:
			continue
		}
		if expiry == 0 {
			continue
		}
		_, err = s.db.UpdateItem(
			ctx,
			&dynamodb.UpdateItemInput{
				TableName:        aws.String(s.table),
				Key:              key(pk, sk),
				UpdateExpression: aws.String("SET expiresAt = :expiry"),
				ConditionExpression: aws.String(
					"attribute_exists(PK) AND attribute_not_exists(expiresAt)",
				),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":expiry": mustAV(expiry),
				},
			},
		)
		if err != nil {
			return false, fmt.Errorf("backfill expiry update: %w", ErrUnavailable)
		}
		stamped++
	}
	update := "SET backfill.stamped = :n"
	values := map[string]types.AttributeValue{":n": mustAV(f.Backfill.Stamped + stamped)}
	if len(out.LastEvaluatedKey) > 0 {
		update += ", #cursor = :cursor"
		values[":cursor"] = &types.AttributeValueMemberM{Value: out.LastEvaluatedKey}
	} else {
		update += ", backfill.#state = :done, backfill.finishedAt = :at REMOVE #cursor"
		values[":done"] = mustAV("done")
		values[":at"] = mustAV(s.now().UTC().Format(time.RFC3339Nano))
	}
	names := map[string]string{"#cursor": "cursor"}
	if len(out.LastEvaluatedKey) == 0 {
		names["#state"] = "state"
	}
	_, err = s.db.UpdateItem(
		ctx,
		&dynamodb.UpdateItemInput{
			TableName:                 aws.String(s.table),
			Key:                       key("SYSTEM", "FORMAT"),
			UpdateExpression:          aws.String(update),
			ExpressionAttributeNames:  names,
			ExpressionAttributeValues: values,
		},
	)
	if err != nil {
		return false, fmt.Errorf("backfill marker: %w", ErrUnavailable)
	}
	return len(out.LastEvaluatedKey) == 0, nil
}
func parseStamp(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func (s *Store) markerReportedAt(ctx context.Context, pk, markerID string) (string, error) {
	var cursor map[string]types.AttributeValue
	for {
		out, e := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk": mustAV(pk),
					":sk": mustAV("DEP#"),
				},
				ConsistentRead:    aws.Bool(true),
				ExclusiveStartKey: cursor,
			},
		)
		if e != nil {
			return "", ErrUnavailable
		}
		for _, item := range out.Items {
			if avString(item["markerId"]) == markerID {
				return avString(item["reportedAt"]), nil
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return "", nil
		}
		cursor = out.LastEvaluatedKey
	}
}

func avString(v types.AttributeValue) string {
	if s, ok := v.(*types.AttributeValueMemberS); ok {
		return s.Value
	}
	return ""
}
