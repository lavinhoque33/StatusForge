package cloudwork_test

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// The infrastructure grants each Lambda role exactly the DynamoDB actions in
// this file. The drills record what the planner pass and
// the worker handler actually call; TestMain fails if one is not granted.
const iamActionsFile = "../../../infra/iam/dynamodb-actions.json"

type role string

const (
	rolePlanner role = "planner"
	roleWorker  role = "worker"
	// roleBoth marks Validate, which both handlers call before their work.
	roleBoth role = "both"
)

type roleKey struct{}

// withRole attributes the DynamoDB calls made under ctx to a Lambda role.
// Calls without a role (test setup and assertions) are not recorded.
func withRole(ctx context.Context, r role) context.Context {
	return context.WithValue(ctx, roleKey{}, r)
}

// observed maps role → IAM action → true, across every test in the package.
var observed = struct {
	mu      sync.Mutex
	actions map[role]map[string]bool
}{actions: map[role]map[string]bool{rolePlanner: {}, roleWorker: {}}}

func record(ctx context.Context, actions ...string) {
	r, ok := ctx.Value(roleKey{}).(role)
	if !ok {
		return
	}
	roles := []role{r}
	if r == roleBoth {
		roles = []role{rolePlanner, roleWorker}
	}
	observed.mu.Lock()
	defer observed.mu.Unlock()
	for _, r := range roles {
		for _, action := range actions {
			observed.actions[r]["dynamodb:"+action] = true
		}
	}
}

// recordingDB records each call's IAM actions, then forwards it unchanged. It
// names db instead of embedding store.DynamoDB, so a method added to that
// interface fails to compile here rather than forwarding unrecorded.
type recordingDB struct{ db store.DynamoDB }

func (d recordingDB) BatchWriteItem(
	ctx context.Context,
	in *dynamodb.BatchWriteItemInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.BatchWriteItemOutput, error) {
	record(ctx, "BatchWriteItem")
	return d.db.BatchWriteItem(ctx, in, opts...)
}

func (d recordingDB) CreateTable(
	ctx context.Context,
	in *dynamodb.CreateTableInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.CreateTableOutput, error) {
	record(ctx, "CreateTable")
	return d.db.CreateTable(ctx, in, opts...)
}

func (d recordingDB) DeleteItem(
	ctx context.Context,
	in *dynamodb.DeleteItemInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.DeleteItemOutput, error) {
	record(ctx, "DeleteItem")
	return d.db.DeleteItem(ctx, in, opts...)
}

func (d recordingDB) DescribeTable(
	ctx context.Context,
	in *dynamodb.DescribeTableInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.DescribeTableOutput, error) {
	record(ctx, "DescribeTable")
	return d.db.DescribeTable(ctx, in, opts...)
}

func (d recordingDB) DescribeTimeToLive(
	ctx context.Context,
	in *dynamodb.DescribeTimeToLiveInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.DescribeTimeToLiveOutput, error) {
	record(ctx, "DescribeTimeToLive")
	return d.db.DescribeTimeToLive(ctx, in, opts...)
}

func (d recordingDB) GetItem(
	ctx context.Context,
	in *dynamodb.GetItemInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.GetItemOutput, error) {
	record(ctx, "GetItem")
	return d.db.GetItem(ctx, in, opts...)
}

func (d recordingDB) PutItem(
	ctx context.Context,
	in *dynamodb.PutItemInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.PutItemOutput, error) {
	record(ctx, "PutItem")
	return d.db.PutItem(ctx, in, opts...)
}

func (d recordingDB) Query(
	ctx context.Context,
	in *dynamodb.QueryInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.QueryOutput, error) {
	record(ctx, "Query")
	return d.db.Query(ctx, in, opts...)
}

func (d recordingDB) Scan(
	ctx context.Context,
	in *dynamodb.ScanInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.ScanOutput, error) {
	record(ctx, "Scan")
	return d.db.Scan(ctx, in, opts...)
}

// TransactWriteItems has no IAM action of its own: IAM authorises each item
// as the matching item-level action.
func (d recordingDB) TransactWriteItems(
	ctx context.Context,
	in *dynamodb.TransactWriteItemsInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.TransactWriteItemsOutput, error) {
	for _, item := range in.TransactItems {
		switch {
		case item.Put != nil:
			record(ctx, "PutItem")
		case item.Update != nil:
			record(ctx, "UpdateItem")
		case item.Delete != nil:
			record(ctx, "DeleteItem")
		case item.ConditionCheck != nil:
			record(ctx, "ConditionCheckItem")
		}
	}
	return d.db.TransactWriteItems(ctx, in, opts...)
}

func (d recordingDB) UpdateItem(
	ctx context.Context,
	in *dynamodb.UpdateItemInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.UpdateItemOutput, error) {
	record(ctx, "UpdateItem")
	return d.db.UpdateItem(ctx, in, opts...)
}

func (d recordingDB) UpdateTimeToLive(
	ctx context.Context,
	in *dynamodb.UpdateTimeToLiveInput,
	opts ...func(*dynamodb.Options),
) (*dynamodb.UpdateTimeToLiveOutput, error) {
	record(ctx, "UpdateTimeToLive")
	return d.db.UpdateTimeToLive(ctx, in, opts...)
}

