package store

import (
	"context"
	"crypto/rand"
	"errors"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func monitorKind(m monitor.Monitor) string {
	if m.Kind == "heartbeat" {
		return "heartbeat"
	}
	return "http"
}

func payloadURL(m monitor.Monitor) *string {
	if m.Kind == "heartbeat" {
		return nil
	}
	u := m.Check.URL
	return &u
}

func (s *Store) PatchHeartbeat(
	ctx context.Context,
	id string,
	expected int,
	name *string,
	schedule *heartbeat.Schedule,
	policy *incident.Policy,
	now time.Time,
) (monitor.Monitor, error) {
	m, e := s.Get(ctx, id)
	if e != nil {
		return m, e
	}
	if m.Kind != "heartbeat" {
		return m, ErrNotEligible
	}
	if m.Lifecycle == "archived" {
		return m, ErrArchived
	}
	if m.ConfigVersion != expected {
		return m, ErrVersionConflict
	}
	next := m.Patch(name, nil, now)
	if policy != nil {
		next.IncidentPolicy = *policy
	}
	changed := schedule != nil && *schedule != m.Heartbeat.Schedule
	config := *m.Heartbeat
	next.Heartbeat = &config
	if changed {
		next.Heartbeat.Schedule = *schedule
		if m.Expectation != nil {
			v := heartbeat.Change(*m.Expectation, *schedule, now)
			next.Expectation = &v
		}
	}
	trigger := ""
	if changed {
		trigger = "config_change"
		next.Evaluation = incident.Clear(m.Evaluation)
	}
	e = s.saveHeartbeat(
		ctx,
		next,
		m,
		"configVersion = :version AND updatedAt = :updated",
		map[string]types.AttributeValue{
			":version": mustAV(expected),
			":updated": mustAV(m.UpdatedAt),
		},
		trigger,
		now,
	)
	return next, e
}

func (s *Store) saveHeartbeat(
	ctx context.Context,
	m, previous monitor.Monitor,
	condition string,
	values map[string]types.AttributeValue,
	trigger string,
	now time.Time,
) error {
	values[":name"] = mustAV(m.Name)
	values[":hb"] = mustAV(m.Heartbeat)
	values[":policy"] = mustAV(m.IncidentPolicy.Defaults())
	values[":updatedNext"] = mustAV(m.UpdatedAt)
	values[":lifecycle"] = mustAV(m.Lifecycle)
	values[":revision"] = mustAV(previous.Evaluation.Revision)
	if previous.Heartbeat != nil && previous.Heartbeat.Token != nil {
		condition += " AND heartbeat.#token.#hash = :oldHash"
		values[":oldHash"] = mustAV(previous.Heartbeat.Token.Hash)
	} else {
		condition += " AND attribute_not_exists(heartbeat.#token.#hash)"
	}
	expr := "SET #name = :name, heartbeat = :hb, incidentPolicy = :policy, updatedAt = :updatedNext, lifecycle = :lifecycle"
	if m.Expectation != nil {
		expr += ", expectation = :expectation"
		values[":expectation"] = mustAV(m.Expectation)
	}
	condition += " AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
	if trigger == "resume" || trigger == "config_change" {
		values[":evaluation"] = mustAV(m.Evaluation)
		expr += ", evaluation = :evaluation"
		values[":maintenance"] = mustAV(m.Maintenance)
		expr += ", maintenance = :maintenance"
		if m.OpenIncident != nil {
			values[":open"] = mustAV(m.OpenIncident)
			expr += ", openIncident = :open"
		}
	}
	if m.Lifecycle == "paused" {
		expr += ", pausedAt = :paused"
		values[":paused"] = mustAV(m.PausedAt)
	}
	if m.Lifecycle == "archived" {
		expr += ", archivedAt = :archived"
		values[":archived"] = mustAV(m.ArchivedAt)
	}
	removals := []string{}
	if m.Expectation == nil {
		removals = append(removals, "expectation")
	}
	if m.Lifecycle != "paused" {
		removals = append(removals, "pausedAt")
	}
	if m.Lifecycle == "archived" && previous.OpenIncident != nil {
		removals = append(removals, "openIncident")
	}
	if len(removals) > 0 {
		expr += " REMOVE " + strings.Join(removals, ",")
	}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName:           aws.String(s.table),
				Key:                 key("MONITORS", "MON#"+m.ID),
				ConditionExpression: aws.String(condition),
				UpdateExpression:    aws.String(expr),
				ExpressionAttributeNames: map[string]string{
					"#name":  "name",
					"#token": "token",
					"#hash":  "hash",
				},
				ExpressionAttributeValues: values,
			},
		},
	}
	if m.Lifecycle != previous.Lifecycle {
		action := map[string]string{"paused": "paused", "active": "resumed", "archived": "archived"}[m.Lifecycle]
		tx = append(tx, s.lifecycleEvent(m.ID, action, now))
	}
	if previous.OpenIncident != nil {
		kind := ""
		details := map[string]any{}
		switch {
		case m.Lifecycle == "archived":
			kind = "resolved"
		case m.Lifecycle == "paused" && previous.Lifecycle == "active":
			kind = "paused"
		case m.Lifecycle == "active" && previous.Lifecycle == "paused":
			kind = "resumed"
		case trigger == "config_change":
			kind = "config_changed"
			details["fromVersion"] = previous.ConfigVersion
			details["toVersion"] = m.ConfigVersion
		}
		if kind != "" {
			tx = append(tx, s.event(m.ID, previous.OpenIncident.ID, kind, now, details))
		}
		if kind == "resolved" {
			at := monitor.Stamp(now)
			resolution := "archived"
			tx = append(
				tx,
				types.TransactWriteItem{
					Update: &types.Update{
						TableName: aws.String(s.table),
						Key: key(
							incidentPK(m.ID),
							"INC#"+previous.OpenIncident.ID,
						),
						ConditionExpression: aws.String("#state = :open"),
						UpdateExpression: aws.String(
							"SET #state = :resolved, resolution = :resolution, resolvedAt = :now, recoveryEvidence = :empty",
						),
						ExpressionAttributeNames: map[string]string{"#state": "state"},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":open":       mustAV("open"),
							":resolved":   mustAV("resolved"),
							":resolution": mustAV(resolution),
							":now":        mustAV(at),
							":empty":      mustAV([]incident.Evidence{}),
						},
					},
				},
			)
			inc, e := s.incident(ctx, m.ID, previous.OpenIncident.ID)
			if e != nil {
				return e
			}
			inc.State = "resolved"
			inc.ResolvedAt = &at
			inc.Resolution = &resolution
			notes, e := s.intent(previous, inc, "resolved", "resolved", nil, nil, now)
			if e != nil {
				return e
			}
			tx = append(tx, notes...)
		}
	}
	_, e := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if e != nil {
		if cancelled(e, 0) {
			return ErrVersionConflict
		}
		return ErrUnavailable
	}
	return nil
}

