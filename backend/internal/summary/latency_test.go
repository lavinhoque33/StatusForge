package summary

import (
	"testing"
	"time"
)

func TestLatencyResponseClassification(t *testing.T) {
	to := time.Date(2026, 9, 28, 12, 30, 0, 0, time.UTC)
	at := stamp(to.Add(-12*time.Hour - 10*time.Minute))
	type observationCase struct {
		reason      string
		durationMs  int64
		maintenance bool
		manual      bool
		notCounted  bool
	}
	four := []observationCase{
		{reason: "ok", durationMs: 400},
		{reason: "wrong_status", durationMs: 100},
		{reason: "ok", durationMs: 300, manual: true},
		{reason: "wrong_status", durationMs: 200, notCounted: true},
	}
	five := append(
		append([]observationCase{}, four...),
		observationCase{reason: "ok", durationMs: 500},
	)
	cases := []struct {
		name            string
		observations    []observationCase
		samples         int
		noResponse      int
		checkerProblems int
		median, p95     *int64
		max             int64
	}{
		{name: "four responses are insufficient", observations: four, samples: 4, max: 400},
		{
			name: "five responses have nearest ranks", observations: five, samples: 5,
			median: new(int64(300)), p95: new(int64(500)), max: 500,
		},
		{
			name: "failures and maintenance never become response samples",
			observations: append(append([]observationCase{}, five...),
				observationCase{reason: "timeout", durationMs: 999},
				observationCase{reason: "connection_refused", durationMs: 999},
				observationCase{reason: "connection_error", durationMs: 999},
				observationCase{reason: "refused_by_policy", durationMs: 999},
				observationCase{reason: "internal", durationMs: 999},
				observationCase{reason: "ok", durationMs: 10000, maintenance: true},
				observationCase{reason: "wrong_status", durationMs: 10000, maintenance: true},
			),
			samples: 5, noResponse: 3, checkerProblems: 2,
			median: new(int64(300)), p95: new(int64(500)), max: 500,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observations := make([]Observation, 0, len(tc.observations))
			for _, entry := range tc.observations {
				o := Observation{
					StartedAt: at, Reason: entry.reason, DurationMs: entry.durationMs,
					Counted: !entry.notCounted,
				}
				if !entry.manual {
					o.DueAt = &at
				}
				if entry.maintenance {
					id := "window"
					o.MaintenanceWindowID = &id
				}
				observations = append(observations, o)
			}
			r := Compute(Input{
				MonitorID: "sample", Window: "24h", Lifecycle: "active",
				To: to, Observations: observations,
			})
			if r.Latency.Samples != tc.samples || r.Latency.NoResponse != tc.noResponse ||
				r.Latency.CheckerProblems != tc.checkerProblems || r.Latency.MaxMs == nil ||
				*r.Latency.MaxMs != tc.max || !sameNumber(r.Latency.MedianMs, tc.median) ||
				!sameNumber(r.Latency.P95Ms, tc.p95) {
				t.Fatalf("overall latency: %+v", r.Latency)
			}
			var matching *Bucket
			for i := range r.Buckets {
				if r.Buckets[i].Latency.Samples > 0 {
					matching = &r.Buckets[i]
					break
				}
			}
			if matching == nil || matching.Latency.Samples != tc.samples ||
				matching.Latency.MaxMs == nil || *matching.Latency.MaxMs != tc.max ||
				!sameNumber(matching.Latency.MedianMs, tc.median) ||
				!sameNumber(matching.Latency.P95Ms, tc.p95) {
				t.Fatalf("bucket latency: %+v", matching)
			}
		})
	}
}

func sameNumber(actual, expected *int64) bool {
	if actual == nil || expected == nil {
		return actual == expected
	}
	return *actual == *expected
}
