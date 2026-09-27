package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// testNow is the instant the injected clock reports, and testCheckedAt is how
// the readiness body must render it.
var testNow = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

const testCheckedAt = "2026-01-02T03:04:05Z"

// stubDependency is a configurable Dependency for tests.
type stubDependency struct {
	name  string
	check func(ctx context.Context) error
}

func (d stubDependency) Name() string { return d.name }

func (d stubDependency) Check(ctx context.Context) error { return d.check(ctx) }

// stoppedClock returns a clock that always reports at, so the readiness body is
// byte-for-byte predictable.
func stoppedClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

// testClock returns a clock that advances by step on every reading, which makes
// the request logger's duration_ms deterministic.
func testClock(step time.Duration) func() time.Time {
	var mu sync.Mutex
	readings := 0
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		readings++
		return testNow.Add(time.Duration(readings) * step)
	}
}

// refusedError is the shape a real dial failure has: *url.Error wrapping
// *net.OpError wrapping *os.SyscallError wrapping ECONNREFUSED.
func refusedError() error {
	return &url.Error{
		Op:  "Post",
		URL: "http://127.0.0.1:8000",
		Err: &net.OpError{
			Op:   "dial",
			Net:  "tcp",
			Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8000},
			Err:  &os.SyscallError{Syscall: "connect", Err: syscall.ECONNREFUSED},
		},
	}
}

// logRecorder collects structured log output written from several goroutines.
type logRecorder struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func newLogRecorder() (*logRecorder, *slog.Logger) {
	recorder := &logRecorder{}
	return recorder, slog.New(slog.NewJSONHandler(recorder, nil))
}

func (r *logRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.Write(p)
}

func (r *logRecorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

// find returns the single captured line with the given message.
func (r *logRecorder) find(t *testing.T, message string) map[string]any {
	t.Helper()
	var found []map[string]any
	for _, line := range r.lines(t) {
		if line["msg"] == message {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one %q log line, got %d in:\n%s", message, len(found), r.String())
	}
	return found[0]
}

// lines decodes the captured JSON log output.
func (r *logRecorder) lines(t *testing.T) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, raw := range strings.Split(r.String(), "\n") {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("log line is not JSON: %v: %q", err, raw)
		}
		lines = append(lines, line)
	}
	return lines
}

// logString returns a string field of a decoded log line.
func logString(t *testing.T, line map[string]any, key string) string {
	t.Helper()
	value, ok := line[key].(string)
	if !ok {
		t.Fatalf("log field %q = %v (%T), want a string", key, line[key], line[key])
	}
	return value
}

// logNumber returns a numeric field of a decoded log line.
func logNumber(t *testing.T, line map[string]any, key string) float64 {
	t.Helper()
	value, ok := line[key].(float64)
	if !ok {
		t.Fatalf("log field %q = %v (%T), want a number", key, line[key], line[key])
	}
	return value
}

// serve runs one request through handler.
func serve(handler http.Handler, method, target string, headers http.Header, body io.Reader) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, body)
	for name, values := range headers {
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func assertJSONContentType(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	if got := recorder.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", got, contentTypeJSON)
	}
}

// decodeBody decodes a JSON response body into T.
func decodeBody[T any](t *testing.T, recorder *httptest.ResponseRecorder) T {
	t.Helper()
	assertJSONContentType(t, recorder)
	var got T
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("response body is not JSON: %v: %q", err, recorder.Body.String())
	}
	return got
}

