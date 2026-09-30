package cloudwork_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"

	"github.com/lavinhoque33/statusforge/backend/internal/cloudtargetpolicy"
	"github.com/lavinhoque33/statusforge/backend/internal/cloudwork"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// clock is the drills' injected clock.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

func (c *clock) Advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// fakeQueue records sent bodies and fails whole batches on demand.
type fakeQueue struct {
	mu    sync.Mutex
	sent  []string
	fails int
}

func (q *fakeQueue) SendBatch(_ context.Context, bodies []string) ([]int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(bodies) > cloudwork.BatchSize {
		return nil, fmt.Errorf("batch of %d exceeds SendMessageBatch's limit", len(bodies))
	}
	if q.fails > 0 {
		q.fails--
		return nil, errors.New("send failed by test")
	}
	q.sent = append(q.sent, bodies...)
	return nil, nil
}

func (q *fakeQueue) failNext(n int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.fails = n
}

// take returns and forgets the sent messages.
func (q *fakeQueue) take() []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	sent := q.sent
	q.sent = nil
	return sent
}

// scriptedChecker returns scripted outcomes without any network, and can run
// a hook mid-check (between claim and record).
type scriptedChecker struct {
	mu       sync.Mutex
	outcomes []string
	calls    int
	during   func()
}

func (c *scriptedChecker) Run(_ context.Context, m monitor.Monitor) monitor.Observation {
	c.mu.Lock()
	outcome := "healthy"
	if len(c.outcomes) > 0 {
		outcome, c.outcomes = c.outcomes[0], c.outcomes[1:]
	}
	c.calls++
	during := c.during
	c.during = nil
	c.mu.Unlock()
	if during != nil {
		during()
	}
	status, reason := 200, "ok"
	if outcome == "failing" {
		status, reason = 503, "wrong_status"
	}
	started := m.Lease.StartedAt
	return monitor.Observation{
		Kind: "http_check", ID: rand.Text(), MonitorID: m.ID, ConfigVersion: m.ConfigVersion,
		Request: m.Check, StartedAt: started, CompletedAt: started, Outcome: outcome,
		Reason: reason, ObservedStatus: &status,
	}
}

func (c *scriptedChecker) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// logBuffer is a goroutine-safe JSON log sink.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

// lines returns the logged records with message msg, oldest first.
func (l *logBuffer) lines(msg string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var result []map[string]any
	for line := range strings.SplitSeq(l.buf.String(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["msg"] == msg {
			result = append(result, record)
		}
	}
	return result
}

type drill struct {
	t       *testing.T
	db      *dynamodb.Client
	table   string
	clock   *clock
	queue   *fakeQueue
	checker *scriptedChecker
	policy  *cloudtargetpolicy.Policy
	logs    *logBuffer
	logger  *slog.Logger
	// Monitors is the declared STATUSFORGE_CLOUD_MONITORS value.
	monitors string
}

const drillOrigin = "https://status.example.com"

// newDrill creates a scratch table with the M7 prefix on DynamoDB Local.
func newDrill(t *testing.T) *drill {
	t.Helper()
	conn, err := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if err != nil {
		t.Skip("DynamoDB Local on 127.0.0.1:8000 unreachable:", err)
	}
	conn.Close()
	db := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local").
		DynamoDB()
	return newDrillOn(t, db)
}

func newDrillOn(t *testing.T, db *dynamodb.Client) *drill {
	t.Helper()
	policy, err := cloudtargetpolicy.Parse(drillOrigin, "127.0.0.1:9001")
	if err != nil {
		t.Fatal(err)
	}
	// The scripted checker never dials; the resolver only serves save-time checks.
	policy.SetResolver(func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	})
	logs := &logBuffer{}
	d := &drill{
		t:        t,
		db:       db,
		table:    "statusforge_m7t_" + strings.ToLower(rand.Text()[:12]),
		clock:    &clock{},
		queue:    &fakeQueue{},
		checker:  &scriptedChecker{},
		policy:   policy,
		logs:     logs,
		logger:   slog.New(slog.NewJSONHandler(logs, nil)),
		monitors: "[]",
	}
	// The infrastructure owns the hosted table; locally the test creates it.
	if err := store.New(db, d.table, time.Second, time.Now).Initialize(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(d.table)}); err != nil {
			t.Logf("could not remove scratch table %s: %v", d.table, err)
		}
	})
	return d
}

// store is a fresh hosted store, as a cold Lambda container builds it.
func (d *drill) store() *store.Store {
	d.t.Helper()
	s := store.New(d.db, d.table, time.Second, d.clock.Now)
	s.SetNotifications(store.NotificationsNone)
	if err := s.Validate(d.t.Context()); err != nil {
		d.t.Fatal(err)
	}
	return s
}

func declare(key string, interval, deadlineMs int) string {
	return fmt.Sprintf(
		`{"key":%q,"name":"Drill %s","url":%q,"intervalSeconds":%d,"expectedStatus":200,"deadlineMs":%d}`,
		key,
		key,
		drillOrigin+"/"+key,
		interval,
		deadlineMs,
	)
}

func (d *drill) declare(entries ...string) { d.monitors = "[" + strings.Join(entries, ",") + "]" }

