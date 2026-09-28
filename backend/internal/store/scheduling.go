package store

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

const sortLayout = "2006-01-02T15:04:05.000000000Z"

func workStamp(t time.Time) string { return t.UTC().Format(sortLayout) }
func slotStamp(id string, interval int, t time.Time) string {
	return workStamp(monitor.GridSlot(id, interval, t))
}
func mustAV(v any) types.AttributeValue { a, _ := attributevalue.Marshal(v); return a }
func monitorItem(m monitor.Monitor) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return nil, ErrUnavailable
	}
	item["PK"] = &types.AttributeValueMemberS{Value: "MONITORS"}
	item["SK"] = &types.AttributeValueMemberS{Value: "MON#" + m.ID}
	item["entityType"] = &types.AttributeValueMemberS{Value: "monitor"}
	return item, nil
}

type Work struct {
	MonitorID     string `dynamodbav:"monitorId"`
	DueAt         string `dynamodbav:"dueAt"`
	Trigger       string `dynamodbav:"trigger"`
	ConfigVersion int    `dynamodbav:"configVersion"`
	State         string `dynamodbav:"state"`
	Attempts      int    `dynamodbav:"attempts"`
	ClaimToken    string `dynamodbav:"claimToken,omitempty"`
	LeaseUntil    string `dynamodbav:"leaseUntil,omitempty"`
	OutcomeRef    string `dynamodbav:"outcomeRef,omitempty"`
	ClosedReason  string `dynamodbav:"closedReason,omitempty"`
}

func workItem(w Work) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(w)
	if err != nil {
		return nil, ErrUnavailable
	}
	item["PK"] = &types.AttributeValueMemberS{Value: "MON#" + w.MonitorID}
	item["SK"] = &types.AttributeValueMemberS{Value: "WORK#" + w.DueAt}
	item["entityType"] = &types.AttributeValueMemberS{Value: "work"}
	t, _ := time.Parse(sortLayout, w.DueAt)
	item["expiresAt"] = &types.AttributeValueMemberN{
		Value: fmt.Sprint(t.Add(7 * 24 * time.Hour).Unix()),
	}
	return item, nil
}

func gapItem(g monitor.Gap) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(g)
	if err != nil {
		return nil, ErrUnavailable
	}
	item["PK"] = &types.AttributeValueMemberS{Value: "MON#" + g.MonitorID}
	item["SK"] = &types.AttributeValueMemberS{Value: "GAP#" + g.FromDueAt}
	item["entityType"] = &types.AttributeValueMemberS{Value: "gap"}
	t, _ := time.Parse(time.RFC3339Nano, g.RecordedAt)
	item["expiresAt"] = &types.AttributeValueMemberN{
		Value: fmt.Sprint(t.Add(90 * 24 * time.Hour).Unix()),
	}
	return item, nil
}

func cancelled(err error, index int) bool {
	var tx *types.TransactionCanceledException
	return errors.As(err, &tx) && len(tx.CancellationReasons) > index &&
		tx.CancellationReasons[index].Code != nil &&
		*tx.CancellationReasons[index].Code == "ConditionalCheckFailed"
}

func (s *Store) markTickFailure(monitors []monitor.Monitor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(monitors) == 0 {
		for id := range s.lastSuccessfulTick {
			s.needsRecovery[id] = true
		}
		return
	}
	for _, m := range monitors {
		s.needsRecovery[m.ID] = true
	}
}

