package sampletarget

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newFixture builds a fixture for tests; it never listens on a real address.
func newFixture(t *testing.T, slowDelay time.Duration) *Server {
	t.Helper()
	s, err := New(Config{Addr: DefaultAddr, SlowDelay: slowDelay})
	if err != nil {
		t.Fatalf("New() error: %v", err)
	}
	return s
}

// setMode returns a table configure step that switches the fixture mode.
func setMode(m Mode) func(*testing.T, *Server) {
	return func(t *testing.T, s *Server) {
		t.Helper()
		if err := s.SetMode(m); err != nil {
			t.Fatalf("SetMode(%q) error: %v", m, err)
		}
	}
}

type response struct {
	status int
	header http.Header
	body   string
}

func doRequest(t *testing.T, client *http.Client, method, url, body string) response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("http.NewRequest(%s %s) error: %v", method, url, err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s error: %v", method, url, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading %s %s body: %v", method, url, err)
	}
	return response{status: resp.StatusCode, header: resp.Header, body: string(data)}
}

func TestRoutes(t *testing.T) {
	// The table uses a one-millisecond slow delay so slow-mode cases stay fast.
	const testDelay = time.Millisecond

	tests := []struct {
		name         string
		configure    func(*testing.T, *Server)
		method       string
		path         string
		body         string
		wantStatus   int
		wantBody     string
		wantContains []string
	}{
		{
			name:       "root is ok in healthy mode",
			method:     http.MethodGet,
			path:       "/",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "root simulates failure in failing mode",
			configure:  setMode(ModeFailing),
			method:     http.MethodGet,
			path:       "/",
			wantStatus: http.StatusInternalServerError,
			wantBody:   "simulated failure",
		},
		{
			name:       "root is slow in slow mode",
			configure:  setMode(ModeSlow),
			method:     http.MethodGet,
			path:       "/",
			wantStatus: http.StatusOK,
			wantBody:   "ok (slow)",
		},
		{
			name:       "healthy is fixed 200 in failing mode",
			configure:  setMode(ModeFailing),
			method:     http.MethodGet,
			path:       "/healthy",
			wantStatus: http.StatusOK,
			wantBody:   "ok",
		},
		{
			name:       "failing is fixed 500 in healthy mode",
			method:     http.MethodGet,
			path:       "/failing",
			wantStatus: http.StatusInternalServerError,
			wantBody:   "simulated failure",
		},
		{
			name:       "slow is fixed 200 in failing mode",
			configure:  setMode(ModeFailing),
			method:     http.MethodGet,
			path:       "/slow",
			wantStatus: http.StatusOK,
			wantBody:   "ok (slow)",
		},
		{
			name:       "mode control reports the default mode",
			method:     http.MethodGet,
			path:       "/control/mode",
			wantStatus: http.StatusOK,
			wantBody:   `{"mode":"healthy"}`,
		},
		{
			name:       "mode control switches mode",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":"failing"}`,
			wantStatus: http.StatusOK,
			wantBody:   `{"mode":"failing"}`,
		},
		{
			name:       "mode control accepts every allowed mode",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":"slow"}`,
			wantStatus: http.StatusOK,
			wantBody:   `{"mode":"slow"}`,
		},
		{
			name:       "mode control rejects an unknown mode",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":"bogus"}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid_mode","allowed":["healthy","failing","slow"]}`,
		},
		{
			name:       "mode control rejects an empty mode",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":""}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid_mode","allowed":["healthy","failing","slow"]}`,
		},
		{
			name:       "mode control rejects a differently cased mode",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":"Healthy"}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid_mode","allowed":["healthy","failing","slow"]}`,
		},
		{
			name:       "mode control rejects malformed JSON",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid_mode","allowed":["healthy","failing","slow"]}`,
		},
		{
			name:       "mode control rejects unknown fields",
			method:     http.MethodPut,
			path:       "/control/mode",
			body:       `{"mode":"healthy","extra":true}`,
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid_mode","allowed":["healthy","failing","slow"]}`,
		},
		{
			name:       "mode control requires a body",
			method:     http.MethodPut,
			path:       "/control/mode",
			wantStatus: http.StatusBadRequest,
			wantBody:   `{"error":"invalid_mode","allowed":["healthy","failing","slow"]}`,
		},
		{
			name:         "unknown route is not found",
			method:       http.MethodGet,
			path:         "/nope",
			wantStatus:   http.StatusNotFound,
			wantContains: []string{"404 page not found"},
		},
		{
			name:         "wrong method is rejected",
			method:       http.MethodPost,
			path:         "/control/mode",
			body:         `{"mode":"healthy"}`,
			wantStatus:   http.StatusMethodNotAllowed,
			wantContains: []string{"Method Not Allowed"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newFixture(t, testDelay)
			if tt.configure != nil {
				tt.configure(t, s)
			}

			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, body))

			if rec.Code != tt.wantStatus {
				t.Fatalf(
					"%s %s status = %d, want %d (body %q)",
					tt.method,
					tt.path,
					rec.Code,
					tt.wantStatus,
					rec.Body.String(),
				)
			}
			if tt.wantBody != "" && rec.Body.String() != tt.wantBody {
				t.Errorf(
					"%s %s body = %q, want %q",
					tt.method,
					tt.path,
					rec.Body.String(),
					tt.wantBody,
				)
			}
			for _, want := range tt.wantContains {
				if !strings.Contains(rec.Body.String(), want) {
					t.Errorf(
						"%s %s body %q does not contain %q",
						tt.method,
						tt.path,
						rec.Body.String(),
						want,
					)
				}
			}
			if got := rec.Header().Get(FixtureHeader); got != FixtureName {
				t.Errorf(
					"%s %s %s = %q, want %q",
					tt.method,
					tt.path,
					FixtureHeader,
					got,
					FixtureName,
				)
			}
		})
	}
}

