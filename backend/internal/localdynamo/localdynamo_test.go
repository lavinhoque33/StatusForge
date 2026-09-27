package localdynamo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

func TestPingUsesExplicitLocalEndpoint(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "ambient-secret")
	var requestBody string
	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		requestBody = string(body)
		authorization = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		_, _ = w.Write([]byte(`{"TableNames":[]}`))
	}))
	defer server.Close()
	client := New(server.URL, "127.0.0.1", "local", "local-key", "local-secret")
	if err := client.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	if requestBody != `{"Limit":1}` {
		t.Errorf("ListTables request = %s", requestBody)
	}
	if !strings.Contains(authorization, "Credential=local-key/") ||
		strings.Contains(authorization, "ambient-secret") {
		t.Errorf("did not use explicit static credentials: %s", authorization)
	}
}

func TestPingErrorDoesNotExposeEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := server.URL
	server.Close()
	client := New(endpoint, "127.0.0.1", "local", "local", "local")
	err := client.Ping(context.Background())
	if err == nil {
		t.Fatal("expected refused connection")
	}
	if strings.Contains(err.Error(), endpoint) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("endpoint leaked in loggable error: %v", err)
	}
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Errorf("lost underlying cause: %v", err)
	}
}
