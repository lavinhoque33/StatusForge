package cloudwork

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aws/aws-lambda-go/events"

	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/checkwork"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// Worker-only outcomes beside checkwork's (ADR 0008 D2).
const (
	// Missing: no such work item or monitor. Acknowledged.
	Missing checkwork.Outcome = "missing"
	// InvalidMessage: bad JSON, unknown v, or malformed fields. Reported.
	InvalidMessage checkwork.Outcome = "invalid_message"
	// Deferred: not attempted, because too little invocation time remained
	// or an earlier record hit a dependency failure. Reported.
	Deferred checkwork.Outcome = "deferred"
)

// Headroom is the invocation time kept beyond the monitor's deadline.
const Headroom = 15 * time.Second

// Acknowledged reports whether an outcome removes the message.
func Acknowledged(o checkwork.Outcome) bool {
	switch o {
	case checkwork.Recorded, checkwork.NotEligible, checkwork.LeaseHeld, checkwork.Expired,
		Missing:
		return true
	}
	return false
}

type (
	WorkerStore interface {
		checkwork.Store
		GetWork(ctx context.Context, monitorID, dueAt string) (store.Work, error)
		Get(ctx context.Context, id string) (monitor.Monitor, error)
	}
	// Worker handles one SQS batch sequentially.
	Worker struct {
		Store   WorkerStore
		Checker checkwork.Checker
		Now     func() time.Time
		Logger  *slog.Logger
	}
)

// Handle runs each record and reports the unacknowledged ones in
// batchItemFailures. A target failure is recorded evidence, never a
// processing failure. After a dependency failure, or once less than the
// monitor's deadline plus Headroom remains, the remaining records are
// reported without being claimed.
func (w Worker) Handle(
	ctx context.Context,
	event events.SQSEvent,
) (events.SQSEventResponse, error) {
	started := time.Now()
	logger := w.Logger
	if logger == nil {
		logger = slog.Default()
	}
	counts := map[checkwork.Outcome]int{}
	var response events.SQSEventResponse
	stop := false
	for _, record := range event.Records {
		outcome := Deferred
		if !stop {
			outcome = w.record(ctx, record.Body, logger)
			stop = outcome == checkwork.DependencyFailure || outcome == Deferred
		}
		counts[outcome]++
		if !Acknowledged(outcome) {
			response.BatchItemFailures = append(
				response.BatchItemFailures,
				events.SQSBatchItemFailure{ItemIdentifier: record.MessageId},
			)
		}
	}
	logger.Info(
		"worker batch",
		"records", len(event.Records),
		"recorded", counts[checkwork.Recorded],
		"not_eligible", counts[checkwork.NotEligible],
		"lease_held", counts[checkwork.LeaseHeld],
		"expired", counts[checkwork.Expired],
		"missing", counts[Missing],
		"dependency_failure", counts[checkwork.DependencyFailure],
		"invalid_message", counts[InvalidMessage],
		"deferred", counts[Deferred],
		"batch_ms", time.Since(started).Milliseconds(),
	)
	return response, nil
}

func (w Worker) record(ctx context.Context, body string, logger *slog.Logger) checkwork.Outcome {
	msg, err := ParseMessage(body)
	if err != nil {
		return InvalidMessage
	}
	if remaining(ctx) < Headroom+time.Second {
		return Deferred
	}
	readCtx, cancel := context.WithTimeout(ctx, checkwork.StoreTimeout)
	work, err := w.Store.GetWork(readCtx, msg.MonitorID, msg.DueAt)
	cancel()
	switch {
	case errors.Is(err, store.ErrWorkNotFound):
		return Missing
	case err != nil:
		logger.Warn("work read failed", "reason", "dependency_failure")
		return checkwork.DependencyFailure
	case work.State != "pending" && work.State != "claimed":
		return checkwork.NotEligible
	}
	readCtx, cancel = context.WithTimeout(ctx, checkwork.StoreTimeout)
	m, err := w.Store.Get(readCtx, msg.MonitorID)
	cancel()
	switch {
	case errors.Is(err, store.ErrNotFound):
		return Missing
	case err != nil:
		logger.Warn("work read failed", "reason", "dependency_failure")
		return checkwork.DependencyFailure
	}
	if remaining(ctx) < time.Duration(m.Check.DeadlineMs)*time.Millisecond+Headroom {
		return Deferred
	}
	now := time.Now
	if w.Now != nil {
		now = w.Now
	}
	r := checkwork.Runner{Store: w.Store, Checker: w.Checker}.RunSlot(ctx, work, now())
	switch r.Outcome {
	case checkwork.Recorded:
		checker.Log(logger, r.Observation)
	case checkwork.DependencyFailure:
		logger.Warn("slot failed", "reason", "dependency_failure", "stage", r.Stage,
			"monitor_id", msg.MonitorID)
	}
	return r.Outcome
}

func remaining(ctx context.Context) time.Duration {
	deadline, ok := ctx.Deadline()
	if !ok {
		return time.Duration(1<<63 - 1)
	}
	return time.Until(deadline)
}
