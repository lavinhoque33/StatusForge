package samplejob

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

// Mode is the fixture's simulated job behaviour. It decides what a scheduled
// tick does; POST /control/run applies the same behaviour immediately.
type Mode string

const (
	// ModeNormal reports a successful run every tick.
	ModeNormal Mode = "normal"
	// ModeSkip reports nothing: the simulated job did not run.
	ModeSkip Mode = "skip"
	// ModeFail reports a failed run (status failure, exit code 1) every tick.
	ModeFail Mode = "fail"
	// ModeLate reports a successful run LateSeconds after every tick.
	ModeLate Mode = "late"
	// ModeStop reports nothing and ignores ticks; POST /control/run is
	// refused with 409.
	ModeStop Mode = "stop"
)

// modes lists every valid mode in the order used by control error responses.
var modes = []string{
	string(ModeNormal),
	string(ModeSkip),
	string(ModeFail),
	string(ModeLate),
	string(ModeStop),
}

// ParseMode returns the mode named by value, or an error listing the valid
// modes.
func ParseMode(value string) (Mode, error) {
	switch Mode(value) {
	case ModeNormal, ModeSkip, ModeFail, ModeLate, ModeStop:
		return Mode(value), nil
	default:
		return "", fmt.Errorf("unknown sample job mode %q (want one of %v)", value, modes)
	}
}

// Ticker is the subset of *time.Ticker the fixture uses. Tests inject their
// own implementation; the real one wraps time.Ticker.
type Ticker interface {
	Chan() <-chan time.Time
	Stop()
}

// realTicker adapts *time.Ticker to Ticker, whose channel is a method.
type realTicker struct {
	ticker *time.Ticker
}

func newRealTicker(interval time.Duration) Ticker {
	return realTicker{ticker: time.NewTicker(interval)}
}

func (t realTicker) Chan() <-chan time.Time { return t.ticker.C }

func (t realTicker) Stop() { t.ticker.Stop() }

// Status is the fixture's report state, served by GET /status. LastAttemptAt
// is null until the first attempt and LastResponseStatus is 0 when there was
// no HTTP response (no attempt yet or a transport failure).
type Status struct {
	Mode               Mode    `json:"mode"`
	LastAttemptAt      *string `json:"lastAttemptAt"`
	LastResponseStatus int     `json:"lastResponseStatus"`
	Attempts           int     `json:"attempts"`
	Failures           int     `json:"failures"`
}

// reportBody is the JSON body of one report (contract section 3.2). Message is
// omitted when empty so a success report carries only the required fields.
type reportBody struct {
	Status     string `json:"status"`
	RunID      string `json:"runId"`
	FinishedAt string `json:"finishedAt"`
	DurationMs int64  `json:"durationMs"`
	ExitCode   int    `json:"exitCode"`
	Message    string `json:"message,omitempty"`
}

// Server is a configured fixture. Create it with New; the zero value is not
// usable because it has no routes and no client.
type Server struct {
	cfg    Config
	client *http.Client

	mu                 sync.Mutex
	mode               Mode
	lateSeconds        int
	attempts           int
	failures           int
	lastAttemptAt      time.Time
	lastResponseStatus int
	lastReport         []byte
	modeChanged        chan struct{}

	mux http.Handler
}

// New validates cfg and returns a fixture in ModeNormal with the default late
// delay. It does not start anything: call Run for the report ticker and serve
// Handler (or HTTPServer) for the control routes.
func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.NewTicker == nil {
		cfg.NewTicker = newRealTicker
	}
	s := &Server{
		cfg:         cfg,
		client:      newReportClient(),
		mode:        ModeNormal,
		lateSeconds: DefaultLateSeconds,
		modeChanged: make(chan struct{}, 1),
	}
	s.mux = s.routes()
	return s, nil
}

