package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/summary"
)

type dailyFake struct {
	*fakeMonitorStore
	failed bool
}

func (f *dailyFake) Summary(
	_ context.Context,
	m monitor.Monitor,
	w string,
	now time.Time,
) (summary.Result, error) {
	if f.failed {
		return summary.Result{}, store.ErrUnavailable
	}
	return summary.Compute(
		summary.Input{MonitorID: m.ID, Window: w, To: now, Lifecycle: m.Lifecycle},
	), nil
}

func (f *dailyFake) HeartbeatSummary(
	_ context.Context, m monitor.Monitor, w string, now time.Time,
) (summary.HeartbeatResult, error) {
	if f.failed {
		return summary.HeartbeatResult{}, store.ErrUnavailable
	}
	return summary.ComputeHeartbeat(
		summary.Input{MonitorID: m.ID, Window: w, To: now, Lifecycle: m.Lifecycle},
	), nil
}

func (f *dailyFake) Overview(_ context.Context, now time.Time) (store.OverviewResult, error) {
	if f.failed {
		return store.OverviewResult{}, store.ErrUnavailable
	}
	return store.OverviewResult{EvaluatedAt: monitor.Stamp(now)}, nil
}

func TestDailyAPIValidationAndErrors(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	m := monitor.New("sample", monitor.Check{}, now)
	db := &dailyFake{fakeMonitorStore: &fakeMonitorStore{m: m}}
	h := NewMonitorRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		time.Second,
		func() time.Time { return now },
		db,
		nil,
		nil,
	)
	call := func(path string) (int, map[string]any) {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "localhost:8080"
		h.ServeHTTP(rec, req)
		var body map[string]any
		if e := json.Unmarshal(rec.Body.Bytes(), &body); e != nil {
			t.Fatal(e)
		}
		return rec.Code, body
	}
	if code, body := call("/api/monitors/" + m.ID + "/summary?window=1h"); code != 400 ||
		body["error"] != "validation_failed" ||
		body["fields"].(map[string]any)["window"].(map[string]any)["code"] != "invalid_value" {
		t.Fatalf("invalid window: %d %+v", code, body)
	}
	if code, body := call("/api/monitors/missing/summary"); code != 404 ||
		body["error"] != "monitor_not_found" {
		t.Fatalf("unknown: %d %+v", code, body)
	}
	db.m.Kind = "heartbeat"
	if code, body := call("/api/monitors/" + m.ID + "/summary"); code != 200 ||
		body["kind"] != "heartbeat" || body["latency"] != nil {
		t.Fatalf("heartbeat: %d %+v", code, body)
	}
	db.m.Kind = "http"
	db.failed = true
	for _, path := range []string{"/api/overview", "/api/monitors/" + m.ID + "/summary"} {
		if code, body := call(path); code != 503 || body["error"] != "store_unavailable" {
			t.Fatalf("failure %s: %d %+v", path, code, body)
		}
	}
	db.failed = false
	if code, body := call("/api/monitors/" + m.ID + "/summary?window=7d"); code != 200 ||
		body["bucketSeconds"] != float64(21600) ||
		body["window"] != "7d" {
		t.Fatalf("seven days: %d %+v", code, body)
	}
}

type coverageFake struct {
	*fakeMonitorStore
	asked time.Time
}

func (f *coverageFake) SchedulerCoverage(now time.Time) store.SchedulerCoverage {
	f.asked = now
	return store.SchedulerCoverage{State: "disabled", WindowMinutes: 5, Workers: 4}
}

func TestSchedulerCoverageEndpoint(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	get := func(s MonitorStore) map[string]any {
		t.Helper()
		h := NewMonitorRouter(
			slog.New(slog.NewTextHandler(io.Discard, nil)),
			nil,
			time.Second,
			func() time.Time { return now },
			s,
			nil,
			nil,
		)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/system/scheduler", nil)
		req.Host = "localhost:8080"
		h.ServeHTTP(rec, req)
		var body map[string]any
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("scheduler endpoint %d %s", rec.Code, rec.Body)
		}
		return body
	}
	f := &coverageFake{fakeMonitorStore: &fakeMonitorStore{}}
	if body := get(f); body["state"] != "disabled" || body["workers"] != float64(4) ||
		!f.asked.Equal(now) {
		t.Fatalf("configured coverage %+v asked at %s", body, f.asked)
	}
	// A process without a scheduler source has no evidence: unknown, never ok.
	if body := get(&fakeMonitorStore{}); body["state"] != "unknown" {
		t.Fatalf("default coverage %+v", body)
	}
}