func TestFixtureHeaderOnEveryResponse(t *testing.T) {
	s := newFixture(t, time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := ts.Client()

	requests := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/", ""},
		{http.MethodGet, "/healthy", ""},
		{http.MethodGet, "/failing", ""},
		{http.MethodGet, "/slow", ""},
		{http.MethodGet, "/control/mode", ""},
		{http.MethodPut, "/control/mode", `{"mode":"healthy"}`},
		{http.MethodGet, "/missing", ""},
		{http.MethodPost, "/control/mode", `{"mode":"healthy"}`},
	}
	for _, req := range requests {
		t.Run(req.method+" "+req.path, func(t *testing.T) {
			resp := doRequest(t, client, req.method, ts.URL+req.path, req.body)
			if got := resp.header.Get(FixtureHeader); got != FixtureName {
				t.Errorf(
					"%s %s %s = %q, want %q",
					req.method,
					req.path,
					FixtureHeader,
					got,
					FixtureName,
				)
			}
		})
	}
}

func TestModeTransitionsAffectRoot(t *testing.T) {
	s := newFixture(t, time.Millisecond)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := ts.Client()

	if resp := doRequest(t, client, http.MethodGet, ts.URL+"/", ""); resp.status != http.StatusOK ||
		resp.body != "ok" {
		t.Fatalf(
			"initial GET / = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusOK,
			"ok",
		)
	}

	if resp := doRequest(t, client, http.MethodPut, ts.URL+"/control/mode", `{"mode":"failing"}`); resp.status != http.StatusOK {
		t.Fatalf(
			"PUT /control/mode failing = (%d, %q), want status %d",
			resp.status,
			resp.body,
			http.StatusOK,
		)
	}
	if resp := doRequest(t, client, http.MethodGet, ts.URL+"/", ""); resp.status != http.StatusInternalServerError ||
		resp.body != "simulated failure" {
		t.Fatalf(
			"GET / after controlled failure = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusInternalServerError,
			"simulated failure",
		)
	}
	if resp := doRequest(t, client, http.MethodGet, ts.URL+"/control/mode", ""); resp.status != http.StatusOK ||
		resp.body != `{"mode":"failing"}` {
		t.Fatalf(
			"GET /control/mode = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusOK,
			`{"mode":"failing"}`,
		)
	}

	if resp := doRequest(t, client, http.MethodPut, ts.URL+"/control/mode", `{"mode":"healthy"}`); resp.status != http.StatusOK {
		t.Fatalf(
			"PUT /control/mode healthy = (%d, %q), want status %d",
			resp.status,
			resp.body,
			http.StatusOK,
		)
	}
	if resp := doRequest(t, client, http.MethodGet, ts.URL+"/", ""); resp.status != http.StatusOK ||
		resp.body != "ok" {
		t.Fatalf(
			"GET / after recovery = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusOK,
			"ok",
		)
	}
}

func TestRootSlowModeWaitsAndIsCancelable(t *testing.T) {
	const delay = 40 * time.Millisecond
	s := newFixture(t, delay)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := ts.Client()

	if resp := doRequest(t, client, http.MethodPut, ts.URL+"/control/mode", `{"mode":"slow"}`); resp.status != http.StatusOK {
		t.Fatalf(
			"PUT /control/mode slow = (%d, %q), want status %d",
			resp.status,
			resp.body,
			http.StatusOK,
		)
	}

	start := time.Now()
	resp := doRequest(t, client, http.MethodGet, ts.URL+"/", "")
	elapsed := time.Since(start)
	if resp.status != http.StatusOK || resp.body != "ok (slow)" {
		t.Fatalf(
			"GET / in slow mode = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusOK,
			"ok (slow)",
		)
	}
	if elapsed < delay/2 {
		t.Errorf("GET / in slow mode returned after %s, want at least %s", elapsed, delay/2)
	}
}

