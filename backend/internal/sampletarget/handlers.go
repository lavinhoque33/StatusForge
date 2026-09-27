package sampletarget

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// modeResponse is the JSON body of GET and PUT /control/mode.
type modeResponse struct {
	Mode string `json:"mode"`
}

// invalidModeError is the error code returned for every rejected mode change.
// Parsing and validation details are deliberately not exposed.
const invalidModeError = "invalid_mode"

// controlErrorResponse is the JSON body of a rejected control request.
type controlErrorResponse struct {
	Error   string   `json:"error"`
	Allowed []string `json:"allowed"`
}

// handleRoot serves GET /; the response follows the current mode.
func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	switch s.Mode() {
	case ModeFailing:
		writeText(w, http.StatusInternalServerError, "simulated failure")
	case ModeSlow:
		if err := sleepContext(r.Context(), s.slowDelay); err != nil {
			return // client went away; there is nothing to write
		}
		writeText(w, http.StatusOK, "ok (slow)")
	default:
		writeText(w, http.StatusOK, "ok")
	}
}

// handleHealthy serves GET /healthy: a fixed 200 regardless of the current mode.
func (s *Server) handleHealthy(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusOK, "ok")
}

// handleFailing serves GET /failing: a fixed 500 regardless of the current mode.
func (s *Server) handleFailing(w http.ResponseWriter, r *http.Request) {
	writeText(w, http.StatusInternalServerError, "simulated failure")
}

// handleSlow serves GET /slow: a fixed delayed 200 regardless of the current
// mode. The delay is the configured slow delay and honors request cancellation.
func (s *Server) handleSlow(w http.ResponseWriter, r *http.Request) {
	if err := sleepContext(r.Context(), s.slowDelay); err != nil {
		return // client went away; there is nothing to write
	}
	writeText(w, http.StatusOK, "ok (slow)")
}

// handleGetMode serves GET /control/mode.
func (s *Server) handleGetMode(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, modeResponse{Mode: string(s.Mode())})
}

// handlePutMode serves PUT /control/mode. Every invalid request, whether the
// body cannot be parsed or the mode is not one of the allowed values, is
// rejected with the same error code and the list of allowed modes.
func (s *Server) handlePutMode(w http.ResponseWriter, r *http.Request) {
	var body modeResponse
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxControlBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, controlErrorResponse{Error: invalidModeError, Allowed: modes})
		return
	}

	mode, err := ParseMode(body.Mode)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, controlErrorResponse{Error: invalidModeError, Allowed: modes})
		return
	}
	if err := s.SetMode(mode); err != nil {
		writeJSON(w, http.StatusBadRequest, controlErrorResponse{Error: invalidModeError, Allowed: modes})
		return
	}
	writeJSON(w, http.StatusOK, modeResponse{Mode: string(mode)})
}

// sleepContext waits for d or until ctx is done, whichever happens first. It
// returns nil only when the full delay elapsed, so callers can tell that a
// cancelled client no longer needs a response.
func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// writeText writes a plain-text response with the given status.
func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

// writeJSON writes a JSON response with the given status. The body is written
// without a trailing newline so callers can match it exactly.
func writeJSON(w http.ResponseWriter, status int, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
