package main

import (
	"math"
	"math/rand/v2"
	"time"
)

// The synthetic week is defined relative to its start. Every pseudo-random draw
// comes from a PCG stream keyed by sceneSeed, the service and the slot, so each
// run produces the same scene shape regardless of the wall-clock start.
const (
	day = 24 * time.Hour
	// Checks run every 15 minutes until the final day, then every 10 minutes, so
	// that each hourly bucket of the 24-hour charts holds enough samples for a median.
	interval      = 15 * time.Minute
	finalInterval = 10 * time.Minute

	sceneSeed uint64 = 0x5f3759df_2026_0a09
)

// Story times, as offsets from the start of the week, in order.
const (
	checkerProblemAt    = day + 14*time.Hour + 20*time.Minute
	checkerProblemSlots = 4
	recoveryIncidentAt  = 2 * day
	recoveryIncidentEnd = recoveryIncidentAt + 3*time.Hour
	slowAt              = 3 * day
	slowEnd             = slowAt + 3*time.Hour
	receiveGapAt        = 3*day + 6*time.Hour
	receiveGapEnd       = receiveGapAt + 3*time.Hour
	maintenanceAt       = 4 * day
	// A planned restart fails checks inside the maintenance window, which never opens an incident.
	restartAt          = maintenanceAt + 45*time.Minute
	restartEnd         = maintenanceAt + 90*time.Minute
	workerCreatedAt    = 4*day + 2*time.Hour
	heartbeatClusterAt = 4*day + 14*time.Hour
	workerPausedAt     = 5*day + 7*time.Hour + 40*time.Minute
	tightenAt          = 5*day + 22*time.Hour
	deployAt           = 6 * day
	curvePausedAt      = 6*day + 8*time.Hour + 20*time.Minute
	curveResumedAt     = 6*day + 14*time.Hour + 5*time.Minute
)

// result is one synthetic check outcome as the checker would have recorded it.
type result struct {
	outcome  string
	reason   string
	status   int // zero when no response arrived
	duration int64
}

// profile models one service's response times: a log-normal spread around a
// baseline that follows a daily load cycle and drifts slowly over the week, with
// occasional spikes. Peak is an offset into each scene day, not a wall-clock hour.
type profile struct {
	stream   uint64
	baseMs   float64
	spread   float64
	daily    float64
	peak     time.Duration
	spikes   float64
	deadline int64
}

var (
	recoveriesProfile = profile{
		stream: 1, baseMs: 260, spread: 0.28, daily: 0.18,
		peak: 15 * time.Hour, spikes: 0.03, deadline: 3000,
	}
	incidentProfile = profile{
		stream: 2, baseMs: 380, spread: 0.3, daily: 0.25,
		peak: 11 * time.Hour, spikes: 0.04, deadline: 1500,
	}
	brokenProfile = profile{
		stream: 3, baseMs: 520, spread: 0.35, daily: 0.1,
		peak: 11 * time.Hour, spikes: 0.05, deadline: 1500,
	}
	curveProfile = profile{
		stream: 4, baseMs: 300, spread: 0.16, daily: 0.6,
		peak: 14 * time.Hour, spikes: 0.02, deadline: 1200,
	}
	workerProfile = profile{
		stream: 5, baseMs: 95, spread: 0.3, daily: 0.12,
		peak: 3 * time.Hour, spikes: 0.03, deadline: 1000,
	}
)

const heartbeatStream uint64 = 6

// draw returns the generator for the slot at age, independent of earlier draws.
func (p profile) draw(age time.Duration) *rand.Rand {
	return rand.New(rand.NewPCG(sceneSeed^p.stream, uint64(age/time.Minute)))
}

// latency stays below 90 % of the deadline; only timeouts reach it. Values above
// 70 % are compressed smoothly, so slow responses do not pile up at one ceiling.
func (p profile) latency(r *rand.Rand, age time.Duration) int64 {
	cycle := math.Cos(2 * math.Pi * float64(age-p.peak) / float64(day))
	drift := math.Sin(2 * math.Pi * float64(age) / float64(61*time.Hour))
	ms := p.baseMs * (1 + p.daily*cycle) * (1 + 0.08*drift) * math.Exp(p.spread*r.NormFloat64())
	if r.Float64() < p.spikes {
		ms *= 1.8 + 1.4*r.Float64()
	}
	knee, room := 0.7*float64(p.deadline), 0.2*float64(p.deadline)
	if ms > knee {
		ms = knee + room*math.Tanh((ms-knee)/room)
	}
	return max(int64(ms), 1)
}

func (p profile) healthy(r *rand.Rand, age time.Duration) result {
	return result{"healthy", "ok", 200, p.latency(r, age)}
}

// failure is an error status, which a server usually returns faster than a full response.
func (p profile) failure(r *rand.Rand, age time.Duration, status int) result {
	return result{"failing", "wrong_status", status, max(p.latency(r, age)*2/5, 1)}
}