func (s *Store) HeartbeatToken(
	ctx context.Context,
	id string,
	revoke bool,
	now time.Time,
) (string, *heartbeat.Token, error) {
	for range 3 {
		m, e := s.Get(ctx, id)
		if e != nil {
			return "", nil, e
		}
		if m.Kind != "heartbeat" {
			return "", nil, ErrNotEligible
		}
		if m.Lifecycle == "archived" {
			return "", nil, ErrArchived
		}
		var value string
		var token *heartbeat.Token
		if !revoke {
			value, e = heartbeat.Generate()
			if e != nil {
				return "", nil, ErrUnavailable
			}
			token = &heartbeat.Token{
				Hash:      heartbeat.Hash(value),
				Hint:      heartbeat.Hint(value),
				CreatedAt: monitor.Stamp(now),
			}
		}
		condition := "attribute_exists(PK) AND lifecycle <> :archived AND #kind = :heartbeat"
		values := map[string]types.AttributeValue{
			":archived":  mustAV("archived"),
			":heartbeat": mustAV("heartbeat"),
		}
		if m.Heartbeat.Token == nil {
			condition += " AND attribute_not_exists(heartbeat.#token.#hash)"
		} else {
			condition += " AND heartbeat.#token.#hash = :old"
			values[":old"] = mustAV(m.Heartbeat.Token.Hash)
		}
		expr := "REMOVE heartbeat.#token"
		if !revoke {
			expr = "SET heartbeat.#token = :new"
			values[":new"] = mustAV(token)
		}
		_, e = s.db.UpdateItem(
			ctx,
			&dynamodb.UpdateItemInput{
				TableName:           aws.String(s.table),
				Key:                 key("MONITORS", "MON#"+id),
				ConditionExpression: aws.String(condition),
				UpdateExpression:    aws.String(expr),
				ExpressionAttributeNames: map[string]string{
					"#kind":  "kind",
					"#token": "token",
					"#hash":  "hash",
				},
				ExpressionAttributeValues: values,
			},
		)
		if e == nil {
			return value, token, nil
		}
		var conflict *types.ConditionalCheckFailedException
		if !errors.As(e, &conflict) {
			return "", nil, ErrUnavailable
		}
	}
	return "", nil, ErrVersionConflict
}

