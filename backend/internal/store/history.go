package store

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

var ErrInvalidCursor = errors.New("invalid_cursor")

const historyScanBound = 2000

type HistoryFilter struct {
	Before      string
	Outcomes    []string
	Counted     *bool
	Maintenance *bool
	From        *time.Time
	To          *time.Time
}

type HistoryPage[T any] struct {
	Items           []T
	NextCursor      *string
	SearchedThrough *string
}

// A cursor binds the last examined key to the monitor, not to a mutable offset.
// SK alone cannot prove provenance: all monitor partitions have overlapping keys.
func historyCursor(id, sk string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(id + "\x00" + sk))
}

func decodeHistoryCursor(id, prefix, value string) (string, bool) {
	if value == "" {
		return "", true
	}
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", false
	}
	parts := strings.Split(string(decoded), "\x00")
	if len(parts) != 2 || parts[0] != id || !strings.HasPrefix(parts[1], prefix) {
		return "", false
	}
	stamp := strings.TrimPrefix(parts[1], prefix)
	if prefix == "OBS#" {
		point, suffix, sep := strings.Cut(stamp, "#")
		if !sep || suffix == "" {
			return "", false
		}
		stamp = point
	}
	if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
		return "", false
	}
	return parts[1], true
}

func (s *Store) HistoryObservations(
	ctx context.Context,
	id string,
	limit int,
	filter HistoryFilter,
) (HistoryPage[monitor.Observation], error) {
	page, err := s.history(ctx, id, "OBS#", limit, filter)
	result := HistoryPage[monitor.Observation]{
		Items:           []monitor.Observation{},
		NextCursor:      page.NextCursor,
		SearchedThrough: page.SearchedThrough,
	}
	if err != nil {
		return result, err
	}
	for _, item := range page.Items {
		var o monitor.Observation
		if attributevalue.UnmarshalMap(item, &o) != nil {
			return result, ErrUnavailable
		}
		if o.InitiatedBy == "" {
			o.InitiatedBy = "manual"
		}
		result.Items = append(result.Items, o)
	}
	return result, nil
}

func (s *Store) HistoryGaps(
	ctx context.Context,
	id string,
	limit int,
	before string,
) (HistoryPage[monitor.Gap], error) {
	page, err := s.history(ctx, id, "GAP#", limit, HistoryFilter{Before: before})
	result := HistoryPage[monitor.Gap]{
		Items:           []monitor.Gap{},
		NextCursor:      page.NextCursor,
		SearchedThrough: page.SearchedThrough,
	}
	if err != nil {
		return result, err
	}
	for _, item := range page.Items {
		var g monitor.Gap
		if attributevalue.UnmarshalMap(item, &g) != nil {
			return result, ErrUnavailable
		}
		result.Items = append(result.Items, g)
	}
	return result, nil
}

