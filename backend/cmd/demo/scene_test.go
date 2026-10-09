package main

import (
	"testing"
	"time"
)

// slots lists one monitor's check ages: 15 minutes apart, then 10 minutes apart
// from the interval change on.
func slots(offset time.Duration) (ages, every []time.Duration) {
	for age := offset; age < 7*day; {
		step := interval
		if age >= tightenAt {
			step = finalInterval
		}
		ages, every = append(ages, age), append(every, step)
		age += step
	}
	return ages, every
}

// Isolated failures and timeouts must never open an incident under the default
// two-failure threshold; only the designed incident spans may fail repeatedly.
func TestSceneFailuresStayIsolated(t *testing.T) {
	for _, tc := range []struct {
		name     string
		plan     func(age, every time.Duration) result
		deadline int64
		incident func(time.Duration) bool
	}{
		{"recoveries", recoveriesCheck, recoveriesProfile.deadline, func(age time.Duration) bool {
			return within(age, recoveryIncidentAt, recoveryIncidentEnd) ||
				within(age, restartAt, restartEnd)
		}},
		{"incident", func(age, every time.Duration) result {
			return currentIncidentCheck(age, every, age >= deployAt)
		}, incidentProfile.deadline, func(age time.Duration) bool { return age >= deployAt }},
		{"curve", curveCheck, curveProfile.deadline, nil},
		{"worker", workerCheck, workerProfile.deadline, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, offset := range []time.Duration{0, 7 * time.Minute, interval - time.Second} {
				previous, failures, timeouts := false, 0, 0
				latencies := map[int64]bool{}
				ages, every := slots(offset)
				for i, age := range ages {
					r := tc.plan(age, every[i])
					if r != tc.plan(age, every[i]) {
						t.Fatalf("plan not deterministic at %s", age)
					}
					failing := r.outcome == "failing"
					designed := tc.incident != nil && tc.incident(age)
					if failing && previous && !designed {
						t.Fatalf("consecutive failures at %s would open an incident", age)
					}
					// A checker problem neither extends nor resets a failure run.
					if r.outcome != "checker_problem" {
						previous = failing && !designed
					}
					switch {
					case r.reason == "timeout":
						timeouts++
						if r.status != 0 || r.duration < tc.deadline {
							t.Fatalf("timeout at %s: %+v", age, r)
						}
					case r.outcome == "checker_problem":
						if r.status != 0 {
							t.Fatalf("checker problem with a response at %s: %+v", age, r)
						}
					default:
						if r.duration <= 0 || r.duration >= tc.deadline || r.status == 0 {
							t.Fatalf("response at %s: %+v", age, r)
						}
						latencies[r.duration] = true
					}
					if failing && !designed {
						failures++
					}
				}
				if failures == 0 || timeouts == 0 && tc.name != "worker" {
					t.Fatalf(
						"offset %s: %d isolated failures, %d timeouts",
						offset,
						failures,
						timeouts,
					)
				}
				if len(latencies) < 100 {
					t.Fatalf("offset %s: only %d distinct latencies", offset, len(latencies))
				}
			}
		})
	}
}

func TestSceneCheckerBurstAndSlowPeriod(t *testing.T) {
	problems := 0
	ages, every := slots(0)
	for i, age := range ages {
		if curveCheck(age, every[i]).outcome == "checker_problem" {
			problems++
		}
		if within(age, slowAt, slowEnd) {
			if d := recoveriesCheck(age, every[i]).duration; d < 1400 || d > 2700 {
				t.Fatalf("slow response at %s: %d ms", age, d)
			}
		}
	}
	if problems != checkerProblemSlots {
		t.Fatalf("checker problems: %d", problems)
	}
}

func TestHeartbeatPlanIsIrregular(t *testing.T) {
	var plan heartbeatPlan
	counts := map[report]int{}
	leads := map[time.Duration]bool{}
	var previous report
	var positions []int
	for n := range 700 {
		kind, offset := plan.next(n, time.Duration(n)*interval)
		if kind == missed && previous == missed {
			t.Fatalf("consecutive misses at %d", n)
		}
		switch kind {
		case onTime:
			if offset >= 0 {
				t.Fatalf("on-time report after the deadline at %d", n)
			}
			leads[offset] = true
		case late:
			if offset <= 0 || offset >= time.Minute {
				t.Fatalf("late report outside grace at %d: %s", n, offset)
			}
		}
		if kind != onTime {
			positions = append(positions, n)
		}
		counts[kind]++
		previous = kind
	}
	if counts[late] == 0 || counts[missed] == 0 || counts[onTime] < 665 || len(leads) < 600 {
		t.Fatalf("report mix: %v, %d distinct leads", counts, len(leads))
	}
	gaps := map[int]bool{}
	for i := 1; i < len(positions); i++ {
		gaps[positions[i]-positions[i-1]] = true
	}
	if len(gaps) < len(positions)/2 {
		t.Fatalf("late and missed reports fall on a regular pattern: %v", positions)
	}
}