func (s *Store) AuthenticateHeartbeat(
	ctx context.Context,
	id, token string,
) (monitor.Monitor, error) {
	m, e := s.Get(ctx, id)
	if e != nil {
		return m, e
	}
	if m.Kind != "heartbeat" || m.Heartbeat == nil || m.Heartbeat.Token == nil ||
		!heartbeat.Matches(token, m.Heartbeat.Token.Hash) {
		return m, ErrNotEligible
	}
	return m, nil
}

type ReportResult struct {
	Accepted         bool    `json:"accepted"`
	Duplicate        bool    `json:"duplicate"`
	Counted          bool    `json:"counted"`
	NotCountedReason *string `json:"notCountedReason"`
	Late             bool    `json:"late"`
	NextDueAt        string  `json:"nextDueAt"`
}

func (s *Store) RecordHeartbeat(
	ctx context.Context,
	id, hash string,
	report heartbeat.Report,
	now time.Time,
) (ReportResult, error) {
	result := ReportResult{Accepted: true}
	for range 3 {
		m, e := s.Get(ctx, id)
		if e != nil {
			return result, e
		}
		if m.Lifecycle == "archived" {
			return result, ErrArchived
		}
		if m.Kind != "heartbeat" || m.Heartbeat.Token == nil || m.Heartbeat.Token.Hash != hash {
			return result, ErrNotEligible
		}
		stamp := monitor.Stamp(now)
		late := false
		if m.Expectation != nil {
			due, _ := time.Parse(time.RFC3339Nano, m.Expectation.DueAt)
			missing, _ := time.Parse(time.RFC3339Nano, m.Expectation.MissingAt)
			late = now.After(due) && !now.After(missing)
		}
		report.Late = late
		o := monitor.Observation{
			ID:            rand.Text(),
			MonitorID:     id,
			Kind:          "heartbeat_report",
			ConfigVersion: m.ConfigVersion,
			InitiatedBy:   "job",
			StartedAt:     stamp,
			CompletedAt:   stamp,
			DurationMs:    0,
			Outcome:       "healthy",
			Reason:        "ok",
			Report:        &report,
		}
		trigger := "report"
		o.Trigger = &trigger
		if report.DurationMs != nil {
			o.DurationMs = *report.DurationMs
		}
		if report.Status == "failure" {
			o.Outcome = "failing"
			o.Reason = "reported_failure"
		}
		result.Late = late
		reason := ""
		if m.Lifecycle == "paused" {
			reason = monitor.NotCountedPaused
		} else if report.FinishedAt != nil && m.Expectation != nil && m.Expectation.LastFinishedAt != "" {
			latest, _ := time.Parse(time.RFC3339Nano, m.Expectation.LastFinishedAt)
			finished, _ := time.Parse(time.RFC3339Nano, *report.FinishedAt)
			if finished.Before(latest) {
				reason = monitor.NotCountedOlder
			}
		}
		o.Counted = reason == ""
		result.Counted = o.Counted
		if reason != "" {
			o.NotCountedReason = &reason
			result.NotCountedReason = &reason
		}
		if active := incident.Active(m.Maintenance.Windows, now); active != "" {
			o.MaintenanceWindowID = &active
		}
		item, _ := observationItem(o)
		tx := []types.TransactWriteItem{}
		if report.RunID != nil {
			guard := map[string]types.AttributeValue{
				"PK":        mustAV("MON#" + id),
				"SK":        mustAV("RUN#" + *report.RunID),
				"expiresAt": mustAV(now.Add(7 * 24 * time.Hour).Unix()),
			}
			tx = append(
				tx,
				types.TransactWriteItem{
					Put: &types.Put{
						TableName: aws.String(s.table),
						Item:      guard,
						ConditionExpression: aws.String(
							"attribute_not_exists(PK) OR expiresAt < :now",
						),
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":now": mustAV(now.Unix()),
						},
					},
				},
			)
		}
		guardIndex := len(tx) - 1
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
		expected := map[string]types.AttributeValue{
			":hash":      mustAV(hash),
			":lifecycle": mustAV(m.Lifecycle),
			":kind":      mustAV("heartbeat"),
			":revision":  mustAV(m.Evaluation.Revision),
		}
		condition := "heartbeat.#token.#hash = :hash AND lifecycle = :lifecycle AND #kind = :kind AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
		names := map[string]string{"#token": "token", "#hash": "hash", "#kind": "kind"}
		expr := "SET evaluation = :evaluation"
		var next incident.Evaluation
		var open *incident.Open
		var effects []types.TransactWriteItem
		if o.Counted {
			next, open, effects, e = s.evaluationItems(ctx, m, o, now)
			if e != nil {
				return result, e
			}
			expected[":evaluation"] = mustAV(next)
			evidence := monitor.Evidence{
				Kind:          o.Kind,
				ObservationID: o.ID,
				InitiatedBy:   o.InitiatedBy,
				StartedAt:     o.StartedAt,
				CompletedAt:   o.CompletedAt,
				Outcome:       o.Outcome,
				Reason:        o.Reason,
				ConfigVersion: o.ConfigVersion,
				Observation:   o,
			}
			expected[":evidence"] = mustAV(evidence)
			expect := heartbeat.Expect(now, m.Heartbeat.Schedule)
			if m.Expectation != nil {
				expect.LastFinishedAt = m.Expectation.LastFinishedAt
			}
			if report.FinishedAt != nil {
				expect.LastFinishedAt = *report.FinishedAt
			}
			expected[":expectation"] = mustAV(expect)
			expr = "SET evaluation = :evaluation, #status = :evidence, expectation = :expectation"
			names["#status"] = "status"
			result.NextDueAt = expect.DueAt
			if open == nil {
				expr += " REMOVE openIncident"
			} else {
				expr += ", openIncident = :open"
				expected[":open"] = mustAV(open)
			}
		} else {
			next = m.Evaluation
			next.Revision++
			expected[":evaluation"] = mustAV(next)
			if m.Expectation != nil {
				result.NextDueAt = m.Expectation.DueAt
			}
		}
		if o.Counted {
			expected[":lastReportAt"] = mustAV(stamp)
			if strings.Contains(expr, " REMOVE ") {
				expr = strings.Replace(
					expr,
					" REMOVE ",
					", heartbeat.lastReportAt = :lastReportAt REMOVE ",
					1,
				)
			} else {
				expr += ", heartbeat.lastReportAt = :lastReportAt"
			}
		}
		tx = append(
			tx,
			types.TransactWriteItem{
				Update: &types.Update{
					TableName:                 aws.String(s.table),
					Key:                       key("MONITORS", "MON#"+id),
					ConditionExpression:       aws.String(condition),
					UpdateExpression:          aws.String(expr),
					ExpressionAttributeNames:  names,
					ExpressionAttributeValues: expected,
				},
			},
		)
		tx = append(tx, effects...)
		_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if e == nil {
			if o.Counted && m.OpenIncident != nil && open == nil {
				s.cancelResolvedReminders(ctx, id, m.OpenIncident.ID)
			}
			return result, nil
		}
		if guardIndex >= 0 && cancelled(e, guardIndex) {
			return ReportResult{Accepted: true, Duplicate: true}, nil
		}
		if !cancelled(e, len(tx)-len(effects)-1) {
			return result, ErrUnavailable
		}
		current, readErr := s.Get(ctx, id)
		if readErr != nil {
			return result, readErr
		}
		if current.Lifecycle == "archived" {
			return result, ErrArchived
		}
		if current.Heartbeat == nil || current.Heartbeat.Token == nil ||
			current.Heartbeat.Token.Hash != hash {
			return result, ErrNotEligible
		}
	}
	return result, ErrUnavailable
}

