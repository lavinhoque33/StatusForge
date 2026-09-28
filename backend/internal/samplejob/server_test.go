package samplejob

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const testToken = "sfh_test-token-never-logged"

var runIDPattern = regexp.MustCompile(`^sjob-[0-9a-f]{16}$`)

// recordedRequest is one request the stub ingest endpoint received.
type recordedRequest struct {
	method        string
	path          string
	authorization string
	contentType   string
	body          []byte
}

// receiver is a loopback stub for the heartbeat ingest endpoint. It records
// every request and answers with a fixed status.
type receiver struct {
	mu       sync.Mutex
	requests []recordedRequest
	status   int
	location string
}

func newReceiver(t *testing.T, status int) (*receiver, *httptest.Server) {
	t.Helper()
	rec := &receiver{status: status}
	server := httptest.NewServer(rec)
	t.Cleanup(server.Close)
	return rec, server
}

func (r *receiver) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, err := io.ReadAll(req.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	r.mu.Lock()
	r.requests = append(r.requests, recordedRequest{
		method:        req.Method,
		path:          req.URL.Path,
		authorization: req.Header.Get("Authorization"),
		contentType:   req.Header.Get("Content-Type"),
		body:          body,
	})
	status, location := r.status, r.location
	r.mu.Unlock()
	if status == 0 {
		status = http.StatusAccepted
	}
	if location != "" {
		w.Header().Set("Location", location)
	}
	w.WriteHeader(status)
}