func (s *Store) Tick(ctx context.Context, now time.Time) (int, int, int, error) {
	ms, err := s.List(ctx)
	if err != nil {
		s.markTickFailure(nil)
		return 0, 0, 0, err
	}
	active, created, gaps := 0, 0, 0
	for _, m := range ms {
		if m.Lifecycle == "active" {
			active++
			latest := slotStamp(m.ID, m.IntervalSeconds, now)
			if latest > m.ScheduledThrough {
				prev, _ := time.Parse(sortLayout, m.ScheduledThrough)
				if prev.IsZero() {
					prev = monitor.GridSlot(m.ID, m.IntervalSeconds, now)
				}
				first, last, count := monitor.SlotsBetween(m.ID, m.IntervalSeconds, prev, now)
				witem, _ := workItem(
					Work{
						MonitorID:     m.ID,
						DueAt:         latest,
						Trigger:       "schedule",
						ConfigVersion: m.ConfigVersion,
						State:         "pending",
					},
				)
				tx := []types.TransactWriteItem{
					{
						Put: &types.Put{
							TableName:           aws.String(s.table),
							Item:                witem,
							ConditionExpression: aws.String("attribute_not_exists(PK)"),
						},
					},
				}
				if count > 0 {
					gi, _ := gapItem(
						monitor.Gap{
							ID:          rand.Text(),
							MonitorID:   m.ID,
							FromDueAt:   workStamp(first),
							ToDueAt:     workStamp(last),
							MissedCount: count,
							Reason:      "not_scheduled",
							RecordedAt:  monitor.Stamp(now),
						},
					)
					tx = append(
						tx,
						types.TransactWriteItem{
							Put: &types.Put{
								TableName:           aws.String(s.table),
								Item:                gi,
								ConditionExpression: aws.String("attribute_not_exists(PK)"),
							},
						},
					)
				}
				vals := map[string]types.AttributeValue{
					":latest": mustAV(latest),
					":active": mustAV("active"),
				}
				cond := "attribute_not_exists(scheduledThrough) AND lifecycle = :active"
				if !m.LegacyCursor {
					cond = "scheduledThrough = :previous AND lifecycle = :active"
					vals[":previous"] = mustAV(m.ScheduledThrough)
				}
				updateExpr := "SET scheduledThrough = :latest"
				if count > 0 {
					updateExpr += ", evaluation = :evaluation"
					vals[":evaluation"] = mustAV(incident.Clear(m.Evaluation))
					vals[":revision"] = mustAV(m.Evaluation.Revision)
					if m.Evaluation.Revision == 0 {
						cond += " AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
					} else {
						cond += " AND evaluation.revision = :revision"
					}
				}
				tx = append(
					tx,
					types.TransactWriteItem{
						Update: &types.Update{
							TableName:                 aws.String(s.table),
							Key:                       key("MONITORS", "MON#"+m.ID),
							UpdateExpression:          aws.String(updateExpr),
							ConditionExpression:       aws.String(cond),
							ExpressionAttributeValues: vals,
						},
					},
				)
				_, err = s.db.TransactWriteItems(
					ctx,
					&dynamodb.TransactWriteItemsInput{TransactItems: tx},
				)
				if err != nil && !cancelled(err, len(tx)-1) && !cancelled(err, 0) {
					s.markTickFailure(ms)
					return active, created, gaps, ErrUnavailable
				}
				if err == nil {
					created++
					if count > 0 {
						gaps++
					}
				}
			}
		}
		from := workWindow(m, now)
		s.mu.Lock()
		last, known := s.lastSuccessfulTick[m.ID]
		recovering := s.needsRecovery[m.ID]
		s.mu.Unlock()
		if !known {
			from = now.Add(-7 * 24 * time.Hour)
		} else if recovering {
			from = workWindow(m, last)
		}
		retention := now.Add(-7 * 24 * time.Hour).Add(time.Nanosecond)
		if from.Before(retention) {
			from = retention
		}
		closed, scanErr := s.closeDueWork(ctx, m, from, now)
		if scanErr != nil {
			s.markTickFailure(ms)
			return active, created, gaps, scanErr
		}
		gaps += closed
		s.mu.Lock()
		s.lastSuccessfulTick[m.ID] = now
		delete(s.needsRecovery, m.ID)
		s.mu.Unlock()
	}
	return active, created, gaps, nil
}

