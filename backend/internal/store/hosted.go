package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

// Hosted start-up check failures (ADR 0008 D4). Each fails closed: the
// infrastructure owns the table, so the store never repairs it.
var (
	ErrTableNotReady     = errors.New("table_not_ready")
	ErrKeySchemaMismatch = errors.New("key_schema_mismatch")
	ErrTTLNotEnabled     = errors.New("ttl_not_enabled")
	ErrWorkNotFound      = errors.New("work_not_found")
)

// SetNotifications selects how incident notification intents are written.
// Call it before the store is used; the local default is NotificationsDeliver.
func (s *Store) SetNotifications(n Notifications) { s.notifications = n }

// Validate is the hosted start-up check. The table must be ACTIVE with the
// PK/SK string key schema and TTL ENABLED on expiresAt; the format marker is
// then written or read with item-level calls. It never calls CreateTable,
// UpdateTable, UpdateTimeToLive, or DeleteTable. After Validate the store
// never falls back to Initialize: until a validation succeeds, every call
// re-validates instead.
func (s *Store) Validate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hosted = true
	if s.ready {
		return nil
	}
	return s.validate(ctx)
}

// validate runs with s.mu held.
func (s *Store) validate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	out, err := s.db.DescribeTable(
		ctx,
		&dynamodb.DescribeTableInput{TableName: aws.String(s.table)},
	)
	if err != nil {
		var missing *types.ResourceNotFoundException
		if errors.As(err, &missing) {
			return fmt.Errorf("%w: table %s does not exist", ErrTableNotReady, s.table)
		}
		return ErrUnavailable
	}
	if out.Table == nil || out.Table.TableStatus != types.TableStatusActive {
		return fmt.Errorf("%w: table %s is not ACTIVE", ErrTableNotReady, s.table)
	}
	if !stringKeySchema(out.Table) {
		return fmt.Errorf("%w: table %s must have string keys PK (hash) and SK (range)",
			ErrKeySchemaMismatch, s.table)
	}
	ttl, err := s.db.DescribeTimeToLive(
		ctx,
		&dynamodb.DescribeTimeToLiveInput{TableName: aws.String(s.table)},
	)
	if err != nil {
		return ErrUnavailable
	}
	desc := ttl.TimeToLiveDescription
	if desc == nil || desc.TimeToLiveStatus != types.TimeToLiveStatusEnabled ||
		aws.ToString(desc.AttributeName) != "expiresAt" {
		return fmt.Errorf("%w: table %s needs TTL ENABLED on expiresAt", ErrTTLNotEnabled, s.table)
	}
	if err := s.marker(ctx); err != nil {
		return err
	}
	s.ready = true
	return nil
}

func stringKeySchema(t *types.TableDescription) bool {
	if len(t.KeySchema) != 2 {
		return false
	}
	want := map[string]types.KeyType{"PK": types.KeyTypeHash, "SK": types.KeyTypeRange}
	for _, k := range t.KeySchema {
		if kind, ok := want[aws.ToString(k.AttributeName)]; !ok || kind != k.KeyType {
			return false
		}
	}
	defs := map[string]types.ScalarAttributeType{}
	for _, d := range t.AttributeDefinitions {
		defs[aws.ToString(d.AttributeName)] = d.AttributeType
	}
	return defs["PK"] == types.ScalarAttributeTypeS && defs["SK"] == types.ScalarAttributeTypeS
}

// ValidWorkStamp reports whether value is a canonical work-item dueAt stamp.
func ValidWorkStamp(value string) bool {
	t, err := time.Parse(sortLayout, value)
	return err == nil && workStamp(t) == value
}

// GetWork reads one work item with a strongly consistent GetItem.
func (s *Store) GetWork(ctx context.Context, monitorID, dueAt string) (Work, error) {
	if err := s.ensure(ctx); err != nil {
		return Work{}, err
	}
	out, err := s.db.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      aws.String(s.table),
		Key:            key("MON#"+monitorID, "WORK#"+dueAt),
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		return Work{}, ErrUnavailable
	}
	if len(out.Item) == 0 {
		return Work{}, ErrWorkNotFound
	}
	var w Work
	if attributevalue.UnmarshalMap(out.Item, &w) != nil {
		return Work{}, ErrUnavailable
	}
	return w, nil
}

// RetentionStep runs one bounded retention-jobs phase (ADR 0007 D3), with the
// same two-second budget as the local housekeeping worker's phase, and
// returns the number of jobs visited.
func (s *Store) RetentionStep(ctx context.Context) (int, error) {
	if err := s.ensure(ctx); err != nil {
		return 0, err
	}
	return s.retentionPhase(ctx)
}

func (s *Store) retentionPhase(ctx context.Context) (int, error) {
	visited := 0
	until := time.Now().Add(2 * time.Second)
	for ctx.Err() == nil && time.Now().Before(until) {
		count, err := s.processRetention(ctx)
		visited += count
		if err != nil {
			return visited, err
		}
		if count == 0 {
			break
		}
	}
	return visited, nil
}