// newReportClient returns the client used for every report. It has a fixed
// deadline, treats a redirect as the final response instead of following it,
// and consults no proxy: Transport.Proxy stays nil even when the environment
// sets HTTP_PROXY.
func newReportClient() *http.Client {
	transport := &http.Transport{IdleConnTimeout: 30 * time.Second}
	return &http.Client{
		Timeout:   ReportTimeout,
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// Handler returns the fixture's control handler.
func (s *Server) Handler() http.Handler { return s.mux }

// HTTPServer returns an *http.Server serving the fixture on addr with the
// package's timeout budget. Control requests are quick; the report client's
// deadline is separate and applies to outbound traffic only.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

// Mode returns the current simulated job behaviour.
func (s *Server) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// LateSeconds returns the delay the late mode adds to a tick.
func (s *Server) LateSeconds() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lateSeconds
}

// SetMode switches the simulated job behaviour. A non-nil lateSeconds also
// replaces the late mode's delay. Invalid input is rejected and leaves the
// current state unchanged.
func (s *Server) SetMode(mode Mode, lateSeconds *int) error {
	if _, err := ParseMode(string(mode)); err != nil {
		return err
	}
	if lateSeconds != nil && (*lateSeconds < 0 || *lateSeconds > MaxLateSeconds) {
		return fmt.Errorf(
			"lateSeconds %d: must be between 0 and %d",
			*lateSeconds,
			MaxLateSeconds,
		)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	if lateSeconds != nil {
		s.lateSeconds = *lateSeconds
	}
	if mode == ModeStop {
		select {
		case s.modeChanged <- struct{}{}:
		default:
		}
	}
	return nil
}

// Status returns a snapshot of the report counters.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	status := Status{
		Mode:               s.mode,
		LastResponseStatus: s.lastResponseStatus,
		Attempts:           s.attempts,
		Failures:           s.failures,
	}
	if !s.lastAttemptAt.IsZero() {
		at := s.lastAttemptAt.Format(timestampLayout)
		status.LastAttemptAt = &at
	}
	return status
}

// Run drives scheduled reports. Delayed reports remain queued while the ticker
// continues, so a late delay longer than the interval does not drop ticks.
func (s *Server) Run(ctx context.Context) {
	ticker := s.cfg.NewTicker(s.cfg.Interval)
	defer ticker.Stop()
	var pending []time.Time
	var timer *time.Timer
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		var due <-chan time.Time
		if len(pending) > 0 {
			delay := time.Until(pending[0])
			if timer == nil {
				timer = time.NewTimer(delay)
			} else {
				timer.Reset(delay)
			}
			due = timer.C
		}
		select {
		case <-ctx.Done():
			return
		case <-s.modeChanged:
			pending = nil
		case <-ticker.Chan():
			if !s.cfg.Reporting() {
				continue
			}
			mode, lateSeconds := s.modeAndLate()
			switch mode {
			case ModeStop, ModeSkip:
			case ModeLate:
				pending = append(pending, time.Now().Add(time.Duration(lateSeconds)*time.Second))
			case ModeFail:
				s.report(ctx, true)
			default:
				s.report(ctx, false)
			}
		case <-due:
			pending = pending[1:]
			if s.Mode() != ModeStop && ctx.Err() == nil {
				s.report(ctx, false)
			}
		}
	}
}

// tick performs one scheduled iteration for direct tests. Run queues delayed
// ticks instead of blocking the interval loop on the late timer.
func (s *Server) tick(ctx context.Context) {
	if !s.cfg.Reporting() {
		return
	}
	mode, lateSeconds := s.modeAndLate()
	switch mode {
	case ModeStop, ModeSkip:
		return
	case ModeLate:
		if !wait(ctx, time.Duration(lateSeconds)*time.Second) || s.Mode() == ModeStop {
			return
		}
		s.report(ctx, false)
	case ModeFail:
		s.report(ctx, true)
	default:
		s.report(ctx, false)
	}
}