func TestNewRouterRoutes(t *testing.T) {
	const shortTimeout = 20 * time.Millisecond

	healthy := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { return nil },
	}
	refused := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { return refusedError() },
	}
	unresolvable := stubDependency{
		name: "dynamodb.invalid",
		check: func(context.Context) error {
			return &net.DNSError{Err: "no such host", Name: "dynamodb.invalid", IsNotFound: true}
		},
	}
	broken := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { return errors.New("table status is not ACTIVE") },
	}
	expired := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { return context.DeadlineExceeded },
	}
	blocking := stubDependency{
		name:  "127.0.0.1",
		check: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
	}
	stuck := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { time.Sleep(2 * time.Second); return nil },
	}
	panicking := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { panic("dependency client exploded") },
	}
	needsDeadline := stubDependency{
		name: "127.0.0.1",
		check: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok {
				return errors.New("probe context carries no deadline")
			}
			if !deadline.After(time.Now()) {
				return errors.New("probe deadline already passed")
			}
			return nil
		},
	}

	const (
		readyBody    = `{"status":"ready","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"ready"}}}`
		emptyBody    = `{"status":"ready","checkedAt":"` + testCheckedAt + `","dependencies":{}}`
		unreachable  = `{"status":"degraded","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"unavailable","reason":"unreachable"}}}`
		timeout      = `{"status":"degraded","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"unavailable","reason":"timeout"}}}`
		dependencyEr = `{"status":"degraded","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"unavailable","reason":"error"}}}`
		notFoundBody = `{"error":"not_found"}`
	)

	tests := []struct {
		name              string
		deps              []Dependency
		timeout           time.Duration
		method            string
		target            string
		wantStatus        int
		wantBody          string
		wantMaxElapsed    time.Duration
		wantLogDependency string
		forbidden         []string
	}{
		{
			name:       "liveness never checks a dependency",
			deps:       []Dependency{refused},
			timeout:    shortTimeout,
			method:     http.MethodGet,
			target:     "/api/health/live",
			wantStatus: http.StatusOK,
			wantBody:   `{"status":"ok"}`,
		},
		{
			name:       "readiness without dependencies",
			method:     http.MethodGet,
			target:     "/api/health/ready",
			wantStatus: http.StatusOK,
			wantBody:   emptyBody,
		},
		{
			name:       "readiness with a healthy dependency",
			deps:       []Dependency{healthy},
			timeout:    shortTimeout,
			method:     http.MethodGet,
			target:     "/api/health/ready",
			wantStatus: http.StatusOK,
			wantBody:   readyBody,
		},
		{
			name:              "readiness reports an unreachable dependency",
			deps:              []Dependency{refused},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          unreachable,
			wantLogDependency: "127.0.0.1",
			forbidden:         []string{"http://127.0.0.1:8000", "connection refused", "operator"},
		},
		{
			name:              "readiness reports an unresolvable host as unreachable",
			deps:              []Dependency{unresolvable},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          unreachable,
			wantLogDependency: "dynamodb.invalid",
			forbidden:         []string{"no such host", "dynamodb.invalid"},
		},
		{
			name:              "readiness reports any other dependency failure as an error",
			deps:              []Dependency{broken},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          dependencyEr,
			wantLogDependency: "127.0.0.1",
			forbidden:         []string{"table status is not ACTIVE"},
		},
		{
			name:              "readiness reports an expired dependency deadline as a timeout",
			deps:              []Dependency{expired},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          timeout,
			wantLogDependency: "127.0.0.1",
			forbidden:         []string{"context deadline exceeded"},
		},
		{
			name:              "readiness reports a probe timeout",
			deps:              []Dependency{blocking},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          timeout,
			wantMaxElapsed:    500 * time.Millisecond,
			wantLogDependency: "",
		},
		{
			name:           "readiness abandons a dependency that ignores its context",
			deps:           []Dependency{stuck},
			timeout:        shortTimeout,
			method:         http.MethodGet,
			target:         "/api/health/ready",
			wantStatus:     http.StatusServiceUnavailable,
			wantBody:       timeout,
			wantMaxElapsed: 500 * time.Millisecond,
		},
		{
			name:              "readiness survives a panicking dependency check",
			deps:              []Dependency{panicking},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          dependencyEr,
			wantLogDependency: "127.0.0.1",
			forbidden:         []string{"dependency client exploded"},
		},
		{
			name:              "readiness fails closed on a nil dependency",
			deps:              []Dependency{nil},
			timeout:           shortTimeout,
			method:            http.MethodGet,
			target:            "/api/health/ready",
			wantStatus:        http.StatusServiceUnavailable,
			wantBody:          dependencyEr,
			wantLogDependency: "unknown",
		},
		{
			name:       "readiness is degraded when any dependency fails",
			deps:       []Dependency{healthy, refused},
			timeout:    shortTimeout,
			method:     http.MethodGet,
			target:     "/api/health/ready",
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   unreachable,
		},
		{
			name:       "readiness applies the default timeout for a zero timeout",
			deps:       []Dependency{needsDeadline},
			timeout:    0,
			method:     http.MethodGet,
			target:     "/api/health/ready",
			wantStatus: http.StatusOK,
			wantBody:   readyBody,
		},
		{
			name:       "readiness applies the default timeout for a negative timeout",
			deps:       []Dependency{needsDeadline},
			timeout:    -time.Second,
			method:     http.MethodGet,
			target:     "/api/health/ready",
			wantStatus: http.StatusOK,
			wantBody:   readyBody,
		},
		{
			name:       "unknown API route",
			method:     http.MethodGet,
			target:     "/api/unknown",
			wantStatus: http.StatusNotFound,
			wantBody:   notFoundBody,
		},
		{
			name:       "unknown API prefix",
			method:     http.MethodGet,
			target:     "/api",
			wantStatus: http.StatusNotFound,
			wantBody:   notFoundBody,
		},
		{
			name:       "trailing slash is not redirected",
			method:     http.MethodGet,
			target:     "/api/health/live/",
			wantStatus: http.StatusNotFound,
			wantBody:   notFoundBody,
		},
		{
			name:       "unknown root route",
			method:     http.MethodGet,
			target:     "/",
			wantStatus: http.StatusNotFound,
			wantBody:   notFoundBody,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			logs, logger := newLogRecorder()
			router := NewRouter(logger, test.deps, test.timeout, stoppedClock(testNow))

			start := time.Now()
			recorder := serve(router, test.method, test.target, nil, nil)
			elapsed := time.Since(start)

			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			assertJSONContentType(t, recorder)
			if got, want := recorder.Body.String(), test.wantBody+"\n"; got != want {
				t.Errorf("body = %q, want %q", got, want)
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(recorder.Body.String(), forbidden) {
					t.Errorf("response body leaked %q: %s", forbidden, recorder.Body.String())
				}
			}
			if test.wantMaxElapsed > 0 && elapsed > test.wantMaxElapsed {
				t.Errorf("response took %s, want at most %s", elapsed, test.wantMaxElapsed)
			}
			if test.wantLogDependency != "" {
				warn := logs.find(t, "dependency check failed")
				if got := logString(t, warn, "dependency"); got != test.wantLogDependency {
					t.Errorf("logged dependency = %q, want %q", got, test.wantLogDependency)
				}
			}
		})
	}
}