func (r *receiver) setStatus(status int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

func (r *receiver) snapshot() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

// waitForRequests blocks until at least n requests have been recorded or the
// deadline passes.
func (r *receiver) waitForRequests(t *testing.T, n int) []recordedRequest {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		requests := r.snapshot()
		if len(requests) >= n {
			return requests
		}
		if time.Now().After(deadline) {
			t.Fatalf("received %d requests, want at least %d", len(requests), n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// assertSilence fails if the number of recorded requests changes within the
// quiet period, which is how "no report" is observed for skip and stop.
func (r *receiver) assertSilence(t *testing.T, want int) {
	t.Helper()
	time.Sleep(150 * time.Millisecond)
	if got := len(r.snapshot()); got != want {
		t.Fatalf("received %d requests, want %d (no report expected)", got, want)
	}
}

// startFixture builds a fixture that reports to reportURL with testToken.
// Scheduled ticks are driven explicitly, so the real ticker is not started.
func startFixture(t *testing.T, reportURL string, mutate func(*Config)) *Server {
	t.Helper()
	cfg := DefaultConfig()
	cfg.ReportURL = reportURL
	cfg.Token = testToken
	cfg.Interval = time.Hour
	if mutate != nil {
		mutate(&cfg)
	}
	fixture, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return fixture
}

func startControl(t *testing.T, fixture *Server) (*httptest.Server, *http.Client) {
	t.Helper()
	server := httptest.NewServer(fixture.Handler())
	t.Cleanup(server.Close)
	return server, server.Client()
}

// doControl performs one control request and returns the response and its body.
func doControl(
	t *testing.T,
	client *http.Client,
	method, url, body string,
) (*http.Response, []byte) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, payload
}

func decodeReport(t *testing.T, request recordedRequest) reportBody {
	t.Helper()
	var body reportBody
	if err := json.Unmarshal(request.body, &body); err != nil {
		t.Fatalf("report body %s: %v", request.body, err)
	}
	return body
}

func TestTickReportsPerMode(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	ctx := t.Context()

	fixture.tick(ctx)
	first := rec.waitForRequests(t, 1)[0]
	if first.method != http.MethodPost {
		t.Errorf("method = %q, want POST", first.method)
	}
	if first.authorization != "Bearer "+testToken {
		t.Errorf("Authorization = %q, want the bearer token", first.authorization)
	}
	if first.contentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", first.contentType)
	}
	success := decodeReport(t, first)
	if success.Status != "success" || success.ExitCode != 0 || success.DurationMs != 0 {
		t.Errorf("success report = %+v, want status success, exitCode 0, durationMs 0", success)
	}
	if success.Message != "" {
		t.Errorf("success report message = %q, want empty", success.Message)
	}
	if !runIDPattern.MatchString(success.RunID) {
		t.Errorf("runId = %q, want the sjob- form", success.RunID)
	}
	finishedAt, err := time.Parse(time.RFC3339, success.FinishedAt)
	if err != nil {
		t.Fatalf("finishedAt %q: %v", success.FinishedAt, err)
	}
	if age := time.Since(finishedAt); age < 0 || age > time.Minute {
		t.Errorf(
			"finishedAt %s is %s away from now, want a fresh receipt time",
			success.FinishedAt,
			age,
		)
	}

	if err := fixture.SetMode(ModeFail, nil); err != nil {
		t.Fatal(err)
	}
	fixture.tick(ctx)
	failed := decodeReport(t, rec.waitForRequests(t, 2)[1])
	if failed.Status != "failure" || failed.ExitCode != 1 {
		t.Errorf("failure report = %+v, want status failure, exitCode 1", failed)
	}
	if failed.Message != "simulated failure" {
		t.Errorf("failure report message = %q", failed.Message)
	}
	if failed.RunID == success.RunID {
		t.Errorf("runId %q repeated across reports, want a fresh one", failed.RunID)
	}

	if err := fixture.SetMode(ModeSkip, nil); err != nil {
		t.Fatal(err)
	}
	fixture.tick(ctx)
	if err := fixture.SetMode(ModeStop, nil); err != nil {
		t.Fatal(err)
	}
	fixture.tick(ctx)
	rec.assertSilence(t, 2)

	status := fixture.Status()
	if status.Attempts != 2 || status.Failures != 0 {
		t.Errorf("Status() = %+v, want 2 attempts and 0 failures", status)
	}
}

func TestLateModeDelaysTheReport(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	lateSeconds := 1
	if err := fixture.SetMode(ModeLate, &lateSeconds); err != nil {
		t.Fatal(err)
	}

	startedAt := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.tick(t.Context())
	}()

	time.Sleep(250 * time.Millisecond)
	if got := len(rec.snapshot()); got != 0 {
		t.Fatalf("received %d reports inside the late delay, want 0", got)
	}
	rec.waitForRequests(t, 1)
	<-done
	if elapsed := time.Since(startedAt); elapsed < 900*time.Millisecond {
		t.Errorf("report arrived after %s, want the %ds late delay", elapsed, lateSeconds)
	}
}

func TestStopModeRefusesRunAndIgnoresTicks(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	control, client := startControl(t, fixture)
	ctx := t.Context()

	resp, payload := doControl(
		t,
		client,
		http.MethodPut,
		control.URL+"/control/mode",
		`{"mode":"stop"}`,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /control/mode = %d (%s), want 200", resp.StatusCode, payload)
	}

	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("POST /control/run in stop mode = %d (%s), want 409", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "stopped") {
		t.Errorf("stop-mode error body = %s, want the stopped code", payload)
	}

	fixture.tick(ctx)
	rec.assertSilence(t, 0)

	if err := fixture.SetMode(ModeNormal, nil); err != nil {
		t.Fatal(err)
	}
	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/run = %d (%s), want 200", resp.StatusCode, payload)
	}
	first := rec.waitForRequests(t, 1)[0]

	// A second run reports again with a fresh runId; replay resends the last
	// body byte for byte so the endpoint sees a duplicate runId.
	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/run = %d (%s), want 200", resp.StatusCode, payload)
	}
	second := rec.waitForRequests(t, 2)[1]
	if bytes.Equal(first.body, second.body) {
		t.Error("two runs sent identical bodies, want a fresh runId")
	}

	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/replay", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/replay = %d (%s), want 200", resp.StatusCode, payload)
	}
	replayed := rec.waitForRequests(t, 3)[2]
	if !bytes.Equal(second.body, replayed.body) {
		t.Errorf("replay body = %s, want the last body %s", replayed.body, second.body)
	}
}

