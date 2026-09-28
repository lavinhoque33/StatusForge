package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5/middleware"
)

// security headers applied to every /api response. The API returns JSON state
// reports, so responses must never be cached, sniffed, framed, or rendered as
// documents.
var securityHeaderValues = [...]struct{ name, value string }{
	{"Cache-Control", "no-store"},
	{
		"Content-Security-Policy",
		"default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
	},
	{"Referrer-Policy", "no-referrer"},
	{"X-Content-Type-Options", "nosniff"},
	{"X-Frame-Options", "DENY"},
}

// securityHeaders hardens every /api response, including 404, 405, 503, and
// recovered-panic responses, because it wraps the whole routing chain.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) {
			header := w.Header()
			for _, h := range securityHeaderValues {
				header.Set(h.name, h.value)
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestIDHeader echoes the request ID to the client so that a report of a
// failing response can be tied to its log line. chi's RequestID middleware
// stores the ID in the request context only.
func requestIDHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if id := middleware.GetReqID(r.Context()); id != "" {
			w.Header().Set(middleware.RequestIDHeader, id)
		}
		next.ServeHTTP(w, r)
	})
}

// requestLogger writes one structured line per request.
//
// It records the method, path, status, duration_ms, and request_id; a canceled
// request also carries client_canceled=true. Query strings, request headers,
// and request bodies are not logged.
func requestLogger(logger *slog.Logger, now func() time.Time) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			defer func() {
				attrs := []slog.Attr{
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Int("status", recorder.status),
					slog.Int64("duration_ms", now().Sub(start).Milliseconds()),
					slog.String("request_id", middleware.GetReqID(r.Context())),
				}
				level := requestLevel(recorder.status)
				if r.Context().Err() != nil {
					level = slog.LevelInfo
					attrs = append(attrs, slog.Bool("client_canceled", true))
				}
				logger.LogAttrs(r.Context(), level, "http request", attrs...)
			}()
			next.ServeHTTP(recorder, r)
		})
	}
}

// recoverPanics gives a panicking handler the JSON error envelope and logs the
// panic with its stack through slog, so one bad request cannot take the process
// down and the panic lands in the configured log format. http.ErrAbortHandler
// is re-panicked unchanged: the standard library uses it to abort a connection
// on purpose.
func recoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				logger.ErrorContext(r.Context(), "panic recovered",
					"panic", recovered,
					"stack", string(debug.Stack()),
					"method", r.Method,
					"path", r.URL.Path,
					"request_id", middleware.GetReqID(r.Context()),
				)
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: errorInternal})
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// jsonErrorBodies keeps the JSON contract for the error responses chi writes
// without a body, most notably its 405 answer, which must keep the Allow header
// that the HTTP specification requires. A handler that writes its own body,
// including its own Content-Type, is left untouched.
func jsonErrorBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		// Set it before anything can write a status line: a body-less error
		// still has to report JSON, and the headers are already on the wire by
		// the time the missing body is detected.
		w.Header().Set("Content-Type", contentTypeJSON)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		if recorder.status < http.StatusBadRequest || recorder.wroteBody {
			return
		}
		body, err := json.Marshal(errorResponse{Error: errorReasonFor(recorder.status)})
		if err != nil {
			return
		}
		_, _ = recorder.ResponseWriter.Write(append(body, '\n'))
	})
}

// requestLevel maps a response status to a log level: server errors are
// errors, client errors are warnings, and everything else is informational.
func requestLevel(status int) slog.Level {
	switch {
	case status >= http.StatusInternalServerError:
		return slog.LevelError
	case status >= http.StatusBadRequest:
		return slog.LevelWarn
	default:
		return slog.LevelInfo
	}
}

// statusRecorder captures the status a handler writes so the request logger can
// report it and so a body-less error can be completed. The first status wins:
// net/http ignores later WriteHeader calls.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	wroteBody   bool
}

func (rec *statusRecorder) WriteHeader(status int) {
	if !rec.wroteHeader {
		rec.wroteHeader = true
		rec.status = status
	}
	rec.ResponseWriter.WriteHeader(status)
}

func (rec *statusRecorder) Write(b []byte) (int, error) {
	if len(b) > 0 {
		rec.wroteBody = true
	}
	return rec.ResponseWriter.Write(b)
}
