package checker

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

type Runner struct {
	client *http.Client
	Now    func() time.Time
}

type (
	// Dialer is the destination policy every plain connection goes through.
	Dialer interface {
		DialContext(ctx context.Context, network, address string) (net.Conn, error)
	}
	// TLSDialer also completes TLS itself, so HTTPS connections go only to
	// the address the policy pinned (the cloud destination policy).
	TLSDialer interface {
		Dialer
		DialTLSContext(ctx context.Context, network, address string) (net.Conn, error)
	}
)

// New builds a runner whose every connection goes through policy: no proxy,
// no redirects, no keep-alives, and the M1 header and body bounds.
func New(policy Dialer, now func() time.Time) *Runner {
	if now == nil {
		now = time.Now
	}
	transport := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		MaxResponseHeaderBytes: 65536,
		DisableCompression:     true,
		ForceAttemptHTTP2:      false,
		DialContext:            policy.DialContext,
	}
	if tlsPolicy, ok := policy.(TLSDialer); ok {
		transport.DialTLSContext = tlsPolicy.DialTLSContext
	}
	return &Runner{
		client: &http.Client{
			Transport:     transport,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		Now: now,
	}
}

func (r *Runner) Run(ctx context.Context, m monitor.Monitor) (o monitor.Observation) {
	started := r.Now()
	if m.Lease != nil && m.Lease.StartedAt != "" {
		if claimed, err := time.Parse(time.RFC3339Nano, m.Lease.StartedAt); err == nil {
			started = claimed
		}
	}
	startMono := time.Now()
	o = monitor.Observation{
		Kind:          "http_check",
		ID:            rand.Text(),
		MonitorID:     m.ID,
		ConfigVersion: m.ConfigVersion,
		InitiatedBy:   "manual",
		Request:       m.Check,
		StartedAt:     monitor.Stamp(started),
		Outcome:       "checker_problem",
		Reason:        "internal",
	}
	defer func() {
		if recover() != nil {
			o.Outcome = "checker_problem"
			o.Reason = "internal"
			o.ObservedStatus = nil
		}
		o.CompletedAt = monitor.Stamp(r.Now())
		o.DurationMs = time.Since(startMono).Milliseconds()
	}()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.Check.DeadlineMs)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, m.Check.Method, m.Check.URL, nil)
	if err != nil {
		return
	}
	req.Header.Set("User-Agent", "StatusForge/M1 (local)")
	req.Header.Set("Accept", "*/*")
	resp, err := r.client.Do(req)
	if err != nil {
		classify(&o, err, ctx)
		return
	}
	defer resp.Body.Close()
	o.ObservedStatus = &resp.StatusCode
	n, err := io.Copy(io.Discard, io.LimitReader(resp.Body, int64(m.Check.MaxBodyBytes)+1))
	o.BodyBytesRead = int(n)
	if n > int64(m.Check.MaxBodyBytes) {
		o.BodyBytesRead = m.Check.MaxBodyBytes
		o.BodyTruncated = true
	}
	if err != nil || ctx.Err() != nil {
		classify(&o, err, ctx)
		return
	}
	if resp.StatusCode == m.Check.ExpectedStatus {
		o.Outcome = "healthy"
		o.Reason = "ok"
	} else {
		o.Outcome = "failing"
		o.Reason = "wrong_status"
	}
	return
}

func classify(o *monitor.Observation, err error, ctx context.Context) {
	switch {
	case errors.Is(err, targetpolicy.ErrRefused):
		o.Outcome = "checker_problem"
		o.Reason = "refused_by_policy"
		o.ObservedStatus = nil
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		o.Outcome = "failing"
		o.Reason = "timeout"
	case errors.Is(err, syscall.ECONNREFUSED):
		o.Outcome = "failing"
		o.Reason = "connection_refused"
	default:
		o.Outcome = "failing"
		o.Reason = "connection_error"
	}
}

func Log(logger *slog.Logger, o monitor.Observation) {
	level := slog.LevelInfo
	if o.Outcome == "checker_problem" {
		level = slog.LevelWarn
	}
	attrs := []slog.Attr{
		slog.String("monitor_id", o.MonitorID),
		slog.String("observation_id", o.ID),
		slog.Int("config_version", o.ConfigVersion),
		slog.String("outcome", o.Outcome),
		slog.String("reason", o.Reason),
		slog.Int64("duration_ms", o.DurationMs),
		slog.Int("body_bytes_read", o.BodyBytesRead),
		slog.Bool("body_truncated", o.BodyTruncated),
		slog.String("initiated_by", o.InitiatedBy),
		slog.Bool("counted", o.Counted),
	}
	if o.ObservedStatus != nil {
		attrs = append(attrs, slog.Int("observed_status", *o.ObservedStatus))
	}
	if o.Trigger != nil {
		attrs = append(attrs, slog.String("trigger", *o.Trigger))
	} else {
		attrs = append(attrs, slog.Any("trigger", nil))
	}
	if o.NotCountedReason != nil {
		attrs = append(attrs, slog.String("not_counted_reason", *o.NotCountedReason))
	} else {
		attrs = append(attrs, slog.Any("not_counted_reason", nil))
	}
	logger.LogAttrs(context.Background(), level, "check", attrs...)
}
