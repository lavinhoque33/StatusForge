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
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

var ErrDuplicateName = errors.New("duplicate_name")

type ApplicationMember struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type Application struct {
	ID         string              `json:"id"         dynamodbav:"applicationId"`
	Name       string              `json:"name"       dynamodbav:"name"`
	Token      *heartbeat.Token    `json:"token"      dynamodbav:"token,omitempty"`
	Members    []ApplicationMember `json:"members"    dynamodbav:"-"`
	CreatedAt  string              `json:"createdAt"  dynamodbav:"createdAt"`
	UpdatedAt  string              `json:"updatedAt"  dynamodbav:"updatedAt"`
	ArchivedAt *string             `json:"archivedAt" dynamodbav:"archivedAt,omitempty"`
}

func appKey(id string) map[string]types.AttributeValue { return key("APPLICATIONS", "APP#"+id) }
func nameKey(name string) map[string]types.AttributeValue {
	return key("APPLICATIONS", "NAME#"+strings.ToLower(name))
}

func (s *Store) applicationRaw(ctx context.Context, id string) (Application, error) {
	if err := s.ensure(ctx); err != nil {
		return Application{}, err
	}
	out, err := s.db.GetItem(
		ctx,
		&dynamodb.GetItemInput{
			TableName:      aws.String(s.table),
			Key:            appKey(id),
			ConsistentRead: aws.Bool(true),
		},
	)
	if err != nil {
		return Application{}, ErrUnavailable
	}
	if len(out.Item) == 0 {
		return Application{}, ErrNotFound
	}
	var a Application
	if attributevalue.UnmarshalMap(out.Item, &a) != nil {
		return a, ErrUnavailable
	}
	return a, nil
}

func (s *Store) visibleApplication(ctx context.Context, m *monitor.Monitor) error {
	if m.ApplicationID == "" {
		return nil
	}
	app, err := s.applicationRaw(ctx, m.ApplicationID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			m.ApplicationID = ""
			return nil
		}
		return err
	}
	if app.ArchivedAt != nil {
		m.ApplicationID = ""
	}
	return nil
}

func (s *Store) Application(ctx context.Context, id string) (Application, error) {
	a, err := s.applicationRaw(ctx, id)
	if err != nil {
		return a, err
	}
	if a.ArchivedAt != nil {
		a.Members = []ApplicationMember{}
		return a, nil
	}
	ms, err := s.List(ctx)
	if err != nil {
		return a, err
	}
	a.Members = []ApplicationMember{}
	for _, m := range ms {
		if m.ApplicationID == id && m.Lifecycle != "archived" {
			a.Members = append(a.Members, ApplicationMember{m.ID, m.Name, m.Kind})
		}
	}
	// Recheck archive after the monitor read: archived applications never publish members.
	current, err := s.applicationRaw(ctx, id)
	if err != nil {
		return a, err
	}
	if current.ArchivedAt != nil {
		a.Members = []ApplicationMember{}
		a.ArchivedAt = current.ArchivedAt
		a.Token = nil
	}
	return a, nil
}

