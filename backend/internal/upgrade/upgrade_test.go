package upgrade_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
	"github.com/lavinhoque33/statusforge/backend/internal/dataexport"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/retention"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type facts struct {
	Milestone int `json:"milestone"`
	Monitors  []struct {
		ID               string `json:"id"`
		Name             string `json:"name"`
		Kind             string `json:"kind"`
		Lifecycle        string `json:"lifecycle"`
		ObservationCount int    `json:"observationCount"`
		GapCount         int    `json:"gapCount"`
		Incidents        []struct {
			ID                 string   `json:"id"`
			State              string   `json:"state"`
			Resolution         *string  `json:"resolution"`
			NotificationStates []string `json:"notificationStates"`
		} `json:"incidents"`
	} `json:"monitors"`
	Applications []struct {
		ID        string   `json:"id"`
		Name      string   `json:"name"`
		MarkerIDs []string `json:"markerIds"`
	} `json:"applications"`
}

func local(t *testing.T) (*localdynamo.Client, string) {
	t.Helper()
	c, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unreachable:", e)
	}
	c.Close()
	client := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local")
	table := "statusforge_test_upgrade_" + rand.Text()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.DynamoDB().
			DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	})
	return client, table
}

// Fixtures remain immutable. In scratch copies, move legacy short-lived OBS/GAP/WORK/RUN
// TTL to 2100 so DynamoDB Local's real clock cannot physically delete source evidence.
// Readers use the fixture's fixed export timestamp, not the test runner's wall clock.
func stableFixture(t *testing.T, path string) (string, time.Time) {
	t.Helper()
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	lines := bytes.Split(bytes.TrimSpace(raw), []byte{'\n'})
	var h dataexport.Header
	if json.Unmarshal(lines[0], &h) != nil {
		t.Fatal("fixture header invalid")
	}
	fixed, e := time.Parse(time.RFC3339Nano, h.StartedAt)
	if e != nil {
		t.Fatal(e)
	}
	sum := sha256.New()
	var out bytes.Buffer
	out.Write(lines[0])
	out.WriteByte('\n')
	for _, line := range lines[1 : len(lines)-1] {
		var item map[string]any
		if json.Unmarshal(line, &item) != nil {
			t.Fatal("invalid fixture item")
		}
		sk := item["SK"].(map[string]any)["S"].(string)
		if strings.HasPrefix(sk, "OBS#") || strings.HasPrefix(sk, "GAP#") ||
			strings.HasPrefix(sk, "WORK#") ||
			strings.HasPrefix(sk, "RUN#") {
			if _, ok := item["expiresAt"]; ok {
				item["expiresAt"] = map[string]string{"N": "4102444800"}
			}
		}
		encoded, e := json.Marshal(item)
		if e != nil {
			t.Fatal(e)
		}
		out.Write(encoded)
		out.WriteByte('\n')
		_, _ = sum.Write(append(encoded, '\n'))
	}
	var trailer dataexport.Trailer
	if json.Unmarshal(lines[len(lines)-1], &trailer) != nil {
		t.Fatal("fixture trailer invalid")
	}
	trailer.SHA256 = hex.EncodeToString(sum.Sum(nil))
	encoded, _ := json.Marshal(trailer)
	out.Write(encoded)
	out.WriteByte('\n')
	tmp := filepath.Join(t.TempDir(), "stable.jsonl")
	if e = os.WriteFile(tmp, out.Bytes(), 0o600); e != nil {
		t.Fatal(e)
	}
	return tmp, fixed
}

func disableTTL(t *testing.T, db *dynamodb.Client, table string) {
	t.Helper()
	ctx := t.Context()
	for range 30 {
		out, e := db.DescribeTimeToLive(
			ctx,
			&dynamodb.DescribeTimeToLiveInput{TableName: aws.String(table)},
		)
		if e != nil {
			t.Fatal(e)
		}
		if out.TimeToLiveDescription.TimeToLiveStatus == types.TimeToLiveStatusEnabled {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, e := db.UpdateTimeToLive(
		ctx,
		&dynamodb.UpdateTimeToLiveInput{
			TableName: aws.String(table),
			TimeToLiveSpecification: &types.TimeToLiveSpecification{
				AttributeName: aws.String("expiresAt"),
				Enabled:       aws.Bool(false),
			},
		},
	)
	if e != nil {
		t.Fatal(e)
	}
}

func item(t *testing.T, db *dynamodb.Client, table, pk, sk string) map[string]types.AttributeValue {
	t.Helper()
	out, e := db.GetItem(
		t.Context(),
		&dynamodb.GetItemInput{
			TableName: aws.String(table),
			Key: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: pk},
				"SK": &types.AttributeValueMemberS{Value: sk},
			},
			ConsistentRead: aws.Bool(true),
		},
	)
	if e != nil {
		t.Fatal(e)
	}
	return out.Item
}

