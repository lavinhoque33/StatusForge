package notify

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

type (
	Persistence interface {
		Due(context.Context, time.Time) ([]store.Due, error)
		ClaimDelivery(
			context.Context,
			store.Due,
			time.Time,
		) (store.Notification, store.Attempt, string, error)
		CompleteDelivery(
			context.Context,
			store.Due,
			store.Notification,
			store.Attempt,
			string,
			string,
			*int,
			int64,
			time.Time,
			[]time.Duration,
		) (string, error)
	}
	Worker struct {
		Store    Persistence
		URL      string
		Policy   *targetpolicy.Policy
		Workers  int
		Schedule []time.Duration
		Now      func() time.Time
		Logger   *slog.Logger
	}
)

func (w *Worker) Send(ctx context.Context, n store.Notification) (string, *int, int64) {
	start := time.Now()
	var written atomic.Bool
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		w.URL,
		bytes.NewBufferString(n.Payload),
	)
	if err != nil {
		return "connection_error", nil, 0
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "StatusForge/0.6 (local)")
	req.Header.Set("Idempotency-Key", n.ID)
	req = req.WithContext(
		httptrace.WithClientTrace(
			req.Context(),
			&httptrace.ClientTrace{WroteRequest: func(info httptrace.WroteRequestInfo) {
				if info.Err == nil {
					written.Store(true)
				}
			}},
		),
	)
	transport := &http.Transport{
		Proxy:             nil,
		DisableKeepAlives: true,
		DialContext:       w.Policy.DialContext,
	}
	client := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	duration := time.Since(start).Milliseconds()
	if err != nil {
		var netErr net.Error
		switch {
		case errors.Is(err, targetpolicy.ErrRefused):
			return "refused_by_policy", nil, duration
		case errors.Is(err, syscall.ECONNREFUSED):
			return "connection_refused", nil, duration
		case written.Load():
			return "outcome_unknown", nil, duration
		case errors.Is(err, context.DeadlineExceeded) || errors.As(err, &netErr) && netErr.Timeout():
			return "timeout", nil, duration
		default:
			return "connection_error", nil, duration
		}
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	status := resp.StatusCode
	if status >= 200 && status < 300 {
		return "delivered", &status, duration
	}
	if status == 408 || status == 429 || status >= 500 {
		return "http_error", &status, duration
	}
	return "rejected", &status, duration
}

func (w *Worker) Run(ctx context.Context) {
	now := w.Now
	if now == nil {
		now = time.Now
	}
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}
	workers := w.Workers
	if workers < 1 {
		workers = 1
	}
	jobs := make(chan store.Due, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range jobs {
				claimCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				n, a, token, err := w.Store.ClaimDelivery(claimCtx, d, now())
				cancel()
				if err != nil {
					if !errors.Is(err, store.ErrNotEligible) {
						logger.Warn("delivery claim failed", "reason", "dependency_failure")
					}
					continue
				}
				sendCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
				result, status, duration := w.Send(sendCtx, n)
				stop()
				completeCtx, done := context.WithTimeout(context.Background(), 5*time.Second)
				state, err := w.Store.CompleteDelivery(
					completeCtx,
					d,
					n,
					a,
					token,
					result,
					status,
					duration,
					now(),
					w.Schedule,
				)
				done()
				if err != nil {
					logger.Warn("delivery completion failed", "reason", "dependency_failure")
					continue
				}
				level := slog.LevelInfo
				if state == "failed" {
					level = slog.LevelWarn
				}
				var httpStatus any
				if status != nil {
					httpStatus = *status
				}
				logger.Log(
					context.Background(),
					level,
					"delivery attempt",
					"monitor_id",
					d.MonitorID,
					"incident_id",
					d.IncidentID,
					"notification",
					n.ID,
					"attempt",
					a.Number,
					"result",
					result,
					"http_status",
					httpStatus,
					"duration_ms",
					duration,
					"next_state",
					state,
				)
			}
		}()
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	poll := func() {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		due, err := w.Store.Due(fetchCtx, now())
		cancel()
		if err != nil {
			logger.Warn("delivery poll failed", "reason", "dependency_failure")
			return
		}
		for _, d := range due {
			select {
			case jobs <- d:
			case <-ctx.Done():
				return
			}
		}
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			close(jobs)
			done := make(chan struct{})
			go func() { wg.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
			return
		case <-ticker.C:
			poll()
		}
	}
}

func PolicyForURL(raw string) (*targetpolicy.Policy, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	port := u.Port()
	if port == "" {
		port = "80"
	}
	return targetpolicy.Parse(net.JoinHostPort(u.Hostname(), port))
}
