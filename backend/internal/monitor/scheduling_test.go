package monitor

import (
	"encoding/json"
	"hash/fnv"
	"strings"
	"testing"
	"time"
)

func TestGridAndMissedSlots(t *testing.T) {
	id := "fixed-monitor"
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	interval := 10
	offset := (int64(h.Sum32()) % (int64(interval) * 1000)) * int64(time.Millisecond)
	first := time.Unix(0, offset).UTC()
	if got := GridSlot(id, interval, first.Add(10*time.Second-time.Nanosecond)); !got.Equal(first) {
		t.Fatalf("slot before boundary %v", got)
	}
	if got := GridSlot(id, interval, first.Add(10*time.Second)); !got.Equal(
		first.Add(10 * time.Second),
	) {
		t.Fatalf("boundary %v", got)
	}
	if got := GridSlot(id, interval, first.Add(-time.Nanosecond)); !got.Equal(
		first.Add(-10 * time.Second),
	) {
		t.Fatalf("negative floor %v", got)
	}
	a, b, n := SlotsBetween(id, interval, first, first.Add(40*time.Second))
	if n != 3 || !a.Equal(first.Add(10*time.Second)) || !b.Equal(first.Add(30*time.Second)) {
		t.Fatalf("missed %v %v %d", a, b, n)
	}
	_, _, n = SlotsBetween(id, interval, first, first.Add(10*time.Second))
	if n != 0 {
		t.Fatalf("adjacent slots missed=%d", n)
	}
}

func TestScheduledJSONUsesMilliseconds(t *testing.T) {
	due := "2026-09-27T21:54:22.232587555Z"
	o := Observation{ID: "evidence", DueAt: &due, InitiatedBy: "scheduled"}
	g := Gap{ID: "missed", FromDueAt: due, ToDueAt: "2026-09-27T21:54:32.232587555Z"}
	for _, tc := range []struct {
		value any
		want  string
	}{
		{o, `"dueAt":"2026-09-27T21:54:22.232Z"`},
		{g, `"fromDueAt":"2026-09-27T21:54:22.232Z","toDueAt":"2026-09-27T21:54:32.232Z"`},
		{Status{Observation: &o}, `"dueAt":"2026-09-27T21:54:22.232Z"`},
	} {
		body, err := json.Marshal(tc.value)
		if err != nil || !strings.Contains(string(body), tc.want) {
			t.Fatalf("scheduled JSON %s %v, want %s", body, err, tc.want)
		}
	}
	if *o.DueAt != due {
		t.Fatal("serialization changed the persistence key")
	}
}

func TestStatusEvaluation(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	o := Observation{ID: "obs", Outcome: "healthy", CompletedAt: Stamp(now.Add(-30 * time.Second))}
	m := Monitor{
		Lifecycle:       "active",
		ConfigVersion:   2,
		IntervalSeconds: 10,
		Check:           Check{DeadlineMs: 10000},
	}
	for _, tc := range []struct {
		name, lifecycle string
		evidence        *Evidence
		want, reason    string
		fresh           bool
	}{
		{"archived", "archived", &Evidence{ConfigVersion: 2, Observation: o}, "archived", "", false},
		{"paused", "paused", &Evidence{ConfigVersion: 2, Observation: o}, "paused", "", false},
		{"none", "active", nil, "unknown", "no_checks", false},
		{"config", "active", &Evidence{ConfigVersion: 1, Observation: o}, "unknown", "config_changed", false},
		{"boundary", "active", &Evidence{ConfigVersion: 2, CompletedAt: o.CompletedAt, Outcome: "healthy", Observation: o}, "healthy", "", true},
		{"failing", "active", &Evidence{ConfigVersion: 2, CompletedAt: o.CompletedAt, Outcome: "failing", Observation: o}, "failing", "", true},
		{"checker", "active", &Evidence{ConfigVersion: 2, CompletedAt: o.CompletedAt, Outcome: "checker_problem", Observation: o}, "checker_problem", "", true},
		{"stale", "active", &Evidence{ConfigVersion: 2, CompletedAt: Stamp(now.Add(-30*time.Second - time.Millisecond)), Outcome: "healthy", Observation: o}, "stale", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.Lifecycle = tc.lifecycle
			m.Evidence = tc.evidence
			got := Evaluate(m, now)
			if got.State != tc.want || (got.Reason != nil && *got.Reason != tc.reason) ||
				(got.Reason == nil && tc.reason != "") ||
				(got.FreshUntil != nil) != tc.fresh {
				t.Fatalf("status=%+v", got)
			}
		})
	}
}