func TestRunInFailModeReportsAFailure(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	control, client := startControl(t, fixture)

	resp, payload := doControl(
		t,
		client,
		http.MethodPut,
		control.URL+"/control/mode",
		`{"mode":"fail"}`,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /control/mode = %d (%s), want 200", resp.StatusCode, payload)
	}
	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/run = %d (%s), want 200", resp.StatusCode, payload)
	}
	var status Status
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("run response %s: %v", payload, err)
	}
	if status.Attempts != 1 || status.Failures != 0 ||
		status.LastResponseStatus != http.StatusAccepted {
		t.Errorf("run response = %+v, want one accepted attempt", status)
	}
	report := decodeReport(t, rec.waitForRequests(t, 1)[0])
	if report.Status != "failure" || report.ExitCode != 1 {
		t.Errorf("report = %+v, want status failure and exitCode 1", report)
	}
}

func TestControlModeValidationLeavesStateUnchanged(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	control, client := startControl(t, fixture)

	modeState := func() (Mode, int) {
		resp, payload := doControl(t, client, http.MethodGet, control.URL+"/control/mode", "")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET /control/mode = %d (%s), want 200", resp.StatusCode, payload)
		}
		var body struct {
			Mode        Mode `json:"mode"`
			LateSeconds int  `json:"lateSeconds"`
		}
		if err := json.Unmarshal(payload, &body); err != nil {
			t.Fatalf("mode body %s: %v", payload, err)
		}
		return body.Mode, body.LateSeconds
	}

	if mode, late := modeState(); mode != ModeNormal || late != DefaultLateSeconds {
		t.Fatalf("initial mode = %q/%ds, want normal/%ds", mode, late, DefaultLateSeconds)
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{"unknown mode", `{"mode":"bogus"}`, "invalid_mode"},
		{"missing mode", `{"lateSeconds":3}`, "invalid_mode"},
		{"negative late seconds", `{"mode":"normal","lateSeconds":-1}`, "invalid_mode"},
		{"late seconds above the maximum", `{"mode":"normal","lateSeconds":86401}`, "invalid_mode"},
		{"unknown field", `{"mode":"normal","watch":true}`, "invalid_request"},
		{"not an object", `"normal"`, "invalid_request"},
		{"not json", `normal`, "invalid_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, payload := doControl(
				t,
				client,
				http.MethodPut,
				control.URL+"/control/mode",
				tt.body,
			)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("PUT %s = %d (%s), want 400", tt.body, resp.StatusCode, payload)
			}
			if !strings.Contains(string(payload), tt.want) {
				t.Errorf("error body = %s, want %q", payload, tt.want)
			}
			if mode, late := modeState(); mode != ModeNormal || late != DefaultLateSeconds {
				t.Errorf(
					"mode after rejected PUT = %q/%ds, want normal/%ds",
					mode,
					late,
					DefaultLateSeconds,
				)
			}
		})
	}

	resp, payload := doControl(
		t,
		client,
		http.MethodPut,
		control.URL+"/control/mode",
		`{"mode":"late","lateSeconds":2}`,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /control/mode = %d (%s), want 200", resp.StatusCode, payload)
	}
	if mode, late := modeState(); mode != ModeLate || late != 2 {
		t.Errorf("mode = %q/%ds, want late/2s", mode, late)
	}
	rec.assertSilence(t, 0)
}

