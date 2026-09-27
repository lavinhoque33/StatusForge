package store

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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
)

type Store struct {
	db      *dynamodb.Client
	table   string
	mu      sync.Mutex
	ready   bool
	now     func() time.Time
	timeout time.Duration
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
	return &Store{db: client.DynamoDB(), table: table, timeout: timeout * 5, now: now}
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
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return ErrUnavailable
	}
	item["PK"] = &types.AttributeValueMemberS{Value: "MONITORS"}
	item["SK"] = &types.AttributeValueMemberS{Value: "MON#" + m.ID}
	item["entityType"] = &types.AttributeValueMemberS{Value: "monitor"}
	_, err = s.db.PutItem(
		ctx,
		&dynamodb.PutItemInput{
			TableName:           aws.String(s.table),
			Item:                item,
			ConditionExpression: aws.String("attribute_not_exists(PK)"),
		},
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
	condition string,
	values map[string]types.AttributeValue,
) error {
	item, err := attributevalue.MarshalMap(m)
	if err != nil {
		return ErrUnavailable
	}
	item["PK"] = &types.AttributeValueMemberS{Value: "MONITORS"}
	item["SK"] = &types.AttributeValueMemberS{Value: "MON#" + m.ID}
	item["entityType"] = &types.AttributeValueMemberS{Value: "monitor"}
	_, err = s.db.PutItem(
		ctx,
		&dynamodb.PutItemInput{
			TableName:                 aws.String(s.table),
			Item:                      item,
			ConditionExpression:       aws.String(condition),
			ExpressionAttributeValues: values,
			ExpressionAttributeNames:  map[string]string{"#name": "name"},
		},
	)
	if err != nil {
		var conflict *types.ConditionalCheckFailedException
		if errors.As(err, &conflict) {
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
	err = s.save(
		ctx,
		next,
		"attribute_exists(PK) AND configVersion = :version AND lifecycle <> :archived AND updatedAt = :updated AND #name = :previousName",
		map[string]types.AttributeValue{
			":version":      &types.AttributeValueMemberN{Value: fmt.Sprint(expected)},
			":archived":     &types.AttributeValueMemberS{Value: "archived"},
			":updated":      &types.AttributeValueMemberS{Value: m.UpdatedAt},
			":previousName": &types.AttributeValueMemberS{Value: m.Name},
		},
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
	err = s.save(
		ctx,
		next,
		"attribute_exists(PK) AND lifecycle = :from AND updatedAt = :updated AND #name = :previousName",
		map[string]types.AttributeValue{
			":from":         &types.AttributeValueMemberS{Value: m.Lifecycle},
			":updated":      &types.AttributeValueMemberS{Value: m.UpdatedAt},
			":previousName": &types.AttributeValueMemberS{Value: m.Name},
		},
	)
	if err == ErrVersionConflict {
		current, e := s.Get(ctx, id)
		if e == nil && current.Lifecycle == "archived" {
			err = ErrArchived
		} else {
			err = ErrInvalidTransition
		}
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
