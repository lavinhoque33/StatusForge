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

	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/config"
	"github.com/lavinhoque33/statusforge/backend/internal/httpapi"
	"github.com/lavinhoque33/statusforge/backend/internal/localdynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/notify"
	"github.com/lavinhoque33/statusforge/backend/internal/scheduler"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
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
	dependency := localdynamo.New(
		cfg.DynamoDBEndpoint,
		host,
		cfg.DynamoDBRegion,
		cfg.DynamoDBAccessKeyID,
		cfg.DynamoDBSecretAccessKey,
	)
	policy, err := targetpolicy.Parse(cfg.AllowedTargets)
	if err != nil {
		return err
	}
	persistence := store.New(dependency, cfg.DynamoDBTable, cfg.ReadinessTimeout, time.Now)
	persistence.SetReminderInterval(time.Duration(cfg.ReminderIntervalSeconds) * time.Second)
	notifyPolicy, err := notify.PolicyForURL(cfg.NotifyURL)
	if err != nil {
		return err
	}
	runner := checker.New(policy, time.Now)
	server := &http.Server{
		Addr: cfg.HTTPAddr,
		Handler: httpapi.NewMonitorRouter(
			logger,
			[]httpapi.Dependency{dependency},
			cfg.ReadinessTimeout,
			time.Now,
			persistence,
			runner,
			policy,
			cfg.MinIntervalSeconds,
		),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      40 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	listener, err := net.Listen("tcp", cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	defer listener.Close()
	logger.Info(
		"starting",
		"addr",
		cfg.HTTPAddr,
		"dynamodb_endpoint_host",
		host,
		"level",
		cfg.LogLevel,
	)
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ReadinessTimeout)
	if err := dependency.Ping(ctx); err != nil {
		logger.Warn("initial readiness degraded", "dynamodb_endpoint_host", host, "error", err)
	} else {
		logger.Info("initial readiness ready", "dynamodb_endpoint_host", host)
	}
	cancel()
	tableCtx, tableCancel := context.WithTimeout(context.Background(), cfg.ReadinessTimeout*5)
	if err := persistence.Initialize(tableCtx); err != nil {
		logger.Warn("initial table creation degraded", "reason", "dependency_failure")
	}
	tableCancel()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	schedulerCtx, stopScheduler := context.WithCancel(context.Background())
	schedulerDone := make(chan struct{})
	if cfg.SchedulerEnabled {
		go func() {
			defer close(schedulerDone)
			(&scheduler.Scheduler{Store: persistence, Runner: runner, Workers: cfg.Workers, Logger: logger, ReminderInterval: time.Duration(cfg.ReminderIntervalSeconds) * time.Second}).Run(
				schedulerCtx,
			)
		}()
	} else {
		close(schedulerDone)
	}
	defer func() { stopScheduler(); <-schedulerDone }()
	serveErrors := make(chan error, 1)
	deliveryCtx, stopDelivery := context.WithCancel(context.Background())
	deliveryDone := make(chan struct{})
	go func() {
		defer close(deliveryDone)
		(&notify.Worker{Store: persistence, URL: cfg.NotifyURL, Policy: notifyPolicy, Workers: cfg.DeliveryWorkers, Schedule: cfg.DeliveryRetrySchedule, Logger: logger}).Run(
			deliveryCtx,
		)
	}()
	defer func() { stopDelivery(); <-deliveryDone }()
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
	stopScheduler()
	<-schedulerDone
	logger.Info("stopped")
	return nil
}
