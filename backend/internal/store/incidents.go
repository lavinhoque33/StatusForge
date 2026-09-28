package store

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

var (
	ErrIncidentNotFound     = errors.New("incident_not_found")
	ErrNotificationNotFound = errors.New("notification_not_found")
	ErrNotFailed            = errors.New("not_failed")
)

type Incident struct {
	ID                          string              `json:"id"                          dynamodbav:"incidentId"`
	MonitorID                   string              `json:"monitorId"                   dynamodbav:"monitorId"`
	ApplicationID               *string             `json:"applicationId"               dynamodbav:"applicationId,omitempty"`
	MonitorName                 string              `json:"monitorName"                 dynamodbav:"monitorName"`
	State                       string              `json:"state"                       dynamodbav:"state"`
	Resolution                  *string             `json:"resolution"                  dynamodbav:"resolution,omitempty"`
	OpenedAt                    string              `json:"openedAt"                    dynamodbav:"openedAt"`
	ResolvedAt                  *string             `json:"resolvedAt"                  dynamodbav:"resolvedAt,omitempty"`
	OpeningEvidence             []incident.Evidence `json:"openingEvidence"             dynamodbav:"openingEvidence"`
	RecoveryEvidence            []incident.Evidence `json:"recoveryEvidence"            dynamodbav:"recoveryEvidence"`
	FailureCount                int                 `json:"failureCount"                dynamodbav:"failureCount"`
	FirstFailureAt              string              `json:"firstFailureAt"              dynamodbav:"firstFailureAt"`
	LastFailure                 incident.Evidence   `json:"lastFailure"                 dynamodbav:"lastFailure"`
	CheckerProblemCount         int                 `json:"checkerProblemCount"         dynamodbav:"checkerProblemCount"`
	LastCheckerProblem          *incident.Evidence  `json:"lastCheckerProblem"          dynamodbav:"lastCheckerProblem,omitempty"`
	MaintenanceObservationCount int                 `json:"maintenanceObservationCount" dynamodbav:"maintenanceObservationCount"`
	MonitoringPaused            bool                `json:"monitoringPaused"            dynamodbav:"-"`
	InMaintenance               bool                `json:"inMaintenance"               dynamodbav:"-"`
	NotificationSummary         Summary             `json:"notificationSummary"         dynamodbav:"-"`
}
type (
	Summary struct {
		Delivered int `json:"delivered"`
		Pending   int `json:"pending"`
		Failed    int `json:"failed"`
	}
	Event struct {
		Type    string         `json:"type"    dynamodbav:"type"`
		At      string         `json:"at"      dynamodbav:"at"`
		Details map[string]any `json:"details" dynamodbav:"details"`
	}
	Attempt struct {
		EntityType  string  `json:"-"           dynamodbav:"entityType"`
		Number      int     `json:"number"      dynamodbav:"number"`
		StartedAt   string  `json:"startedAt"   dynamodbav:"startedAt"`
		CompletedAt *string `json:"completedAt" dynamodbav:"completedAt,omitempty"`
		Manual      bool    `json:"manual"      dynamodbav:"manual"`
		Result      string  `json:"result"      dynamodbav:"result"`
		HTTPStatus  *int    `json:"httpStatus"  dynamodbav:"httpStatus,omitempty"`
		DurationMs  *int64  `json:"durationMs"  dynamodbav:"durationMs,omitempty"`
	}
	Notification struct {
		EntityType      string         `json:"-"                     dynamodbav:"entityType"`
		ID              string         `json:"id"                    dynamodbav:"id"`
		MonitorID       string         `json:"monitorId"             dynamodbav:"monitorId"`
		MonitorName     string         `json:"monitorName,omitempty" dynamodbav:"monitorName,omitempty"`
		IncidentID      string         `json:"incidentId"            dynamodbav:"incidentId"`
		NoteKey         string         `json:"-"                     dynamodbav:"noteKey"`
		Kind            string         `json:"kind"                  dynamodbav:"kind"`
		ReminderSeq     *int           `json:"reminderSeq"           dynamodbav:"reminderSeq,omitempty"`
		Payload         string         `json:"-"                     dynamodbav:"payload"`
		State           string         `json:"state"                 dynamodbav:"state"`
		Attempts        []Attempt      `json:"attempts"              dynamodbav:"-"`
		AttemptCount    int            `json:"-"                     dynamodbav:"attempts"`
		CreatedAt       string         `json:"createdAt"             dynamodbav:"createdAt"`
		NextAttemptAt   *string        `json:"nextAttemptAt"         dynamodbav:"nextAttemptAt,omitempty"`
		DeliveredAt     *string        `json:"deliveredAt"           dynamodbav:"deliveredAt,omitempty"`
		FailedAt        *string        `json:"failedAt"              dynamodbav:"failedAt,omitempty"`
		CancelledReason *string        `json:"cancelledReason"       dynamodbav:"cancelledReason,omitempty"`
		Lease           *DeliveryLease `json:"-"                     dynamodbav:"lease,omitempty"`
		ManualRetry     bool           `json:"-"                     dynamodbav:"manualRetry,omitempty"`
		LastResult      string         `json:"-"                     dynamodbav:"lastResult,omitempty"`
	}
	DeliveryLease struct {
		Token string `dynamodbav:"token"`
		Until string `dynamodbav:"until"`
	}
	Due struct {
		MonitorID  string
		IncidentID string
		NoteKey    string
		At         string
	}
)

