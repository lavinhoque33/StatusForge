package store

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/summary"
)

const (
	summaryObservationLimit = 20000
	summaryGapLimit         = 5000
)

type windowRecord struct {
	Kind                string            `dynamodbav:"kind"`
	DueAt               *string           `dynamodbav:"dueAt"`
	StartedAt           string            `dynamodbav:"startedAt"`
	Counted             bool              `dynamodbav:"counted"`
	NotCountedReason    *string           `dynamodbav:"notCountedReason"`
	MaintenanceWindowID *string           `dynamodbav:"maintenanceWindowId"`
	Outcome             string            `dynamodbav:"outcome"`
	Reason              string            `dynamodbav:"reason"`
	Report              *heartbeat.Report `dynamodbav:"report"`
	DurationMs          int64             `dynamodbav:"durationMs"`
}

// windowQuery returns up to limit newest records from the key range. A sentinel record
// detects truncation without excluding a record when the exact bound is reached.
func (s *Store) windowQuery(
	ctx context.Context,
	pk, prefix string,
	from, to time.Time,
	limit int,
	projection string,
) ([]map[string]types.AttributeValue, bool, error) {
	items := make([]map[string]types.AttributeValue, 0)
	var cursor map[string]types.AttributeValue
	for len(items) <= limit {
		count := limit + 1 - len(items)
		if count > 500 {
			count = 500
		}
		input := &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :from AND :to"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":   mustAV(pk),
				":from": mustAV(prefix + workStamp(from)),
				":to":   mustAV(prefix + workStamp(to) + "~"),
			},
			ConsistentRead:    aws.Bool(true),
			ScanIndexForward:  aws.Bool(false),
			Limit:             aws.Int32(int32(count)),
			ExclusiveStartKey: cursor,
		}
		if projection != "" {
			names := map[string]string{}
			fields := strings.Split(projection, ", ")
			for i, field := range fields {
				alias := "#f" + strconv.Itoa(i)
				names[alias] = field
				fields[i] = alias
			}
			input.ProjectionExpression = aws.String(strings.Join(fields, ", "))
			input.ExpressionAttributeNames = names
		}
		out, e := s.db.Query(ctx, input)
		if e != nil {
			return nil, false, ErrUnavailable
		}
		items = append(items, out.Items...)
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	truncated := len(items) > limit
	if truncated {
		items = items[:limit]
	}
	return items, truncated, nil
}

func expiryValid(item map[string]types.AttributeValue, now time.Time) bool {
	v, ok := item["expiresAt"].(*types.AttributeValueMemberN)
	if !ok {
		return false
	}
	n, e := strconv.ParseInt(v.Value, 10, 64)
	return e == nil && time.Unix(n, 0).After(now)
}

func (s *Store) Summary(
	ctx context.Context,
	m monitor.Monitor,
	window string,
	now time.Time,
) (summary.Result, error) {
	in, err := s.summaryInput(ctx, m, window, now, summaryObservationLimit, summaryGapLimit)
	if err != nil {
		return summary.Result{}, err
	}
	return summary.Compute(in), nil
}

func (s *Store) HeartbeatSummary(
	ctx context.Context, m monitor.Monitor, window string, now time.Time,
) (summary.HeartbeatResult, error) {
	in, err := s.summaryInput(ctx, m, window, now, summaryObservationLimit, summaryGapLimit)
	if err != nil {
		return summary.HeartbeatResult{}, err
	}
	return summary.ComputeHeartbeat(in), nil
}

func (s *Store) summaryWithBounds(
	ctx context.Context, m monitor.Monitor, window string, now time.Time,
	observationLimit, gapLimit int,
) (summary.Result, error) {
	in, err := s.summaryInput(ctx, m, window, now, observationLimit, gapLimit)
	if err != nil {
		return summary.Result{}, err
	}
	return summary.Compute(in), nil
}

