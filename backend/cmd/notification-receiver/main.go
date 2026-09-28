// Command notification-receiver runs the loopback-only local notification fixture.
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

	"github.com/lavinhoque33/statusforge/backend/internal/notifyreceiver"
)

func main() {
	if err := run(); err != nil {
		slog.Error("notification receiver failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := notifyreceiver.LoadConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	fixture, err := notifyreceiver.New(cfg)
	if err != nil {
		return err
	}
	srv := fixture.HTTPServer(cfg.Addr)
	srv.ErrorLog = slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	slog.Info(
		"notification receiver listening (development fixture, no authentication, loopback only)",
		"addr",
		cfg.Addr,
		"mode",
		fixture.Mode(),
	)

	select {
	case err := <-serveErr:
		return serveResult(cfg.Addr, err)
	case <-ctx.Done():
	}
	stop()
	slog.Info("notification receiver shutting down", "timeout", notifyreceiver.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), notifyreceiver.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	if err := serveResult(cfg.Addr, <-serveErr); err != nil {
		return err
	}
	slog.Info("notification receiver stopped")
	return nil
}

func serveResult(addr string, err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve %s: %w", addr, err)
}