func (s *Summary) count(n Notification) {
	if n.EntityType != "notification" {
		return
	}
	switch n.State {
	case "delivered":
		s.Delivered++
	case "failed":
		s.Failed++
	case "pending", "retry_wait", "sending":
		s.Pending++
	}
}

func incidentID(now time.Time) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return strings.Replace(
		now.UTC().Format("20060102T150405.000Z"),
		".",
		"",
		1,
	) + "-" + strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]))[:6]
}
func incidentPK(id string) string { return "MON#" + id }

func eventSK(
	id, at string,
) string {
	return "INCX#" + id + "#EV#" + at + "#" + incidentID(time.Now())
}
func noteSK(id, key string) string        { return "INCX#" + id + "#NOTE#" + key }
func dueSK(at, mid, id, nk string) string { return "DUE#" + at + "#" + mid + "#" + id + "#" + nk }
func pointer(sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{"PK": mustAV("DELIVERY"), "SK": mustAV(sk)}
}

func putItem(table string, item map[string]types.AttributeValue) types.TransactWriteItem {
	return types.TransactWriteItem{
		Put: &types.Put{
			TableName:           aws.String(table),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(PK)"),
		},
	}
}

func (s *Store) intent(
	m monitor.Monitor,
	inc Incident,
	kind, key string,
	seq *int,
	evidence []incident.Evidence,
	now time.Time,
) ([]types.TransactWriteItem, error) {
	at := monitor.Stamp(now)
	payload, err := incident.Payload(
		inc.ID+":"+key,
		kind,
		seq,
		incident.PayloadMonitor{ID: m.ID, Name: m.Name, URL: payloadURL(m), Kind: monitorKind(m)},
		incident.PayloadIncident{
			ID:           inc.ID,
			OpenedAt:     inc.OpenedAt,
			ResolvedAt:   inc.ResolvedAt,
			Resolution:   inc.Resolution,
			FailureCount: inc.FailureCount,
			Path:         "/incidents/" + m.ID + "/" + inc.ID,
		},
		evidence,
		at,
	)
	if err != nil {
		return nil, ErrUnavailable
	}
	note := Notification{
		ID:            inc.ID + ":" + key,
		MonitorID:     m.ID,
		MonitorName:   m.Name,
		IncidentID:    inc.ID,
		NoteKey:       key,
		Kind:          kind,
		ReminderSeq:   seq,
		Payload:       payload,
		State:         "pending",
		CreatedAt:     at,
		NextAttemptAt: &at,
	}
	note.EntityType = "notification"
	item, _ := attributevalue.MarshalMap(note)
	item["PK"] = mustAV(incidentPK(m.ID))
	item["SK"] = mustAV(noteSK(inc.ID, key))
	return []types.TransactWriteItem{
		putItem(s.table, item),
		putItem(s.table, pointer(dueSK(at, m.ID, inc.ID, key))),
	}, nil
}

func (s *Store) event(
	monitorID, incidentID, kind string,
	now time.Time,
	details map[string]any,
) types.TransactWriteItem {
	ev := Event{Type: kind, At: monitor.Stamp(now), Details: details}
	item, _ := attributevalue.MarshalMap(ev)
	item["PK"] = mustAV(incidentPK(monitorID))
	item["SK"] = mustAV(eventSK(incidentID, ev.At))
	return putItem(s.table, item)
}

