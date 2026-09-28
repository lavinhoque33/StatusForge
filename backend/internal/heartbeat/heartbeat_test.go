package heartbeat

import (
	"testing"
	"time"
)

func TestValidationAndContainment(t *testing.T) {
	now := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	tests := []struct{ body, field, code string }{
		{`{"surprise":1}`, "surprise", "unknown_field"},
		{`{"finishedAt":"2026-09-27T12:01:01Z"}`, "finishedAt", "out_of_range"},
		{`{"runId":"bad id"}`, "runId", "invalid_value"},
		{`{"durationMs":604800001}`, "durationMs", "out_of_range"},
		{`{"exitCode":2147483648}`, "exitCode", "out_of_range"},
		{`{"message":"a\nb"}`, "message", "invalid_value"},
		{`{"status":"other"}`, "status", "invalid_value"},
	}
	for _, tt := range tests {
		_, issues, e := Validate([]byte(tt.body), now)
		if e != nil || issues[tt.field].Code != tt.code {
			t.Errorf("%s: %v, %v", tt.body, issues, e)
		}
	}
	_, issues, e := Validate(
		[]byte(`{"status":"failure","finishedAt":"2026-09-27T12:01:00Z"}`),
		now,
	)
	if e != nil || len(issues) > 0 {
		t.Fatalf("inclusive validation: %v %v", issues, e)
	}
	outages := []Outage{{From: Stamp(now), To: Stamp(now.Add(time.Minute))}}
	for _, tt := range []struct {
		at     time.Time
		inside bool
	}{{now, false}, {now.Add(time.Millisecond), true}, {now.Add(time.Minute), true}, {now.Add(time.Minute + time.Millisecond), false}} {
		if got := Inside(tt.at, outages); got != tt.inside {
			t.Errorf("containment at %v: %v", tt.at, got)
		}
	}
}

func TestOverlappingOutageWindow(t *testing.T) {
	start := time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC)
	interval := 15 * time.Second
	tests := []struct {
		name         string
		due, missing time.Time
		outages      []Outage
		want         time.Time
	}{
		{
			"early window outage",
			start.Add(15 * time.Second),
			start.Add(20 * time.Second),
			[]Outage{{Stamp(start.Add(3 * time.Second)), Stamp(start.Add(10 * time.Second))}},
			start.Add(10 * time.Second),
		},
		{
			"outage ends at window start",
			start.Add(15 * time.Second),
			start.Add(20 * time.Second),
			[]Outage{{Stamp(start.Add(-5 * time.Second)), Stamp(start)}},
			time.Time{},
		},
		{
			"outage starts at deadline",
			start.Add(15 * time.Second),
			start.Add(20 * time.Second),
			[]Outage{{Stamp(start.Add(20 * time.Second)), Stamp(start.Add(21 * time.Second))}},
			time.Time{},
		},
		{
			"outage entirely before later window",
			start.Add(45 * time.Second),
			start.Add(50 * time.Second),
			[]Outage{{Stamp(start.Add(3 * time.Second)), Stamp(start.Add(20 * time.Second))}},
			time.Time{},
		},
		{
			"latest overlapping outage",
			start.Add(15 * time.Second),
			start.Add(50 * time.Second),
			[]Outage{
				{Stamp(start.Add(3 * time.Second)), Stamp(start.Add(10 * time.Second))},
				{Stamp(start.Add(40 * time.Second)), Stamp(start.Add(47 * time.Second))},
			},
			start.Add(47 * time.Second),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			end, overlap := OverlappingOutage(start, tt.due, tt.missing, interval, tt.outages)
			if overlap != !tt.want.IsZero() || !end.Equal(tt.want) {
				t.Fatalf("overlap end %v, overlap %t", end, overlap)
			}
		})
	}
}

func TestTokenHash(t *testing.T) {
	token, e := Generate()
	if e != nil || len(token) != 56 || token[:4] != "sfh_" {
		t.Fatalf("token: %q %v", token, e)
	}
	if !Matches(token, Hash(token)) || Matches(token+"x", Hash(token)) ||
		Hint(token) != token[len(token)-4:] {
		t.Fatal("credential mismatch")
	}
}
