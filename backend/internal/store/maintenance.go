package store

import (
	"context"
	"crypto/rand"
	"errors"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
)

var (
	ErrOverlaps       = errors.New("overlaps")
	ErrTooManyWindows = errors.New("too_many_windows")
	ErrWindowClosed   = errors.New("window_closed")
	ErrWindowNotFound = errors.New("window_not_found")
)

func windowKey(id string, w monitor.MaintenanceWindow) map[string]types.AttributeValue {
	return key("MON#"+id, "MAINT#"+workStamp(mustTime(w.StartAt))+"#"+w.ID)
}
func mustTime(stamp string) time.Time { at, _ := time.Parse(time.RFC3339Nano, stamp); return at }
func maintenanceCondition(m monitor.Monitor) (string, map[string]types.AttributeValue) {
	values := map[string]types.AttributeValue{":revision": mustAV(m.Evaluation.Revision)}
	cond := "attribute_exists(PK) AND evaluation.revision = :revision"
	if m.Evaluation.Revision == 0 {
		cond = "attribute_exists(PK) AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
	}
	if m.Maintenance.Windows == nil {
		cond += " AND (attribute_not_exists(maintenance.#windows) OR attribute_type(maintenance.#windows, :null) OR maintenance.#windows = :windows)"
		values[":windows"] = mustAV([]incident.WindowRef{})
		values[":null"] = mustAV("NULL")
	} else {
		cond += " AND maintenance.#windows = :windows"
		values[":windows"] = mustAV(m.Maintenance.Windows)
	}
	return cond, values
}

func indexCondition(m monitor.Monitor) (string, map[string]types.AttributeValue) {
	condition, values := maintenanceCondition(m)
	return condition + " AND attribute_not_exists(deletion)", values
}

func (s *Store) ListMaintenance(
	ctx context.Context,
	id string,
	limit int,
	now time.Time,
) ([]monitor.MaintenanceWindow, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	windows := []monitor.MaintenanceWindow{}
	var cursor map[string]types.AttributeValue
	for {
		out, err := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":     mustAV("MON#" + id),
					":prefix": mustAV("MAINT#"),
				},
				ConsistentRead:    aws.Bool(true),
				ExclusiveStartKey: cursor,
			},
		)
		if err != nil {
			return nil, ErrUnavailable
		}
		for _, item := range out.Items {
			if expiredAt(item, now) {
				continue
			}
			var w monitor.MaintenanceWindow
			if attributevalue.UnmarshalMap(item, &w) != nil {
				return nil, ErrUnavailable
			}
			windows = append(windows, w.WithState(now))
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		cursor = out.LastEvaluatedKey
	}
	sort.Slice(windows, func(i, j int) bool {
		a, b := windows[i], windows[j]
		liveA := a.State == "active" || a.State == "scheduled"
		liveB := b.State == "active" || b.State == "scheduled"
		if liveA != liveB {
			return liveA
		}
		if liveA {
			if a.StartAt == b.StartAt {
				return a.ID < b.ID
			}
			return a.StartAt < b.StartAt
		}
		if a.StartAt == b.StartAt {
			return a.ID > b.ID
		}
		return a.StartAt > b.StartAt
	})
	if len(windows) > limit {
		windows = windows[:limit]
	}
	return windows, nil
}

