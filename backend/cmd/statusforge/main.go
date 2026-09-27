package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/config"
	"github.com/lavinhoque33/statusforge/backend/internal/httpapi"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return err
	}
	var level slog.Level
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(os.Stderr, opts)
	if cfg.LogFormat == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}
	logger := slog.New(handler)
	u, _ := url.Parse(cfg.DynamoDBEndpoint) // validated by config.Load
	host := u.Hostname()
	dependency := localdynamo.New(cfg.DynamoDBEndpoint, host, cfg.DynamoDBRegion, cfg.DynamoDBAccessKeyID, cfg.DynamoDBSecretAccessKey)
	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpapi.NewRouter(logger, []httpapi.Dependency{dependency}, cfg.ReadinessTimeout, time.Now),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	logger.Info("starting", "addr", cfg.HTTPAddr, "dynamodb_endpoint_host", host, "level", cfg.LogLevel)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ReadinessTimeout)
	if err := dependency.Ping(ctx); err != nil {
		logger.Warn("initial readiness degraded", "dynamodb_endpoint_host", host, "error", err)
	} else {
		logger.Info("initial readiness ready", "dynamodb_endpoint_host", host)
	}
	cancel()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	select {
	case err := <-serveErrors:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("serve: %w", err)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			return fmt.Errorf("shutdown: %w", err)
		}
		<-serveErrors
	}
	logger.Info("stopped")
	return nil
}