func (s *Store) Liveness(ctx context.Context) (heartbeat.Liveness, error) {
	out, e := s.db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            key("SYSTEM", "LIVENESS"),
			ConsistentRead: aws.Bool(true),
		},
	)
	if e != nil {
		return heartbeat.Liveness{}, ErrUnavailable
	}
	var live heartbeat.Liveness
	live.Outages = []heartbeat.Outage{}
	if len(out.Item) > 0 && attributevalue.UnmarshalMap(out.Item, &live) != nil {
		return live, ErrUnavailable
	}
	return live, nil
}

func (s *Store) WriteLiveness(
	ctx context.Context,
	now time.Time,
	interval time.Duration,
) (heartbeat.Liveness, error) {
	for range 3 {
		live, e := s.Liveness(ctx)
		if e != nil {
			return live, e
		}
		before := live.AliveAt
		live.AliveAt = monitor.Stamp(now)
		if before != "" {
			old, e := time.Parse(time.RFC3339Nano, before)
			if e == nil && now.Sub(old) > 3*interval {
				live.Outages = append(
					[]heartbeat.Outage{{From: before, To: live.AliveAt}},
					live.Outages...)
				if len(live.Outages) > 50 {
					live.Outages = live.Outages[:50]
				}
			}
		}
		values := map[string]types.AttributeValue{
			":now":     mustAV(live.AliveAt),
			":outages": mustAV(live.Outages),
		}
		condition := "attribute_not_exists(aliveAt)"
		if before != "" {
			condition = "aliveAt = :previous"
			values[":previous"] = mustAV(before)
		}
		_, e = s.db.UpdateItem(
			ctx,
			&dynamodb.UpdateItemInput{
				TableName:                 aws.String(s.table),
				Key:                       key("SYSTEM", "LIVENESS"),
				ConditionExpression:       aws.String(condition),
				UpdateExpression:          aws.String("SET aliveAt = :now, outages = :outages"),
				ExpressionAttributeValues: values,
			},
		)
		if e == nil {
			return live, nil
		}
		var conflict *types.ConditionalCheckFailedException
		if !errors.As(e, &conflict) {
			return live, ErrUnavailable
		}
	}
	return heartbeat.Liveness{}, ErrUnavailable
}

