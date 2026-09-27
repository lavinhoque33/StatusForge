// Package httpapi builds the StatusForge HTTP handler: the health endpoints
// used by operators and the dashboard, the JSON error contract for unknown API
// routes, and the middleware chain that keeps responses observable and safe.
//
// The package depends only on the standard library and the chi router. The
// composition root (cmd/statusforge) owns configuration, dependency clients,
// and the decision of which endpoints it is willing to name in a log line.
package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// DefaultReadinessTimeout bounds one readiness probe when the caller passes a
// non-positive timeout. A readiness probe must answer promptly; an unbounded
// probe would hold the request open for as long as a broken dependency takes.
const DefaultReadinessTimeout = 2 * time.Second

// apiPrefix is the mount point of every route this package serves.
const apiPrefix = "/api"

// dependencyReportKey is the role key the M0 storage dependency is reported
// under in the readiness body. Dependency.Name() is the host-safe label for
// logs, so it must not be published to clients; the response names the
// dependency role instead. A second role needs a key of its own, and adding one
// means changing this package and the readiness contract with the web client.
const dependencyReportKey = "dynamodb"

// Dependency is one named thing the API needs before it can serve traffic.
//
// Name returns a host-safe label — an endpoint host, never a URL with
// credentials, a port, or a path — because the value is written to logs.
//
// Check reports whether the dependency is reachable. It must honor the
// context deadline; a check that blocks forever is abandoned by the readiness
// probe rather than holding the response open.
type Dependency interface {
	Name() string
	Check(ctx context.Context) error
}

// NewRouter returns the API handler.
//
// Routes and bodies:
//
//	GET /api/health/live   {"status":"ok"}                       always, and never checks a dependency
//	GET /api/health/ready  {"status":"ready","checkedAt":"<RFC3339 UTC>","dependencies":{"<role>":{"status":"ready"}}}
//	                       or 503 {"status":"degraded",...} with a coarse reason per dependency
//	unknown API path       404 {"error":"not_found"}
//
// Every /api response is JSON and carries the same security headers, and every
// request is logged with slog as method, path, status, duration_ms, and
// request_id.
//
// A nil logger falls back to slog.Default, a nil now to time.Now, and a
// non-positive readinessTimeout to DefaultReadinessTimeout.
func NewRouter(logger *slog.Logger, deps []Dependency, readinessTimeout time.Duration, now func() time.Time) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if readinessTimeout <= 0 {
		readinessTimeout = DefaultReadinessTimeout
	}

	r := newMux(logger, now)
	r.Get(apiPrefix+"/health/live", handleLive)
	r.Get(apiPrefix+"/health/ready", handleReady(logger, deps, readinessTimeout, now))
	r.NotFound(jsonErrorHandler(http.StatusNotFound, errorNotFound))
	return r
}

// newMux returns a router with the API middleware chain installed.
func newMux(logger *slog.Logger, now func() time.Time) *chi.Mux {
	r := chi.NewRouter()
	r.Use(middlewareChain(logger, now)...)
	return r
}

// middlewareChain returns the API middleware chain, outermost first:
//
//	RequestID       so every later log line can carry request_id
//	requestIDHeader so the client can quote the ID from the response
//	securityHeaders so even an error or panic response is hardened
//	requestLogger   so every request is logged with its final status
//	recoverPanics   so a handler bug becomes a JSON 500, not a crash
//	jsonErrorBodies so chi's body-less errors still answer with JSON
//
// middleware.RealIP is deliberately absent: X-Forwarded-For is client supplied
// and this local-first service has no validated use for a client address.
func middlewareChain(logger *slog.Logger, now func() time.Time) []func(http.Handler) http.Handler {
	return []func(http.Handler) http.Handler{
		middleware.RequestID,
		requestIDHeader,
		securityHeaders,
		requestLogger(logger, now),
		recoverPanics(logger),
		jsonErrorBodies,
	}
}

// handleLive reports that the process is running. It deliberately checks
// nothing else: a liveness failure means "restart me", and restarting does not
// repair a dependency.
func handleLive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, liveResponse{Status: statusOK})
}