func (s *Store) query(
	ctx context.Context,
	pk, prefix string,
	limit int,
	descending bool,
) ([]map[string]types.AttributeValue, error) {
	out, err := s.db.Query(
		ctx,
		&dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     mustAV(pk),
				":prefix": mustAV(prefix),
			},
			ConsistentRead:   aws.Bool(true),
			ScanIndexForward: aws.Bool(!descending),
			Limit:            aws.Int32(int32(limit)),
		},
	)
	if err != nil {
		return nil, ErrUnavailable
	}
	live := out.Items[:0]
	for _, item := range out.Items {
		if !s.expired(item) {
			live = append(live, item)
		}
	}
	return live, nil
}

func (s *Store) incident(ctx context.Context, mid, id string) (Incident, error) {
	out, err := s.db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            key(incidentPK(mid), "INC#"+id),
			ConsistentRead: aws.Bool(true),
		},
	)
	if err != nil {
		return Incident{}, ErrUnavailable
	}
	if len(out.Item) == 0 || s.expired(out.Item) {
		return Incident{}, ErrIncidentNotFound
	}
	var v Incident
	if attributevalue.UnmarshalMap(out.Item, &v) != nil {
		return v, ErrUnavailable
	}
	return v, nil
}

func (s *Store) notification(ctx context.Context, mid, id, nk string) (Notification, error) {
	out, err := s.db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            key(incidentPK(mid), noteSK(id, nk)),
			ConsistentRead: aws.Bool(true),
		},
	)
	if err != nil {
		return Notification{}, ErrUnavailable
	}
	if len(out.Item) == 0 || s.expired(out.Item) {
		return Notification{}, ErrNotificationNotFound
	}
	var n Notification
	if attributevalue.UnmarshalMap(out.Item, &n) != nil {
		return n, ErrUnavailable
	}
	return n, nil
}

