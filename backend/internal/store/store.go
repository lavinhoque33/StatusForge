package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

var (
	ErrNotFound          = errors.New("monitor_not_found")
	ErrVersionConflict   = errors.New("version_conflict")
	ErrInvalidTransition = errors.New("invalid_transition")
	ErrArchived          = errors.New("archived")
	ErrDuplicate         = errors.New("duplicate_observation")
	ErrUnavailable       = errors.New("store_unavailable")
	ErrLeaseHeld         = errors.New("check_in_progress")
	ErrNotEligible       = errors.New("not_eligible")
)

type Store struct {
	db                 *dynamodb.Client
	table              string
	mu                 sync.Mutex
	ready              bool
	now                func() time.Time
	timeout            time.Duration
	lastSuccessfulTick map[string]time.Time
	needsRecovery      map[string]bool
	reminderInterval   time.Duration
}

func New(
	client *localdynamo.Client,
	table string,
	timeout time.Duration,
	now func() time.Time,
) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{
		db:                 client.DynamoDB(),
		table:              table,
		timeout:            timeout * 5,
		now:                now,
		lastSuccessfulTick: make(map[string]time.Time),
		reminderInterval:   6 * time.Hour,
		needsRecovery:      make(map[string]bool),
	}
}
func (s *Store) Initialize(ctx context.Context) error { return s.ensure(ctx) }
func (s *Store) ensure(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ready {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	_, err := s.db.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(s.table)})
	if err != nil {
		var missing *types.ResourceNotFoundException
		if !errors.As(err, &missing) {
			return ErrUnavailable
		}
		_, err = s.db.CreateTable(
			ctx,
			&dynamodb.CreateTableInput{
				TableName: aws.String(s.table),
				AttributeDefinitions: []types.AttributeDefinition{
					{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
					{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
				},
				KeySchema: []types.KeySchemaElement{
					{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
					{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
				},
				BillingMode: types.BillingModePayPerRequest,
			},
		)
		if err != nil {
			var existing *types.ResourceInUseException
			if !errors.As(err, &existing) {
				return ErrUnavailable
			}
		}
	}
	waiter := dynamodb.NewTableExistsWaiter(s.db)
	if err := waiter.Wait(ctx, &dynamodb.DescribeTableInput{TableName: aws.String(s.table)}, s.timeout); err != nil {
		return ErrUnavailable
	}
	s.ready = true
	return nil
}

func key(pk, sk string) map[string]types.AttributeValue {
	return map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: pk},
		"SK": &types.AttributeValueMemberS{Value: sk},
	}
}

func (s *Store) Create(ctx context.Context, m monitor.Monitor) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	now, _ := time.Parse(time.RFC3339Nano, m.CreatedAt)
	if m.Kind == "heartbeat" {
		item, e := monitorItem(m)
		if e != nil {
			return e
		}
		_, e = s.db.PutItem(
			ctx,
			&dynamodb.PutItemInput{
				TableName:           aws.String(s.table),
				Item:                item,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		)
		if e != nil {
			return ErrUnavailable
		}
		return nil
	}
	m.ScheduledThrough = slotStamp(m.ID, m.IntervalSeconds, now)
	item, err := monitorItem(m)
	if err != nil {
		return err
	}
	work, err := workItem(
		Work{
			MonitorID:     m.ID,
			DueAt:         workStamp(now),
			Trigger:       "create",
			ConfigVersion: m.ConfigVersion,
			State:         "pending",
		},
	)
	if err != nil {
		return err
	}
	_, err = s.db.TransactWriteItems(
		ctx,
		&dynamodb.TransactWriteItemsInput{TransactItems: []types.TransactWriteItem{
			{
				Put: &types.Put{
					TableName:           aws.String(s.table),
					Item:                item,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
			{
				Put: &types.Put{
					TableName:           aws.String(s.table),
					Item:                work,
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
		}},
	)
	if err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Get(ctx context.Context, id string) (monitor.Monitor, error) {
	if err := s.ensure(ctx); err != nil {
		return monitor.Monitor{}, err
	}
	out, err := s.db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            key("MONITORS", "MON#"+id),
			ConsistentRead: aws.Bool(true),
		},
	)
	if err != nil {
		return monitor.Monitor{}, ErrUnavailable
	}
	if len(out.Item) == 0 {
		return monitor.Monitor{}, ErrNotFound
	}
	var m monitor.Monitor
	if attributevalue.UnmarshalMap(out.Item, &m) != nil {
		return monitor.Monitor{}, ErrUnavailable
	}
	if err := s.visibleApplication(ctx, &m); err != nil {
		return monitor.Monitor{}, err
	}
	m.IncidentPolicy = m.IncidentPolicy.Defaults()
	if err := s.presentMaintenance(ctx, &m); err != nil {
		return monitor.Monitor{}, err
	}
	m, err = s.initializeLegacyCursor(ctx, m)
	if err != nil {
		return monitor.Monitor{}, err
	}
	return m, nil
}

func (s *Store) List(ctx context.Context) ([]monitor.Monitor, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	result := []monitor.Monitor{}
	var start map[string]types.AttributeValue
	for {
		out, err := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :sk)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk": &types.AttributeValueMemberS{Value: "MONITORS"},
					":sk": &types.AttributeValueMemberS{Value: "MON#"},
				},
				ConsistentRead:    aws.Bool(true),
				ExclusiveStartKey: start,
			},
		)
		if err != nil {
			return nil, ErrUnavailable
		}
		for _, item := range out.Items {
			var m monitor.Monitor
			if attributevalue.UnmarshalMap(item, &m) != nil {
				return nil, ErrUnavailable
			}
			m.IncidentPolicy = m.IncidentPolicy.Defaults()
			if err := s.visibleApplication(ctx, &m); err != nil {
				return nil, err
			}
			if err := s.presentMaintenance(ctx, &m); err != nil {
				return nil, err
			}
			m, err = s.initializeLegacyCursor(ctx, m)
			if err != nil {
				return nil, err
			}
			result = append(result, m)
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].CreatedAt == result[j].CreatedAt {
			return result[i].ID < result[j].ID
		}
		return result[i].CreatedAt < result[j].CreatedAt
	})
	return result, nil
}