func (s *Store) CreateMaintenance(
	ctx context.Context,
	id string,
	start, end time.Time,
	note string,
	now time.Time,
) (monitor.MaintenanceWindow, error) {
	for range 4 {
		m, err := s.Get(ctx, id)
		if err != nil {
			return monitor.MaintenanceWindow{}, err
		}
		if m.Lifecycle == "archived" {
			return monitor.MaintenanceWindow{}, ErrArchived
		}
		remaining := incident.Prune(m.Maintenance.Windows, now)
		if incident.Overlaps(remaining, start, end) {
			return monitor.MaintenanceWindow{}, ErrOverlaps
		}
		if len(remaining) >= 10 {
			return monitor.MaintenanceWindow{}, ErrTooManyWindows
		}
		w := monitor.MaintenanceWindow{
			ID:        rand.Text(),
			MonitorID: id,
			StartAt:   monitor.Stamp(start),
			EndAt:     monitor.Stamp(end),
			Note:      note,
			CreatedAt: monitor.Stamp(now),
		}
		ref := incident.WindowRef{WindowID: w.ID, StartAt: w.StartAt, EndAt: w.EndAt}
		next := m.Maintenance
		next.Windows = incident.Insert(remaining, ref)
		item, _ := attributevalue.MarshalMap(w)
		for k, v := range windowKey(id, w) {
			item[k] = v
		}
		item["entityType"] = mustAV("maintenance")
		item["expiresAt"] = mustAV(retention.History(end))
		cond, vals := indexCondition(m)
		cond += " AND lifecycle <> :archived"
		vals[":archived"] = mustAV("archived")
		vals[":maintenance"] = mustAV(next)
		vals[":evaluation"] = mustAV(
			incident.Evaluation{
				Revision:   m.Evaluation.Revision + 1,
				FailRun:    m.Evaluation.FailRun,
				HealthyRun: m.Evaluation.HealthyRun,
			},
		)
		tx := []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:           aws.String(s.table),
					Key:                 key("MONITORS", "MON#"+id),
					ConditionExpression: aws.String(cond),
					UpdateExpression: aws.String(
						"SET maintenance = :maintenance, evaluation = :evaluation",
					),
					ExpressionAttributeNames:  map[string]string{"#windows": "windows"},
					ExpressionAttributeValues: vals,
				},
			},
			{
				Put: &types.Put{
					TableName:           aws.String(s.table),
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if err == nil {
			return w.WithState(now), nil
		}
		if !cancelled(err, 0) {
			return w, ErrUnavailable
		}
	}
	return monitor.MaintenanceWindow{}, ErrVersionConflict
}

func (s *Store) CancelMaintenance(
	ctx context.Context,
	id, windowID string,
	now time.Time,
) (monitor.MaintenanceWindow, error) {
	for range 4 {
		m, err := s.Get(ctx, id)
		if err != nil {
			return monitor.MaintenanceWindow{}, err
		}
		if m.Lifecycle == "archived" {
			return monitor.MaintenanceWindow{}, ErrArchived
		}
		windows, err := s.ListMaintenance(ctx, id, 100000, now)
		if err != nil {
			return monitor.MaintenanceWindow{}, err
		}
		var found *monitor.MaintenanceWindow
		for i := range windows {
			if windows[i].ID == windowID {
				found = &windows[i]
				break
			}
		}
		if found == nil {
			return monitor.MaintenanceWindow{}, ErrWindowNotFound
		}
		w := *found
		if w.State != "active" && w.State != "scheduled" {
			return w, ErrWindowClosed
		}
		at := monitor.Stamp(now)
		w.CancelledAt = &at
		next := m.Maintenance
		next.Windows = make([]incident.WindowRef, 0, len(m.Maintenance.Windows))
		for _, ref := range m.Maintenance.Windows {
			if ref.WindowID != w.ID {
				next.Windows = append(next.Windows, ref)
			} else if found.State == "active" {
				ref.EndAt = at
				next.Windows = append(next.Windows, ref)
			}
		}
		if found.State == "active" {
			w.EndAt = at
		}
		cond, vals := indexCondition(m)
		cond += " AND lifecycle <> :archived"
		vals[":archived"] = mustAV("archived")
		vals[":maintenance"] = mustAV(next)
		vals[":evaluation"] = mustAV(
			incident.Evaluation{
				Revision:   m.Evaluation.Revision + 1,
				FailRun:    m.Evaluation.FailRun,
				HealthyRun: m.Evaluation.HealthyRun,
			},
		)
		tx := []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName:           aws.String(s.table),
					Key:                 key("MONITORS", "MON#"+id),
					ConditionExpression: aws.String(cond),
					UpdateExpression: aws.String(
						"SET maintenance = :maintenance, evaluation = :evaluation",
					),
					ExpressionAttributeNames:  map[string]string{"#windows": "windows"},
					ExpressionAttributeValues: vals,
				},
			},
			{
				Update: &types.Update{
					TableName: aws.String(s.table),
					Key:       windowKey(id, *found),
					ConditionExpression: aws.String(
						"attribute_not_exists(cancelledAt) AND endAt = :oldEnd",
					),
					UpdateExpression: aws.String(
						"SET cancelledAt = :now, endAt = :end, expiresAt = :expires",
					),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":now":     mustAV(at),
						":end":     mustAV(w.EndAt),
						":oldEnd":  mustAV(found.EndAt),
						":expires": mustAV(retention.History(mustTime(w.EndAt))),
					},
				},
			},
		}
		_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if err == nil {
			return w.WithState(now), nil
		}
		if !cancelled(err, 0) && !cancelled(err, 1) {
			return w, ErrUnavailable
		}
	}
	return monitor.MaintenanceWindow{}, ErrVersionConflict
}