func (s *Store) ListIncidents(
	ctx context.Context,
	mid, state string,
	limit int,
	now time.Time,
) ([]Incident, error) {
	ms := []monitor.Monitor{}
	if mid != "" {
		m, e := s.Get(ctx, mid)
		if e != nil {
			return nil, e
		}
		ms = append(ms, m)
	} else {
		var e error
		ms, e = s.List(ctx)
		if e != nil {
			return nil, e
		}
	}
	all := []Incident{}
	for _, m := range ms {
		items, e := s.query(ctx, incidentPK(m.ID), "INC#", limit, true)
		if e != nil {
			return nil, e
		}
		for _, item := range items {
			var in Incident
			if attributevalue.UnmarshalMap(item, &in) != nil {
				return nil, ErrUnavailable
			}
			if state != "all" && state != "" && in.State != state {
				continue
			}
			in.MonitoringPaused = in.State == "open" && m.Lifecycle == "paused"
			in.InMaintenance = in.State == "open" && maintenanceActive(m, now)
			notes, e := s.query(ctx, incidentPK(m.ID), "INCX#"+in.ID+"#NOTE#", 200, false)
			if e != nil {
				return nil, e
			}
			for _, item := range notes {
				var n Notification
				if attributevalue.UnmarshalMap(item, &n) != nil {
					return nil, ErrUnavailable
				}
				in.NotificationSummary.count(n)
			}
			all = append(all, in)
		}
	}
	if mid == "" {
		sort.Slice(all, func(i, j int) bool {
			if all[i].State != all[j].State {
				return all[i].State == "open"
			}
			if all[i].State == "resolved" && all[i].ResolvedAt != nil && all[j].ResolvedAt != nil {
				return *all[i].ResolvedAt > *all[j].ResolvedAt
			}
			return all[i].OpenedAt > all[j].OpenedAt
		})
	}
	if len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

func (s *Store) IncidentDetail(
	ctx context.Context,
	mid, id string,
	now time.Time,
) (Incident, []Event, []monitor.Gap, []Notification, error) {
	in, err := s.incident(ctx, mid, id)
	if err != nil {
		return in, nil, nil, nil, err
	}
	m, err := s.Get(ctx, mid)
	if err != nil {
		return in, nil, nil, nil, err
	}
	in.MonitoringPaused = in.State == "open" && m.Lifecycle == "paused"
	in.InMaintenance = in.State == "open" && maintenanceActive(m, now)
	items, err := s.query(ctx, incidentPK(mid), "INCX#"+id+"#", 1000, false)
	if err != nil {
		return in, nil, nil, nil, err
	}
	events := []Event{}
	notes := []Notification{}
	for _, item := range items {
		sk := item["SK"].(*types.AttributeValueMemberS).Value
		if strings.Contains(sk, "#EV#") {
			var v Event
			if attributevalue.UnmarshalMap(item, &v) != nil {
				return in, nil, nil, nil, ErrUnavailable
			}
			events = append(events, v)
		} else if entity, ok := item["entityType"].(*types.AttributeValueMemberS); ok && entity.Value == "notification" {
			var n Notification
			if attributevalue.UnmarshalMap(item, &n) != nil {
				return in, nil, nil, nil, ErrUnavailable
			}
			n.Attempts = []Attempt{}
			att, e := s.query(ctx, incidentPK(mid), noteSK(id, n.NoteKey)+"#ATT#", n.AttemptCount+1, false)
			if e != nil {
				return in, nil, nil, nil, e
			}
			for _, it := range att {
				var a Attempt
				if attributevalue.UnmarshalMap(it, &a) != nil {
					return in, nil, nil, nil, ErrUnavailable
				}
				n.Attempts = append(n.Attempts, a)
			}
			notes = append(notes, n)
			in.NotificationSummary.count(n)
		}
	}
	gaps, err := s.Gaps(ctx, mid, 200)
	if err != nil {
		return in, nil, nil, nil, err
	}
	filtered := []monitor.Gap{}
	for _, g := range gaps {
		if g.ToDueAt >= in.OpenedAt && (in.ResolvedAt == nil || g.FromDueAt <= *in.ResolvedAt) {
			filtered = append(filtered, g)
		}
	}
	sort.Slice(events, func(i, j int) bool { return events[i].At < events[j].At })
	sort.Slice(
		filtered,
		func(i, j int) bool { return filtered[i].FromDueAt < filtered[j].FromDueAt },
	)
	return in, events, filtered, notes, nil
}

func (s *Store) Reminder(
	ctx context.Context,
	m monitor.Monitor,
	now time.Time,
	interval time.Duration,
) error {
	if m.Lifecycle != "active" || m.OpenIncident == nil ||
		m.OpenIncident.NextReminderAt > monitor.Stamp(now) {
		return nil
	}
	op := m.OpenIncident
	in, err := s.incident(ctx, m.ID, op.ID)
	if err != nil {
		return err
	}
	opened, _ := time.Parse(time.RFC3339Nano, op.OpenedAt)
	next := monitor.Stamp(incident.NextSlot(opened, interval, now))
	if maintenanceActive(m, now) {
		cond, vals := indexCondition(m)
		vals[":active"] = mustAV("active")
		vals[":id"] = mustAV(op.ID)
		vals[":due"] = mustAV(op.NextReminderAt)
		vals[":next"] = mustAV(next)
		_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
			TableName: aws.String(s.table), Key: key("MONITORS", "MON#"+m.ID),
			ConditionExpression: aws.String(
				"lifecycle = :active AND openIncident.id = :id AND openIncident.nextReminderAt = :due AND " + cond,
			),
			UpdateExpression:          aws.String("SET openIncident.nextReminderAt = :next"),
			ExpressionAttributeNames:  map[string]string{"#windows": "windows"},
			ExpressionAttributeValues: vals,
		})
		var conflict *types.ConditionalCheckFailedException
		if errors.As(err, &conflict) {
			return ErrNotEligible
		}
		if err != nil {
			return ErrUnavailable
		}
		return nil
	}
	seq := op.ReminderSeq + 1
	nk := incident.ReminderKey(seq)
	tx, e := s.intent(m, in, "reminder", nk, &seq, []incident.Evidence{in.LastFailure}, now)
	if e != nil {
		return e
	}
	condition, values := indexCondition(m)
	values[":active"] = mustAV("active")
	values[":id"] = mustAV(op.ID)
	values[":due"] = mustAV(op.NextReminderAt)
	values[":next"] = mustAV(next)
	values[":seq"] = mustAV(seq)
	tx = append(
		tx,
		types.TransactWriteItem{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key:       key("MONITORS", "MON#"+m.ID),
				ConditionExpression: aws.String(
					"lifecycle = :active AND openIncident.id = :id AND openIncident.nextReminderAt = :due AND " + condition,
				),
				UpdateExpression: aws.String(
					"SET openIncident.nextReminderAt = :next, openIncident.reminderSeq = :seq",
				),
				ExpressionAttributeNames:  map[string]string{"#windows": "windows"},
				ExpressionAttributeValues: values,
			},
		},
	)
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if cancelled(err, len(tx)-1) {
		return ErrNotEligible
	}
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Due(ctx context.Context, now time.Time) ([]Due, error) {
	items, err := s.query(ctx, "DELIVERY", "DUE#", 100, false)
	if err != nil {
		return nil, err
	}
	result := []Due{}
	for _, it := range items {
		sk := it["SK"].(*types.AttributeValueMemberS).Value
		parts := strings.SplitN(strings.TrimPrefix(sk, "DUE#"), "#", 4)
		if len(parts) == 4 && parts[0] <= monitor.Stamp(now) {
			result = append(result, Due{parts[1], parts[2], parts[3], parts[0]})
			if len(result) == 10 {
				break
			}
		}
	}
	return result, nil
}