func (s *Store) save(
	ctx context.Context,
	m monitor.Monitor,
	previous monitor.Monitor,
	condition string,
	values map[string]types.AttributeValue,
	trigger string,
	now time.Time,
) error {
	if m.Kind == "heartbeat" {
		return s.saveHeartbeat(ctx, m, previous, condition, values, trigger, now)
	}
	values[":name"] = &types.AttributeValueMemberS{Value: m.Name}
	values[":check"] = mustAV(m.Check)
	values[":nextVersion"] = &types.AttributeValueMemberN{Value: fmt.Sprint(m.ConfigVersion)}
	values[":nextUpdated"] = &types.AttributeValueMemberS{Value: m.UpdatedAt}
	values[":nextLifecycle"] = &types.AttributeValueMemberS{Value: m.Lifecycle}
	values[":interval"] = &types.AttributeValueMemberN{Value: fmt.Sprint(m.IntervalSeconds)}
	values[":through"] = &types.AttributeValueMemberS{Value: m.ScheduledThrough}
	values[":policy"] = mustAV(m.IncidentPolicy.Defaults())
	expr := "SET #name = :name, #check = :check, incidentPolicy = :policy, configVersion = :nextVersion, updatedAt = :nextUpdated, lifecycle = :nextLifecycle, intervalSeconds = :interval, scheduledThrough = :through"
	if m.Lifecycle == "paused" {
		values[":paused"] = &types.AttributeValueMemberS{Value: m.PausedAt}
		expr += ", pausedAt = :paused"
	}
	if m.Lifecycle == "archived" {
		values[":archivedAt"] = &types.AttributeValueMemberS{Value: m.ArchivedAt}
		expr += ", archivedAt = :archivedAt"
	}
	if m.Lifecycle != "paused" {
		expr += " REMOVE pausedAt"
	}
	condition += " AND "
	if previous.OpenIncident == nil {
		condition += "attribute_not_exists(openIncident)"
	} else {
		condition += "openIncident.id = :openID"
		values[":openID"] = mustAV(previous.OpenIncident.ID)
	}
	changesRun := trigger == "config_change" || trigger == "resume"
	if changesRun {
		values[":evaluation"] = mustAV(incident.Clear(previous.Evaluation))
		expr = strings.Replace(expr, " REMOVE ", ", evaluation = :evaluation REMOVE ", 1)
		if !strings.Contains(expr, " REMOVE ") {
			expr += ", evaluation = :evaluation"
		}
		values[":revision"] = mustAV(previous.Evaluation.Revision)
		if previous.Evaluation.Revision == 0 {
			condition += " AND (attribute_not_exists(evaluation.revision) OR evaluation.revision = :revision)"
		} else {
			condition += " AND evaluation.revision = :revision"
		}
	}
	if trigger == "resume" {
		maintenance := previous.Maintenance
		maintenance.Windows = incident.Prune(maintenance.Windows, now)
		maintenance.ActiveID = ""
		values[":maintenance"] = mustAV(maintenance)
		expr = strings.Replace(expr, " REMOVE ", ", maintenance = :maintenance REMOVE ", 1)
		if !strings.Contains(expr, "maintenance = :maintenance") {
			expr += ", maintenance = :maintenance"
		}
	}
	if trigger == "resume" && previous.OpenIncident != nil {
		opened, _ := time.Parse(time.RFC3339Nano, previous.OpenIncident.OpenedAt)
		op := *previous.OpenIncident
		op.NextReminderAt = monitor.Stamp(incident.NextSlot(opened, s.reminderInterval, now))
		values[":open"] = mustAV(op)
		expr = strings.Replace(expr, " REMOVE ", ", openIncident = :open REMOVE ", 1)
		if !strings.Contains(expr, "openIncident = :open") {
			expr += ", openIncident = :open"
		}
	}
	if m.Lifecycle == "archived" && previous.OpenIncident != nil {
		if strings.Contains(expr, " REMOVE ") {
			expr += ", openIncident"
		} else {
			expr += " REMOVE openIncident"
		}
	}
	input := &dynamodb.UpdateItemInput{
		TableName:                 aws.String(s.table),
		Key:                       key("MONITORS", "MON#"+m.ID),
		ConditionExpression:       aws.String(condition),
		UpdateExpression:          aws.String(expr),
		ExpressionAttributeValues: values,
		ExpressionAttributeNames:  map[string]string{"#name": "name", "#check": "check"},
	}
	tx := []types.TransactWriteItem{
		{
			Update: &types.Update{
				TableName:                 input.TableName,
				Key:                       input.Key,
				ConditionExpression:       input.ConditionExpression,
				UpdateExpression:          input.UpdateExpression,
				ExpressionAttributeValues: input.ExpressionAttributeValues,
				ExpressionAttributeNames:  input.ExpressionAttributeNames,
			},
		},
	}
	if trigger != "" {
		work, _ := workItem(
			Work{
				MonitorID:     m.ID,
				DueAt:         workStamp(now),
				Trigger:       trigger,
				ConfigVersion: m.ConfigVersion,
				State:         "pending",
			},
		)
		tx = append(tx, putItem(s.table, work))
	}
	if previous.OpenIncident != nil {
		kind := ""
		details := map[string]any{}
		switch {
		case m.Lifecycle == "archived":
			kind = "resolved"
			details["resolution"] = "archived"
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
		if m.Lifecycle == "archived" {
			in, readErr := s.incident(ctx, m.ID, previous.OpenIncident.ID)
			if readErr != nil {
				return readErr
			}
			at := monitor.Stamp(now)
			resolution := "archived"
			in.ResolvedAt = &at
			in.Resolution = &resolution
			in.State = "resolved"
			tx = append(
				tx,
				types.TransactWriteItem{
					Update: &types.Update{
						TableName:           aws.String(s.table),
						Key:                 key(incidentPK(m.ID), "INC#"+in.ID),
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
			notes, noteErr := s.intent(previous, in, "resolved", "resolved", nil, nil, now)
			if noteErr != nil {
				return noteErr
			}
			tx = append(tx, notes...)
		}
	}
	_, err := s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if err != nil {
		var conflict *types.ConditionalCheckFailedException
		if errors.As(err, &conflict) || cancelled(err, 0) {
			return ErrVersionConflict
		}
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Patch(
	ctx context.Context,
	id string,
	expected int,
	name *string,
	check *monitor.Check,
	now time.Time,
) (monitor.Monitor, error) {
	return s.PatchInterval(ctx, id, expected, name, check, nil, now)
}

func (s *Store) PatchInterval(
	ctx context.Context,
	id string,
	expected int,
	name *string,
	check *monitor.Check,
	interval *int,
	now time.Time,
) (monitor.Monitor, error) {
	return s.PatchIntervalPolicy(ctx, id, expected, name, check, interval, nil, now)
}

func (s *Store) PatchIntervalPolicy(
	ctx context.Context,
	id string,
	expected int,
	name *string,
	check *monitor.Check,
	interval *int,
	policy *incident.Policy,
	now time.Time,
) (monitor.Monitor, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return m, err
	}
	if m.Lifecycle == "archived" {
		return m, ErrArchived
	}
	if m.ConfigVersion != expected {
		return m, ErrVersionConflict
	}
	next := m.Patch(name, check, now)
	if m.Kind == "heartbeat" {
		return m, ErrNotEligible
	}
	if policy != nil {
		next.IncidentPolicy = *policy
	}
	trigger := ""
	if next.ConfigVersion != m.ConfigVersion {
		trigger = "config_change"
	}
	if interval != nil && *interval != m.IntervalSeconds {
		next.IntervalSeconds = *interval
		next.ScheduledThrough = slotStamp(id, *interval, now)
	}
	err = s.save(
		ctx,
		next,
		m,
		"attribute_exists(PK) AND configVersion = :version AND lifecycle <> :archived AND updatedAt = :updated AND #name = :previousName AND scheduledThrough = :previousCursor",
		map[string]types.AttributeValue{
			":version":        &types.AttributeValueMemberN{Value: fmt.Sprint(expected)},
			":archived":       &types.AttributeValueMemberS{Value: "archived"},
			":updated":        &types.AttributeValueMemberS{Value: m.UpdatedAt},
			":previousName":   &types.AttributeValueMemberS{Value: m.Name},
			":previousCursor": &types.AttributeValueMemberS{Value: m.ScheduledThrough},
		},
		trigger,
		now,
	)
	if err == ErrVersionConflict {
		current, e := s.Get(ctx, id)
		if e == nil && current.Lifecycle == "archived" {
			err = ErrArchived
		}
	}
	return next, err
}

func (s *Store) Lifecycle(
	ctx context.Context,
	id, action string,
	now time.Time,
) (monitor.Monitor, error) {
	m, err := s.Get(ctx, id)
	if err != nil {
		return m, err
	}
	if m.Lifecycle == "archived" {
		return m, ErrArchived
	}
	next, ok := m.Transition(action, now)
	if !ok {
		return m, ErrInvalidTransition
	}
	if m.Kind == "heartbeat" {
		if action == "resume" {
			next.Evaluation = incident.Clear(m.Evaluation)
			next.Maintenance.Windows = incident.Prune(m.Maintenance.Windows, now)
			next.Maintenance.ActiveID = ""
			if m.OpenIncident != nil {
				op := *m.OpenIncident
				opened, _ := time.Parse(time.RFC3339Nano, op.OpenedAt)
				op.NextReminderAt = monitor.Stamp(
					incident.NextSlot(opened, s.reminderInterval, now),
				)
				next.OpenIncident = &op
			}
		}
		err = s.saveHeartbeat(
			ctx,
			next,
			m,
			"lifecycle = :from AND updatedAt = :updated",
			map[string]types.AttributeValue{
				":from":    mustAV(m.Lifecycle),
				":updated": mustAV(m.UpdatedAt),
			},
			action,
			now,
		)
		if err == nil && action == "archive" && m.OpenIncident != nil {
			s.cancelResolvedReminders(ctx, m.ID, m.OpenIncident.ID)
		}
		return next, err
	}
	trigger := ""
	if action == "resume" {
		next.ScheduledThrough = slotStamp(id, next.IntervalSeconds, now)
		trigger = "resume"
	}
	if action == "archive" {
		next.OpenIncident = nil
	}
	if action == "resume" && m.OpenIncident != nil {
		op := *m.OpenIncident
		opened, _ := time.Parse(time.RFC3339Nano, op.OpenedAt)
		op.NextReminderAt = monitor.Stamp(incident.NextSlot(opened, s.reminderInterval, now))
		next.OpenIncident = &op
	}
	err = s.save(
		ctx,
		next,
		m,
		"attribute_exists(PK) AND lifecycle = :from AND updatedAt = :updated AND #name = :previousName AND scheduledThrough = :previousCursor",
		map[string]types.AttributeValue{
			":from":           &types.AttributeValueMemberS{Value: m.Lifecycle},
			":updated":        &types.AttributeValueMemberS{Value: m.UpdatedAt},
			":previousName":   &types.AttributeValueMemberS{Value: m.Name},
			":previousCursor": &types.AttributeValueMemberS{Value: m.ScheduledThrough},
		},
		trigger,
		now,
	)
	if err == ErrVersionConflict {
		current, e := s.Get(ctx, id)
		if e == nil && current.Lifecycle == "archived" {
			err = ErrArchived
		} else {
			err = ErrInvalidTransition
		}
	}
	if err == nil && action == "archive" && m.OpenIncident != nil {
		s.cancelResolvedReminders(ctx, m.ID, m.OpenIncident.ID)
	}
	return next, err
}

func (s *Store) PutObservation(ctx context.Context, o monitor.Observation) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	item, err := attributevalue.MarshalMap(o)
	if err != nil {
		return ErrUnavailable
	}
	started, err := time.Parse(time.RFC3339Nano, o.StartedAt)
	if err != nil {
		return ErrUnavailable
	}
	item["PK"] = &types.AttributeValueMemberS{Value: "MON#" + o.MonitorID}
	item["SK"] = &types.AttributeValueMemberS{
		Value: "OBS#" + started.UTC().Format("2006-01-02T15:04:05.000000000Z") + "#" + o.ID,
	}
	item["entityType"] = &types.AttributeValueMemberS{Value: "observation"}
	item["expiresAt"] = &types.AttributeValueMemberN{
		Value: fmt.Sprint(started.Add(90 * 24 * time.Hour).Unix()),
	}
	_, err = s.db.PutItem(
		ctx,
		&dynamodb.PutItemInput{
			TableName:           aws.String(s.table),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(PK)"),
		},
	)
	if err != nil {
		var duplicate *types.ConditionalCheckFailedException
		if errors.As(err, &duplicate) {
			return ErrDuplicate
		}
		return ErrUnavailable
	}
	return nil
}

func (s *Store) Observations(
	ctx context.Context,
	id string,
	limit int,
) ([]monitor.Observation, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	result := []monitor.Observation{}
	var start map[string]types.AttributeValue
	for len(result) < limit {
		out, err := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":     &types.AttributeValueMemberS{Value: "MON#" + id},
					":prefix": &types.AttributeValueMemberS{Value: "OBS#"},
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
			var o monitor.Observation
			if attributevalue.UnmarshalMap(item, &o) != nil {
				return nil, ErrUnavailable
			}
			if o.InitiatedBy == "" {
				o.InitiatedBy = "manual"
			}
			expires, ok := item["expiresAt"].(*types.AttributeValueMemberN)
			if !ok {
				return nil, ErrUnavailable
			}
			epoch, err := strconv.ParseInt(expires.Value, 10, 64)
			if err != nil {
				return nil, ErrUnavailable
			}
			if time.Unix(epoch, 0).After(s.now()) {
				result = append(result, o)
			}
		}
		if len(out.LastEvaluatedKey) == 0 {
			break
		}
		start = out.LastEvaluatedKey
	}
	return result, nil
}