// report builds and sends one report: a failure report when failed is true,
// otherwise a success report. Either way the attempt is recorded, including a
// failed one.
func (s *Server) report(ctx context.Context, failed bool) {
	body, err := s.newReportBody(failed)
	if err != nil {
		slog.Error("sample job: report body could not be built", "error", err)
		return
	}
	s.send(ctx, body)
}

// newReportBody renders one report. The fixture does no real work, so
// durationMs is always 0 and the exit code is 0 for success and 1 for failure.
// Every report carries a fresh runId.
func (s *Server) newReportBody(failed bool) ([]byte, error) {
	body := reportBody{
		Status:     "success",
		RunID:      newRunID(),
		FinishedAt: s.cfg.Now().UTC().Format(timestampLayout),
	}
	if failed {
		body.Status = "failure"
		body.ExitCode = 1
		body.Message = "simulated failure"
	}
	return json.Marshal(body)
}

// send posts body to the report URL with the heartbeat token and records the
// outcome. body is stored verbatim so /control/replay can resend it unchanged.
func (s *Server) send(ctx context.Context, body []byte) {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.cfg.ReportURL,
		bytes.NewReader(body),
	)
	if err != nil {
		s.recordAttempt(0, body)
		slog.Error("sample job: report request could not be built")
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		s.recordAttempt(0, body)
		// net/http errors can embed the URL, which is caller-supplied and
		// could contain sensitive query parameters. Never log it or headers.
		slog.Warn("sample job: report transport failed")
		return
	}
	defer resp.Body.Close()
	// The body is untrusted and unused; read a bounded prefix and discard it.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
	s.recordAttempt(resp.StatusCode, body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		slog.Warn("sample job: report rejected", "status", resp.StatusCode)
		return
	}
	slog.Info("sample job: report accepted", "status", resp.StatusCode)
}

// recordAttempt stores the outcome of one report attempt. Status 0 means the
// request never produced an HTTP response.
func (s *Server) recordAttempt(status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts++
	if status < 200 || status > 299 {
		s.failures++
	}
	s.lastAttemptAt = s.cfg.Now().UTC()
	s.lastResponseStatus = status
	s.lastReport = bytes.Clone(body)
}

// lastReportBody returns the most recent report body, if any.
func (s *Server) lastReportBody() ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.lastReport) == 0 {
		return nil, false
	}
	return bytes.Clone(s.lastReport), true
}

// modeAndLate reads the mode and late delay together so one tick sees a
// consistent pair.
func (s *Server) modeAndLate() (Mode, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode, s.lateSeconds
}

// wait sleeps for d or until ctx is cancelled, and reports whether the full
// delay elapsed.
func wait(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// newRunID returns a fresh, contract-legal run ID (1-100 characters of
// [A-Za-z0-9._:-]). crypto/rand cannot realistically fail; the monotonic
// fallback keeps the fixture working if it ever does.
func newRunID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("sjob-%d", time.Now().UTC().UnixNano())
	}
	return "sjob-" + hex.EncodeToString(buf[:])
}

// routes builds the fixed route set. Method-qualified patterns let net/http
// answer wrong methods with 405 automatically.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", s.handleStatus)
	mux.HandleFunc("GET /control/mode", s.handleGetMode)
	mux.HandleFunc("PUT /control/mode", s.handlePutMode)
	mux.HandleFunc("POST /control/run", s.handleRun)
	mux.HandleFunc("POST /control/replay", s.handleReplay)
	mux.HandleFunc("POST /control/deploy", s.handleDeploy)
	return mux
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.Status())
}

func (s *Server) handleGetMode(w http.ResponseWriter, _ *http.Request) {
	mode, lateSeconds := s.modeAndLate()
	writeJSON(w, http.StatusOK, struct {
		Mode        Mode `json:"mode"`
		LateSeconds int  `json:"lateSeconds"`
	}{Mode: mode, LateSeconds: lateSeconds})
}

