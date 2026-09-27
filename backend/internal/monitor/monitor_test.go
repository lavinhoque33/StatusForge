package monitor

import (
	"testing"
	"time"
)

func TestLifecycleAndVersion(t *testing.T) {
	now := time.Date(2026, 9, 27, 10, 15, 30, 123000000, time.UTC)
	c := Check{
		URL:            "http://127.0.0.1:8090",
		Method:         "GET",
		ExpectedStatus: 200,
		DeadlineMs:     10000,
		MaxBodyBytes:   MaxBodyBytes,
	}
	m := New("test", c, now)
	name := "renamed"
	m = m.Patch(&name, nil, now)
	if m.ConfigVersion != 1 {
		t.Fatal("name changed version")
	}
	m = m.Patch(nil, &c, now)
	if m.ConfigVersion != 1 {
		t.Fatal("unchanged check bumped version")
	}
	c.DeadlineMs = 1000
	m = m.Patch(nil, &c, now)
	if m.ConfigVersion != 2 {
		t.Fatal("check change did not bump version")
	}
	paused, ok := m.Transition("pause", now)
	if !ok || paused.PausedAt == "" {
		t.Fatal("pause failed")
	}
	if _, ok = paused.Transition("pause", now); ok {
		t.Fatal("repeat pause accepted")
	}
	active, ok := paused.Transition("resume", now)
	if !ok || active.PausedAt != "" {
		t.Fatal("resume failed")
	}
	archived, ok := active.Transition("archive", now)
	if !ok || archived.ArchivedAt == "" {
		t.Fatal("archive failed")
	}
	if _, ok = archived.Transition("resume", now); ok {
		t.Fatal("resumed archive")
	}
}