func TestNewRouterDefaults(t *testing.T) {
	// A nil logger, nil clock, and zero timeout must still produce a working API.
	router := NewRouter(nil, nil, 0, nil)

	recorder := serve(router, http.MethodGet, "/api/health/ready", nil, nil)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	report := decodeBody[healthResponse](t, recorder)
	if report.Status != statusReady {
		t.Errorf("status = %q, want %q", report.Status, statusReady)
	}
	if _, err := time.Parse(time.RFC3339, report.CheckedAt); err != nil {
		t.Errorf("checkedAt = %q, want RFC3339: %v", report.CheckedAt, err)
	}
	if len(report.Dependencies) != 0 {
		t.Errorf("dependencies = %v, want none", report.Dependencies)
	}
}

// TestReadinessCheckedAtIsUTC pins the timestamp format the web client parses:
// RFC3339 in UTC, whatever zone the injected clock reports.
func TestReadinessCheckedAtIsUTC(t *testing.T) {
	zone := time.FixedZone("UTC+2", 2*60*60)
	router := NewRouter(nil, nil, time.Second, stoppedClock(testNow.In(zone)))

	recorder := serve(router, http.MethodGet, "/api/health/ready", nil, nil)

	want := `{"status":"ready","checkedAt":"` + testCheckedAt + `","dependencies":{}}` + "\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
}

func TestMethodMismatchKeepsAllowHeader(t *testing.T) {
	_, logger := newLogRecorder()
	router := NewRouter(logger, nil, time.Second, stoppedClock(testNow))

	recorder := serve(router, http.MethodDelete, "/api/health/live", nil, nil)

	if recorder.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusMethodNotAllowed)
	}
	assertJSONContentType(t, recorder)
	if got, want := recorder.Body.String(), `{"error":"method_not_allowed"}`+"\n"; got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	allow := recorder.Header().Values("Allow")
	if len(allow) != 1 || allow[0] != http.MethodGet {
		t.Errorf("Allow = %v, want [GET]", allow)
	}
}

// wantSecurityHeaders is the hardening contract for every /api response.
var wantSecurityHeaders = map[string]string{
	"Cache-Control":           "no-store",
	"Content-Security-Policy": "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
	"Referrer-Policy":         "no-referrer",
	"X-Content-Type-Options":  "nosniff",
	"X-Frame-Options":         "DENY",
}

func TestSecurityHeaders(t *testing.T) {
	_, logger := newLogRecorder()
	clock := stoppedClock(testNow)
	refused := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { return refusedError() },
	}
	api := NewRouter(logger, []Dependency{refused}, 20*time.Millisecond, clock)
	panicking := newMux(logger, clock)
	panicking.Get("/api/panic", func(http.ResponseWriter, *http.Request) { panic("boom") })

	tests := []struct {
		name         string
		handler      http.Handler
		method       string
		target       string
		wantStatus   int
		wantHardened bool
	}{
		{name: "success", handler: api, method: http.MethodGet, target: "/api/health/live", wantStatus: http.StatusOK, wantHardened: true},
		{name: "readiness failure", handler: api, method: http.MethodGet, target: "/api/health/ready", wantStatus: http.StatusServiceUnavailable, wantHardened: true},
		{name: "unknown API route", handler: api, method: http.MethodGet, target: "/api/unknown", wantStatus: http.StatusNotFound, wantHardened: true},
		{name: "API prefix only", handler: api, method: http.MethodGet, target: "/api", wantStatus: http.StatusNotFound, wantHardened: true},
		{name: "method mismatch", handler: api, method: http.MethodDelete, target: "/api/health/live", wantStatus: http.StatusMethodNotAllowed, wantHardened: true},
		{name: "recovered panic", handler: panicking, method: http.MethodGet, target: "/api/panic", wantStatus: http.StatusInternalServerError, wantHardened: true},
		{name: "non-API path stays unhardened", handler: api, method: http.MethodGet, target: "/", wantStatus: http.StatusNotFound, wantHardened: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := serve(test.handler, test.method, test.target, nil, nil)

			if recorder.Code != test.wantStatus {
				t.Errorf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
			assertJSONContentType(t, recorder)
			for name, want := range wantSecurityHeaders {
				got := recorder.Header().Get(name)
				if test.wantHardened && got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
				if !test.wantHardened && got != "" {
					t.Errorf("%s = %q, want it unset outside /api", name, got)
				}
			}
		})
	}
}