// start aligns the clock one second after key's grid slot, so the next slot is
// a full interval away and every drill is deterministic.
func (d *drill) start(key string, interval int) time.Time {
	id := cloudwork.DeclaredID(key, 0)
	base := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	at := monitor.GridSlot(id, interval, base).Add(interval2(interval)).Add(time.Second)
	d.clock.Set(at)
	return at
}

func interval2(seconds int) time.Duration { return time.Duration(seconds) * time.Second }

// pass runs one planner pass on s at the clock's time.
func (d *drill) pass(s *store.Store) cloudwork.PassReport {
	d.t.Helper()
	report, err := d.passErr(s)
	if err != nil {
		d.t.Fatalf("planner pass: %v", err)
	}
	return report
}

func (d *drill) passErr(s *store.Store) (cloudwork.PassReport, error) {
	return cloudwork.Planner{
		Store: s, Queue: d.queue, Policy: d.policy, Monitors: d.monitors,
		Now: d.clock.Now, Logger: d.logger,
	}.Pass(d.t.Context())
}

func (d *drill) worker(s *store.Store) cloudwork.Worker {
	return cloudwork.Worker{Store: s, Checker: d.checker, Now: d.clock.Now, Logger: d.logger}
}

func event(bodies ...string) events.SQSEvent {
	var e events.SQSEvent
	for i, body := range bodies {
		e.Records = append(e.Records, events.SQSMessage{
			MessageId: fmt.Sprintf("m%d", i), Body: body, EventSource: "aws:sqs",
		})
	}
	return e
}

// deliver hands bodies to a worker invocation with Lambda's 60 s timeout and
// returns the response and the batch's outcome counts.
func (d *drill) deliver(
	s *store.Store,
	bodies ...string,
) (events.SQSEventResponse, map[string]int) {
	d.t.Helper()
	ctx, cancel := context.WithTimeout(d.t.Context(), 60*time.Second)
	defer cancel()
	return d.deliverCtx(ctx, s, bodies...)
}

func (d *drill) deliverCtx(
	ctx context.Context,
	s *store.Store,
	bodies ...string,
) (events.SQSEventResponse, map[string]int) {
	d.t.Helper()
	before := len(d.logs.lines("worker batch"))
	response, err := d.worker(s).Handle(ctx, event(bodies...))
	if err != nil {
		d.t.Fatalf("worker returned an invocation error: %v", err)
	}
	lines := d.logs.lines("worker batch")
	if len(lines) != before+1 {
		d.t.Fatalf("expected one worker batch line, got %d", len(lines)-before)
	}
	counts := map[string]int{}
	for k, v := range lines[len(lines)-1] {
		if n, ok := v.(float64); ok {
			counts[k] = int(n)
		}
	}
	return response, counts
}

func failed(r events.SQSEventResponse) []string {
	ids := []string{}
	for _, f := range r.BatchItemFailures {
		ids = append(ids, f.ItemIdentifier)
	}
	return ids
}

func (d *drill) monitor(key string) monitor.Monitor {
	d.t.Helper()
	ms, err := d.store().List(d.t.Context())
	if err != nil {
		d.t.Fatal(err)
	}
	for _, m := range ms {
		if m.DeclaredKey == key && m.Lifecycle != "archived" {
			return m
		}
	}
	for _, m := range ms {
		if m.DeclaredKey == key {
			return m
		}
	}
	d.t.Fatalf("no monitor declared as %s", key)
	return monitor.Monitor{}
}

func (d *drill) observations(id string) []monitor.Observation {
	d.t.Helper()
	obs, err := d.store().Observations(d.t.Context(), id, 100)
	if err != nil {
		d.t.Fatal(err)
	}
	return obs
}

func (d *drill) gaps(id string) []monitor.Gap {
	d.t.Helper()
	gaps, err := d.store().Gaps(d.t.Context(), id, 100)
	if err != nil {
		d.t.Fatal(err)
	}
	return gaps
}

// works returns every work item of a monitor, oldest first.
func (d *drill) works(id string) []store.Work {
	d.t.Helper()
	items := d.query("MON#"+id, "WORK#")
	result := make([]store.Work, 0, len(items))
	for _, item := range items {
		var w store.Work
		if err := attributevalue.UnmarshalMap(item, &w); err != nil {
			d.t.Fatal(err)
		}
		result = append(result, w)
	}
	return result
}

func (d *drill) query(pk, prefix string) []map[string]types.AttributeValue {
	d.t.Helper()
	out, err := d.db.Query(d.t.Context(), &dynamodb.QueryInput{
		TableName:              aws.String(d.table),
		KeyConditionExpression: aws.String("PK = :pk AND begins_with(SK, :prefix)"),
		ExpressionAttributeValues: map[string]types.AttributeValue{
			":pk":     &types.AttributeValueMemberS{Value: pk},
			":prefix": &types.AttributeValueMemberS{Value: prefix},
		},
		ConsistentRead: aws.Bool(true),
	})
	if err != nil {
		d.t.Fatal(err)
	}
	return out.Items
}

func str(item map[string]types.AttributeValue, name string) string {
	if v, ok := item[name].(*types.AttributeValueMemberS); ok {
		return v.Value
	}
	return ""
}

func message(id, dueAt string) string {
	return cloudwork.Message{MonitorID: id, DueAt: dueAt}.Encode()
}
