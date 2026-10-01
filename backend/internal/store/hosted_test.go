package store

import (
	"context"
	"crypto/rand"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
)

// recordingDB fails and records every table-level write the store attempts.
// UpdateTable and DeleteTable are not in the DynamoDB interface at all.
type recordingDB struct {
	DynamoDB
	mu     sync.Mutex
	writes []string
}

func (r *recordingDB) record(call string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes = append(r.writes, call)
}

func (r *recordingDB) tableWrites() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.writes...)
}

func (r *recordingDB) CreateTable(
	context.Context,
	*dynamodb.CreateTableInput,
	...func(*dynamodb.Options),
) (*dynamodb.CreateTableOutput, error) {
	r.record("CreateTable")
	return nil, errors.New("table-level write refused by test")
}

func (r *recordingDB) UpdateTimeToLive(
	context.Context,
	*dynamodb.UpdateTimeToLiveInput,
	...func(*dynamodb.Options),
) (*dynamodb.UpdateTimeToLiveOutput, error) {
	r.record("UpdateTimeToLive")
	return nil, errors.New("table-level write refused by test")
}

func scratchClient(t *testing.T) *dynamodb.Client {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local on 127.0.0.1:8000 unreachable:", err)
	}
	conn.Close()
	return localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").
		DynamoDB()
}

func scratchTable(t *testing.T, db *dynamodb.Client, keys []string, ttl string) string {
	t.Helper()
	name := "statusforge_m7t_" + strings.ToLower(rand.Text()[:12])
	if keys == nil {
		return name
	}
	defs := []types.AttributeDefinition{}
	schema := []types.KeySchemaElement{}
	for i, k := range keys {
		defs = append(defs, types.AttributeDefinition{
			AttributeName: aws.String(k), AttributeType: types.ScalarAttributeTypeS,
		})
		kind := types.KeyTypeHash
		if i == 1 {
			kind = types.KeyTypeRange
		}
		schema = append(schema, types.KeySchemaElement{AttributeName: aws.String(k), KeyType: kind})
	}
	ctx := t.Context()
	if _, err := db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(name), AttributeDefinitions: defs, KeySchema: schema,
		BillingMode: types.BillingModePayPerRequest,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.DeleteTable(cleanup, &dynamodb.DeleteTableInput{TableName: aws.String(name)}); err != nil {
			t.Logf("could not remove scratch table: %v", err)
		}
	})
	if ttl != "" {
		if _, err := db.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{
			TableName: aws.String(name),
			TimeToLiveSpecification: &types.TimeToLiveSpecification{
				AttributeName: aws.String(ttl), Enabled: aws.Bool(true),
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	return name
}

// Validate fails closed with a named error for a missing table, a
// wrong key schema, TTL off, and TTL on the wrong attribute, and never calls
// a table-level write, neither in Validate nor in later store calls.
func TestHostedValidation(t *testing.T) {
	db := scratchClient(t)
	cases := []struct {
		name string
		keys []string
		ttl  string
		want error
	}{
		{"missing", nil, "", ErrTableNotReady},
		{"wrong key schema", []string{"id"}, "expiresAt", ErrKeySchemaMismatch},
		{"wrong key names", []string{"PK", "sort"}, "expiresAt", ErrKeySchemaMismatch},
		{"ttl off", []string{"PK", "SK"}, "", ErrTTLNotEnabled},
		{"ttl wrong attribute", []string{"PK", "SK"}, "ttl", ErrTTLNotEnabled},
		{"ready", []string{"PK", "SK"}, "expiresAt", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			table := scratchTable(t, db, c.keys, c.ttl)
			recorder := &recordingDB{DynamoDB: db}
			s := New(recorder, table, time.Second, time.Now)
			err := s.Validate(t.Context())
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Fatalf("Validate = %v, want %v", err, c.want)
			}
			// Later calls re-validate; they never fall back to Initialize.
			if _, err := s.List(t.Context()); (c.want == nil) != (err == nil) {
				t.Fatalf("List after Validate = %v", err)
			}
			if writes := recorder.tableWrites(); len(writes) != 0 {
				t.Fatalf("table-level writes: %v", writes)
			}
			if c.want != nil {
				return
			}
			f, err := s.format(t.Context())
			if err != nil || f.DataFormat != DataFormat {
				t.Fatalf("format marker: %+v %v", f, err)
			}
			again := New(&recordingDB{DynamoDB: db}, table, time.Second, time.Now)
			if err := again.Validate(t.Context()); err != nil {
				t.Fatalf("second Validate: %v", err)
			}
		})
	}
}
