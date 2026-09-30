// Package checkwork runs one scheduled slot: claim → check under the
// monitor's deadline → record. The local scheduler and the cloud worker share
// it, so the eligibility, lease, and deadline rules exist once (ADR 0008 D3).
package checkwork

import (
	"context"
	"errors"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// Outcome classifies one RunSlot call.
type Outcome string

const (
	// Recorded: claimed, checked, and the result stored (counted or not).
	Recorded Outcome = "recorded"
	// NotEligible: done, cancelled, stale configuration version, or inactive.
	NotEligible Outcome = "not_eligible"
	// LeaseHeld: another claim holds the monitor lease.
	LeaseHeld Outcome = "lease_held"
	// Expired: closed as an overdue or lease_expired gap instead of claimed.
	Expired Outcome = "expired"
	// DependencyFailure: a claim or record call failed transiently.
	DependencyFailure Outcome = "dependency_failure"
)

// StoreTimeout bounds the claim call and the record call separately.
const StoreTimeout = 5 * time.Second

type (
	Store interface {
		Claim(context.Context, store.Work, time.Time) (monitor.Monitor, string, error)
		RecordResult(context.Context, monitor.Observation, string) (monitor.Observation, error)
	}
	Checker interface {
		Run(context.Context, monitor.Monitor) monitor.Observation
	}
	// Runner runs slots against Store with Checker.
	Runner struct {
		Store   Store
		Checker Checker
		// Claimed, if set, runs right after a successful claim and before the
		// check, with the claim time.
		Claimed func(w store.Work, m monitor.Monitor, now time.Time)
	}
	// Result is one RunSlot outcome with the evidence callers log or count.
	Result struct {
		Outcome Outcome
		// Reason is "overdue" or "lease_expired" for Expired.
		Reason string
		// Stage is "claim" or "record" for DependencyFailure.
		Stage string
		// Monitor is the monitor as read by the claim, when it was read.
		Monitor monitor.Monitor
		// Closed reports that the claim closed the item instead of leasing it.
		Closed bool
		// Observation is the stored observation for Recorded.
		Observation monitor.Observation
	}
)

// RunSlot claims work, runs the check under the monitor's deadline, and
// records the result with the work's trigger and dueAt. The claim and record
// calls each run under StoreTimeout, derived from ctx.
func (r Runner) RunSlot(ctx context.Context, w store.Work, now time.Time) Result {
	if w.State != "pending" && w.State != "claimed" {
		return Result{Outcome: NotEligible}
	}
	claimCtx, cancel := context.WithTimeout(ctx, StoreTimeout)
	m, token, err := r.Store.Claim(claimCtx, w, now)
	cancel()
	switch {
	case errors.Is(err, store.ErrLeaseHeld):
		return Result{Outcome: LeaseHeld, Monitor: m}
	case errors.Is(err, store.ErrNotEligible):
		return Result{Outcome: NotEligible, Monitor: m}
	case err != nil:
		return Result{Outcome: DependencyFailure, Stage: "claim", Monitor: m}
	case token == "":
		return closed(w, m)
	}
	if r.Claimed != nil {
		r.Claimed(w, m, now)
	}
	checkCtx, stop := context.WithTimeout(
		ctx,
		time.Duration(m.Check.DeadlineMs)*time.Millisecond,
	)
	o := r.Checker.Run(checkCtx, m)
	stop()
	o.InitiatedBy = "scheduled"
	o.Trigger = &w.Trigger
	o.DueAt = &w.DueAt
	saveCtx, saveCancel := context.WithTimeout(ctx, StoreTimeout)
	o, err = r.Store.RecordResult(saveCtx, o, token)
	saveCancel()
	if err != nil {
		return Result{Outcome: DependencyFailure, Stage: "record", Monitor: m}
	}
	return Result{Outcome: Recorded, Monitor: m, Observation: o}
}

// closed classifies a claim that closed the item: the same order as the
// store's Claim, so an inactive or re-versioned monitor is never a gap.
func closed(w store.Work, m monitor.Monitor) Result {
	r := Result{Outcome: NotEligible, Monitor: m, Closed: true}
	if m.Lifecycle != "active" || m.ConfigVersion != w.ConfigVersion {
		return r
	}
	r.Outcome = Expired
	r.Reason = "lease_expired"
	if w.State == "pending" {
		r.Reason = "overdue"
	}
	return r
}
