package main

import (
	"context"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// heartbeatWeek replays a 15-minute job's reports through the ingest and
// deadline paths: mostly on time with a varying lead, some late within grace, a
// few missed, and one short cluster.
type heartbeatWeek struct {
	ctx        context.Context
	s          *store.Store
	now        time.Time
	scene, end time.Time
	app, token string
	monitor    monitor.Monitor
}

func (h *heartbeatWeek) run() error {
	h.now = h.scene
	hb := monitor.New("Demo heartbeat · late and missed", monitor.Check{}, h.scene)
	hb.Kind = "heartbeat"
	hb.IntervalSeconds = 0
	hb.Heartbeat = &heartbeat.Configuration{
		Schedule: heartbeat.Schedule{IntervalSeconds: 900, GraceSeconds: 60},
		Token: &heartbeat.Token{
			Hash:      heartbeat.Hash(h.token),
			Hint:      heartbeat.Hint(h.token),
			CreatedAt: monitor.Stamp(h.scene),
		},
		IngestPath: "/ingest/heartbeats/" + hb.ID,
	}
	expectation := heartbeat.Expect(h.scene, hb.Heartbeat.Schedule)
	hb.Expectation = &expectation
	if err := h.s.Create(h.ctx, hb); err != nil {
		return err
	}
	if _, err := h.s.SetApplication(h.ctx, hb.ID, h.app); err != nil {
		return err
	}
	h.monitor = hb
	var plan heartbeatPlan
	var alive time.Time
	due := expectation.DueAt
	for n := 0; ; n++ {
		dueAt, err := time.Parse(time.RFC3339Nano, due)
		if err != nil {
			return err
		}
		if !dueAt.Add(61 * time.Second).Before(h.end) {
			return nil
		}
		kind, offset := plan.next(n, dueAt.Sub(h.scene))
		if kind == missed {
			h.now = dueAt.Add(61 * time.Second)
			live, err := h.s.WriteLiveness(h.ctx, h.now, time.Hour)
			if err != nil {
				return err
			}
			alive = h.now
			current, err := h.s.Get(h.ctx, hb.ID)
			if err != nil {
				return err
			}
			if err := h.s.Deadline(h.ctx, current, h.now, live); err != nil {
				return err
			}
			if current, err = h.s.Get(h.ctx, hb.ID); err != nil {
				return err
			}
			due = current.Expectation.DueAt
			continue
		}
		h.now = dueAt.Add(offset)
		// Half-hourly liveness stays well inside the outage threshold of three hours.
		if h.now.Sub(alive) >= 30*time.Minute {
			if _, err := h.s.WriteLiveness(h.ctx, h.now, time.Hour); err != nil {
				return err
			}
			alive = h.now
		}
		received, err := h.s.RecordHeartbeat(
			h.ctx, hb.ID, heartbeat.Hash(h.token), heartbeat.Report{}, h.now,
		)
		if err != nil {
			return err
		}
		due = received.NextDueAt
	}
}
