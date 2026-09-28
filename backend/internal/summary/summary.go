package summary

import (
	"math"
	"sort"
	"time"
)

type (
	Observation struct {
		Kind                string
		FailureReport       bool
		Late                bool
		DueAt               *string
		StartedAt           string
		Counted             bool
		NotCountedReason    *string
		MaintenanceWindowID *string
		Outcome, Reason     string
		DurationMs          int64
	}
	Gap struct {
		FromDueAt, ToDueAt string
		MissedCount        int
	}
	LifecycleEvent struct{ Action, At string }
	Span           struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	MaintenanceSpan struct {
		ID   string `json:"id"`
		From string `json:"from"`
		To   string `json:"to"`
	}
	Outcomes struct {
		Healthy        int `json:"healthy"`
		Failing        int `json:"failing"`
		CheckerProblem int `json:"checkerProblem"`
	}
	Coverage struct {
		Expected          int            `json:"expected"`
		Recorded          int            `json:"recorded"`
		NotObserved       int            `json:"notObserved"`
		Maintenance       int            `json:"maintenance"`
		NotCounted        int            `json:"notCounted"`
		NotCountedReasons map[string]int `json:"notCountedReasons"`
		Outcomes          Outcomes       `json:"outcomes"`
		ManualChecks      int            `json:"manualChecks"`
		PausedSeconds     int64          `json:"pausedSeconds"`
	}
	Latency struct {
		Samples         int    `json:"samples"`
		MedianMs        *int64 `json:"medianMs"`
		P95Ms           *int64 `json:"p95Ms"`
		MaxMs           *int64 `json:"maxMs"`
		NoResponse      int    `json:"noResponse"`
		CheckerProblems int    `json:"checkerProblems"`
	}
	BucketLatency struct {
		Samples  int    `json:"samples"`
		MedianMs *int64 `json:"medianMs"`
		P95Ms    *int64 `json:"p95Ms"`
		MaxMs    *int64 `json:"maxMs"`
	}
	Bucket struct {
		From           string        `json:"from"`
		To             string        `json:"to"`
		Expected       int           `json:"expected"`
		Recorded       int           `json:"recorded"`
		NotObserved    int           `json:"notObserved"`
		Maintenance    int           `json:"maintenance"`
		NotCounted     int           `json:"notCounted"`
		Healthy        int           `json:"healthy"`
		Failing        int           `json:"failing"`
		CheckerProblem int           `json:"checkerProblem"`
		PausedSeconds  int64         `json:"pausedSeconds"`
		Latency        BucketLatency `json:"latency"`
	}
	Result struct {
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
		Coverage             Coverage          `json:"coverage"`
		Latency              Latency           `json:"latency"`
		Outages              []Span            `json:"outages"`
		MaintenanceWindows   []MaintenanceSpan `json:"maintenanceWindows"`
		Buckets              []Bucket          `json:"buckets"`
	}
	Input struct {
		MonitorID, Window, Lifecycle, PausedAt, State string
		To                                            time.Time
		Observations                                  []Observation
		Gaps                                          []Gap
		Events                                        []LifecycleEvent
		Outages                                       []Span
		MaintenanceWindows                            []MaintenanceSpan
		Truncated                                     bool
		CoveredFrom                                   time.Time
	}
)

func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func parse(s string) time.Time { t, _ := time.Parse(time.RFC3339Nano, s); return t }
func clipped(start, end, from, to time.Time) (time.Time, time.Time, bool) {
	if start.Before(from) {
		start = from
	}
	if end.After(to) {
		end = to
	}
	return start, end, end.After(start)
}