func (s *Store) Boundary(ctx context.Context, m monitor.Monitor, now time.Time) error {
	if m.Lifecycle != "active" {
		return nil
	}
	desired := incident.Active(m.Maintenance.Windows, now)
	if desired == m.Maintenance.ActiveID {
		// Expired windows are history even if there was no active boundary to record.
		if len(incident.Prune(m.Maintenance.Windows, now)) == len(m.Maintenance.Windows) {
			return nil
		}
	}
	next := m.Maintenance
	next.Windows = incident.Prune(next.Windows, now)
	kind := ""
	old := m.Maintenance.ActiveID
	if old != "" && old != desired {
		kind = "maintenance_ended"
		next.ActiveID = ""
	} else if desired != "" && old == "" {
		kind = "maintenance_started"
		next.ActiveID = desired
	} else if old == desired {
		next.ActiveID = old
	}
	// An ended window and a new active window require separate ticks, preserving both boundaries.
	if kind == "maintenance_ended" && desired != "" {
		for _, ref := range m.Maintenance.Windows {
			if ref.WindowID == desired {
				next.Windows = incident.Insert(next.Windows, ref)
				break
			}
		}
	}
	cond, vals := indexCondition(m)
	cond += " AND lifecycle = :active"
	vals[":active"] = mustAV("active")
	if old == "" {
		cond += " AND attribute_not_exists(maintenance.activeId)"
	} else {
		cond += " AND maintenance.activeId = :previous"
		vals[":previous"] = mustAV(old)
	}
	vals[":maintenance"] = mustAV(next)
	vals[":evaluation"] = mustAV(incident.Clear(m.Evaluation))
	if m.OpenIncident == nil {
		cond += " AND attribute_not_exists(openIncident)"
	} else {
		cond += " AND openIncident.id = :openID"
		vals[":openID"] = mustAV(m.OpenIncident.ID)
	}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key:       key("MONITORS", "MON#"+m.ID),
				UpdateExpression: aws.String(
					"SET maintenance = :maintenance, evaluation = :evaluation",
				),
				ConditionExpression:       aws.String(cond),
				ExpressionAttributeNames:  map[string]string{"#windows": "windows"},
				ExpressionAttributeValues: vals,
			},
		},
	}
	if kind != "" && m.OpenIncident != nil {
		tx = append(
			tx,
			s.event(m.ID, m.OpenIncident.ID, kind, now, map[string]any{"windowId": func() string {
				if kind == "maintenance_ended" {
					return old
				}
				return desired
			}()}),
		)
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if cancelled(err, 0) {
		return ErrNotEligible
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}
