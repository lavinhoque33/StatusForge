// Command lambda-worker runs SQS work messages through the shared slot runner
// (ADR 0008 D2, D3). It refuses to start outside AWS Lambda.
package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"

	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/cloudtargetpolicy"
	"github.com/lavinhoque33/statusforge/backend/internal/cloudwork"
	"github.com/lavinhoque33/statusforge/backend/internal/hosteddynamo"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func main() { os.Exit(run(os.LookupEnv, os.Stderr)) }

func run(lookup func(string) (string, bool), stderr io.Writer) int {
	runtimeAPI, ok := lookup(cloudwork.RuntimeAPIVariable)
	if !ok || runtimeAPI == "" {
		fmt.Fprintln(stderr, "not running in AWS Lambda")
		return 1
	}
	cfg, err := cloudwork.LoadConfig(lookup, cloudwork.WorkerBinary)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	policy, err := cloudtargetpolicy.Parse(cfg.Targets, runtimeAPI)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	logger := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	awsCfg, err := hosteddynamo.Load(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	persistence := store.New(hosteddynamo.DynamoDB(awsCfg), cfg.Table, time.Second, time.Now)
	persistence.SetNotifications(store.NotificationsNone)
	worker := cloudwork.Worker{
		Store:   persistence,
		Checker: checker.New(policy, time.Now),
		Now:     time.Now,
		Logger:  logger,
	}
	logger.Info("starting", "binary", "lambda-worker", "targets_empty", policy.Empty())
	lambda.Start(func(ctx context.Context, event events.SQSEvent) (events.SQSEventResponse, error) {
		if err := persistence.Validate(ctx); err != nil {
			logger.Error("hosted validation failed", "error", err.Error())
			return events.SQSEventResponse{}, err
		}
		return worker.Handle(ctx, event)
	})
	return 0
}