func TestFixedRoutesIgnoreMode(t *testing.T) {
	const delay = 20 * time.Millisecond
	s := newFixture(t, delay)
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	client := ts.Client()

	if resp := doRequest(t, client, http.MethodPut, ts.URL+"/control/mode", `{"mode":"failing"}`); resp.status != http.StatusOK {
		t.Fatalf(
			"PUT /control/mode failing = (%d, %q), want status %d",
			resp.status,
			resp.body,
			http.StatusOK,
		)
	}

	if resp := doRequest(t, client, http.MethodGet, ts.URL+"/healthy", ""); resp.status != http.StatusOK ||
		resp.body != "ok" {
		t.Errorf(
			"GET /healthy in failing mode = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusOK,
			"ok",
		)
	}
	if resp := doRequest(t, client, http.MethodGet, ts.URL+"/failing", ""); resp.status != http.StatusInternalServerError ||
		resp.body != "simulated failure" {
		t.Errorf(
			"GET /failing in failing mode = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusInternalServerError,
			"simulated failure",
		)
	}

	start := time.Now()
	resp := doRequest(t, client, http.MethodGet, ts.URL+"/slow", "")
	elapsed := time.Since(start)
	if resp.status != http.StatusOK || resp.body != "ok (slow)" {
		t.Errorf(
			"GET /slow in failing mode = (%d, %q), want (%d, %q)",
			resp.status,
			resp.body,
			http.StatusOK,
			"ok (slow)",
		)
	}
	if elapsed < delay/2 {
		t.Errorf("GET /slow returned after %s, want at least %s", elapsed, delay/2)
	}
}

func TestSlowRoutesHonorCancellation(t *testing.T) {
	t.Run("cancelled context releases the handler", func(t *testing.T) {
		s := newFixture(t, 30*time.Second)
		if err := s.SetMode(ModeSlow); err != nil {
			t.Fatalf("SetMode(slow) error: %v", err)
		}
		h := s.Handler()

		for _, path := range []string{"/", "/slow"} {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil).WithContext(ctx)

			done := make(chan struct{})
			go func() {
				defer close(done)
				h.ServeHTTP(rec, req)
			}()

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatalf("GET %s ignored request cancellation; the delay is not cancelable", path)
			}
			if rec.Body.Len() != 0 {
				t.Errorf(
					"GET %s response body = %q after cancellation, want no response written",
					path,
					rec.Body.String(),
				)
			}
		}
	})

	t.Run("disconnecting client is released early", func(t *testing.T) {
		s := newFixture(t, 30*time.Second)
		ts := httptest.NewServer(s.Handler())
		defer ts.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/slow", nil)
		if err != nil {
			t.Fatalf("http.NewRequestWithContext error: %v", err)
		}
		go func() {
			time.Sleep(50 * time.Millisecond)
			cancel()
		}()

		start := time.Now()
		if _, err := ts.Client().Do(req); err == nil {
			t.Fatal(
				"GET /slow after client cancellation returned no error, want a cancellation error",
			)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Errorf("cancelled GET /slow took %s; the 30s delay ignored cancellation", elapsed)
		}
	})
}

func TestHTTPServerTimeoutBudget(t *testing.T) {
	s := newFixture(t, DefaultSlowDelay)
	srv := s.HTTPServer(DefaultAddr)

	if srv.Addr != DefaultAddr {
		t.Errorf("HTTPServer addr = %q, want %q", srv.Addr, DefaultAddr)
	}
	if srv.Handler == nil {
		t.Error("HTTPServer handler is nil")
	}
	if srv.ReadHeaderTimeout <= 0 {
		t.Error("ReadHeaderTimeout must be bounded")
	}
	if srv.ReadTimeout <= 0 {
		t.Error("ReadTimeout must be bounded")
	}
	if srv.IdleTimeout <= 0 {
		t.Error("IdleTimeout must be bounded")
	}
	if srv.WriteTimeout <= MaxSlowDelay {
		t.Errorf(
			"WriteTimeout = %s, want more than MaxSlowDelay (%s) so a maximum slow response can complete",
			srv.WriteTimeout,
			MaxSlowDelay,
		)
	}
}
