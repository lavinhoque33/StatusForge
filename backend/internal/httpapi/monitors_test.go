package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

type fakeMonitorStore struct {
	MonitorStore
	mu           sync.Mutex
	m            monitor.Monitor
	observations []monitor.Observation
}

func (f *fakeMonitorStore) Create(_ context.Context, m monitor.Monitor) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m = m
	return nil
}

func (f *fakeMonitorStore) Get(_ context.Context, id string) (monitor.Monitor, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m, nil
}

func (f *fakeMonitorStore) PutObservation(_ context.Context, o monitor.Observation) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.observations = append(f.observations, o)
	return nil
}

func (f *fakeMonitorStore) Observations(
	_ context.Context,
	_ string,
	_ int,
) ([]monitor.Observation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]monitor.Observation{}, f.observations...), nil
}

type waitingRunner struct {
	entered chan struct{}
	release chan struct{}
}

func (r *waitingRunner) Run(_ context.Context, m monitor.Monitor) monitor.Observation {
	close(r.entered)
	<-r.release
	return monitor.Observation{ID: "sample", MonitorID: m.ID, Outcome: "healthy", Reason: "ok"}
}

func TestMonitorAPIValidationAndSingleFlight(t *testing.T) {
	p, _ := targetpolicy.Parse("127.0.0.1:8090")
	f := &fakeMonitorStore{}
	runner := &waitingRunner{entered: make(chan struct{}), release: make(chan struct{})}
	server := httptest.NewServer(
		NewMonitorRouter(
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			nil,
			time.Second,
			time.Now,
			f,
			runner,
			p,
		),
	)
	defer server.Close()
	client := server.Client()
	request := func(method, path, body string) *http.Response {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp, err := client.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for _, tc := range []struct {
		body, code string
		status     int
	}{{`{"name":"a","check":{"url":"https://127.0.0.1:8090/"}}`, "scheme_not_allowed", 400}, {`{"name":"a","check":{"url":"http://127.0.0.1:8090/","unexpected":true}}`, "unknown_field", 400}, {`{"name":"a","check":{"url":"http://127.0.0.1:8090/"},"extra":true}`, "unknown_field", 400}, {`{`, "invalid_json", 400}} {
		resp := request("POST", "/api/monitors", tc.body)
		var payload struct {
			Error  string                        `json:"error"`
			Fields map[string]monitor.FieldError `json:"fields"`
		}
		json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if resp.StatusCode != tc.status {
			t.Fatalf("%q status=%d", tc.body, resp.StatusCode)
		}
		if tc.code != payload.Error && payload.Fields["check.url"].Code != tc.code &&
			payload.Fields["check.unexpected"].Code != tc.code &&
			payload.Fields["extra"].Code != tc.code {
			t.Fatalf("%q response=%+v", tc.body, payload)
		}
	}
	resp := request(
		"POST",
		"/api/monitors",
		`{"name":"a","check":{"url":"http://127.0.0.1:8090/"}}`,
	)
	if resp.StatusCode != 201 || !strings.HasPrefix(resp.Header.Get("Location"), "/api/monitors/") {
		t.Fatalf("create status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	var created monitor.Monitor
	json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	first := make(chan *http.Response, 1)
	go func() { first <- request("POST", "/api/monitors/"+created.ID+"/checks", "") }()
	<-runner.entered
	second := request("POST", "/api/monitors/"+created.ID+"/checks", "")
	if second.StatusCode != 409 {
		t.Fatalf("parallel check status=%d", second.StatusCode)
	}
	second.Body.Close()
	close(runner.release)
	result := <-first
	if result.StatusCode != 201 {
		t.Fatalf("completed check status=%d", result.StatusCode)
	}
	result.Body.Close()
	f.mu.Lock()
	count := len(f.observations)
	f.mu.Unlock()
	if count != 1 {
		t.Fatalf("stored %d observations", count)
	}
}
