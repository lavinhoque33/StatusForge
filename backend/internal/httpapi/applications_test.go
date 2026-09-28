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

func TestApplicationIngestHTTP(t *testing.T) {
	conn, e := net.DialTimeout("tcp", "127.0.0.1:8000", 200*time.Millisecond)
	if e != nil {
		t.Skip("DynamoDB Local unavailable:", e)
	}
	conn.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	client := localdynamo.New("http://127.0.0.1:8000", "127.0.0.1", "local", "local", "local")
	table := "statusforge_app_http_" + rand.Text()
	s := store.New(client, table, time.Second, func() time.Time { return now })
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = client.DynamoDB().
			DeleteTable(ctx, &dynamodb.DeleteTableInput{TableName: aws.String(table)})
	})
	policy, _ := targetpolicy.Parse("127.0.0.1:8090")
	router := NewMonitorRouter(
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
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w.Code, w.Body.String(), w.Header()
	}
	status, body, _ := call("POST", "/api/applications", "", `{"name":"App"}`)
	if status != 201 {
		t.Fatalf("create: %d %s", status, body)
	}
	var app struct {
		ID          string `json:"id"`
		IssuedToken string `json:"issuedToken"`
	}
	if json.Unmarshal([]byte(body), &app) != nil || app.ID == "" ||
		!strings.HasPrefix(app.IssuedToken, "sfd_") {
		t.Fatal("invalid app token")
	}
	if code, response, _ := call("PUT", "/api/monitors/missing/application", "", `{"applicationId":"`+app.ID+`"}`); code != 404 ||
		!strings.Contains(response, "monitor_not_found") {
		t.Fatalf("unknown monitor membership: %d %s", code, response)
	}
	path := "/ingest/applications/" + app.ID + "/deployments"
	_, unauth, _ := call("POST", path, "", `{"version":"v1"}`)
	for _, tc := range []struct{ path, token string }{{path, "wrong"}, {"/ingest/applications/missing/deployments", app.IssuedToken}} {
		code, text, _ := call("POST", tc.path, tc.token, `{"version":"v1"}`)
		if code != 401 || text != unauth {
			t.Fatalf("auth: %d %q vs %q", code, text, unauth)
		}
	}
	if code, _, _ := call("POST", path, app.IssuedToken, strings.Repeat("x", 4097)); code != 413 {
		t.Fatalf("oversize: %d", code)
	}
	for _, payload := range []string{`{"version":"v1","link":"file:///bad"}`, `{"version":"v1","unknown":true}`} {
		if code, _, _ := call("POST", path, app.IssuedToken, payload); code != 400 {
			t.Fatalf("validation: %d", code)
		}
	}
	items, e := s.Deployments(t.Context(), app.ID, 50)
	if e != nil || len(items) != 0 {
		t.Fatalf("invalid writes: %v %+v", e, items)
	}
	for range 1 {
		code, text, _ := call(
			"POST",
			path,
			app.IssuedToken,
			`{"version":"v1","deploymentId":"deploy-1"}`,
		)
		if code != 202 {
			t.Fatalf("accept: %d %s", code, text)
		}
	}
	if code, _, _ := call("POST", path, app.IssuedToken, `{"version":"v1","deploymentId":"deploy-1"}`); code != 200 {
		t.Fatalf("duplicate: %d", code)
	}
	now = now.Add(10 * time.Second)
	for range 5 {
		if code, _, _ := call("POST", path, app.IssuedToken, `{"version":"v2"}`); code != 202 {
			t.Fatalf("burst: %d", code)
		}
	}
	if code, _, header := call("POST", path, app.IssuedToken, `{"version":"v3"}`); code != 429 ||
		header.Get("Retry-After") != "1" {
		t.Fatalf("rate limit: %d", code)
	}
	items, e = s.Deployments(t.Context(), app.ID, 50)
	if e != nil || len(items) != 6 {
		t.Fatalf("writes after duplicates and rejects: %v %d", e, len(items))
	}
	if code, _, _ := call("POST", "/api/applications/"+app.ID+"/archive", "", ""); code != 200 {
		t.Fatalf("archive: %d", code)
	}
	if code, _, _ := call("POST", path, app.IssuedToken, `{"version":"v4"}`); code != 401 &&
		code != 429 {
		t.Fatalf("revoked token: %d", code)
	}
}