func (s *Store) Applications(ctx context.Context) ([]Application, error) {
	if err := s.ensure(ctx); err != nil {
		return nil, err
	}
	out := []Application{}
	var start map[string]types.AttributeValue
	for {
		page, e := s.db.Query(
			ctx,
			&dynamodb.QueryInput{
				TableName:              aws.String(s.table),
				KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":pk":     mustAV("APPLICATIONS"),
					":prefix": mustAV("APP#"),
				},
				ConsistentRead:    aws.Bool(true),
				ExclusiveStartKey: start,
			},
		)
		if e != nil {
			return nil, ErrUnavailable
		}
		for _, item := range page.Items {
			var a Application
			if attributevalue.UnmarshalMap(item, &a) != nil {
				return nil, ErrUnavailable
			}
			a.Members = []ApplicationMember{}
			out = append(out, a)
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	ms, e := s.List(ctx)
	if e != nil {
		return nil, e
	}
	for i := range out {
		if out[i].ArchivedAt != nil {
			continue
		}
		for _, m := range ms {
			if m.ApplicationID == out[i].ID && m.Lifecycle != "archived" {
				out[i].Members = append(out[i].Members, ApplicationMember{m.ID, m.Name, m.Kind})
			}
		}
		current, e := s.applicationRaw(ctx, out[i].ID)
		if e != nil {
			return nil, e
		}
		if current.ArchivedAt != nil {
			out[i].Members = []ApplicationMember{}
			out[i].ArchivedAt = current.ArchivedAt
			out[i].Token = nil
		}
	}
	return out, nil
}

func (s *Store) CreateApplication(
	ctx context.Context,
	name string,
	now time.Time,
) (Application, string, error) {
	if err := s.ensure(ctx); err != nil {
		return Application{}, "", err
	}
	value, e := heartbeat.GenerateDeployment()
	if e != nil {
		return Application{}, "", ErrUnavailable
	}
	stamp := monitor.Stamp(now)
	a := Application{
		ID:   rand.Text(),
		Name: name,
		Token: &heartbeat.Token{
			Hash:      heartbeat.Hash(value),
			Hint:      heartbeat.Hint(value),
			CreatedAt: stamp,
		},
		Members:   []ApplicationMember{},
		CreatedAt: stamp,
		UpdatedAt: stamp,
	}
	item, e := attributevalue.MarshalMap(a)
	if e != nil {
		return a, "", ErrUnavailable
	}
	item["PK"] = mustAV("APPLICATIONS")
	item["SK"] = mustAV("APP#" + a.ID)
	item["entityType"] = mustAV("application")
	guard := nameKey(name)
	guard["applicationId"] = mustAV(a.ID)
	_, e = s.db.TransactWriteItems(
		ctx,
		&dynamodb.TransactWriteItemsInput{
			TransactItems: []types.TransactWriteItem{
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
						Item:                guard,
						ConditionExpression: aws.String("attribute_not_exists(PK)"),
					},
				},
			},
		},
	)
	if e != nil {
		if cancelled(e, 1) {
			return Application{}, "", ErrDuplicateName
		}
		return Application{}, "", ErrUnavailable
	}
	return a, value, nil
}

func (s *Store) RenameApplication(
	ctx context.Context,
	id, name string,
	now time.Time,
) (Application, error) {
	for range 3 {
		a, e := s.applicationRaw(ctx, id)
		if e != nil {
			return a, e
		}
		if a.ArchivedAt != nil {
			return a, ErrArchived
		}
		if a.Name == name {
			return s.Application(ctx, id)
		}
		if strings.EqualFold(a.Name, name) {
			_, err := s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
				TableName: aws.String(s.table), Key: appKey(id),
				ConditionExpression: aws.String(
					"#name = :old AND attribute_not_exists(archivedAt)",
				),
				UpdateExpression:         aws.String("SET #name = :new, updatedAt = :at"),
				ExpressionAttributeNames: map[string]string{"#name": "name"},
				ExpressionAttributeValues: map[string]types.AttributeValue{
					":old": mustAV(a.Name),
					":new": mustAV(name),
					":at":  mustAV(monitor.Stamp(now)),
				},
			})
			if err == nil {
				return s.Application(ctx, id)
			}
			var conflict *types.ConditionalCheckFailedException
			if errors.As(err, &conflict) {
				continue
			}
			return Application{}, ErrUnavailable
		}
		tx := []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName: aws.String(s.table),
					Key:       appKey(id),
					ConditionExpression: aws.String(
						"#name = :old AND attribute_not_exists(archivedAt)",
					),
					UpdateExpression:         aws.String("SET #name = :new, updatedAt = :at"),
					ExpressionAttributeNames: map[string]string{"#name": "name"},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":old": mustAV(a.Name),
						":new": mustAV(name),
						":at":  mustAV(monitor.Stamp(now)),
					},
				},
			},
			{
				Put: &types.Put{
					TableName: aws.String(s.table),
					Item: map[string]types.AttributeValue{
						"PK":            mustAV("APPLICATIONS"),
						"SK":            mustAV("NAME#" + strings.ToLower(name)),
						"applicationId": mustAV(id),
					},
					ConditionExpression: aws.String("attribute_not_exists(PK)"),
				},
			},
			{
				Delete: &types.Delete{
					TableName:                 aws.String(s.table),
					Key:                       nameKey(a.Name),
					ConditionExpression:       aws.String("applicationId = :id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{":id": mustAV(id)},
				},
			},
		}
		_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if e == nil {
			return s.Application(ctx, id)
		}
		if cancelled(e, 1) {
			return Application{}, ErrDuplicateName
		}
		if !cancelled(e, 0) && !cancelled(e, 2) {
			return Application{}, ErrUnavailable
		}
	}
	return Application{}, ErrVersionConflict
}