// closeDueWork closes every item that has crossed its claim/retry boundary.
// This runs even with a saturated worker channel, before dispatch, so no
// pending item can age silently out of the short WORK# tick range.
func (s *Store) closeDueWork(
	ctx context.Context,
	m monitor.Monitor,
	from, now time.Time,
) (int, error) {
	items, err := s.queryWorks(ctx, m.ID, from, now)
	if err != nil {
		return 0, err
	}
	gaps := 0
	for _, w := range items {
		due, err := time.Parse(sortLayout, w.DueAt)
		if err != nil {
			return gaps, ErrUnavailable
		}
		if w.State == "claimed" {
			until, err := time.Parse(sortLayout, w.LeaseUntil)
			if err != nil {
				return gaps, ErrUnavailable
			}
			if !now.After(until) {
				continue
			}
		}
		reason := ""
		switch {
		case m.Lifecycle != "active":
			reason = m.Lifecycle
		case m.ConfigVersion != w.ConfigVersion:
			reason = "config_changed"
		case w.State == "pending" &&
			!now.Before(due.Add(time.Duration(m.IntervalSeconds)*time.Second)):
			reason = "overdue"
		case w.State == "claimed" &&
			(w.Attempts >= 2 || !now.Before(due.Add(time.Duration(m.IntervalSeconds)*time.Second))):
			reason = "lease_expired"
		}
		if reason == "" {
			continue
		}
		if err := s.closeWork(ctx, w, reason, now); err != nil {
			if err == ErrNotEligible {
				continue
			}
			return gaps, err
		}
		if reason == "overdue" || reason == "lease_expired" {
			gaps++
		}
	}
	return gaps, nil
}

func workWindow(m monitor.Monitor, now time.Time) time.Time {
	interval := m.IntervalSeconds
	if interval == 0 {
		interval = monitor.DefaultIntervalSeconds
	}
	return now.Add(-time.Duration(interval+50) * time.Second)
}

// queryWorks bounds each query by dueAt. The startup sweep uses a seven-day
// history range; ordinary ticks only inspect the interval and lease horizon.
func (s *Store) queryWorks(
	ctx context.Context,
	id string,
	from, to time.Time,
) ([]Work, error) {
	result := make([]Work, 0, 16)
	var start map[string]types.AttributeValue
	for {
		out, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :from AND :to"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":   mustAV("MON#" + id),
				":from": mustAV("WORK#" + workStamp(from)),
				":to":   mustAV("WORK#" + workStamp(to)),
			},
			ConsistentRead:    aws.Bool(true),
			Limit:             aws.Int32(200),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return nil, ErrUnavailable
		}
		for _, item := range out.Items {
			var w Work
			if attributevalue.UnmarshalMap(item, &w) != nil {
				return nil, ErrUnavailable
			}
			if w.State == "pending" || w.State == "claimed" {
				result = append(result, w)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			return result, nil
		}
		start = out.LastEvaluatedKey
	}
}

func (s *Store) Works(ctx context.Context, m monitor.Monitor, now time.Time) ([]Work, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	return s.queryWorks(ctx, m.ID, workWindow(m, now), now)
}

// SweepOld is the one-time startup recovery of retained old obligations.
// Subsequent successful ticks use a short query, widened only after failures.
func (s *Store) SweepOld(ctx context.Context, now time.Time) (int, error) {
	monitors, err := s.List(ctx)
	if err != nil {
		return 0, err
	}
	gaps := 0
	for _, m := range monitors {
		works, err := s.queryWorks(
			ctx,
			m.ID,
			now.Add(-7*24*time.Hour).Add(time.Nanosecond),
			workWindow(m, now).Add(-time.Nanosecond),
		)
		if err != nil {
			return gaps, err
		}
		for _, w := range works {
			_, token, err := s.Claim(ctx, w, now)
			if err == ErrLeaseHeld || err == ErrNotEligible {
				continue
			}
			if err != nil {
				return gaps, err
			}
			if token != "" {
				// An interval increase can make an old work item eligible
				// under the new interval. It still needs normal dispatch.
				return gaps, ErrNotEligible
			}
			if m.Lifecycle == "active" && m.ConfigVersion == w.ConfigVersion {
				gaps++
			}
		}
		s.mu.Lock()
		s.lastSuccessfulTick[m.ID] = now
		delete(s.needsRecovery, m.ID)
		s.mu.Unlock()
	}
	return gaps, nil
}