func (s *Store) noteAt(ctx context.Context, d Due) (Notification, error) {
	return s.notification(ctx, d.MonitorID, d.IncidentID, d.NoteKey)
}

func (s *Store) ClaimDelivery(
	ctx context.Context,
	d Due,
	now time.Time,
) (Notification, Attempt, string, error) {
	n, err := s.noteAt(ctx, d)
	if err != nil {
		return n, Attempt{}, "", err
	}
	var maintenanceMonitor *monitor.Monitor
	if n.Kind == "opened" || n.Kind == "reminder" {
		current, e := s.Get(ctx, d.MonitorID)
		if e != nil {
			return n, Attempt{}, "", e
		}
		if maintenanceActive(current, now) {
			return n, Attempt{}, "", ErrNotEligible
		}
		maintenanceMonitor = &current
	}
	if n.Kind != "opened" {
		if n.Kind == "reminder" {
			in, e := s.incident(ctx, d.MonitorID, d.IncidentID)
			if e != nil {
				return n, Attempt{}, "", e
			}
			if in.State != "open" {
				if n.State == "sending" && (n.Lease == nil || n.Lease.Until >= monitor.Stamp(now)) {
					return n, Attempt{}, "", ErrNotEligible
				}
				reason := "incident_resolved"
				tx := []types.TransactWriteItem{
					{Update: &types.Update{
						TableName: aws.String(
							s.table,
						), Key: key(incidentPK(d.MonitorID), noteSK(d.IncidentID, d.NoteKey)),
						ConditionExpression: aws.String(
							"#state IN (:pending,:wait) OR (#state = :sending AND lease.#until < :now)",
						),
						UpdateExpression: aws.String(
							"SET #state = :cancel, cancelledReason = :reason REMOVE nextAttemptAt, lease",
						),
						ExpressionAttributeNames: map[string]string{
							"#state": "state",
							"#until": "until",
						},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":pending": mustAV("pending"),
							":wait":    mustAV("retry_wait"),
							":sending": mustAV("sending"),
							":now":     mustAV(monitor.Stamp(now)),
							":cancel":  mustAV("cancelled"),
							":reason":  mustAV(reason),
						},
					}},
					{
						Delete: &types.Delete{
							TableName: aws.String(s.table),
							Key:       pointer(dueSK(d.At, d.MonitorID, d.IncidentID, d.NoteKey)),
						},
					},
				}
				if n.State == "sending" {
					previous := fmt.Sprintf(
						"%s#ATT#%04d",
						noteSK(d.IncidentID, d.NoteKey),
						n.AttemptCount,
					)
					tx = append(tx, types.TransactWriteItem{Update: &types.Update{
						TableName: aws.String(s.table), Key: key(incidentPK(d.MonitorID), previous),
						ConditionExpression: aws.String("#result = :inflight"),
						UpdateExpression: aws.String(
							"SET #result = :stopped, completedAt = :now",
						),
						ExpressionAttributeNames: map[string]string{"#result": "result"},
						ExpressionAttributeValues: map[string]types.AttributeValue{
							":inflight": mustAV("in_flight"),
							":stopped":  mustAV("process_stopped"),
							":now":      mustAV(monitor.Stamp(now)),
						},
					}})
				}
				_, e = s.db.TransactWriteItems(
					ctx,
					&dynamodb.TransactWriteItemsInput{TransactItems: tx},
				)
				if e != nil {
					if cancelled(e, 0) {
						return n, Attempt{}, "", ErrNotEligible
					}
					return n, Attempt{}, "", ErrUnavailable
				}
				return n, Attempt{}, "", ErrNotEligible
			}
		}
		opened, e := s.notification(ctx, d.MonitorID, d.IncidentID, "opened")
		if e != nil {
			return n, Attempt{}, "", e
		}
		if opened.State != "delivered" && opened.State != "failed" && opened.State != "cancelled" {
			return n, Attempt{}, "", ErrNotEligible
		}
	}
	if n.NextAttemptAt != nil && *n.NextAttemptAt > monitor.Stamp(now) {
		return n, Attempt{}, "", ErrNotEligible
	}
	if n.State == "sending" && (n.Lease == nil || n.Lease.Until >= monitor.Stamp(now)) {
		return n, Attempt{}, "", ErrNotEligible
	}
	if n.State != "pending" && n.State != "retry_wait" && n.State != "sending" {
		return n, Attempt{}, "", ErrNotEligible
	}
	token := incidentID(now)
	until := monitor.Stamp(now.Add(15 * time.Second))
	a := Attempt{
		Number:    n.AttemptCount + 1,
		StartedAt: monitor.Stamp(now),
		Manual:    n.ManualRetry,
		Result:    "in_flight",
	}
	a.EntityType = "attempt"
	item, _ := attributevalue.MarshalMap(a)
	item["PK"] = mustAV(incidentPK(d.MonitorID))
	item["SK"] = mustAV(fmt.Sprintf("%s#ATT#%04d", noteSK(d.IncidentID, d.NoteKey), a.Number))
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key: key(
					incidentPK(d.MonitorID),
					noteSK(d.IncidentID, d.NoteKey),
				),
				ConditionExpression: aws.String(
					"(#state IN (:pending,:wait) AND nextAttemptAt <= :now) OR (#state = :sending AND lease.#until < :now)",
				),
				UpdateExpression: aws.String(
					"SET #state = :sending, lease = :lease, attempts = :count",
				),
				ExpressionAttributeNames: map[string]string{"#state": "state", "#until": "until"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pending": mustAV("pending"),
					":wait":    mustAV("retry_wait"),
					":sending": mustAV("sending"),
					":now":     mustAV(monitor.Stamp(now)),
					":lease":   mustAV(DeliveryLease{token, until}),
					":count":   mustAV(a.Number),
				},
			},
		},
		putItem(s.table, item),
	}
	if n.State == "sending" {
		lastKey := fmt.Sprintf("%s#ATT#%04d", noteSK(d.IncidentID, d.NoteKey), n.AttemptCount)
		tx = append(
			tx,
			types.TransactWriteItem{
				Update: &types.Update{
					TableName:           aws.String(s.table),
					Key:                 key(incidentPK(d.MonitorID), lastKey),
					ConditionExpression: aws.String("#result = :old"),
					UpdateExpression: aws.String(
						"SET #result = :stopped, completedAt = :now",
					),
					ExpressionAttributeNames: map[string]string{"#result": "result"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":old":     mustAV("in_flight"),
						":stopped": mustAV("process_stopped"),
						":now":     mustAV(monitor.Stamp(now)),
					},
				},
			},
		)
	}
	if n.Kind != "opened" {
		tx = append(tx, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
			TableName:                aws.String(s.table),
			Key:                      key(incidentPK(d.MonitorID), noteSK(d.IncidentID, "opened")),
			ConditionExpression:      aws.String("#state IN (:delivered,:failed,:cancelled)"),
			ExpressionAttributeNames: map[string]string{"#state": "state"},
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":delivered": mustAV("delivered"),
				":failed":    mustAV("failed"),
				":cancelled": mustAV("cancelled"),
			},
		}})
		if n.Kind == "reminder" {
			tx = append(tx, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
				TableName: aws.String(
					s.table,
				), Key: key(incidentPK(d.MonitorID), "INC#"+d.IncidentID),
				ConditionExpression: aws.String(
					"#state = :open",
				), ExpressionAttributeNames: map[string]string{"#state": "state"},
				ExpressionAttributeValues: map[string]types.AttributeValue{":open": mustAV("open")},
			}})
		}
	}
	if maintenanceMonitor != nil {
		condition, values := maintenanceCondition(*maintenanceMonitor)
		tx = append(tx, types.TransactWriteItem{ConditionCheck: &types.ConditionCheck{
			TableName:                 aws.String(s.table),
			Key:                       key("MONITORS", "MON#"+d.MonitorID),
			ConditionExpression:       aws.String(condition),
			ExpressionAttributeNames:  map[string]string{"#windows": "windows"},
			ExpressionAttributeValues: values,
		}})
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if err != nil {
		if cancelled(err, 0) ||
			(n.Kind == "resolved" && cancelled(err, len(tx)-1)) ||
			(n.Kind == "reminder" && (cancelled(err, len(tx)-2) || cancelled(err, len(tx)-3))) ||
			(maintenanceMonitor != nil && cancelled(err, len(tx)-1)) {
			return n, a, "", ErrNotEligible
		}
		return n, a, "", ErrUnavailable
	}
	return n, a, token, nil
}

