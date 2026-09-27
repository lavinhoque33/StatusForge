package httpapi

import (
	"encoding/json"
	"net/http"
)

// contentTypeJSON is the content type of every /api response.
const contentTypeJSON = "application/json; charset=utf-8"

// liveResponse is the body of GET /api/health/live.
type liveResponse struct {
	Status string `json:"status"`
}

// healthResponse is the body of GET /api/health/ready. It is the contract the
// web client parses, so field names and value vocabulary are fixed.
type healthResponse struct {
	Status       string                      `json:"status"`
	CheckedAt    string                      `json:"checkedAt"`
	Dependencies map[string]dependencyHealth `json:"dependencies"`
}

// dependencyHealth is one entry of a readiness report. Reason is a coarse class
// — never the underlying error, a credential, or a full endpoint URL.
type dependencyHealth struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
}

// errorResponse is the body of every API error that is not a health report.
type errorResponse struct {
	Error string `json:"error"`
}

// Status values.
const (
	statusOK       = "ok"       // liveness
	statusReady    = "ready"    // readiness: every dependency answered
	statusDegraded = "degraded" // readiness: at least one dependency failed
)

// Dependency status values.
const (
	dependencyReady       = "ready"
	dependencyUnavailable = "unavailable"
)

// Coarse dependency failure reasons.
const (
	reasonTimeout     = "timeout"     // the check or the probe hit its deadline
	reasonUnreachable = "unreachable" // connection-level failure: refused, unresolvable, reset
	reasonError       = "error"       // anything else the dependency reported
)

// Error codes.
const (
	errorNotFound         = "not_found"
	errorMethodNotAllowed = "method_not_allowed"
	errorInternal         = "internal_error"
	errorRequestFailed    = "request_failed"
)

// writeJSON writes one JSON response body.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	// A client that vanished mid-response is the only realistic failure here;
	// the status line is already sent, so there is nothing to recover.
	_ = json.NewEncoder(w).Encode(body)
}

// jsonErrorHandler answers with the JSON error envelope.
func jsonErrorHandler(status int, code string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, status, errorResponse{Error: code})
	}
}

// errorReasonFor maps a status to the error code used when a response carries
// no body of its own.
func errorReasonFor(status int) string {
	switch status {
	case http.StatusNotFound:
		return errorNotFound
	case http.StatusMethodNotAllowed:
		return errorMethodNotAllowed
	case http.StatusInternalServerError:
		return errorInternal
	default:
		return errorRequestFailed
	}
}
