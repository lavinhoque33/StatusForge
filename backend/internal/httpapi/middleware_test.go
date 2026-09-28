package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func TestRequestLogger(t *testing.T) {
	const step = 250 * time.Millisecond

	tests := []struct {
		name       string
		method     string
		target     string
		headers    http.Header
		body       string
		handler    http.HandlerFunc
		wantStatus int
		wantLevel  string
		wantBody   string
		forbidden  []string
	}{
		{
			name:   "success is informational",
			method: http.MethodGet,
			target: "/api/health/live?verbose=true&token=query-secret",
			headers: http.Header{
				"Authorization": {"Bearer header-secret"},
			},
			body: `{"password":"body-secret"}`,
			handler: func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusOK, liveResponse{Status: statusOK})
			},
			wantStatus: http.StatusOK,
			wantLevel:  "INFO",
			wantBody:   `{"status":"ok"}`,
			forbidden:  []string{"?", "verbose", "query-secret", "header-secret", "body-secret"},
		},
		{
			name:       "client error is a warning",
			method:     http.MethodGet,
			target:     "/api/unknown",
			handler:    jsonErrorHandler(http.StatusNotFound, errorNotFound),
			wantStatus: http.StatusNotFound,
			wantLevel:  "WARN",
			wantBody:   `{"error":"not_found"}`,
		},
		{
			name:   "server error is an error",
			method: http.MethodGet,
			target: "/api/health/ready",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				writeJSON(w, http.StatusServiceUnavailable, healthResponse{
					Status:    statusDegraded,
					CheckedAt: testCheckedAt,
					Dependencies: map[string]dependencyHealth{
						dependencyReportKey: {Status: dependencyUnavailable, Reason: reasonTimeout},
					},
				})
			},
			wantStatus: http.StatusServiceUnavailable,
			wantLevel:  "ERROR",
			wantBody:   `{"status":"degraded","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"unavailable","reason":"timeout"}}}`,
		},
		{
			name:       "a handler that writes without WriteHeader is logged as 200",
			method:     http.MethodGet,
			target:     "/api/health/live",
			handler:    func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("{\"status\":\"ok\"}\n")) },
			wantStatus: http.StatusOK,
			wantLevel:  "INFO",
			wantBody:   `{"status":"ok"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs, logger := newLogRecorder()
			handler := chi.Chain(middlewareChain(logger, testClock(step))...).Handler(test.handler)

			recorder := serve(
				handler,
				test.method,
				test.target,
				test.headers,
				strings.NewReader(test.body),
			)

			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			assertJSONContentType(t, recorder)
			if got := recorder.Body.String(); got != test.wantBody+"\n" {
				t.Errorf("body = %q, want %q", got, test.wantBody+"\n")
			}

			line := logs.find(t, "http request")
			if got := logString(t, line, "level"); got != test.wantLevel {
				t.Errorf("level = %q, want %q", got, test.wantLevel)
			}
			if got := logString(t, line, "method"); got != test.method {
				t.Errorf("method = %q, want %q", got, test.method)
			}
			wantPath := strings.SplitN(test.target, "?", 2)[0]
			if got := logString(t, line, "path"); got != wantPath {
				t.Errorf("path = %q, want %q", got, wantPath)
			}
			if got := logNumber(t, line, "status"); got != float64(test.wantStatus) {
				t.Errorf("status = %v, want %d", got, test.wantStatus)
			}
			if got := logNumber(t, line, "duration_ms"); got != float64(step.Milliseconds()) {
				t.Errorf("duration_ms = %v, want %d", got, step.Milliseconds())
			}
			requestID := logString(t, line, "request_id")
			if requestID == "" {
				t.Error("request_id is empty")
			}
			if got := recorder.Header().Get(middleware.RequestIDHeader); got != requestID {
				t.Errorf(
					"%s = %q, want the logged request_id %q",
					middleware.RequestIDHeader,
					got,
					requestID,
				)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(logs.String(), forbidden) {
					t.Errorf("log output contains %q:\n%s", forbidden, logs.String())
				}
			}
		})
	}
}

type unavailableListStore struct {
	MonitorStore
	cancel context.CancelFunc
}

func (s unavailableListStore) List(context.Context) ([]monitor.Monitor, error) {
	if s.cancel != nil {
		s.cancel()
	}
	return nil, store.ErrUnavailable
}

func TestStoreUnavailableRequestCancellationLogs(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "live request"
		if canceled {
			name = "client canceled"
		}
		t.Run(name, func(t *testing.T) {
			logs, logger := newLogRecorder()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db := unavailableListStore{}
			if canceled {
				db.cancel = cancel
			}
			api := &monitorAPI{store: db, logger: logger}
			handler := chi.Chain(middlewareChain(logger, stoppedClock(testNow))...).Handler(
				http.HandlerFunc(api.list),
			)
			req := httptest.NewRequest(http.MethodGet, "/api/monitors", nil).WithContext(ctx)
			req.Host = "localhost:8080"
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusServiceUnavailable ||
				rec.Body.String() != "{\"error\":\"store_unavailable\"}\n" {
				t.Fatalf("status %d, body %q", rec.Code, rec.Body.String())
			}
			request := logs.find(t, "http request")
			if canceled {
				if request["level"] != slog.LevelInfo.String() ||
					request["client_canceled"] != true {
					t.Errorf("canceled request log = %v", request)
				}
				for _, line := range logs.lines(t) {
					if line["level"] == slog.LevelError.String() {
						t.Errorf("canceled request generated ERROR: %v", line)
					}
				}
			} else {
				failure := logs.find(t, "store unavailable")
				if failure["level"] != slog.LevelError.String() || failure["reason"] != "dependency_failure" {
					t.Errorf("dependency failure log = %v", failure)
				}
				if request["level"] != slog.LevelError.String() {
					t.Errorf("request log = %v", request)
				}
			}
		})
	}
}

func TestRecoverPanics(t *testing.T) {
	t.Run("a panic becomes a JSON 500 and the API keeps serving", func(t *testing.T) {
		logs, logger := newLogRecorder()
		router := newMux(logger, stoppedClock(testNow))
		router.Get("/api/health/live", handleLive)
		router.Get(
			"/api/panic",
			func(http.ResponseWriter, *http.Request) { panic("handler exploded") },
		)

		recorder := serve(router, http.MethodGet, "/api/panic", nil, nil)

		if recorder.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
		}
		assertJSONContentType(t, recorder)
		if got, want := recorder.Body.String(), `{"error":"internal_error"}`+"\n"; got != want {
			t.Errorf("body = %q, want %q", got, want)
		}
		if strings.Contains(recorder.Body.String(), "handler exploded") {
			t.Errorf("response body leaked the panic value: %s", recorder.Body.String())
		}

		recovered := logs.find(t, "panic recovered")
		if got := logString(t, recovered, "level"); got != "ERROR" {
			t.Errorf("level = %q, want ERROR", got)
		}
		if got := logString(t, recovered, "panic"); got != "handler exploded" {
			t.Errorf("panic = %q, want handler exploded", got)
		}
		if got := logString(t, recovered, "stack"); !strings.Contains(got, "goroutine") {
			t.Errorf("stack = %q, want a stack trace", got)
		}
		if got := logString(t, recovered, "request_id"); got == "" {
			t.Error("request_id is empty")
		}

		request := logs.find(t, "http request")
		if got := logNumber(t, request, "status"); got != http.StatusInternalServerError {
			t.Errorf("request status = %v, want %d", got, http.StatusInternalServerError)
		}

		recorder = serve(router, http.MethodGet, "/api/health/live", nil, nil)
		if recorder.Code != http.StatusOK {
			t.Errorf("status after a panic = %d, want %d", recorder.Code, http.StatusOK)
		}
	})

	t.Run("ErrAbortHandler is re-panicked", func(t *testing.T) {
		logs, logger := newLogRecorder()
		router := newMux(logger, stoppedClock(testNow))
		router.Get(
			"/api/abort",
			func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) },
		)

		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Fatalf("recovered %v, want http.ErrAbortHandler", got)
			}
			if strings.Contains(logs.String(), "panic recovered") {
				t.Errorf(
					"http.ErrAbortHandler must not be reported as a recovered panic:\n%s",
					logs.String(),
				)
			}
		}()

		serve(router, http.MethodGet, "/api/abort", nil, nil)

		t.Fatal("the handler did not panic")
	})
}
