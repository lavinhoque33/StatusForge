package store

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

func (s *Store) evaluationItems(
	ctx context.Context,
	m monitor.Monitor,
	o monitor.Observation,
	at time.Time,
) (incident.Evaluation, *incident.Open, []types.TransactWriteItem, error) {
	snapshot := incident.Evidence{
		Kind:                o.Kind,
		ObservationID:       o.ID,
		StartedAt:           o.StartedAt,
		InitiatedBy:         o.InitiatedBy,
		Outcome:             o.Outcome,
		Reason:              o.Reason,
		ObservedStatus:      o.ObservedStatus,
		ConfigVersion:       o.ConfigVersion,
		MaintenanceWindowID: o.MaintenanceWindowID,
	}
	next, t := incident.Evaluate(m.IncidentPolicy, m.Evaluation, m.OpenIncident, snapshot, at)
	open := m.OpenIncident
	tx := []types.TransactWriteItem{}
	if t == nil {
		return next, open, tx, nil
	}
	if t.Kind == "opened" {
		id := incidentID(at)
		opened := monitor.Stamp(at)
		first := t.Evidence[0]
		last := t.Evidence[len(t.Evidence)-1]
		in := Incident{
			ID:               id,
			MonitorID:        m.ID,
			MonitorName:      m.Name,
			State:            "open",
			OpenedAt:         opened,
			OpeningEvidence:  t.Evidence,
			RecoveryEvidence: []incident.Evidence{},
			FailureCount:     len(t.Evidence),
			FirstFailureAt:   first.StartedAt,
			LastFailure:      last,
		}
		item, _ := attributevalue.MarshalMap(in)
		item["PK"] = mustAV(incidentPK(m.ID))
		item["SK"] = mustAV("INC#" + id)
		tx = append(tx, putItem(s.table, item), s.event(m.ID, id, "opened", at, map[string]any{}))
		notes, err := s.intent(m, in, "opened", "opened", nil, t.Evidence, at)
		if err != nil {
			return next, open, nil, err
		}
		tx = append(tx, notes...)
		open = &incident.Open{
			ID:             id,
			OpenedAt:       opened,
			NextReminderAt: monitor.Stamp(at.Add(s.reminderInterval)),
			ReminderSeq:    0,
		}
		return next, open, tx, nil
	}
	if open == nil {
		return next, open, tx, nil
	}
	values := map[string]types.AttributeValue{":open": mustAV("open")}
	expr := ""
	if t.Kind == "extend" {
		expr = "SET failureCount = failureCount + :one, lastFailure = :last"
		values[":one"] = mustAV(1)
		values[":last"] = mustAV(t.Last)
	} else if t.Kind == "checker_problem" {
		expr = "SET checkerProblemCount = checkerProblemCount + :one, lastCheckerProblem = :last"
		values[":one"] = mustAV(1)
		values[":last"] = mustAV(t.Last)
	} else if t.Kind == "maintenance" {
		expr = "ADD maintenanceObservationCount :one"
		values[":one"] = mustAV(1)
	} else if t.Kind == "resolved" {
		stamp := monitor.Stamp(at)
		expr = "SET #state = :resolved, resolution = :resolution, resolvedAt = :at, recoveryEvidence = :evidence"
		values[":resolved"] = mustAV("resolved")
		values[":resolution"] = mustAV("recovered")
		values[":at"] = mustAV(stamp)
		values[":evidence"] = mustAV(t.Evidence)
	}
	tx = append(
		tx,
		types.TransactWriteItem{
			Update: &types.Update{
				TableName:                 aws.String(s.table),
				Key:                       key(incidentPK(m.ID), "INC#"+open.ID),
				ConditionExpression:       aws.String("#state = :open"),
				UpdateExpression:          aws.String(expr),
				ExpressionAttributeNames:  map[string]string{"#state": "state"},
				ExpressionAttributeValues: values,
			},
		},
	)
	if t.Kind == "resolved" {
		in, err := s.incident(ctx, m.ID, open.ID)
		if err != nil {
			return next, open, nil, err
		}
		stamp := monitor.Stamp(at)
		resolution := "recovered"
		in.ResolvedAt = &stamp
		in.Resolution = &resolution
		in.State = "resolved"
		in.RecoveryEvidence = t.Evidence
		tx = append(
			tx,
			s.event(m.ID, open.ID, "resolved", at, map[string]any{"resolution": "recovered"}),
		)
		notes, e := s.intent(m, in, "resolved", "resolved", nil, t.Evidence, at)
		if e != nil {
			return next, open, nil, e
		}
		tx = append(tx, notes...)
		open = nil
	}
	return next, open, tx, nil
}
func (s *Store) SetReminderInterval(d time.Duration) { s.reminderInterval = d }
