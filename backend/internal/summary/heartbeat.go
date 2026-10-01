package summary

import "time"

// Heartbeat reports reset the deadline at receipt, and therefore have no dueAt.
// Only counted reports and explicit misses occupy slots. Paused and out-of-order
// reports remain visible as not-counted evidence, without adding a deadline.
type HeartbeatOutcomes struct {
	OnTime         int `json:"onTime"`
	Late           int `json:"late"`
	Missed         int `json:"missed"`
	FailureReports int `json:"failureReports"`
}

type HeartbeatCoverage struct {
	Expected          int               `json:"expected"`
	Recorded          int               `json:"recorded"`
	NotObserved       int               `json:"notObserved"`
	Maintenance       int               `json:"maintenance"`
	NotCounted        int               `json:"notCounted"`
	NotCountedReasons map[string]int    `json:"notCountedReasons"`
	Outcomes          HeartbeatOutcomes `json:"outcomes"`
	PausedSeconds     int64             `json:"pausedSeconds"`
}

type HeartbeatBucket struct {
	From           string `json:"from"`
	To             string `json:"to"`
	Expected       int    `json:"expected"`
	Recorded       int    `json:"recorded"`
	NotObserved    int    `json:"notObserved"`
	Maintenance    int    `json:"maintenance"`
	NotCounted     int    `json:"notCounted"`
	OnTime         int    `json:"onTime"`
	Late           int    `json:"late"`
	Missed         int    `json:"missed"`
	FailureReports int    `json:"failureReports"`
	PausedSeconds  int64  `json:"pausedSeconds"`
}

type HeartbeatResult struct {
	MonitorID            string            `json:"monitorId"`
	Kind                 string            `json:"kind"`
	Window               string            `json:"window"`
	From                 string            `json:"from"`
	To                   string            `json:"to"`
	EvaluatedAt          string            `json:"evaluatedAt"`
	Truncated            bool              `json:"truncated"`
	CoveredFrom          string            `json:"coveredFrom"`
	BucketSeconds        int               `json:"bucketSeconds"`
	LifecycleHistoryFrom *string           `json:"lifecycleHistoryFrom"`
	PendingSince         *string           `json:"pendingSince"`
	Coverage             HeartbeatCoverage `json:"coverage"`
	Latency              *Latency          `json:"latency"`
	Outages              []Span            `json:"outages"`
	MaintenanceWindows   []MaintenanceSpan `json:"maintenanceWindows"`
	Buckets              []HeartbeatBucket `json:"buckets"`
}

func ComputeHeartbeat(in Input) HeartbeatResult {
	// The common computation owns clipping, lifecycle time, outage spans, and
	// gap shares; heartbeat observations are attributed by receipt or due time below.
	base := in
	base.Observations = nil
	common := Compute(base)
	r := HeartbeatResult{
		MonitorID: common.MonitorID, Kind: "heartbeat", Window: common.Window,
		From: common.From, To: common.To, EvaluatedAt: common.EvaluatedAt,
		Truncated: common.Truncated, CoveredFrom: common.CoveredFrom,
		BucketSeconds: common.BucketSeconds, LifecycleHistoryFrom: common.LifecycleHistoryFrom,
		Outages: common.Outages, MaintenanceWindows: common.MaintenanceWindows,
		Coverage: HeartbeatCoverage{
			Expected:          common.Coverage.Expected,
			NotObserved:       common.Coverage.NotObserved,
			PausedSeconds:     common.Coverage.PausedSeconds,
			NotCountedReasons: map[string]int{},
		},
		Buckets: make([]HeartbeatBucket, len(common.Buckets)),
	}
	latest := time.Time{}
	for i, b := range common.Buckets {
		r.Buckets[i] = HeartbeatBucket{
			From: b.From, To: b.To,
			Expected: b.Expected, NotObserved: b.NotObserved, PausedSeconds: b.PausedSeconds,
		}
	}
	to, covered := parse(r.To), parse(r.CoveredFrom)
	for _, g := range in.Gaps {
		if g.MissedCount < 1 {
			continue
		}
		start, end := parse(g.FromDueAt), parse(g.ToDueAt)
		step := time.Duration(0)
		if g.MissedCount > 1 {
			step = end.Sub(start) / time.Duration(g.MissedCount-1)
		}
		for i := range g.MissedCount {
			at := start.Add(time.Duration(i) * step)
			if !at.Before(covered) && !at.After(to) && at.After(latest) {
				latest = at
			}
		}
	}
	for _, o := range in.Observations {
		at := parse(o.StartedAt)
		if o.Kind == "heartbeat_missed" && o.DueAt != nil {
			at = parse(*o.DueAt)
		}
		if at.Before(covered) || at.After(to) {
			continue
		}
		var bucket *HeartbeatBucket
		for i := range r.Buckets {
			b := &r.Buckets[i]
			if !at.Before(parse(b.From)) &&
				(at.Before(parse(b.To)) || at.Equal(to) && b.To == r.To) {
				bucket = b
				break
			}
		}
		if bucket == nil {
			continue
		}
		if !o.Counted {
			if o.Kind != "heartbeat_report" {
				continue
			}
			r.Coverage.NotCounted++
			bucket.NotCounted++
			if o.NotCountedReason != nil {
				r.Coverage.NotCountedReasons[*o.NotCountedReason]++
			}
			continue
		}
		if o.Kind != "heartbeat_report" && o.Kind != "heartbeat_missed" {
			continue
		}
		r.Coverage.Recorded++
		r.Coverage.Expected++
		bucket.Recorded++
		bucket.Expected++
		if at.After(latest) {
			latest = at
		}
		if o.MaintenanceWindowID != nil {
			r.Coverage.Maintenance++
			bucket.Maintenance++
			continue
		}
		switch {
		case o.Kind == "heartbeat_missed":
			r.Coverage.Outcomes.Missed++
			bucket.Missed++
		case o.Kind == "heartbeat_report" && o.Reason == "reported_failure" && o.FailureReport:
			if o.Outcome == "failing" {
				r.Coverage.Outcomes.FailureReports++
				bucket.FailureReports++
			}
			fallthrough
		default:
			// A report's late bit is recorded on the report, not in its outcome.
			if o.Late {
				r.Coverage.Outcomes.Late++
				bucket.Late++
			} else {
				r.Coverage.Outcomes.OnTime++
				bucket.OnTime++
			}
		}
	}
	if in.Lifecycle == "active" && in.State == "stale" && !latest.IsZero() {
		value := stamp(latest)
		r.PendingSince = &value
	}
	return r
}
