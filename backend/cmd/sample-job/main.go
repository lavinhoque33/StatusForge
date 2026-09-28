// Command sample-job runs the loopback-only sample heartbeat job fixture: a
// scheduled job that reports to a heartbeat ingest endpoint every interval so
// local development and drills can exercise heartbeats.
//
// The reporting token is read from the environment and never logged; the
// startup line records only whether it is set.
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

	"github.com/lavinhoque33/statusforge/backend/internal/samplejob"
)

func main() {
	if err := run(); err != nil {
		slog.Error("sample job failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := samplejob.LoadConfig(os.LookupEnv)
	if err != nil {
		return err
	}
	fixture, err := samplejob.New(cfg)
	if err != nil {
		return err
	}
	srv := fixture.HTTPServer(cfg.Addr)
	srv.ErrorLog = slog.NewLogLogger(slog.Default().Handler(), slog.LevelWarn)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		fixture.Run(ctx)
	}()

	logStartup(cfg, fixture)

	select {
	case err := <-serveErr:
		stop()
		<-runDone
		return serveResult(cfg.Addr, err)
	case <-ctx.Done():
	}
	stop()
	slog.Info("sample job shutting down", "timeout", samplejob.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), samplejob.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		_ = srv.Close()
		<-runDone
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	<-runDone
	if err := serveResult(cfg.Addr, <-serveErr); err != nil {
		return err
	}
	slog.Info("sample job stopped")
	return nil
}

// logStartup records only whether reporting credentials are configured.
func logStartup(cfg samplejob.Config, fixture *samplejob.Server) {
	slog.Info(
		"sample job listening (development fixture, no authentication, loopback only)",
		"addr",
		cfg.Addr,
		"interval",
		cfg.Interval.String(),
		"mode",
		fixture.Mode(),
		"reportUrlSet",
		cfg.ReportURL != "",
		"tokenSet",
		cfg.Token != "",
		"reporting",
		cfg.Reporting(),
	)
}

func serveResult(addr string, err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return fmt.Errorf("serve %s: %w", addr, err)
}