func TestAcceptedMilestoneUpgrade(t *testing.T) {
	for n := 1; n <= 5; n++ {
		t.Run(fmt.Sprintf("M%d", n), func(t *testing.T) {
			client, table := local(t)
			source := fmt.Sprintf("testdata/m%d.jsonl", n)
			copyPath, fixed := stableFixture(t, source)
			raw, e := os.ReadFile(fmt.Sprintf("testdata/m%d.facts.json", n))
			if e != nil {
				t.Fatal(e)
			}
			var expected facts
			if e = json.Unmarshal(raw, &expected); e != nil {
				t.Fatal(e)
			}
			s := store.New(client.DynamoDB(), table, time.Second, func() time.Time { return fixed })
			_, e = dataexport.Import(t.Context(), client.DynamoDB(), table, copyPath, s.Initialize)
			if e != nil {
				t.Fatal(e)
			}
			disableTTL(t, client.DynamoDB(), table)
			// Import contains no marker; a fresh process identifies unmarked milestone data.
			s = store.New(client.DynamoDB(), table, time.Second, func() time.Time { return fixed })
			if e = s.Initialize(t.Context()); e != nil {
				t.Fatal(e)
			}
			state, e := s.System(t.Context())
			if e != nil || state.DataFormat != 6 || state.UpgradedFrom == nil ||
				*state.UpgradedFrom != "unmarked" {
				t.Fatalf("marker: %+v %v", state, e)
			}
			for range 100 {
				if e = s.RunHousekeeping(t.Context()); e != nil {
					t.Fatal(e)
				}
				state, e = s.System(t.Context())
				if e != nil {
					t.Fatal(e)
				}
				if state.Backfill.State == "done" && state.Housekeeping.PendingRetentionJobs == 0 {
					break
				}
			}
			if state.Backfill.State != "done" || state.Housekeeping.PendingRetentionJobs != 0 {
				t.Fatalf("unfinished upgrade: %+v", state)
			}
			scheduled := false
			for _, want := range expected.Monitors {
				m, e := s.Get(t.Context(), want.ID)
				kind := m.Kind
				if kind == "" {
					kind = "http"
				}
				if e != nil || m.Name != want.Name || kind != want.Kind ||
					m.Lifecycle != want.Lifecycle {
					t.Fatalf("monitor %s: %+v %v", want.ID, m, e)
				}
				obs, e := s.Observations(t.Context(), want.ID, 200)
				if e != nil || len(obs) != want.ObservationCount {
					t.Fatalf(
						"observations %s: got %d want %d err %v",
						want.ID,
						len(obs),
						want.ObservationCount,
						e,
					)
				}
				if n >= 2 {
					gaps, gapErr := s.Gaps(t.Context(), want.ID, 200)
					if gapErr != nil || len(gaps) != want.GapCount {
						t.Fatalf(
							"gaps %s: got %d want %d err %v",
							want.ID,
							len(gaps),
							want.GapCount,
							gapErr,
						)
					}
				}
				for _, observation := range obs {
					if observation.InitiatedBy == "scheduled" && observation.Counted {
						scheduled = true
					}
				}
				if n >= 4 && want.Kind == "heartbeat" &&
					len(
						item(
							t,
							client.DynamoDB(),
							table,
							"MON#"+want.ID,
							fmt.Sprintf("RUN#m%d-fixture-run", n),
						),
					) == 0 {
					t.Fatal("heartbeat fixture missing run duplicate guard")
				}
				for _, inc := range want.Incidents {
					got, _, _, notes, e := s.IncidentDetail(t.Context(), want.ID, inc.ID, fixed)
					if e != nil || got.State != inc.State ||
						!reflect.DeepEqual(got.Resolution, inc.Resolution) {
						t.Fatalf("incident %s: %+v %v", inc.ID, got, e)
					}
					states := make([]string, 0, len(notes))
					for _, note := range notes {
						states = append(states, note.State)
					}
					sort.Strings(states)
					if !reflect.DeepEqual(states, inc.NotificationStates) {
						t.Fatalf(
							"notification states %s: %v want %v",
							inc.ID,
							states,
							inc.NotificationStates,
						)
					}
					persisted := item(t, client.DynamoDB(), table, "MON#"+want.ID, "INC#"+inc.ID)
					if inc.State == "open" {
						if _, ok := persisted["expiresAt"]; ok {
							t.Fatal("open incident expired")
						}
					} else {
						when, _ := time.Parse(time.RFC3339Nano, *got.ResolvedAt)
						if expiry, ok := persisted["expiresAt"].(*types.AttributeValueMemberN); !ok || expiry.Value != fmt.Sprint(retention.History(when)) {
							t.Fatalf("resolved incident expiry %v", persisted["expiresAt"])
						}
					}
				}
			}
			if n == 2 && !scheduled {
				t.Fatal("M2 fixture has no counted scheduled observation")
			}
			for _, want := range expected.Applications {
				a, e := s.Application(t.Context(), want.ID)
				if e != nil || a.Name != want.Name {
					t.Fatalf("application: %+v %v", a, e)
				}
				markers, e := s.Deployments(t.Context(), want.ID, 200)
				if e != nil {
					t.Fatal(e)
				}
				ids := make([]string, 0, len(markers))
				for _, m := range markers {
					ids = append(ids, m.ID)
				}
				sort.Strings(ids)
				if !reflect.DeepEqual(ids, want.MarkerIDs) {
					t.Fatalf("markers %v want %v", ids, want.MarkerIDs)
				}
			}
		})
	}
}