func statistics(samples []int64) Latency {
	v := Latency{Samples: len(samples)}
	if len(samples) == 0 {
		return v
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	max := samples[len(samples)-1]
	v.MaxMs = &max
	if len(samples) >= 5 {
		median, p95 := samples[int(math.Ceil(.5*float64(len(samples))))-1], samples[int(math.Ceil(.95*float64(len(samples))))-1]
		v.MedianMs = &median
		v.P95Ms = &p95
	}
	return v
}

func Compute(in Input) Result {
	to := in.To.UTC()
	length, step := 24*time.Hour, time.Hour
	if in.Window == "7d" {
		length, step = 7*24*time.Hour, 6*time.Hour
	}
	from := to.Add(-length)
	covered := from
	if in.Truncated && in.CoveredFrom.After(from) {
		covered = in.CoveredFrom
	}
	r := Result{
		MonitorID:          in.MonitorID,
		Kind:               "http",
		Window:             in.Window,
		From:               stamp(from),
		To:                 stamp(to),
		EvaluatedAt:        stamp(to),
		Truncated:          in.Truncated,
		CoveredFrom:        stamp(covered),
		BucketSeconds:      int(step.Seconds()),
		Coverage:           Coverage{NotCountedReasons: map[string]int{}},
		Outages:            []Span{},
		MaintenanceWindows: []MaintenanceSpan{},
		Buckets:            []Bucket{},
	}
	for t := covered.Truncate(step); t.Before(to); t = t.Add(step) {
		a, b, _ := clipped(t, t.Add(step), covered, to)
		if b.After(a) {
			r.Buckets = append(r.Buckets, Bucket{From: stamp(a), To: stamp(b)})
		}
	}
	index := func(t time.Time) int {
		if t.Equal(to) && t.Truncate(step).Equal(to) {
			t = t.Add(-time.Nanosecond)
		}
		i := int(t.Truncate(step).Sub(covered.Truncate(step)) / step)
		if i < 0 || i >= len(r.Buckets) {
			return -1
		}
		return i
	}
	samples := []int64{}
	per := make([][]int64, len(r.Buckets))
	latest := time.Time{}
	for _, o := range in.Observations {
		due := time.Time{}
		if o.DueAt != nil {
			due = parse(*o.DueAt)
		}
		at := due
		if o.DueAt == nil {
			at = parse(o.StartedAt)
		}
		if at.Before(covered) || at.After(to) {
			continue
		}
		bi := index(at)
		if bi < 0 {
			continue
		}
		b := &r.Buckets[bi]
		if o.DueAt == nil {
			r.Coverage.ManualChecks++
		} else {
			r.Coverage.Recorded++
			b.Recorded++
			b.Expected++
			r.Coverage.Expected++
			if due.After(latest) {
				latest = due
			}
			switch {
			case o.MaintenanceWindowID != nil:
				r.Coverage.Maintenance++
				b.Maintenance++
			case !o.Counted:
				r.Coverage.NotCounted++
				b.NotCounted++
				if o.NotCountedReason != nil {
					r.Coverage.NotCountedReasons[*o.NotCountedReason]++
				}
			default:
				switch o.Outcome {
				case "healthy":
					r.Coverage.Outcomes.Healthy++
					b.Healthy++
				case "failing":
					r.Coverage.Outcomes.Failing++
					b.Failing++
				case "checker_problem":
					r.Coverage.Outcomes.CheckerProblem++
					b.CheckerProblem++
				}
			}
		}
		if o.MaintenanceWindowID != nil {
			continue
		}
		switch o.Reason {
		case "ok", "wrong_status":
			samples = append(samples, o.DurationMs)
			per[bi] = append(per[bi], o.DurationMs)
		case "timeout", "connection_refused", "connection_error":
			r.Latency.NoResponse++
		case "refused_by_policy", "internal":
			r.Latency.CheckerProblems++
		}
	}
	for _, g := range in.Gaps {
		if g.MissedCount < 1 {
			continue
		}
		start, end := parse(g.FromDueAt), parse(g.ToDueAt)
		interval := time.Duration(0)
		if g.MissedCount > 1 {
			interval = end.Sub(start) / time.Duration(g.MissedCount-1)
		}
		for i := range g.MissedCount {
			at := start.Add(time.Duration(i) * interval)
			if at.Before(covered) || at.After(to) {
				continue
			}
			bi := index(at)
			if bi < 0 {
				continue
			}
			r.Buckets[bi].Expected++
			r.Buckets[bi].NotObserved++
			r.Coverage.Expected++
			r.Coverage.NotObserved++
			if at.After(latest) {
				latest = at
			}
		}
	}
	sort.Slice(in.Events, func(i, j int) bool { return in.Events[i].At < in.Events[j].At })
	if len(in.Events) > 0 {
		first := stamp(parse(in.Events[0].At))
		r.LifecycleHistoryFrom = &first
	}
	paused := time.Time{}
	for _, e := range in.Events {
		at := parse(e.At)
		if !at.After(to) {
			switch e.Action {
			case "paused":
				paused = at
			case "resumed", "archived":
				if !paused.IsZero() {
					addPause(&r, paused, at)
					paused = time.Time{}
				}
			}
		}
	}
	if paused.IsZero() && in.Lifecycle == "paused" && in.PausedAt != "" {
		paused = parse(in.PausedAt)
	}
	if !paused.IsZero() {
		addPause(&r, paused, to)
	}
	for _, o := range in.Outages {
		a, b, ok := clipped(parse(o.From), parse(o.To), covered, to)
		if ok {
			r.Outages = append(r.Outages, Span{stamp(a), stamp(b)})
		}
	}
	for _, w := range in.MaintenanceWindows {
		a, b, ok := clipped(parse(w.From), parse(w.To), covered, to)
		if ok {
			r.MaintenanceWindows = append(
				r.MaintenanceWindows,
				MaintenanceSpan{w.ID, stamp(a), stamp(b)},
			)
		}
	}
	r.Latency.Samples = len(samples)
	stats := statistics(samples)
	r.Latency.MedianMs = stats.MedianMs
	r.Latency.P95Ms = stats.P95Ms
	r.Latency.MaxMs = stats.MaxMs
	for i := range r.Buckets {
		stats := statistics(per[i])
		r.Buckets[i].Latency = BucketLatency{
			stats.Samples,
			stats.MedianMs,
			stats.P95Ms,
			stats.MaxMs,
		}
	}
	if in.Lifecycle == "active" && in.State == "stale" && !latest.IsZero() {
		value := stamp(latest)
		r.PendingSince = &value
	}
	return r
}

func addPause(r *Result, start, end time.Time) {
	a, b, ok := clipped(start, end, parse(r.CoveredFrom), parse(r.To))
	if !ok {
		return
	}
	r.Coverage.PausedSeconds += int64(b.Sub(a).Seconds())
	for i := range r.Buckets {
		bucket := &r.Buckets[i]
		x, y, yes := clipped(a, b, parse(bucket.From), parse(bucket.To))
		if yes {
			bucket.PausedSeconds += int64(y.Sub(x).Seconds())
		}
	}
}