func (s *Store) Gaps(ctx context.Context, id string, limit int) ([]monitor.Gap, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	result := []monitor.Gap{}
	var start map[string]types.AttributeValue
	for len(result) < limit {
		out, err := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":     mustAV("MON#" + id),
					":prefix": mustAV("GAP#"),
				},
				ConsistentRead:    aws.Bool(true),
				ScanIndexForward:  aws.Bool(false),
				Limit:             aws.Int32(int32(limit - len(result))),
				ExclusiveStartKey: start,
			},
		)
		if err != nil {
			return nil, ErrUnavailable
		}
		for _, item := range out.Items {
			expiry, ok := item["expiresAt"].(*types.AttributeValueMemberN)
			if !ok {
				return nil, ErrUnavailable
			}
			n, e := strconv.ParseInt(expiry.Value, 10, 64)
			if e != nil {
				return nil, ErrUnavailable
			}
			if time.Unix(n, 0).After(s.now()) {
				var g monitor.Gap
				if attributevalue.UnmarshalMap(item, &g) != nil {
					return nil, ErrUnavailable
				}
				result = append(result, g)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	return result, nil
}

func (s *Store) closeWork(ctx context.Context, w Work, reason string, now time.Time) error {
	state := "cancelled"
	if reason == "overdue" || reason == "lease_expired" {
		state = "missed"
	}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key:       key("MON#"+w.MonitorID, "WORK#"+w.DueAt),
				UpdateExpression: aws.String(
					"SET #state = :closed, closedReason = :reason",
				),
				ConditionExpression:      aws.String("#state = :old AND attempts = :attempts"),
				ExpressionAttributeNames: map[string]string{"#state": "state"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":closed":   mustAV(state),
					":reason":   mustAV(reason),
					":old":      mustAV(w.State),
					":attempts": mustAV(w.Attempts),
				},
			},
		},
	}
	if reason == "overdue" || reason == "lease_expired" {
		item, _ := gapItem(
			monitor.Gap{
				ID:          rand.Text(),
				MonitorID:   w.MonitorID,
				FromDueAt:   w.DueAt,
				ToDueAt:     w.DueAt,
				MissedCount: 1,
				Reason:      reason,
				RecordedAt:  monitor.Stamp(now),
			},
		)
		tx = append(
			tx,
			types.TransactWriteItem{
				Put: &types.Put{
					TableName:           aws.String(s.table),
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
		)
	}
	if reason == "overdue" || reason == "lease_expired" {
		m, e := s.Get(ctx, w.MonitorID)
		if e != nil {
			return e
		}
		cond := "evaluation.revision = :revision"
		if m.Evaluation.Revision == 0 {
			cond = "attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision"
		}
		tx = append(
			tx,
			types.TransactWriteItem{
				Update: &types.Update{
					TableName:           aws.String(s.table),
					Key:                 key("MONITORS", "MON#"+w.MonitorID),
					UpdateExpression:    aws.String("SET evaluation = :next"),
					ConditionExpression: aws.String(cond),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":next":     mustAV(incident.Clear(m.Evaluation)),
						":revision": mustAV(m.Evaluation.Revision),
					},
				},
			},
		)
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if err != nil {
		if cancelled(err, 0) {
			return ErrNotEligible
		}
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Claim(ctx context.Context, w Work, now time.Time) (monitor.Monitor, string, error) {
	m, err := s.Get(ctx, w.MonitorID)
	if err != nil {
		return m, "", err
	}
	due, _ := time.Parse(sortLayout, w.DueAt)
	if w.State == "claimed" {
		until, _ := time.Parse(time.RFC3339Nano, w.LeaseUntil)
		if !now.After(until) {
			return m, "", ErrLeaseHeld
		}
	}
	if m.Lifecycle != "active" || m.ConfigVersion != w.ConfigVersion {
		reason := "config_changed"
		if m.Lifecycle != "active" {
			reason = m.Lifecycle
		}
		return m, "", s.closeWork(ctx, w, reason, now)
	}
	if w.State == "pending" && !now.Before(due.Add(time.Duration(m.IntervalSeconds)*time.Second)) {
		return m, "", s.closeWork(ctx, w, "overdue", now)
	}
	if w.State == "claimed" &&
		(w.Attempts >= 2 || !now.Before(due.Add(time.Duration(m.IntervalSeconds)*time.Second))) {
		return m, "", s.closeWork(ctx, w, "lease_expired", now)
	}
	token := rand.Text()
	until := workStamp(now.Add(time.Duration(m.Check.DeadlineMs)*time.Millisecond + 10*time.Second))
	lease := monitor.Lease{
		Token:     token,
		Until:     until,
		Kind:      "scheduled",
		StartedAt: monitor.Stamp(now),
	}
	if id := incident.Active(m.Maintenance.Windows, now); id != "" {
		lease.MaintenanceWindowID = &id
	}
	indexCond, indexValues := indexCondition(m)
	indexValues[":lease"] = mustAV(lease)
	indexValues[":active"] = mustAV("active")
	indexValues[":version"] = mustAV(w.ConfigVersion)
	indexValues[":now"] = mustAV(workStamp(now))
	_, err = s.db.TransactWriteItems(
		ctx,
		&dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName: aws.String(s.table),
					Key:       key("MON#"+w.MonitorID, "WORK#"+w.DueAt),
					UpdateExpression: aws.String(
						"SET #state = :claimed, claimToken = :token, leaseUntil = :until, attempts = :next",
					),
					ConditionExpression:      aws.String("#state = :old AND attempts = :attempts"),
					ExpressionAttributeNames: map[string]string{"#state": "state"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":claimed":  mustAV("claimed"),
						":token":    mustAV(token),
						":until":    mustAV(until),
						":next":     mustAV(w.Attempts + 1),
						":old":      mustAV(w.State),
						":attempts": mustAV(w.Attempts),
					},
				},
			},
			{
				Update: &types.Update{
					TableName:        aws.String(s.table),
					Key:              key("MONITORS", "MON#"+w.MonitorID),
					UpdateExpression: aws.String("SET lease = :lease, lastClaimAt = :now"),
					ConditionExpression: aws.String(
						"lifecycle = :active AND configVersion = :version AND (attribute_not_exists(lease) OR lease.#until < :now) AND " + indexCond,
					),
					ExpressionAttributeNames: map[string]string{
						"#until":   "until",
						"#windows": "windows",
					},
					ExpressionAttributeValues: indexValues,
				},
			},
		}},
	)
	if err != nil {
		if cancelled(err, 0) {
			return m, "", ErrNotEligible
		}
		if cancelled(err, 1) {
			return m, "", ErrLeaseHeld
		}
		return m, "", ErrUnavailable
	}
	m.Lease = &lease
	m.LastClaimAt = workStamp(now)
	return m, token, nil
}