func (s *Store) summaryInput(
	ctx context.Context,
	m monitor.Monitor,
	window string,
	now time.Time,
	observationLimit, gapLimit int,
) (summary.Input, error) {
	length := 24 * time.Hour
	if window == "7d" {
		length = 7 * 24 * time.Hour
	}
	from := now.Add(-length)
	observations, otrunc, e := s.windowQuery(
		ctx,
		"MON#"+m.ID,
		"OBS#",
		from,
		now,
		observationLimit,
		"kind, dueAt, startedAt, counted, notCountedReason, maintenanceWindowId, outcome, reason, report, durationMs, expiresAt, PK, SK",
	)
	if e != nil {
		return summary.Input{}, e
	}
	gaps, gtrunc, e := s.windowQuery(
		ctx,
		"MON#"+m.ID,
		"GAP#",
		from.Add(-length),
		now,
		gapLimit,
		"fromDueAt, toDueAt, missedCount, expiresAt, PK, SK",
	)
	if e != nil {
		return summary.Input{}, e
	}
	in := summary.Input{
		MonitorID: m.ID,
		Window:    window,
		Lifecycle: m.Lifecycle,
		PausedAt:  m.PausedAt,
		State:     monitor.WithStatus(m, now).Status.State,
		To:        now,
		Truncated: otrunc || gtrunc,
	}
	if otrunc && len(observations) > 0 {
		in.CoveredFrom = mustTime(
			observations[len(observations)-1]["startedAt"].(*types.AttributeValueMemberS).Value,
		)
	}
	if gtrunc && len(gaps) > 0 {
		oldest := mustTime(gaps[len(gaps)-1]["fromDueAt"].(*types.AttributeValueMemberS).Value)
		if oldest.After(in.CoveredFrom) {
			in.CoveredFrom = oldest
		}
	}
	for _, item := range observations {
		if !expiryValid(item, now) {
			continue
		}
		var o windowRecord
		if attributevalue.UnmarshalMap(item, &o) != nil {
			return summary.Input{}, ErrUnavailable
		}
		in.Observations = append(
			in.Observations,
			summary.Observation{
				Kind:                o.Kind,
				DueAt:               o.DueAt,
				StartedAt:           o.StartedAt,
				Counted:             o.Counted,
				NotCountedReason:    o.NotCountedReason,
				MaintenanceWindowID: o.MaintenanceWindowID,
				Outcome:             o.Outcome,
				FailureReport:       o.Kind == "heartbeat_report" && o.Reason == "reported_failure",
				Late:                o.Report != nil && o.Report.Late,
				Reason:              o.Reason,
				DurationMs:          o.DurationMs,
			},
		)
	}
	for _, item := range gaps {
		if !expiryValid(item, now) {
			continue
		}
		var g monitor.Gap
		if attributevalue.UnmarshalMap(item, &g) != nil {
			return summary.Input{}, ErrUnavailable
		}
		in.Gaps = append(
			in.Gaps,
			summary.Gap{FromDueAt: g.FromDueAt, ToDueAt: g.ToDueAt, MissedCount: g.MissedCount},
		)
	}
	// Read only the window, its preceding transition, and the earliest event
	// (the latter communicates how far back pause history is known).
	events, e := s.lifecycleWindow(ctx, m.ID, from, now)
	if e != nil {
		return summary.Input{}, e
	}
	for _, event := range events {
		in.Events = append(in.Events, summary.LifecycleEvent{Action: event.Action, At: event.At})
	}
	windows, e := s.maintenanceWindow(ctx, m.ID, from.Add(-7*24*time.Hour), now)
	if e != nil {
		return summary.Input{}, e
	}
	for _, w := range windows {
		if w.CancelledAt != nil && *w.CancelledAt <= w.StartAt {
			continue
		}
		end := w.EndAt
		if w.CancelledAt != nil && *w.CancelledAt < end {
			end = *w.CancelledAt
		}
		in.MaintenanceWindows = append(
			in.MaintenanceWindows,
			summary.MaintenanceSpan{ID: w.ID, From: w.StartAt, To: end},
		)
	}
	live, e := s.Liveness(ctx)
	if e != nil {
		return summary.Input{}, e
	}
	for _, o := range live.Outages {
		in.Outages = append(in.Outages, summary.Span{From: o.From, To: o.To})
	}
	return in, nil
}

func (s *Store) lifecycleWindow(
	ctx context.Context, id string, from, to time.Time,
) ([]LifecycleEvent, error) {
	pk := "MON#" + id
	earliest, err := s.query(ctx, pk, "LIFE#", 1, false)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	events := []LifecycleEvent{}
	add := func(items []map[string]types.AttributeValue) error {
		for _, item := range items {
			sk := item["SK"].(*types.AttributeValueMemberS).Value
			if seen[sk] {
				continue
			}
			var event LifecycleEvent
			if attributevalue.UnmarshalMap(item, &event) != nil {
				return ErrUnavailable
			}
			seen[sk] = true
			events = append(events, event)
		}
		return nil
	}
	if err := add(earliest); err != nil {
		return nil, err
	}
	before, err := s.db.Query(ctx, &dynamodb.QueryInput{
		TableName: aws.String(s.table), ConsistentRead: aws.Bool(true),
		KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :lower AND :upper"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk": mustAV(pk), ":lower": mustAV("LIFE#"),
			":upper": mustAV("LIFE#" + workStamp(from)),
		},
		ScanIndexForward: aws.Bool(false), Limit: aws.Int32(1),
	})
	if err != nil {
		return nil, ErrUnavailable
	}
	if err := add(before.Items); err != nil {
		return nil, err
	}
	var cursor map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName: aws.String(s.table), ConsistentRead: aws.Bool(true),
			KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :lower AND :upper"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": mustAV(pk), ":lower": mustAV("LIFE#" + workStamp(from)),
				":upper": mustAV("LIFE#" + workStamp(to) + "~"),
			},
			ExclusiveStartKey: cursor,
		})
		if err != nil {
			return nil, ErrUnavailable
		}
		if err := add(out.Items); err != nil {
			return nil, err
		}
		if len(out.LastEvaluatedKey) == 0 {
			return events, nil
		}
		cursor = out.LastEvaluatedKey
	}
}

func (s *Store) maintenanceWindow(
	ctx context.Context, id string, from, to time.Time,
) ([]monitor.MaintenanceWindow, error) {
	windows := []monitor.MaintenanceWindow{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName: aws.String(s.table), ConsistentRead: aws.Bool(true),
			KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :from AND :to"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk": mustAV("MON#" + id), ":from": mustAV("MAINT#" + workStamp(from)),
				":to": mustAV("MAINT#" + workStamp(to) + "~"),
			},
			ExclusiveStartKey: cursor,
		})
		if err != nil {
			return nil, ErrUnavailable
		}
		for _, item := range out.Items {
			var w monitor.MaintenanceWindow
			if attributevalue.UnmarshalMap(item, &w) != nil {
				return nil, ErrUnavailable
			}
			windows = append(windows, w)
		}
		if len(out.LastEvaluatedKey) == 0 {
			return windows, nil
		}
		cursor = out.LastEvaluatedKey
	}
}