// timeout records no response: the request ran into its deadline.
func (p profile) timeout(r *rand.Rand) result {
	return result{"failing", "timeout", 0, p.deadline + int64(r.IntN(25))}
}

func checkerProblem(r *rand.Rand) result {
	return result{"checker_problem", "internal", 0, int64(r.IntN(8))}
}

// at reports whether the slot at age is the first one at or after any mark,
// given the monitor's interval between slots.
func at(age, every time.Duration, marks ...time.Duration) bool {
	for _, m := range marks {
		if age >= m && age < m+every {
			return true
		}
	}
	return false
}

func within(age, from, to time.Duration) bool { return age >= from && age < to }

func hm(d, h, m int) time.Duration {
	return time.Duration(d)*day + time.Duration(h)*time.Hour + time.Duration(m)*time.Minute
}

// recoveriesCheck: a resolved incident, a slow period, a planned restart during
// maintenance, isolated failures and timeouts that stay below the incident threshold.
func recoveriesCheck(age, every time.Duration) result {
	p := recoveriesProfile
	r := p.draw(age)
	switch {
	case within(age, recoveryIncidentAt, recoveryIncidentEnd), within(age, restartAt, restartEnd):
		return p.failure(r, age, 503)
	case within(age, slowAt, slowEnd):
		f := float64(age-slowAt) / float64(slowEnd-slowAt)
		return result{
			"healthy",
			"ok",
			200,
			1400 + int64(1300*math.Sin(math.Pi*f)*(0.75+0.25*r.Float64())),
		}
	case at(age, every, hm(0, 16, 50), hm(4, 21, 20), hm(6, 13, 10)):
		return p.failure(r, age, 502)
	case at(age, every, hm(1, 9, 35), hm(5, 4, 5), hm(6, 19, 40)):
		return p.timeout(r)
	}
	return p.healthy(r, age)
}

// currentIncidentCheck: jittery but healthy until the deployment, failing on every check after it.
func currentIncidentCheck(age, every time.Duration, deployed bool) result {
	r := incidentProfile.draw(age)
	if deployed {
		if at(
			age,
			every,
			deployAt+5*time.Hour+10*time.Minute,
			deployAt+14*time.Hour+25*time.Minute,
		) {
			return brokenProfile.timeout(r)
		}
		return result{"failing", "wrong_status", 500, brokenProfile.latency(r, age)}
	}
	switch {
	case at(age, every, hm(1, 20, 10)):
		return incidentProfile.failure(r, age, 500)
	case at(age, every, hm(3, 15, 5)):
		return incidentProfile.timeout(r)
	}
	return incidentProfile.healthy(r, age)
}

// curveCheck: a pronounced daily load curve, sporadic timeouts, one isolated
// failure and one short checker-problem burst.
func curveCheck(age, every time.Duration) result {
	p := curveProfile
	r := p.draw(age)
	switch {
	case within(age, checkerProblemAt, checkerProblemAt+checkerProblemSlots*interval):
		return checkerProblem(r)
	case at(age, every, hm(0, 22, 15), hm(2, 13, 50), hm(4, 7, 25), hm(5, 18, 5), hm(6, 21, 30)):
		return p.timeout(r)
	case at(age, every, hm(3, 19, 0)):
		return p.failure(r, age, 503)
	}
	return p.healthy(r, age)
}

// workerCheck: a fast internal service with one isolated failure before it is paused.
func workerCheck(age, every time.Duration) result {
	p := workerProfile
	r := p.draw(age)
	if at(age, every, hm(4, 16, 35)) {
		return p.failure(r, age, 500)
	}
	return p.healthy(r, age)
}

type report int

const (
	onTime report = iota
	late
	missed
)

// heartbeatCluster is one short stretch of trouble; misses never follow each
// other, so the heartbeat stays below its incident threshold.
var heartbeatCluster = []report{late, missed, late, late, missed, late}

// heartbeatPlan decides each expected report: usually on time at a varying lead
// before the deadline, sometimes late within the grace period, rarely missed.
type heartbeatPlan struct {
	previous report
	cluster  int
}

// next returns the n-th report's kind and its offset from the deadline.
func (h *heartbeatPlan) next(n int, age time.Duration) (report, time.Duration) {
	r := rand.New(rand.NewPCG(sceneSeed^heartbeatStream, uint64(n)))
	u := r.Float64()
	kind := onTime
	switch {
	case age >= heartbeatClusterAt && h.cluster < len(heartbeatCluster):
		kind = heartbeatCluster[h.cluster]
		h.cluster++
	case n > 0 && u < 0.02:
		kind = late
	case n > 0 && u < 0.03 && h.previous != missed:
		kind = missed
	}
	h.previous = kind
	switch kind {
	case late:
		return late, 5*time.Second + time.Duration(r.IntN(50_000))*time.Millisecond
	case missed:
		return missed, 0
	}
	lead := 4*time.Second + time.Duration(math.Abs(r.NormFloat64())*40*float64(time.Second))
	return onTime, -min(lead, 4*time.Minute)
}