func (s *Store) Deadline(
	ctx context.Context,
	m monitor.Monitor,
	now time.Time,
	live heartbeat.Liveness,
) error {
	if m.Kind != "heartbeat" || m.Lifecycle != "active" || m.Expectation == nil {
		return nil
	}
	due, e := time.Parse(time.RFC3339Nano, m.Expectation.DueAt)
	if e != nil {
		return ErrUnavailable
	}
	missing := due.Add(time.Duration(m.Heartbeat.GraceSeconds) * time.Second)
	alive, e := time.Parse(time.RFC3339Nano, live.AliveAt)
	if e != nil || alive.Before(missing) || now.Before(missing) {
		return nil
	}
	interval := time.Duration(m.Heartbeat.IntervalSeconds) * time.Second
	count := int(now.Sub(missing)/interval) + 1
	newDue := due.Add(time.Duration(count) * interval)
	next := *m.Expectation
	next.DueAt = monitor.Stamp(newDue)
	next.LateAt = next.DueAt
	next.MissingAt = monitor.Stamp(
		newDue.Add(time.Duration(m.Heartbeat.GraceSeconds) * time.Second),
	)
	next.StaleAt = monitor.Stamp(
		newDue.Add(
			time.Duration(
				m.Heartbeat.GraceSeconds,
			)*time.Second + time.Duration(
				3*heartbeat.LivenessInterval()+5,
			)*time.Second,
		),
	)
	tx := []types.TransactWriteItem{}
	values := map[string]types.AttributeValue{
		":due":      mustAV(m.Expectation.DueAt),
		":next":     mustAV(next),
		":active":   mustAV("active"),
		":revision": mustAV(m.Evaluation.Revision),
	}
	expr := "SET expectation = :next"
	reference, _ := time.Parse(time.RFC3339Nano, m.Expectation.Reference)
	outageEnd, inOutage := heartbeat.OverlappingOutage(
		reference,
		due,
		missing.Add(time.Duration(count-1)*interval),
		interval,
		live.Outages,
	)
	if inOutage {
		lastFinishedAt := next.LastFinishedAt
		next = heartbeat.Expect(outageEnd, m.Heartbeat.Schedule)
		next.LastFinishedAt = lastFinishedAt
		values[":next"] = mustAV(next)
	}
	if count > 1 || inOutage {
		reason := "overdue"
		gapCount := count - 1
		from := due
		to := due.Add(time.Duration(count-2) * interval)
		if inOutage {
			reason = "not_observed"
			gapCount = count
			to = due.Add(time.Duration(count-1) * interval)
		}
		gap := monitor.Gap{
			ID:          rand.Text(),
			MonitorID:   m.ID,
			FromDueAt:   workStamp(from),
			ToDueAt:     workStamp(to),
			MissedCount: gapCount,
			Reason:      reason,
			RecordedAt:  monitor.Stamp(now),
		}
		item, _ := gapItem(gap)
		tx = append(tx, putItem(s.table, item))
	}
	var open *incident.Open
	if inOutage {
		values[":evaluation"] = mustAV(incident.Clear(m.Evaluation))
		expr += ", evaluation = :evaluation"
	} else {
		latest := due.Add(time.Duration(count-1) * interval)
		at := latest.Add(time.Duration(m.Heartbeat.GraceSeconds) * time.Second)
		stamp := monitor.Stamp(at)
		dueStamp := monitor.Stamp(latest)
		trigger := "deadline"
		o := monitor.Observation{ID: rand.Text(), MonitorID: m.ID, Kind: "heartbeat_missed", ConfigVersion: m.ConfigVersion, InitiatedBy: "statusforge", Trigger: &trigger, DueAt: &dueStamp, Counted: true, StartedAt: stamp, CompletedAt: stamp, Outcome: "failing", Reason: "missing"}
		if id := incident.Active(m.Maintenance.Windows, at); id != "" {
			o.MaintenanceWindowID = &id
		}
		nextEval, opened, effects, e := s.evaluationItems(ctx, m, o, at)
		if e != nil {
			return e
		}
		open = opened
		values[":evaluation"] = mustAV(nextEval)
		evidence := monitor.Evidence{Kind: o.Kind, ObservationID: o.ID, InitiatedBy: o.InitiatedBy, StartedAt: o.StartedAt, CompletedAt: o.CompletedAt, Outcome: o.Outcome, Reason: o.Reason, ConfigVersion: o.ConfigVersion, Observation: o}
		values[":evidence"] = mustAV(evidence)
		expr += ", evaluation = :evaluation, #status = :evidence"
		item, _ := observationItem(o)
		tx = append(tx, putItem(s.table, item))
		tx = append(tx, effects...)
		if open == nil {
			expr += " REMOVE openIncident"
		} else {
			expr += ", openIncident = :open"
			values[":open"] = mustAV(open)
		}
	}
	var names map[string]string
	if !inOutage {
		names = map[string]string{"#status": "status"}
	}
	condition := "expectation.dueAt = :due AND lifecycle = :active AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
	update := types.TransactWriteItem{
		Update: &types.Update{
			TableName:                 aws.String(s.table),
			Key:                       key("MONITORS", "MON#"+m.ID),
			ConditionExpression:       aws.String(condition),
			UpdateExpression:          aws.String(expr),
			ExpressionAttributeValues: values,
			ExpressionAttributeNames:  names,
		},
	}
	tx = append([]types.TransactWriteItem{update}, tx...)
	_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if e == nil {
		return nil
	}
	if cancelled(e, 0) {
		return ErrNotEligible
	}
	return ErrUnavailable
}