func TestReadinessFailureLogging(t *testing.T) {
	failure := refusedError()
	dependency := stubDependency{name: "127.0.0.1", check: func(context.Context) error { return failure }}
	logs, logger := newLogRecorder()
	router := NewRouter(logger, []Dependency{dependency}, time.Second, stoppedClock(testNow))

	recorder := serve(router, http.MethodGet, "/api/health/ready", nil, nil)

	want := `{"status":"degraded","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"unavailable","reason":"unreachable"}}}` + "\n"
	if recorder.Code != http.StatusServiceUnavailable || recorder.Body.String() != want {
		t.Fatalf("response = %d %q, want %d %q", recorder.Code, recorder.Body.String(), http.StatusServiceUnavailable, want)
	}
	if strings.Contains(recorder.Body.String(), failure.Error()) {
		t.Errorf("response body leaked the underlying error: %s", recorder.Body.String())
	}

	warn := logs.find(t, "dependency check failed")
	if got := logString(t, warn, "level"); got != "WARN" {
		t.Errorf("level = %q, want WARN", got)
	}
	if got := logString(t, warn, "dependency"); got != "127.0.0.1" {
		t.Errorf("dependency = %q, want the host-safe name 127.0.0.1", got)
	}
	if got := logString(t, warn, "error"); got != failure.Error() {
		t.Errorf("error = %q, want %q", got, failure.Error())
	}

	request := logs.find(t, "http request")
	if got := logNumber(t, request, "status"); got != http.StatusServiceUnavailable {
		t.Errorf("request status = %v, want %d", got, http.StatusServiceUnavailable)
	}
	if got := logString(t, request, "level"); got != "ERROR" {
		t.Errorf("request level = %q, want ERROR", got)
	}
	if got := logString(t, request, "request_id"); got == "" {
		t.Error("request_id is empty")
	}
}

func TestDependencyPanicIsContained(t *testing.T) {
	dependency := stubDependency{
		name:  "127.0.0.1",
		check: func(context.Context) error { panic("client bug") },
	}
	logs, logger := newLogRecorder()
	router := NewRouter(logger, []Dependency{dependency}, time.Second, stoppedClock(testNow))

	recorder := serve(router, http.MethodGet, "/api/health/ready", nil, nil)

	want := `{"status":"degraded","checkedAt":"` + testCheckedAt + `","dependencies":{"dynamodb":{"status":"unavailable","reason":"error"}}}` + "\n"
	if got := recorder.Body.String(); got != want {
		t.Errorf("body = %q, want %q", got, want)
	}
	panicked := logs.find(t, "dependency check panicked")
	if got := logString(t, panicked, "dependency"); got != "127.0.0.1" {
		t.Errorf("dependency = %q, want 127.0.0.1", got)
	}
	if got := logString(t, panicked, "panic"); got != "client bug" {
		t.Errorf("panic = %q, want client bug", got)
	}
	failed := logs.find(t, "dependency check failed")
	if got := logString(t, failed, "error"); !strings.Contains(got, "check panicked") {
		t.Errorf("error = %q, want it to report the contained panic", got)
	}
}

// TestRequestIDHeaderIsEchoed ties the logged request_id to the response header
// so that a reported failure can be found in the log.
func TestRequestIDHeaderIsEchoed(t *testing.T) {
	logs, logger := newLogRecorder()
	router := NewRouter(logger, nil, time.Second, stoppedClock(testNow))

	recorder := serve(router, http.MethodGet, "/api/health/live", nil, nil)

	request := logs.find(t, "http request")
	logged := logString(t, request, "request_id")
	if got := recorder.Header().Get(middleware.RequestIDHeader); got != logged {
		t.Errorf("%s = %q, want the logged request_id %q", middleware.RequestIDHeader, got, logged)
	}
}
