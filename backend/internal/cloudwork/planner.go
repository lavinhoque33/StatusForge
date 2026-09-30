package cloudwork

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// PassBudget bounds one planner pass (the function timeout is 50 s).
const PassBudget = 45 * time.Second

// BatchSize is SendMessageBatch's limit.
const BatchSize = 10

type (
	PlannerStore interface {
		ReconcileStore
		TickCycle(context.Context, time.Time) (store.TickReport, error)
		RetentionStep(context.Context) (int, error)
	}
	// Queue sends up to BatchSize message bodies and returns the indexes of
	// the bodies that were not accepted. An error means none was accepted.
	Queue interface {
		SendBatch(ctx context.Context, bodies []string) (failed []int, err error)
	}
	// Planner runs one pass per invocation (ADR 0008 D1). It keeps no state
	// between passes that its correctness depends on: the durable rows decide.
	Planner struct {
		Store  PlannerStore
		Queue  Queue
		Policy URLPolicy
		// Monitors is the raw STATUSFORGE_CLOUD_MONITORS value.
		Monitors string
		Now      func() time.Time
		Logger   *slog.Logger
	}
	PassReport struct {
		Monitors       int
		Reconciled     ReconcileReport
		WorkCreated    int
		GapsCreated    int
		Expired        int
		Dispatched     int
		SendFailures   int
		RetentionSteps int
	}
)

// Pass reconciles the declared monitors, runs TickCycle, runs one retention
// step, and sends one message per dispatchable work item. An invalid
// declaration or an unavailable table fails the pass before dispatch.
// Per-monitor tick and retention failures are logged, the rest of the pass
// still runs, and the joined error fails the invocation afterwards. Send
// failures are logged only: the next pass sends the same rows again.
func (p Planner) Pass(ctx context.Context) (PassReport, error) {
	started := time.Now()
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}
	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	ctx, cancel := context.WithTimeout(ctx, PassBudget)
	defer cancel()
	var report PassReport
	at := now()
	decls, err := ParseDeclarations(p.Monitors, p.Policy)
	if err != nil {
		logger.Error("planner pass failed", "reason", "invalid_declaration", "error", err.Error())
		return report, err
	}
	report.Reconciled, err = Reconcile(ctx, p.Store, p.Policy, decls, at)
	if err != nil {
		reason := "dependency_failure"
		var invalid DeclarationError
		if errors.As(err, &invalid) {
			reason = "invalid_declaration"
		}
		logger.Error("planner pass failed", "reason", reason, "error", err.Error())
		return report, err
	}
	tick, err := p.Store.TickCycle(ctx, at)
	if err != nil {
		logger.Error("planner pass failed", "reason", "dependency_failure", "stage", "tick")
		return report, err
	}
	report.Monitors = tick.Active
	report.WorkCreated = tick.Created
	report.GapsCreated = tick.Gaps
	report.Expired = tick.Expired
	var failures []error
	if tick.Failed > 0 {
		logger.Warn(
			"planner tick failed",
			"reason",
			"dependency_failure",
			"monitors_failed",
			tick.Failed,
		)
		failures = append(failures, fmt.Errorf("tick: %d monitors failed: %w", tick.Failed,
			store.ErrUnavailable))
	}
	report.RetentionSteps, err = p.Store.RetentionStep(ctx)
	if err != nil {
		logger.Warn("planner retention failed", "reason", "dependency_failure")
		failures = append(failures, fmt.Errorf("retention: %w", err))
	}
	// A failed send is logged and left for the next pass, which sends it.
	report.Dispatched, report.SendFailures = p.dispatch(ctx, Dispatchable(tick, at), logger)
	logger.Info(
		"planner pass",
		"monitors", report.Monitors,
		"reconciled_created", report.Reconciled.Created,
		"reconciled_updated", report.Reconciled.Updated,
		"reconciled_archived", report.Reconciled.Archived,
		"work_created", report.WorkCreated,
		"gaps_created", report.GapsCreated,
		"expired", report.Expired,
		"dispatched", report.Dispatched,
		"send_failures", report.SendFailures,
		"retention_steps", report.RetentionSteps,
		"pass_ms", time.Since(started).Milliseconds(),
	)
	return report, errors.Join(failures...)
}

// dispatch sends in groups of BatchSize. A failed send is logged and left
// for the next pass: the durable row stays dispatchable.
func (p Planner) dispatch(
	ctx context.Context,
	works []store.Work,
	logger *slog.Logger,
) (sent, failed int) {
	for start := 0; start < len(works); start += BatchSize {
		group := works[start:min(start+BatchSize, len(works))]
		bodies := make([]string, len(group))
		for i, w := range group {
			bodies[i] = Message{MonitorID: w.MonitorID, DueAt: w.DueAt}.Encode()
		}
		rejected, err := p.Queue.SendBatch(ctx, bodies)
		if err != nil {
			logger.Warn(
				"planner send failed",
				"reason",
				"dependency_failure",
				"messages",
				len(group),
			)
			failed += len(group)
			continue
		}
		for _, i := range rejected {
			logger.Warn("planner send failed", "reason", "rejected",
				"monitor_id", group[i].MonitorID, "due_at", group[i].DueAt)
		}
		failed += len(rejected)
		sent += len(group) - len(rejected)
	}
	return sent, failed
}

// Dispatchable returns the work a pass sends, oldest first: pending with
// dueAt ≤ now < dueAt + interval, or claimed with an expired lease and fewer
// than two attempts (ADR 0008 D1).
func Dispatchable(tick store.TickReport, now time.Time) []store.Work {
	var result []store.Work
	for _, m := range tick.Monitors {
		if m.Lifecycle != "active" || m.Kind == "heartbeat" || m.Deletion != nil {
			continue
		}
		interval := time.Duration(m.IntervalSeconds) * time.Second
		for _, w := range tick.Open[m.ID] {
			due, err := time.Parse(time.RFC3339Nano, w.DueAt)
			if err != nil || w.ConfigVersion != m.ConfigVersion || now.Before(due) ||
				!now.Before(due.Add(interval)) {
				continue
			}
			switch w.State {
			case "pending":
				result = append(result, w)
			case "claimed":
				until, err := time.Parse(time.RFC3339Nano, w.LeaseUntil)
				if err == nil && now.After(until) && w.Attempts < 2 {
					result = append(result, w)
				}
			}
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].DueAt == result[j].DueAt {
			return result[i].MonitorID < result[j].MonitorID
		}
		return result[i].DueAt < result[j].DueAt
	})
	return result
}