func (s *Store) CompleteDelivery(
	ctx context.Context,
	d Due,
	n Notification,
	a Attempt,
	token, result string,
	status *int,
	duration int64,
	now time.Time,
	schedule []time.Duration,
) (string, error) {
	state := "failed"
	retry := result != "delivered" && result != "rejected" && result != "refused_by_policy" &&
		!n.ManualRetry
	nextTime, ok := incident.RetryAt(now, a.Number, schedule)
	if result == "delivered" {
		state = "delivered"
	} else if retry && ok {
		state = "retry_wait"
	}
	at := monitor.Stamp(now)
	a.CompletedAt = &at
	a.Result = result
	a.HTTPStatus = status
	a.DurationMs = &duration
	attemptKey := fmt.Sprintf("%s#ATT#%04d", noteSK(d.IncidentID, d.NoteKey), a.Number)
	vals := map[string]types.AttributeValue{
		":token":   mustAV(token),
		":sending": mustAV("sending"),
		":state":   mustAV(state),
		":result":  mustAV(result),
	}
	expr := "SET #state = :state, lastResult = :result REMOVE lease, nextAttemptAt, manualRetry"
	if state == "retry_wait" {
		vals[":next"] = mustAV(monitor.Stamp(nextTime))
		expr = "SET #state = :state, lastResult = :result, nextAttemptAt = :next REMOVE lease, manualRetry"
	} else if state == "delivered" {
		vals[":now"] = mustAV(at)
		expr = "SET #state = :state, lastResult = :result, deliveredAt = :now REMOVE lease, nextAttemptAt, manualRetry"
	} else {
		vals[":now"] = mustAV(at)
		expr = "SET #state = :state, lastResult = :result, failedAt = :now REMOVE lease, nextAttemptAt, manualRetry"
	}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName: aws.String(s.table),
				Key: key(
					incidentPK(d.MonitorID),
					noteSK(d.IncidentID, d.NoteKey),
				),
				ConditionExpression: aws.String(
					"#state = :sending AND lease.#token = :token",
				),
				UpdateExpression:          aws.String(expr),
				ExpressionAttributeNames:  map[string]string{"#state": "state", "#token": "token"},
				ExpressionAttributeValues: vals,
			},
		},
		{
			Update: &types.Update{
				TableName:           aws.String(s.table),
				Key:                 key(incidentPK(d.MonitorID), attemptKey),
				ConditionExpression: aws.String("#result = :old"),
				UpdateExpression: aws.String(
					"SET #result = :new, completedAt = :at, durationMs = :duration, httpStatus = :http",
				),
				ExpressionAttributeNames: map[string]string{"#result": "result"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":old":      mustAV("in_flight"),
					":new":      mustAV(result),
					":at":       mustAV(at),
					":duration": mustAV(duration),
					":http":     mustAV(status),
				},
			},
		},
		{
			Delete: &types.Delete{
				TableName: aws.String(s.table),
				Key:       pointer(dueSK(d.At, d.MonitorID, d.IncidentID, d.NoteKey)),
			},
		},
	}
	if state == "retry_wait" {
		tx = append(
			tx,
			putItem(
				s.table,
				pointer(dueSK(monitor.Stamp(nextTime), d.MonitorID, d.IncidentID, d.NoteKey)),
			),
		)
	} else if state == "failed" {
		tx = append(tx, putItem(s.table, pointer("ATTENTION#"+d.MonitorID+"#"+d.IncidentID+"#"+d.NoteKey)))
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if err != nil {
		if cancelled(err, 0) {
			return "", ErrNotEligible
		}
		return "", ErrUnavailable
	}
	return state, nil
}