func (s *Store) history(
	ctx context.Context,
	id, prefix string,
	limit int,
	filter HistoryFilter,
) (HistoryPage[map[string]types.AttributeValue], error) {
	result := HistoryPage[map[string]types.AttributeValue]{
		Items: []map[string]types.AttributeValue{},
	}
	if err := s.ensure(ctx); err != nil {
		return result, err
	}
	start, valid := decodeHistoryCursor(id, prefix, filter.Before)
	if !valid {
		return result, ErrInvalidCursor
	}
	values := map[string]types.AttributeValue{
		":pk":   mustAV("MON#" + id),
		":low":  mustAV(prefix),
		":high": mustAV(prefix + "~"),
	}
	names := map[string]string{}
	clauses := []string{}
	if prefix == "OBS#" {
		if len(filter.Outcomes) > 0 {
			args := make([]string, len(filter.Outcomes))
			for i, v := range filter.Outcomes {
				arg := ":out" + string(rune('a'+i))
				args[i] = arg
				values[arg] = mustAV(v)
			}
			names["#outcome"] = "outcome"
			clauses = append(clauses, "#outcome IN ("+strings.Join(args, ", ")+")")
		}
		if filter.Counted != nil {
			names["#counted"] = "counted"
			values[":counted"] = mustAV(*filter.Counted)
			clauses = append(clauses, "#counted = :counted")
		}
		if filter.Maintenance != nil {
			names["#maintenance"] = "maintenanceWindowId"
			if *filter.Maintenance {
				clauses = append(
					clauses,
					"attribute_exists(#maintenance) AND #maintenance <> :empty",
				)
				values[":empty"] = mustAV("")
			} else {
				clauses = append(clauses, "attribute_not_exists(#maintenance) OR #maintenance = :empty")
				values[":empty"] = mustAV("")
			}
		}
		if filter.From != nil {
			names["#started"] = "startedAt"
			from := filter.From.UTC().Truncate(time.Millisecond)
			if from.Before(*filter.From) {
				from = from.Add(time.Millisecond)
			}
			values[":from"] = mustAV(monitor.Stamp(from))
			clauses = append(clauses, "#started >= :from")
		}
		if filter.To != nil {
			names["#started"] = "startedAt"
			values[":to"] = mustAV(monitor.Stamp(*filter.To))
			clauses = append(clauses, "#started <= :to")
		}
	}
	examined := 0
	for examined < historyScanBound && len(result.Items) < limit {
		count := historyScanBound - examined
		if count > 500 {
			count = 500
		}
		if count > limit-len(result.Items) {
			count = limit - len(result.Items)
		}
		// A sparse filter must continue until the match limit or scan bound, not
		// stop when DynamoDB returns an empty filtered page.
		input := &dynamodb.QueryInput{
			TableName:                 aws.String(s.table),
			ConsistentRead:            aws.Bool(true),
			ScanIndexForward:          aws.Bool(false),
			Limit:                     aws.Int32(int32(count)),
			KeyConditionExpression:    aws.String("PK = :pk AND SK BETWEEN :low AND :high"),
			ExpressionAttributeValues: values,
		}
		if len(names) > 0 {
			input.ExpressionAttributeNames = names
		}
		if len(clauses) > 0 {
			input.FilterExpression = aws.String("(" + strings.Join(clauses, ") AND (") + ")")
		}
		if start != "" {
			input.ExclusiveStartKey = key("MON#"+id, start)
		}
		out, err := s.db.Query(ctx, input)
		if err != nil {
			return result, ErrUnavailable
		}
		examined += int(out.ScannedCount)
		for _, item := range out.Items {
			if len(result.Items) >= limit {
				break
			}
			if !expiryValid(item, s.now()) {
				continue
			}
			result.Items = append(result.Items, item)
		}
		if len(out.LastEvaluatedKey) > 0 {
			start = out.LastEvaluatedKey["SK"].(*types.AttributeValueMemberS).Value
		} else if len(out.Items) > 0 {
			start = out.Items[len(out.Items)-1]["SK"].(*types.AttributeValueMemberS).Value
		}
		if len(out.LastEvaluatedKey) == 0 && examined > 0 {
			// FilterExpression hides non-matching keys in the exhausted page.
			// Look up the oldest key to state the actual scan horizon.
			tail, tailErr := s.db.Query(ctx, &dynamodb.QueryInput{
				TableName: aws.String(s.table), ConsistentRead: aws.Bool(true),
				KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :low AND :high"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":   values[":pk"],
					":low":  values[":low"],
					":high": values[":high"],
				},
				Limit: aws.Int32(1),
			})
			if tailErr != nil {
				return result, ErrUnavailable
			}
			if len(tail.Items) > 0 {
				start = tail.Items[0]["SK"].(*types.AttributeValueMemberS).Value
			}
		}
		if examined > 0 && start != "" {
			stamp := strings.TrimPrefix(start, prefix)
			if prefix == "OBS#" {
				stamp, _, _ = strings.Cut(stamp, "#")
			}
			if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				v := monitor.Stamp(t)
				result.SearchedThrough = &v
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return result, nil
		}
	}
	if start != "" {
		cursor := historyCursor(id, start)
		result.NextCursor = &cursor
	}
	return result, nil
}