func (s *Store) ClaimManual(
	ctx context.Context,
	id string,
	now time.Time,
) (monitor.Monitor, string, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return m, "", err
	}
	if m.Lifecycle == "archived" {
		return m, "", ErrArchived
	}
	token := rand.Text()
	lease := monitor.Lease{
		Token: token,
		Kind:  "manual",
		Until: workStamp(
			now.Add(time.Duration(m.Check.DeadlineMs)*time.Millisecond + 10*time.Second),
		),
		StartedAt: monitor.Stamp(now),
	}
	if windowID := incident.Active(m.Maintenance.Windows, now); windowID != "" {
		lease.MaintenanceWindowID = &windowID
	}
	indexCond, indexValues := indexCondition(m)
	indexValues[":lease"] = mustAV(lease)
	indexValues[":archived"] = mustAV("archived")
	indexValues[":now"] = mustAV(workStamp(now))
	_, err = s.db.UpdateItem(
		ctx,
		&dynamodb.UpdateItemInput{
			TableName:        aws.String(s.table),
			Key:              key("MONITORS", "MON#"+id),
			UpdateExpression: aws.String("SET lease = :lease, lastClaimAt = :now"),
			ConditionExpression: aws.String(
				"lifecycle <> :archived AND (attribute_not_exists(lease) OR lease.#until < :now) AND " + indexCond,
			),
			ExpressionAttributeNames:  map[string]string{"#until": "until", "#windows": "windows"},
			ExpressionAttributeValues: indexValues,
		},
	)
	if err != nil {
		var cond *types.ConditionalCheckFailedException
		if errors.As(err, &cond) {
			current, readErr := s.Get(ctx, id)
			if readErr != nil {
				return m, "", readErr
			}
			if current.Lifecycle == "archived" {
				return current, "", ErrArchived
			}
			return current, "", ErrLeaseHeld
		}
		return m, "", ErrUnavailable
	}
	m.LastClaimAt = workStamp(now)
	m.Lease = &lease
	return m, token, nil
}

