package notifyreceiver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Mode string

const (
	ModeAccept  Mode = "accept"
	ModeFail    Mode = "fail"
	ModeReject  Mode = "reject"
	ModeTimeout Mode = "timeout"
	ModeDrop    Mode = "drop"
)

var modes = []string{
	string(ModeAccept),
	string(ModeFail),
	string(ModeReject),
	string(ModeTimeout),
	string(ModeDrop),
}

func ParseMode(value string) (Mode, error) {
	switch Mode(value) {
	case ModeAccept, ModeFail, ModeReject, ModeTimeout, ModeDrop:
		return Mode(value), nil
	default:
		return "", fmt.Errorf("unknown receiver mode %q", value)
	}
}

// Entry is one received notification. Duplicate tracks earlier occurrences of
// a nonempty Idempotency-Key, not whether the body happens to match.
type Entry struct {
	ReceivedAt     string          `json:"receivedAt"`
	IdempotencyKey string          `json:"idempotencyKey"`
	Duplicate      bool            `json:"duplicate"`
	Body           json.RawMessage `json:"body"`
}

// Server holds only fixture state; restarting it discards all received entries.
type Server struct {
	mu       sync.Mutex
	mode     Mode
	received []Entry
	seen     map[string]bool
	delay    time.Duration
	mux      http.Handler
}

func New(cfg Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	s := &Server{mode: ModeAccept, seen: make(map[string]bool), delay: cfg.TimeoutDelay}
	s.mux = s.routes()
	return s, nil
}

func (s *Server) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *Server) SetMode(mode Mode) error {
	if _, err := ParseMode(string(mode)); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
	return nil
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) HTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      MaxDelay + 15*time.Second,
		IdleTimeout:       60 * time.Second,
	}
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /notify", s.handleNotify)
	mux.HandleFunc("GET /received", s.handleGetReceived)
	mux.HandleFunc("DELETE /received", s.handleDeleteReceived)
	mux.HandleFunc("GET /control/mode", s.handleGetMode)
	mux.HandleFunc("PUT /control/mode", s.handlePutMode)
	return mux
}

func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	// Read the full request before any simulated outcome. A drop thus represents
	// an ambiguous outcome after the sender has written the entire request.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "could not read request body", http.StatusBadRequest)
		}
		return
	}
	if !json.Valid(body) {
		http.Error(w, "request body must be JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	mode := s.mode
	if mode == ModeAccept || mode == ModeTimeout || mode == ModeDrop {
		key := r.Header.Get("Idempotency-Key")
		s.received = append(s.received, Entry{
			ReceivedAt:     time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
			IdempotencyKey: key,
			Duplicate:      key != "" && s.seen[key],
			Body:           json.RawMessage(body),
		})
		if key != "" {
			s.seen[key] = true
		}
	}
	s.mu.Unlock()

	switch mode {
	case ModeFail:
		http.Error(w, "simulated receiver failure", http.StatusServiceUnavailable)
	case ModeReject:
		http.Error(w, "simulated receiver rejection", http.StatusBadRequest)
	case ModeTimeout:
		timer := time.NewTimer(s.delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			w.WriteHeader(http.StatusNoContent)
		case <-r.Context().Done():
			return
		}
	case ModeDrop:
		hijacker, ok := w.(http.Hijacker)
		if !ok {
			// No successful response should be written when hijacking is unavailable.
			return
		}
		conn, _, err := hijacker.Hijack()
		if err == nil {
			_ = conn.Close()
		}
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Server) handleGetReceived(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	entries := make([]Entry, len(s.received))
	copy(entries, s.received)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, struct {
		Received []Entry `json:"received"`
	}{Received: entries})
}

func (s *Server) handleDeleteReceived(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.received = nil
	clear(s.seen)
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetMode(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Mode Mode `json:"mode"`
	}{Mode: s.Mode()})
}

func (s *Server) handlePutMode(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Mode Mode `json:"mode"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&request) != nil || decoder.Decode(new(any)) != io.EOF ||
		s.SetMode(request.Mode) != nil {
		writeJSON(w, http.StatusBadRequest, struct {
			Error   string   `json:"error"`
			Allowed []string `json:"allowed"`
		}{Error: "invalid_mode", Allowed: modes})
		return
	}
	writeJSON(w, http.StatusOK, request)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