func (s *Store) ApplicationToken(
	ctx context.Context,
	id string,
	revoke bool,
	now time.Time,
) (string, *heartbeat.Token, error) {
	for range 3 {
		a, e := s.applicationRaw(ctx, id)
		if e != nil {
			return "", nil, e
		}
		if a.ArchivedAt != nil {
			return "", nil, ErrArchived
		}
		var value string
		var token *heartbeat.Token
		if !revoke {
			value, e = heartbeat.GenerateDeployment()
			if e != nil {
				return "", nil, ErrUnavailable
			}
			token = &heartbeat.Token{
				Hash:      heartbeat.Hash(value),
				Hint:      heartbeat.Hint(value),
				CreatedAt: monitor.Stamp(now),
			}
		}
		condition := "attribute_exists(PK) AND attribute_not_exists(archivedAt)"
		vals := map[string]types.AttributeValue{}
		if a.Token == nil {
			condition += " AND attribute_not_exists(#token.#hash)"
		} else {
			condition += " AND #token.#hash = :old"
			vals[":old"] = mustAV(a.Token.Hash)
		}
		expr := "REMOVE #token"
		if !revoke {
			expr = "SET #token = :new"
			vals[":new"] = mustAV(token)
		}
		_, e = s.db.UpdateItem(
			ctx,
			&dynamodb.UpdateItemInput{
				TableName:                 aws.String(s.table),
				Key:                       appKey(id),
				ConditionExpression:       aws.String(condition),
				UpdateExpression:          aws.String(expr),
				ExpressionAttributeNames:  map[string]string{"#token": "token", "#hash": "hash"},
				ExpressionAttributeValues: vals,
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

func (s *Store) AuthenticateApplication(
	ctx context.Context,
	id, value string,
) (Application, error) {
	a, e := s.applicationRaw(ctx, id)
	if e != nil {
		return a, e
	}
	if a.Token == nil || !strings.HasPrefix(value, "sfd_") ||
		!heartbeat.Matches(value, a.Token.Hash) {
		return a, ErrNotEligible
	}
	return a, nil
}

func (s *Store) monitorApplicationRaw(ctx context.Context, id string) (string, error) {
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(s.table), Key: key("MONITORS", "MON#"+id),
		ConsistentRead:       aws.Bool(true),
		ProjectionExpression: aws.String("applicationId"),
	})
	if err != nil {
		return "", ErrUnavailable
	}
	if v, ok := out.Item["applicationId"].(*types.AttributeValueMemberS); ok {
		return v.Value, nil
	}
	return "", nil
}

func (s *Store) SetApplication(ctx context.Context, mid, id string) (monitor.Monitor, error) {
	for range 3 {
		m, e := s.Get(ctx, mid)
		if e != nil {
			return m, e
		}
		if m.Lifecycle == "archived" {
			return m, ErrArchived
		}
		if id != "" {
			a, err := s.applicationRaw(ctx, id)
			if err != nil {
				return m, err
			}
			if a.ArchivedAt != nil {
				return m, ErrArchived
			}
		}
		previous, err := s.monitorApplicationRaw(ctx, mid)
		if err != nil {
			return m, err
		}
		if previous == id {
			return m, nil
		}
		condition := "attribute_exists(PK) AND lifecycle <> :archived AND attribute_not_exists(applicationId)"
		vals := map[string]types.AttributeValue{":archived": mustAV("archived")}
		if previous != "" {
			condition = "attribute_exists(PK) AND lifecycle <> :archived AND applicationId = :old"
			vals[":old"] = mustAV(previous)
		}
		expr := "REMOVE applicationId"
		if id != "" {
			expr = "SET applicationId = :id"
			vals[":id"] = mustAV(id)
		}
		update := types.TransactWriteItem{
			Update: &types.Update{
				TableName:                 aws.String(s.table),
				Key:                       key("MONITORS", "MON#"+mid),
				ConditionExpression:       aws.String(condition),
				UpdateExpression:          aws.String(expr),
				ExpressionAttributeValues: vals,
			},
		}
		tx := []types.TransactWriteItem{update}
		if id != "" {
			tx = append(
				tx,
				types.TransactWriteItem{
					ConditionCheck: &types.ConditionCheck{
						TableName: aws.String(s.table),
						Key:       appKey(id),
						ConditionExpression: aws.String(
							"attribute_exists(PK) AND attribute_not_exists(archivedAt)",
						),
					},
				},
			)
		}
		_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if e == nil {
			return s.Get(ctx, mid)
		}
		if cancelled(e, 1) {
			a, err := s.applicationRaw(ctx, id)
			if err != nil {
				return m, err
			}
			if a.ArchivedAt != nil {
				return m, ErrArchived
			}
		}
		if !cancelled(e, 0) && !cancelled(e, 1) {
			return m, ErrUnavailable
		}
	}
	return monitor.Monitor{}, ErrVersionConflict
}

// Archive commits token revocation and name release first; subsequent calls complete
// conditional member clearing after interruption. Reads exclude archived members.
func (s *Store) ArchiveApplication(
	ctx context.Context,
	id string,
	now time.Time,
) (Application, error) {
	a, e := s.applicationRaw(ctx, id)
	if e != nil {
		return a, e
	}
	if a.ArchivedAt == nil {
		stamp := monitor.Stamp(now)
		tx := []types.TransactWriteItem{
			{
				Update: &types.Update{
					TableName: aws.String(s.table),
					Key:       appKey(id),
					ConditionExpression: aws.String(
						"attribute_not_exists(archivedAt) AND #name = :name",
					),
					UpdateExpression: aws.String(
						"SET archivedAt = :at, updatedAt = :at REMOVE #token",
					),
					ExpressionAttributeNames: map[string]string{
						"#token": "token",
						"#name":  "name",
					},
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":at":   mustAV(stamp),
						":name": mustAV(a.Name),
					},
				},
			},
			{
				Delete: &types.Delete{
					TableName:                 aws.String(s.table),
					Key:                       nameKey(a.Name),
					ConditionExpression:       aws.String("applicationId = :id"),
					ExpressionAttributeValues: map[string]types.AttributeValue{":id": mustAV(id)},
				},
			},
		}
		_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
		if e != nil {
			if !cancelled(e, 0) && !cancelled(e, 1) {
				return a, ErrUnavailable
			}
			a, e = s.applicationRaw(ctx, id)
			if e != nil {
				return a, e
			}
			if a.ArchivedAt == nil {
				return a, ErrVersionConflict
			}
		}
	}
	var start map[string]types.AttributeValue
	for {
		page, err := s.db.Query(ctx, &dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":     mustAV("MONITORS"),
				":prefix": mustAV("MON#"),
			},
			ConsistentRead:    aws.Bool(true),
			ExclusiveStartKey: start,
		})
		if err != nil {
			return a, ErrUnavailable
		}
		for _, item := range page.Items {
			var m monitor.Monitor
			if attributevalue.UnmarshalMap(item, &m) != nil {
				return a, ErrUnavailable
			}
			if m.ApplicationID != id {
				continue
			}
			_, err = s.db.UpdateItem(ctx, &dynamodb.UpdateItemInput{
				TableName:                 aws.String(s.table),
				Key:                       key("MONITORS", "MON#"+m.ID),
				ConditionExpression:       aws.String("applicationId = :id"),
				UpdateExpression:          aws.String("REMOVE applicationId"),
				ExpressionAttributeValues: map[string]types.AttributeValue{":id": mustAV(id)},
			})
			if err != nil {
				var conflict *types.ConditionalCheckFailedException
				if !errors.As(err, &conflict) {
					return a, ErrUnavailable
				}
			}
		}
		if len(page.LastEvaluatedKey) == 0 {
			break
		}
		start = page.LastEvaluatedKey
	}
	return s.Application(ctx, id)
}