func observationItem(o monitor.Observation) (map[string]types.AttributeValue, error) {
	item, err := attributevalue.MarshalMap(o)
	if err != nil {
		return nil, ErrUnavailable
	}
	started, _ := time.Parse(time.RFC3339Nano, o.StartedAt)
	item["PK"] = mustAV("MON#" + o.MonitorID)
	item["SK"] = mustAV("OBS#" + workStamp(started) + "#" + o.ID)
	item["entityType"] = mustAV("observation")
	item["expiresAt"] = mustAV(started.Add(90 * 24 * time.Hour).Unix())
	return item, nil
}

func (s *Store) RecordResult(
	ctx context.Context,
	o monitor.Observation,
	token string,
) (monitor.Observation, error) {
	if err := s.ensure(ctx); err != nil {
		return o, err
	}
	m, err := s.Get(ctx, o.MonitorID)
	if err != nil {
		return o, err
	}
	if m.Lease != nil && m.Lease.Token == token {
		o.MaintenanceWindowID = m.Lease.MaintenanceWindowID
	}
	o.Counted = true
	evidence := monitor.Evidence{
		ObservationID:  o.ID,
		InitiatedBy:    o.InitiatedBy,
		StartedAt:      o.StartedAt,
		CompletedAt:    o.CompletedAt,
		Outcome:        o.Outcome,
		Reason:         o.Reason,
		ObservedStatus: o.ObservedStatus,
		ConfigVersion:  o.ConfigVersion,
		Observation:    o,
	}
	item, _ := observationItem(o)
	monitorKey := key("MONITORS", "MON#"+o.MonitorID)
	workUpdates := func() []types.TransactWriteItem {
		if o.DueAt == nil {
			return nil
		}
		return []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName: aws.String(s.table),
					Key:       key("MON#"+o.MonitorID, "WORK#"+*o.DueAt),
					UpdateExpression: aws.String(
						"SET #state = :done, outcomeRef = :ref, closedReason = :completed",
					),
					ConditionExpression: aws.String(
						"claimToken = :token AND #state = :claimed",
					),
					ExpressionAttributeNames: map[string]string{"#state": "state"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":done":      mustAV("done"),
						":ref":       mustAV(o.ID),
						":completed": mustAV("completed"),
						":token":     mustAV(token),
						":claimed":   mustAV("claimed"),
					},
				},
			},
		}
	}
	var workLost bool
	revisionRace := false
	for range 3 {
		next, open, effects, evalErr := s.evaluationItems(ctx, m, o, s.now())
		if evalErr != nil {
			return o, evalErr
		}
		values := map[string]types.AttributeValue{
			":evidence": mustAV(evidence), ":token": mustAV(token),
			":now": mustAV(workStamp(s.now())), ":active": mustAV("active"),
			":version": mustAV(o.ConfigVersion), ":started": mustAV(o.StartedAt),
			":evaluation": mustAV(next), ":revision": mustAV(m.Evaluation.Revision),
		}
		condition := "lease.#token = :token AND lease.#until >= :now AND lifecycle = :active AND configVersion = :version AND (attribute_not_exists(#status) OR #status.startedAt < :started) AND evaluation.revision = :revision"
		if m.Evaluation.Revision == 0 {
			condition = "lease.#token = :token AND lease.#until >= :now AND lifecycle = :active AND configVersion = :version AND (attribute_not_exists(#status) OR #status.startedAt < :started) AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
		}
		expr := "SET #status = :evidence, evaluation = :evaluation REMOVE lease"
		if open == nil {
			expr += ", openIncident"
		} else {
			expr = "SET #status = :evidence, evaluation = :evaluation, openIncident = :open REMOVE lease"
			values[":open"] = mustAV(open)
		}
		tx := []types.TransactWriteItem{
			{Update: &types.Update{
				TableName: aws.String(s.table), Key: monitorKey,
				UpdateExpression: aws.String(expr), ConditionExpression: aws.String(condition),
				ExpressionAttributeNames: map[string]string{
					"#status": "status",
					"#token":  "token",
					"#until":  "until",
				},
				ExpressionAttributeValues: values,
			}},
			{
				Put: &types.Put{
					TableName:           aws.String(s.table),
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
		}
		tx = append(tx, workUpdates()...)
		tx = append(tx, effects...)
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if err == nil {
			if m.OpenIncident != nil && open == nil {
				s.cancelResolvedReminders(ctx, m.ID, m.OpenIncident.ID)
			}
			return o, nil
		}
		workLost = o.DueAt != nil && cancelled(err, 2)
		if !cancelled(err, 0) && !workLost {
			return o, ErrUnavailable
		}
		current, readErr := s.Get(ctx, o.MonitorID)
		if readErr != nil {
			return o, readErr
		}
		if current.Evaluation.Revision != m.Evaluation.Revision &&
			current.Lease != nil && current.Lease.Token == token &&
			current.Lifecycle == "active" && current.ConfigVersion == o.ConfigVersion &&
			(current.Evidence == nil || current.Evidence.StartedAt < o.StartedAt) &&
			!workLost {
			revisionRace = true
			m = current
			continue
		}
		revisionRace = false
		m = current
		break
	}
	if revisionRace {
		return o, ErrUnavailable
	}
	m, err = s.Get(ctx, o.MonitorID)
	if err != nil {
		return o, err
	}
	reason := monitor.NotCountedOlder
	switch {
	case workLost || m.Lease == nil || m.Lease.Token != token || m.Lease.Until < workStamp(s.now()):
		reason = monitor.NotCountedLeaseLost
	case m.Lifecycle == "paused":
		reason = monitor.NotCountedPaused
	case m.Lifecycle == "archived":
		reason = monitor.NotCountedArchived
	case m.ConfigVersion != o.ConfigVersion:
		reason = monitor.NotCountedConfigChanged
	}
	o.Counted = false
	o.NotCountedReason = &reason
	item, _ = observationItem(o)
	tx := []types.TransactWriteItem{}
	if reason != monitor.NotCountedLeaseLost {
		tx = append(
			tx,
			types.TransactWriteItem{
				Update: &types.Update{
					TableName:                aws.String(s.table),
					Key:                      monitorKey,
					UpdateExpression:         aws.String("REMOVE lease"),
					ConditionExpression:      aws.String("lease.#token = :token"),
					ExpressionAttributeNames: map[string]string{"#token": "token"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":token": mustAV(token),
					},
				},
			},
		)
	}
	tx = append(
		tx,
		types.TransactWriteItem{
			Put: &types.Put{
				TableName:           aws.String(s.table),
				Item:                item,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		},
	)
	if reason != monitor.NotCountedLeaseLost {
		tx = append(tx, workUpdates()...)
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if err != nil {
		return o, ErrUnavailable
	}
	return o, nil
}
