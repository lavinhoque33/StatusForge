package store

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
)

var ErrDuplicateDeployment = errors.New("duplicate_deployment")

type Marker struct {
	ID            string  `json:"id"            dynamodbav:"markerId"`
	ApplicationID string  `json:"applicationId" dynamodbav:"applicationId"`
	Version       string  `json:"version"       dynamodbav:"version"`
	Description   *string `json:"description"   dynamodbav:"description,omitempty"`
	Link          *string `json:"link"          dynamodbav:"link,omitempty"`
	DeployedAt    *string `json:"deployedAt"    dynamodbav:"deployedAt,omitempty"`
	DeploymentID  *string `json:"deploymentId"  dynamodbav:"deploymentId,omitempty"`
	Source        string  `json:"source"        dynamodbav:"source"`
	ReportedAt    string  `json:"reportedAt"    dynamodbav:"reportedAt"`
}
type NearbyMarker struct {
	Marker
	OffsetSeconds int64 `json:"offsetSeconds"`
}

func (s *Store) PutDeployment(
	ctx context.Context,
	id, expectedHash string,
	marker Marker,
) (Marker, error) {
	if err := s.ensure(ctx); err != nil {
		return marker, err
	}
	marker.ID = rand.Text()
	marker.ApplicationID = id
	item, e := attributevalue.MarshalMap(marker)
	if e != nil {
		return marker, ErrUnavailable
	}
	item["PK"] = mustAV("APP#" + id)
	item["SK"] = mustAV("DEP#" + marker.ReportedAt + "#" + marker.ID)
	item["entityType"] = mustAV("deployment")
	expiry := retention.History(parseStamp(marker.ReportedAt))
	item["expiresAt"] = mustAV(expiry)
	condition := &types.ConditionCheck{
		TableName: aws.String(s.table),
		Key:       appKey(id),
		ConditionExpression: aws.String(
			"attribute_exists(PK) AND attribute_not_exists(archivedAt)",
		),
	}
	if expectedHash != "" {
		condition.ConditionExpression = aws.String(
			"attribute_exists(PK) AND attribute_not_exists(archivedAt) AND #token.#hash = :hash",
		)
		condition.ExpressionAttributeNames = map[string]string{"#token": "token", "#hash": "hash"}
		condition.ExpressionAttributeValues = map[string]types.AttributeValue{
			":hash": mustAV(expectedHash),
		}
	}
	tx := []types.TransactWriteItem{
		{ConditionCheck: condition},
		{
			Put: &types.Put{
				TableName:           aws.String(s.table),
				Item:                item,
				ConditionExpression: aws.String("attribute_not_exists(PK)"),
			},
		},
	}
	if marker.DeploymentID != nil {
		guard := key("APP#"+id, "DEPID#"+*marker.DeploymentID)
		guard["markerId"] = mustAV(marker.ID)
		guard["reportedAt"] = mustAV(marker.ReportedAt)
		guard["expiresAt"] = mustAV(expiry)
		tx = append(
			tx,
			types.TransactWriteItem{
				Put: &types.Put{
					TableName:           aws.String(s.table),
					Item:                guard,
					ConditionExpression: aws.String("attribute_not_exists(PK) OR expiresAt < :now"),
					ExpressionAttributeValues: map[string]types.AttributeValue{
						":now": mustAV(s.now().Unix()),
					},
				},
			},
		)
	}
	_, e = s.db.TransactWriteItems(ctx, &dynamodb.TransactWriteItemsInput{TransactItems: tx})
	if e != nil {
		if marker.DeploymentID != nil && cancelled(e, 2) {
			return Marker{}, ErrDuplicateDeployment
		}
		if cancelled(e, 0) {
			a, err := s.applicationRaw(ctx, id)
			if err != nil {
				return Marker{}, err
			}
			if a.ArchivedAt != nil {
				return Marker{}, ErrArchived
			}
			return Marker{}, ErrNotEligible
		}
		return Marker{}, ErrUnavailable
	}
	return marker, nil
}

func (s *Store) Deployments(ctx context.Context, id string, limit int) ([]Marker, error) {
	if _, e := s.applicationRaw(ctx, id); e != nil {
		return nil, e
	}
	items, e := s.query(ctx, "APP#"+id, "DEP#", limit, true)
	if e != nil {
		return nil, e
	}
	result := make([]Marker, 0, len(items))
	for _, item := range items {
		var m Marker
		if attributevalue.UnmarshalMap(item, &m) != nil {
			return nil, ErrUnavailable
		}
		result = append(result, m)
	}
	return result, nil
}

func (s *Store) NearbyDeployments(
	ctx context.Context,
	id string,
	opened time.Time,
	resolved *time.Time,
	now time.Time,
) ([]NearbyMarker, error) {
	result := []NearbyMarker{}
	if id == "" {
		return result, nil
	}
	end := now
	if resolved != nil {
		end = *resolved
	}
	start := opened.Add(-2 * time.Hour)
	out, e := s.db.Query(
		ctx,
		&dynamodb.QueryInput{
			TableName:              aws.String(s.table),
			KeyConditionExpression: aws.String("PK = :pk AND SK BETWEEN :start AND :end"),
			ExpressionAttributeValues: map[string]types.AttributeValue{
				":pk":    mustAV("APP#" + id),
				":start": mustAV("DEP#" + monitor.Stamp(start)),
				":end":   mustAV("DEP#" + monitor.Stamp(end) + "#~"),
			},
			ConsistentRead: aws.Bool(true),
			Limit:          aws.Int32(20),
		},
	)
	if e != nil {
		return nil, ErrUnavailable
	}
	for _, item := range out.Items {
		if expiredAt(item, now) {
			continue
		}
		var m Marker
		if attributevalue.UnmarshalMap(item, &m) != nil {
			return nil, ErrUnavailable
		}
		at, err := time.Parse(time.RFC3339Nano, m.ReportedAt)
		if err != nil {
			return nil, ErrUnavailable
		}
		result = append(result, NearbyMarker{m, int64(at.Sub(opened).Seconds())})
	}
	return result, nil
}
