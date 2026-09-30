package store

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// RunHousekeeping budgets each independent phase to two seconds. A broken job never
// prevents other phases from advancing. Durable cursors/jobs resume interrupted work.
func (s *Store) RunHousekeeping(ctx context.Context) error {
	if err := s.ensure(ctx); err != nil {
		return err
	}
	var failures []error
	backfillUntil := time.Now().Add(2 * time.Second)
	for ctx.Err() == nil && time.Now().Before(backfillUntil) {
		done, err := s.BackfillPage(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("backfill: %w", err))
			break
		}
		if done {
			break
		}
	}
	if _, err := s.retentionPhase(ctx); err != nil {
		failures = append(failures, fmt.Errorf("retention: %w", err))
	}
	deletionUntil := time.Now().Add(2 * time.Second)
	for ctx.Err() == nil && time.Now().Before(deletionUntil) {
		count, err := s.processDeletions(ctx)
		if err != nil {
			failures = append(failures, fmt.Errorf("deletion: %w", err))
			break
		}
		if count == 0 {
			break
		}
	}
	return errors.Join(failures...)
}

func (s *Store) PendingHousekeeping(ctx context.Context) (bool, error) {
	f, err := s.format(ctx)
	if err != nil {
		return false, err
	}
	if f.Backfill.State == "running" {
		return true, nil
	}
	for _, prefix := range []string{"JOB#retain#", "JOB#delete-monitor#", "JOB#delete-application#"} {
		jobs, queryErr := s.queryPage(ctx, "JOBS", prefix, 1)
		if queryErr != nil {
			return false, queryErr
		}
		if len(jobs) > 0 {
			return true, nil
		}
	}
	return false, nil
}
