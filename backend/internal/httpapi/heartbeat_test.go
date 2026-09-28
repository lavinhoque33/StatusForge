package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

func TestHeartbeatIngestHTTP(t *testing.T) {
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	client := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local")
	table := "statusforge_heartbeat_http_" + rand.Text()
	s := store.New(client, table, time.Second, func() time.Time { return now })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, e := client.DynamoDB().DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)}); e != nil {
			t.Errorf("delete isolated table: %v", e)
		}
	})
	policy, _ := targetpolicy.Parse("127.0.0.1:8090")
	handler := NewMonitorRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		time.Second,
		func() time.Time { return now },
		s,
		checker.New(policy, func() time.Time { return now }),
		policy,
		10,
	)
	call := func(method, path, token, body string) (int, string, http.Header) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "localhost:8080"
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code, w.Body.String(), w.Header()
	}
	code, created, _ := call(
		"POST",
		"/api/monitors",
		"",
		`{"kind":"heartbeat","name":"job","heartbeat":{"intervalSeconds":15,"graceSeconds":5}}`,
	)
	if code != 201 {
		t.Fatalf("create status: %d", code)
	}
	var response struct {
		ID            string `json:"id"`
		IssuedToken   string `json:"issuedToken"`
		ConfigVersion int    `json:"configVersion"`
		Heartbeat     struct {
			IntervalSeconds int     `json:"intervalSeconds"`
			LastReportAt    *string `json:"lastReportAt"`
		} `json:"heartbeat"`
		Expectation struct {
			StaleAt string `json:"staleAt"`
		} `json:"expectation"`
		Check           *json.RawMessage `json:"check"`
		IntervalSeconds *int             `json:"intervalSeconds"`
	}
	if json.Unmarshal([]byte(created), &response) != nil || response.IssuedToken == "" {
		t.Fatal("missing token")
	}
	if response.ConfigVersion != 1 || response.Heartbeat.IntervalSeconds != 15 ||
		response.Heartbeat.LastReportAt != nil ||
		response.Expectation.StaleAt == "" ||
		response.Check != nil ||
		response.IntervalSeconds != nil {
		t.Fatal("incorrect monitor schedule response")
	}
	path := "/ingest/heartbeats/" + response.ID
	_, unauth, _ := call("POST", path, "", "{}")
	for _, tt := range []struct{ id, token string }{{response.ID, "wrong"}, {"nonexistent", response.IssuedToken}} {
		status, body, _ := call("POST", "/ingest/heartbeats/"+tt.id, tt.token, "{}")
		if status != 401 || body != unauth {
			t.Errorf("uniform 401: %d %q != %q", status, body, unauth)
		}
	}
	if code, _, _ := call("POST", path, response.IssuedToken, strings.Repeat("x", 4097)); code != 413 {
		t.Errorf("oversize: %d", code)
	}
	if code, body, _ := call("POST", path, response.IssuedToken, `{"surprise":1}`); code != 400 ||
		!strings.Contains(body, "unknown_field") {
		t.Errorf("unknown field: %d %s", code, body)
	}
	for range 4 {
		if code, body, _ := call("POST", path, response.IssuedToken, `{}`); code != 202 {
			t.Fatalf("report: %d %s", code, body)
		}
	}
	if code, _, header := call("POST", path, response.IssuedToken, `{}`); code != 429 ||
		header.Get("Retry-After") != "1" {
		t.Errorf("throttle: %d %s", code, header.Get("Retry-After"))
	}
	now = now.Add(time.Second)
	if code, body, _ := call("POST", path, response.IssuedToken, `{}`); code != 202 {
		t.Errorf("recovery: %d %s", code, body)
	}
	if code, body, _ := call("POST", "/api/monitors/"+response.ID+"/checks", "", `{}`); code != 409 ||
		!strings.Contains(body, "not_supported") {
		t.Errorf("manual check: %d %s", code, body)
	}
	if code, _, _ := call("DELETE", "/api/monitors/"+response.ID+"/heartbeat/token", "", ``); code != 204 {
		t.Fatalf("revoke: %d", code)
	}
	if code, body, _ := call("POST", path, response.IssuedToken, `{}`); code != 401 ||
		body != unauth {
		t.Errorf("revoked uniform 401: %d %s", code, body)
	}
	code, rotated, _ := call("POST", "/api/monitors/"+response.ID+"/heartbeat/token", "", ``)
	if code != 201 {
		t.Fatalf("rotate after revoke: %d", code)
	}
	var next struct {
		Token string `json:"token"`
	}
	if json.Unmarshal([]byte(rotated), &next) != nil || next.Token == "" {
		t.Fatal("missing replacement token")
	}
	if code, body, _ := call("POST", "/api/monitors/"+response.ID+"/lifecycle", "", `{"action":"archive"}`); code != 200 {
		t.Fatalf("archive: %d %s", code, body)
	}
	now = now.Add(time.Second)
	if code, body, _ := call("POST", path, next.Token, `{}`); code != 410 {
		t.Errorf("archived: %d %s", code, body)
	}
}