func TestStatusCountsAttemptsAndCarriesNoToken(t *testing.T) {
	rec, server := newReceiver(t, http.StatusInternalServerError)
	fixture := startFixture(t, server.URL, nil)
	control, client := startControl(t, fixture)

	resp, payload := doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/run = %d (%s), want 200", resp.StatusCode, payload)
	}
	rec.waitForRequests(t, 1)

	rec.setStatus(http.StatusAccepted)
	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/run = %d (%s), want 200", resp.StatusCode, payload)
	}
	rec.waitForRequests(t, 2)

	resp, payload = doControl(t, client, http.MethodGet, control.URL+"/status", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /status = %d (%s), want 200", resp.StatusCode, payload)
	}
	var status Status
	if err := json.Unmarshal(payload, &status); err != nil {
		t.Fatalf("status body %s: %v", payload, err)
	}
	if status.Mode != ModeNormal || status.Attempts != 2 || status.Failures != 1 ||
		status.LastResponseStatus != http.StatusAccepted {
		t.Errorf("status = %+v, want normal, 2 attempts, 1 failure, last 202", status)
	}
	if status.LastAttemptAt == nil {
		t.Fatal("lastAttemptAt = null after two attempts")
	}
	if _, err := time.Parse(time.RFC3339, *status.LastAttemptAt); err != nil {
		t.Errorf("lastAttemptAt %q: %v", *status.LastAttemptAt, err)
	}
	if strings.Contains(string(payload), testToken) || strings.Contains(string(payload), "Bearer") {
		t.Errorf("status body %s carries report credentials", payload)
	}
}

func TestTransportFailureAndRedirectAreNotFollowed(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	redirectTarget, targetServer := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	ctx := t.Context()

	rec.mu.Lock()
	rec.location = targetServer.URL + "/elsewhere"
	rec.status = http.StatusFound
	rec.mu.Unlock()

	fixture.tick(ctx)
	rec.waitForRequests(t, 1)
	if got := len(redirectTarget.snapshot()); got != 0 {
		t.Fatalf("redirect target received %d requests, want 0", got)
	}
	status := fixture.Status()
	if status.LastResponseStatus != http.StatusFound || status.Failures != 1 {
		t.Errorf("status = %+v, want a recorded 302 failure", status)
	}

	// A refused connection is a transport failure with no HTTP status.
	transport, ok := fixture.client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Error("report client must use a transport that never consults a proxy")
	}
	if fixture.client.Timeout != ReportTimeout {
		t.Errorf("report client timeout = %s, want %s", fixture.client.Timeout, ReportTimeout)
	}
	server.Close()
	fixture.tick(ctx)
	status = fixture.Status()
	if status.Attempts != 2 || status.Failures != 2 || status.LastResponseStatus != 0 {
		t.Errorf("status = %+v, want a second, statusless failure", status)
	}
}

// An explicit run always reports (only stop is refused), so a run in skip mode
// is the documented way to force one report while the schedule stays silent.
func TestRunInSkipModeStillReportsOnce(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	control, client := startControl(t, fixture)

	resp, payload := doControl(
		t,
		client,
		http.MethodPut,
		control.URL+"/control/mode",
		`{"mode":"skip"}`,
	)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PUT /control/mode = %d (%s), want 200", resp.StatusCode, payload)
	}
	fixture.tick(t.Context())
	rec.assertSilence(t, 0)

	resp, payload = doControl(t, client, http.MethodPost, control.URL+"/control/run", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/run in skip mode = %d (%s), want 200", resp.StatusCode, payload)
	}
	report := decodeReport(t, rec.waitForRequests(t, 1)[0])
	if report.Status != "success" {
		t.Errorf("report = %+v, want a success report", report)
	}
	if got := fixture.Mode(); got != ModeSkip {
		t.Errorf("Mode() = %q after run, want skip", got)
	}
}

func TestReplayWithoutAReportAndUnconfiguredReportingAreRefused(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	fixture := startFixture(t, server.URL, nil)
	control, client := startControl(t, fixture)

	resp, payload := doControl(t, client, http.MethodPost, control.URL+"/control/replay", "")
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("POST /control/replay = %d (%s), want 409", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), "nothing_to_replay") {
		t.Errorf("replay error body = %s, want nothing_to_replay", payload)
	}

	unconfigured := startFixture(t, "", nil)
	unconfiguredControl, unconfiguredClient := startControl(t, unconfigured)
	unconfigured.tick(t.Context())
	for _, path := range []string{"/control/run", "/control/replay"} {
		resp, payload = doControl(
			t,
			unconfiguredClient,
			http.MethodPost,
			unconfiguredControl.URL+path,
			"",
		)
		if resp.StatusCode != http.StatusConflict {
			t.Fatalf(
				"POST %s without a reporting target = %d (%s), want 409",
				path,
				resp.StatusCode,
				payload,
			)
		}
		if !strings.Contains(string(payload), "report_not_configured") {
			t.Errorf("POST %s error body = %s, want report_not_configured", path, payload)
		}
	}
	rec.assertSilence(t, 0)
	if status := unconfigured.Status(); status.Attempts != 0 {
		t.Errorf("unconfigured Status() = %+v, want no attempts", status)
	}
}

