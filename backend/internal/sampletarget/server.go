package sampletarget

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Mode is the fixture's simulated target state. It governs GET /; the fixed
// /healthy, /failing, and /slow routes ignore it.
type Mode string

const (
	// ModeHealthy is the default mode: GET / returns 200 "ok".
	ModeHealthy Mode = "healthy"
	// ModeFailing makes GET / return 500 "simulated failure".
	ModeFailing Mode = "failing"
	// ModeSlow makes GET / wait out the slow delay, then return 200 "ok (slow)".
	ModeSlow Mode = "slow"
)

// modes lists every valid mode in the order used by control error responses.
var modes = []string{string(ModeHealthy), string(ModeFailing), string(ModeSlow)}

// ParseMode returns the mode named by s, or an error listing the valid modes.
func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeHealthy, ModeFailing, ModeSlow:
		return Mode(s), nil
	default:
		return "", fmt.Errorf("unknown mode %q", s)
	}
}

// Server is a configured fixture. Create it with New; the zero value is not
// usable because it has no routes.
type Server struct {
	mux http.Handler

	// mu guards mode, which the control route can change at any time.
	mu   sync.Mutex
	mode Mode

	// slowDelay is fixed by Config when the fixture is created and never changes.
	slowDelay time.Duration
}

// New validates cfg and returns a fixture in ModeHealthy.
func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Server{mode: ModeHealthy, slowDelay: cfg.SlowDelay}
	s.mux = s.routes()
	return s, nil
}

// Handler returns the fixture's HTTP handler. Every response carries
// FixtureHeader.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(FixtureHeader, FixtureName)
		s.mux.ServeHTTP(w, r)
	})
}

// HTTPServer returns an *http.Server serving the fixture on addr with the
// package's timeout budget. WriteTimeout exceeds MaxSlowDelay on purpose so a
// slow response can still complete.
func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
	}
}

// Mode returns the current simulated target state.
func (s *Server) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// SetMode switches the simulated target state. Unknown modes are rejected and
// leave the current state unchanged.
func (s *Server) SetMode(m Mode) error {
	if _, err := ParseMode(string(m)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = m
	return nil
}

// routes builds the fixed route set. Method-qualified patterns let net/http
// answer wrong methods with 405 automatically.
func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /healthy", s.handleHealthy)
	mux.HandleFunc("GET /failing", s.handleFailing)
	mux.HandleFunc("GET /slow", s.handleSlow)
	mux.HandleFunc("GET /control/mode", s.handleGetMode)
	mux.HandleFunc("PUT /control/mode", s.handlePutMode)
	return mux
}