func TestNewerFormatRefused(t *testing.T) {
	client, table := local(t)
	s := store.New(client.DynamoDB(), table, time.Second, time.Now)
	if e := s.Initialize(t.Context()); e != nil {
		t.Fatal(e)
	}
	_, e := client.DynamoDB().
		UpdateItem(t.Context(), &dynamodb.UpdateItemInput{TableName: aws.String(table), Key: map[string]types.AttributeValue{"PK": &types.AttributeValueMemberS{Value: "SYSTEM"}, "SK": &types.AttributeValueMemberS{Value: "FORMAT"}}, UpdateExpression: aws.String("SET dataFormat = :version"), ExpressionAttributeValues: map[string]types.AttributeValue{":version": &types.AttributeValueMemberN{Value: "7"}}})
	if e != nil {
		t.Fatal(e)
	}
	newProcess := store.New(client.DynamoDB(), table, time.Second, time.Now)
	e = newProcess.Initialize(t.Context())
	if !errors.Is(e, store.ErrNewerFormat) {
		t.Fatalf("format 7 not refused: %v", e)
	}
}

func TestBackfillResumesFromCursor(t *testing.T) {
	client, table := local(t)
	s := store.New(client.DynamoDB(), table, time.Second, time.Now)
	if e := s.Initialize(t.Context()); e != nil {
		t.Fatal(e)
	}
	db := client.DynamoDB()
	markerKey := map[string]types.AttributeValue{
		"PK": &types.AttributeValueMemberS{Value: "SYSTEM"},
		"SK": &types.AttributeValueMemberS{Value: "FORMAT"},
	}
	at := "2026-09-28T00:00:00Z"
	for _, sk := range []string{"DEP#a", "DEP#b"} {
		_, e := db.PutItem(t.Context(), &dynamodb.PutItemInput{
			TableName: aws.String(table),
			Item: map[string]types.AttributeValue{
				"PK":         &types.AttributeValueMemberS{Value: "APP#resume"},
				"SK":         &types.AttributeValueMemberS{Value: sk},
				"reportedAt": &types.AttributeValueMemberS{Value: at},
			},
		})
		if e != nil {
			t.Fatal(e)
		}
	}
	_, e := db.UpdateItem(t.Context(), &dynamodb.UpdateItemInput{
		TableName: aws.String(table), Key: markerKey,
		UpdateExpression:         aws.String("SET backfill.#state = :state, #cursor = :cursor"),
		ExpressionAttributeNames: map[string]string{"#state": "state", "#cursor": "cursor"},
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":state": &types.AttributeValueMemberS{Value: "running"},
			":cursor": &types.AttributeValueMemberM{Value: map[string]types.AttributeValue{
				"PK": &types.AttributeValueMemberS{Value: "APP#resume"},
				"SK": &types.AttributeValueMemberS{Value: "DEP#a"},
			}},
		},
	})
	if e != nil {
		t.Fatal(e)
	}
	restarted := store.New(client.DynamoDB(), table, time.Second, time.Now)
	if e := restarted.Initialize(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, e := restarted.BackfillPage(t.Context()); e != nil {
		t.Fatal(e)
	}
	if _, ok := item(t, db, table, "APP#resume", "DEP#a")["expiresAt"]; ok {
		t.Fatal("cursor restarted at beginning")
	}
	if _, ok := item(t, db, table, "APP#resume", "DEP#b")["expiresAt"]; !ok {
		t.Fatal("unprocessed item not stamped after restart")
	}
}
