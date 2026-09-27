// Command sample-target runs StatusForge's development-only sample target
// fixture: a controlled HTTP server used to exercise monitoring without
// touching real services.
//
// The fixture has no authentication; see package sampletarget for the routes,
// the JSON controls, and the loopback-only safety rule it enforces at startup.
// Never expose it beyond the local machine.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/lavinhoque33/statusforge/backend/internal/sampletarget"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sample target failed", "error", err)
		os.Exit(1)
	}
}

// run starts the fixture and blocks until a termination signal arrives, then
// shuts the server down gracefully within sampletarget.ShutdownTimeout.
func run() error {
	cfg, err := sampletarget.LoadConfig(os.LookupEnv)
	if err != nil {
		return err
	}

	fixture, err := sampletarget.New(cfg)
	if err != nil {
		return err
	}

	srv := fixture.HTTPServer(cfg.Addr)
	srv.ErrorLog = slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()

	slog.Info("sample target listening (development fixture, no authentication, loopback only)",
		"addr", cfg.Addr,
		"mode", fixture.Mode(),
		"slow_delay", cfg.SlowDelay,
	)

	select {
	case err := <-serveErr:
		return serveResult(cfg.Addr, err)
	case <-ctx.Done():
	}

	// Restore default signal handling so a second signal interrupts a slow
	// shutdown immediately.
	stop()
	slog.Info("sample target shutting down", "timeout", sampletarget.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), sampletarget.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := serveResult(cfg.Addr, <-serveErr); err != nil {
		return err
	}

	slog.Info("sample target stopped")
	return nil
}

// serveResult converts the result of ListenAndServe into a run() result. A
// listener closed by Shutdown is the expected outcome, not a failure.
func serveResult(addr string, err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve %s: %w", addr, err)
}