func (s *Store) RetryNotification(
	ctx context.Context,
	mid, id, nk string,
	now time.Time,
) (Notification, error) {
	n, err := s.notification(ctx, mid, id, nk)
	if err != nil {
		return n, err
	}
	n.Attempts = []Attempt{}
	attempts, queryErr := s.query(
		ctx,
		incidentPK(mid),
		noteSK(id, nk)+"#ATT#",
		n.AttemptCount+1,
		false,
	)
	if queryErr != nil {
		return n, queryErr
	}
	for _, item := range attempts {
		var a Attempt
		if attributevalue.UnmarshalMap(item, &a) != nil {
			return n, ErrUnavailable
		}
		n.Attempts = append(n.Attempts, a)
	}
	at := monitor.Stamp(now)
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName:           aws.String(s.table),
				Key:                 key(incidentPK(mid), noteSK(id, nk)),
				ConditionExpression: aws.String("#state = :failed"),
				UpdateExpression: aws.String(
					"SET #state = :pending, nextAttemptAt = :now, manualRetry = :manual REMOVE failedAt, expiresAt",
				),
				ExpressionAttributeNames: map[string]string{"#state": "state"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":failed":  mustAV("failed"),
					":pending": mustAV("pending"),
					":now":     mustAV(at),
					":manual":  mustAV(true),
				},
			},
		},
		{
			Delete: &types.Delete{
				TableName: aws.String(s.table),
				Key:       pointer("ATTENTION#" + mid + "#" + id + "#" + nk),
			},
		},
		putItem(s.table, pointer(dueSK(at, mid, id, nk))),
		{Update: &types.Update{
			TableName: aws.String(s.table), Key: key(incidentPK(mid), "INC#"+id),
			ConditionExpression: aws.String("attribute_exists(PK)"),
			UpdateExpression:    aws.String("REMOVE expiresAt"),
		}},
		s.retentionJob(mid, id),
		{ConditionCheck: &types.ConditionCheck{
			TableName: aws.String(s.table), Key: key("MONITORS", "MON#"+mid),
			ConditionExpression: aws.String(
				"attribute_exists(PK) AND attribute_not_exists(deletion)",
			),
		}},
	}
	_, err = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if err != nil {
		if cancelled(err, 0) {
			return n, ErrNotFailed
		}
		return n, ErrUnavailable
	}
	n.State = "pending"
	n.NextAttemptAt = &at
	n.FailedAt = nil
	return n, nil
}