// fakeTicker hands ticks to the fixture from the test and records that Stop
// was called.
type fakeTicker struct {
	interval time.Duration
	ch       chan time.Time
	stopped  atomic.Bool
}

func (f *fakeTicker) Chan() <-chan time.Time { return f.ch }

func (f *fakeTicker) Stop() { f.stopped.Store(true) }

func TestRunDrivesReportsFromTheInjectedTicker(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	ticker := &fakeTicker{ch: make(chan time.Time, 4)}
	var tickerInterval time.Duration
	fixture := startFixture(t, server.URL, func(cfg *Config) {
		cfg.Interval = 17 * time.Second
		cfg.NewTicker = func(d time.Duration) Ticker {
			tickerInterval = d
			ticker.interval = d
			return ticker
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.Run(ctx)
	}()

	ticker.ch <- time.Now()
	rec.waitForRequests(t, 1)
	if tickerInterval != 17*time.Second {
		t.Errorf("Run ticked every %s, want the configured 17s", tickerInterval)
	}

	if err := fixture.SetMode(ModeStop, nil); err != nil {
		t.Fatal(err)
	}
	ticker.ch <- time.Now()
	rec.assertSilence(t, 1)

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	if !ticker.stopped.Load() {
		t.Error("Run did not stop its ticker")
	}
}

// A delay longer than the ticker interval must not turn two ticks into one.
// Switching to stop cancels any report already waiting for its late timer.
func TestRunQueuesLateTicksAndStopCancelsPending(t *testing.T) {
	rec, server := newReceiver(t, http.StatusAccepted)
	ticker := &fakeTicker{ch: make(chan time.Time)}
	fixture := startFixture(t, server.URL, func(cfg *Config) {
		cfg.NewTicker = func(time.Duration) Ticker { return ticker }
	})
	late := 1
	if err := fixture.SetMode(ModeLate, &late); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fixture.Run(ctx)
	}()
	defer func() {
		cancel()
		<-done
	}()

	ticker.ch <- time.Now()
	ticker.ch <- time.Now()
	rec.waitForRequests(t, 2)
	if err := fixture.SetMode(ModeStop, nil); err != nil {
		t.Fatal(err)
	}
	if err := fixture.SetMode(ModeLate, &late); err != nil {
		t.Fatal(err)
	}
	ticker.ch <- time.Now()
	if err := fixture.SetMode(ModeStop, nil); err != nil {
		t.Fatal(err)
	}
	rec.assertSilence(t, 2)
}

func TestLogsNeverContainTheToken(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(
		slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})),
	)
	t.Cleanup(func() { slog.SetDefault(previous) })

	rec, server := newReceiver(t, http.StatusInternalServerError)
	fixture := startFixture(t, server.URL, nil)
	fixture.tick(t.Context())
	rec.waitForRequests(t, 1)

	rec.setStatus(http.StatusAccepted)
	fixture.tick(t.Context())
	rec.waitForRequests(t, 2)

	if logs.Len() == 0 {
		t.Fatal("no log output was produced, so the check would be vacuous")
	}
	if strings.Contains(logs.String(), testToken) {
		t.Errorf("logs contain the plaintext token:\n%s", logs.String())
	}
	if strings.Contains(logs.String(), "Bearer") {
		t.Errorf("logs contain an Authorization header:\n%s", logs.String())
	}
}