func loadGranted() (map[role][]string, error) {
	raw, err := os.ReadFile(iamActionsFile)
	if err != nil {
		return nil, err
	}
	var granted map[role][]string
	if err := json.Unmarshal(raw, &granted); err != nil {
		return nil, fmt.Errorf("%s: %w", iamActionsFile, err)
	}
	return granted, nil
}

// TestIAMActionsGrantNoTableLevelWrites: IAM enforces §5's "no table-level
// writes" as well as the store code.
func TestIAMActionsGrantNoTableLevelWrites(t *testing.T) {
	granted, err := loadGranted()
	if err != nil {
		t.Fatal(err)
	}
	if len(granted) != 2 || len(granted[rolePlanner]) == 0 || len(granted[roleWorker]) == 0 {
		t.Fatalf("%s must list exactly the planner and worker roles: %v", iamActionsFile, granted)
	}
	forbidden := []string{
		"dynamodb:CreateTable",
		"dynamodb:UpdateTable",
		"dynamodb:UpdateTimeToLive",
		"dynamodb:DeleteTable",
	}
	for r, actions := range granted {
		for _, action := range actions {
			if slices.Contains(forbidden, action) || !strings.HasPrefix(action, "dynamodb:") ||
				strings.Contains(action, "*") {
				t.Errorf("%s grants %s to the %s role", iamActionsFile, action, r)
			}
		}
	}
}

// TestIAMFirstValidateOnFreshTable: the first Validate against a table the
// infrastructure has just created (no format marker yet) scans for existing
// items and writes the marker. Both handlers can be the first to run.
func TestIAMFirstValidateOnFreshTable(t *testing.T) {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local on 127.0.0.1:8000 unreachable:", err)
	}
	conn.Close()
	db := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").
		DynamoDB()
	table := "statusforge_m7t_" + strings.ToLower(rand.Text()[:12])
	ctx := t.Context()
	// The table as the stack defines it: string PK/SK and TTL on expiresAt.
	_, err = db.CreateTable(ctx, &dynamodb.CreateTableInput{
		TableName: aws.String(table),
		AttributeDefinitions: []types.AttributeDefinition{
			{AttributeName: aws.String("PK"), AttributeType: types.ScalarAttributeTypeS},
			{AttributeName: aws.String("SK"), AttributeType: types.ScalarAttributeTypeS},
		},
		KeySchema: []types.KeySchemaElement{
			{AttributeName: aws.String("PK"), KeyType: types.KeyTypeHash},
			{AttributeName: aws.String("SK"), KeyType: types.KeyTypeRange},
		},
		BillingMode: types.BillingModePayPerRequest,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}); err != nil {
			t.Logf("could not remove scratch table %s: %v", table, err)
		}
	})
	_, err = db.UpdateTimeToLive(ctx, &dynamodb.UpdateTimeToLiveInput{
		TableName: aws.String(table),
		TimeToLiveSpecification: &types.TimeToLiveSpecification{
			AttributeName: aws.String("expiresAt"), Enabled: aws.Bool(true),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s := store.New(recordingDB{db}, table, time.Second, time.Now)
	s.SetNotifications(store.NotificationsNone)
	if err := s.Validate(withRole(ctx, roleBoth)); err != nil {
		t.Fatal(err)
	}
}

// checkObserved compares what the drills called with what IAM grants. It
// returns the actions granted but never observed, for the §11 list.
func checkObserved() (unobserved []string, err error) {
	granted, err := loadGranted()
	if err != nil {
		return nil, err
	}
	observed.mu.Lock()
	defer observed.mu.Unlock()
	var missing []string
	for _, r := range []role{rolePlanner, roleWorker} {
		for action := range observed.actions[r] {
			if !slices.Contains(granted[r], action) {
				missing = append(missing, fmt.Sprintf("%s: %s", r, action))
			}
		}
		if len(observed.actions[r]) == 0 {
			// No drill ran for this role (DynamoDB Local unavailable, or -run filtered).
			continue
		}
		for _, action := range granted[r] {
			if !observed.actions[r][action] {
				unobserved = append(unobserved, fmt.Sprintf("%s: %s", r, action))
			}
		}
	}
	sort.Strings(missing)
	sort.Strings(unobserved)
	if len(missing) > 0 {
		return unobserved, fmt.Errorf(
			"drills called DynamoDB actions that %s does not grant:\n  %s",
			iamActionsFile,
			strings.Join(missing, "\n  "),
		)
	}
	return unobserved, nil
}

// TestMain checks the recorded actions after every drill has run, so the
// check covers whichever drills ran (they skip without DynamoDB Local).
func TestMain(m *testing.M) {
	code := m.Run()
	unobserved, err := checkObserved()
	switch {
	case err != nil:
		fmt.Fprintln(os.Stderr, "FAIL: IAM action list:", err)
		code = 1
	case testing.Verbose() && len(unobserved) > 0:
		fmt.Printf("IAM actions granted but not reached by any drill (§11):\n  %s\n",
			strings.Join(unobserved, "\n  "))
	}
	os.Exit(code)
}