func (s *Store) Attention(ctx context.Context, limit int) ([]Notification, error) {
	items, err := s.query(ctx, "DELIVERY", "ATTENTION#", 10000, false)
	if err != nil {
		return nil, err
	}
	result := []Notification{}
	for _, it := range items {
		sk := it["SK"].(*types.AttributeValueMemberS).Value
		parts := strings.SplitN(strings.TrimPrefix(sk, "ATTENTION#"), "#", 3)
		if len(parts) != 3 {
			continue
		}
		n, e := s.notification(ctx, parts[0], parts[1], parts[2])
		if errors.Is(e, ErrNotificationNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		result = append(result, n)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].FailedAt == nil {
			return false
		}
		if result[j].FailedAt == nil {
			return true
		}
		return *result[i].FailedAt > *result[j].FailedAt
	})
	if len(result) > limit {
		result = result[:limit]
	}
	for i := range result {
		n := &result[i]
		n.Attempts = []Attempt{}
		attempts, e := s.query(
			ctx,
			incidentPK(n.MonitorID),
			noteSK(n.IncidentID, n.NoteKey)+"#ATT#",
			n.AttemptCount+1,
			false,
		)
		if e != nil {
			return nil, e
		}
		for _, item := range attempts {
			var a Attempt
			if attributevalue.UnmarshalMap(item, &a) != nil {
				return nil, ErrUnavailable
			}
			n.Attempts = append(n.Attempts, a)
		}
	}
	return result, nil
}

// cancelResolvedReminders is best-effort cleanup after the resolution commit.
// The claim path repeats this check so interruption or a conditional race cannot
// turn a resolved incident's reminder into a delivery.
func (s *Store) cancelResolvedReminders(ctx context.Context, mid, id string) {
	items, err := s.query(ctx, incidentPK(mid), "INCX#"+id+"#NOTE#reminder#", 200, true)
	if err != nil {
		return
	}
	for _, item := range items {
		var n Notification
		if attributevalue.UnmarshalMap(item, &n) != nil || n.NextAttemptAt == nil ||
			(n.State != "pending" && n.State != "retry_wait") {
			continue
		}
		due := *n.NextAttemptAt
		tx := []types.TransactWriteItem{
			{Update: &types.Update{
				TableName: aws.String(s.table),
				Key:       key(incidentPK(mid), noteSK(id, n.NoteKey)),
				ConditionExpression: aws.String(
					"(#state = :pending OR #state = :wait) AND nextAttemptAt = :due",
				),
				UpdateExpression: aws.String(
					"SET #state = :cancel, cancelledReason = :reason REMOVE nextAttemptAt",
				),
				ExpressionAttributeNames: map[string]string{"#state": "state"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pending": mustAV(
						"pending",
					), ":wait": mustAV("retry_wait"), ":due": mustAV(due),
					":cancel": mustAV("cancelled"), ":reason": mustAV("incident_resolved"),
				},
			}},
			{
				Delete: &types.Delete{
					TableName: aws.String(s.table),
					Key:       pointer(dueSK(due, mid, id, n.NoteKey)),
				},
			},
		}
		_, _ = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	}
}