func (s *Server) handlePutMode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode        Mode `json:"mode"`
		LateSeconds *int `json:"lateSeconds"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF {
		writeControlError(
			w,
			http.StatusBadRequest,
			"invalid_request",
			`body must be {"mode":...,"lateSeconds":N}`,
		)
		return
	}
	if err := s.SetMode(request.Mode, request.LateSeconds); err != nil {
		writeControlError(w, http.StatusBadRequest, "invalid_mode", err.Error())
		return
	}
	mode, lateSeconds := s.modeAndLate()
	writeJSON(w, http.StatusOK, struct {
		Mode        Mode `json:"mode"`
		LateSeconds int  `json:"lateSeconds"`
	}{Mode: mode, LateSeconds: lateSeconds})
}

// handleRun reports once now with the current mode's report shape: fail sends
// a failure report, every other mode sends a success report, and stop is
// refused. The late mode's delay belongs to the schedule, so an explicit run
// does not wait.
func (s *Server) handleRun(w http.ResponseWriter, r *http.Request) {
	mode := s.Mode()
	if mode == ModeStop {
		writeControlError(w, http.StatusConflict, "stopped", "stop mode refuses reports")
		return
	}
	if !s.cfg.Reporting() {
		writeControlError(
			w,
			http.StatusConflict,
			"report_not_configured",
			"report URL and token are required to report",
		)
		return
	}
	s.report(r.Context(), mode == ModeFail)
	writeJSON(w, http.StatusOK, s.Status())
}

// handleReplay resends the last report verbatim, so the ingest endpoint sees
// the duplicate runId a replay drill needs.
func (s *Server) handleReplay(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.Reporting() {
		writeControlError(
			w,
			http.StatusConflict,
			"report_not_configured",
			"report URL and token are required to report",
		)
		return
	}
	body, ok := s.lastReportBody()
	if !ok {
		writeControlError(
			w,
			http.StatusConflict,
			"nothing_to_replay",
			"no report has been attempted yet",
		)
		return
	}
	s.send(r.Context(), body)
	writeJSON(w, http.StatusOK, s.Status())
}

func (s *Server) handleDeploy(w http.ResponseWriter, r *http.Request) {
	if s.cfg.DeployURL == "" || s.cfg.DeployToken == "" {
		writeControlError(w, 409, "not_configured", "deploy URL and token are required")
		return
	}
	var request struct {
		Version      string  `json:"version"`
		Description  *string `json:"description,omitempty"`
		Link         *string `json:"link,omitempty"`
		DeploymentID *string `json:"deploymentId,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF ||
		request.Version == "" {
		writeControlError(
			w,
			400,
			"invalid_request",
			"version is required and the body must be valid JSON",
		)
		return
	}
	body, _ := json.Marshal(request)
	req, err := http.NewRequestWithContext(
		r.Context(),
		http.MethodPost,
		s.cfg.DeployURL,
		bytes.NewReader(body),
	)
	if err != nil {
		writeControlError(w, 502, "api_unreachable", "deployment API unreachable")
		return
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.DeployToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		writeControlError(w, 502, "api_unreachable", "deployment API unreachable")
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		writeControlError(w, 502, "api_unreachable", "deployment API response unavailable")
		return
	}
	var payload any
	if len(data) > 0 && json.Unmarshal(data, &payload) != nil {
		payload = nil
	}
	writeJSON(w, 200, map[string]any{"apiStatus": resp.StatusCode, "response": payload})
}

// writeControlError writes the fixture's error envelope. Messages name the
// problem, never the token or the configured URL.
func writeControlError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, struct {
		Error   string   `json:"error"`
		Message string   `json:"message"`
		Allowed []string `json:"allowed,omitempty"`
	}{
		Error:   code,
		Message: message,
		Allowed: allowedFor(code),
	})
}

// allowedFor lists the accepted modes for mode errors, so a caller can correct
// itself without reading the source.
func allowedFor(code string) []string {
	if code == "invalid_mode" {
		return modes
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