// handleReady reports the state of every dependency as of now. The body stays
// coarse — status and reason classes only — while the underlying error is
// logged for the operator.
func handleReady(logger *slog.Logger, deps []Dependency, timeout time.Duration, now func() time.Time) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		results := checkDependencies(r.Context(), logger, deps, timeout)
		report := healthResponse{
			Status:       statusReady,
			CheckedAt:    now().UTC().Format(time.RFC3339),
			Dependencies: make(map[string]dependencyHealth, len(results)),
		}
		for _, result := range results {
			if result.reason != "" {
				report.Status = statusDegraded
			}
			report.Dependencies[dependencyReportKey] = dependencyHealth{Status: result.status, Reason: result.reason}
		}
		if report.Status == statusDegraded {
			writeJSON(w, http.StatusServiceUnavailable, report)
			return
		}
		writeJSON(w, http.StatusOK, report)
	}
}

// dependencyResult is the coarse outcome of one readiness check: the host-safe
// name for the log, plus the status and reason the report publishes.
type dependencyResult struct {
	name   string
	status string
	reason string
}

// checkDependencies probes every dependency concurrently and reports one result
// per dependency, in the order they were configured.
//
// The probe is abandoned as soon as its deadline passes, so a dependency that
// ignores its context delays one readiness answer but never hangs it. A check
// that panics is contained: it is reported as a failing dependency and the
// process keeps serving.
func checkDependencies(ctx context.Context, logger *slog.Logger, deps []Dependency, timeout time.Duration) []dependencyResult {
	results := make([]dependencyResult, len(deps))
	for i, dep := range deps {
		results[i] = dependencyResult{name: dependencyName(dep), status: dependencyReady}
	}
	if len(deps) == 0 {
		return results
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	errs := make([]error, len(deps))
	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i, dep := range deps {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = checkDependency(ctx, logger, dep)
			}()
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-ctx.Done():
		// The probe deadline passed before every check answered: no dependency
		// is confirmed reachable, and the abandoned checks are not awaited.
		logger.WarnContext(ctx, "readiness probe timed out", "timeout", timeout, "dependencies", len(deps))
		for i := range results {
			results[i].status = dependencyUnavailable
			results[i].reason = reasonTimeout
		}
		return results
	}

	for i, err := range errs {
		if err == nil {
			continue
		}
		logger.WarnContext(ctx, "dependency check failed", "dependency", results[i].name, "error", err)
		results[i].status = dependencyUnavailable
		results[i].reason = dependencyReason(err)
	}
	return results
}

// checkDependency runs one dependency check and converts a panic into an error
// so that a bug in a dependency client cannot crash the server.
func checkDependency(ctx context.Context, logger *slog.Logger, dep Dependency) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.ErrorContext(ctx, "dependency check panicked",
				"dependency", dependencyName(dep),
				"panic", recovered,
				"stack", string(debug.Stack()),
			)
			err = fmt.Errorf("check panicked: %v", recovered)
		}
	}()

	if dep == nil {
		return errors.New("dependency is not configured")
	}
	return dep.Check(ctx)
}

// dependencyReason classifies a failed check into one coarse reason class.
func dependencyReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded):
		return reasonTimeout
	case isUnreachable(err):
		return reasonUnreachable
	default:
		return reasonError
	}
}

// unreachableErrors are the connection-level errno values that mean the
// endpoint could not be reached at all.
var unreachableErrors = [...]error{
	syscall.ECONNREFUSED,
	syscall.ECONNRESET,
	syscall.EHOSTUNREACH,
	syscall.ENETUNREACH,
	syscall.ETIMEDOUT,
}

// isUnreachable reports whether err is a connection-level failure — a refused
// or timed-out dial, an unresolvable host, a reset connection — rather than
// something the dependency said about its own state.
func isUnreachable(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	for _, unreachable := range unreachableErrors {
		if errors.Is(err, unreachable) {
			return true
		}
	}
	return false
}

// dependencyName returns the dependency's host-safe log label defensively: a
// nil dependency must surface as an unavailable dependency, not as a panic.
func dependencyName(dep Dependency) string {
	if dep == nil {
		return "unknown"
	}
	if name := dep.Name(); name != "" {
		return name
	}
	return "unknown"
}

// isAPIPath reports whether the request path is served by this API. Security
// headers are scoped to it so that a non-API surface — for example a future
// static build of the web client — does not inherit the API's document-blocking
// content policy.
func isAPIPath(path string) bool {
	return path == apiPrefix || strings.HasPrefix(path, apiPrefix+"/")
}
