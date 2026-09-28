package monitor

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
	"time"
)

const DefaultIntervalSeconds = 300

type Lease struct {
	Token               string  `dynamodbav:"token"`
	Until               string  `dynamodbav:"until"`
	Kind                string  `dynamodbav:"kind"`
	StartedAt           string  `dynamodbav:"startedAt,omitempty"`
	MaintenanceWindowID *string `dynamodbav:"maintenanceWindowId,omitempty"`
}
type Evidence struct {
	ObservationID  string      `dynamodbav:"observationId"`
	InitiatedBy    string      `dynamodbav:"initiatedBy"`
	StartedAt      string      `dynamodbav:"startedAt"`
	CompletedAt    string      `dynamodbav:"completedAt"`
	Outcome        string      `dynamodbav:"outcome"`
	Reason         string      `dynamodbav:"reason"`
	ObservedStatus *int        `dynamodbav:"observedStatus,omitempty"`
	ConfigVersion  int         `dynamodbav:"configVersion"`
	Observation    Observation `dynamodbav:"observation"`
}
type Status struct {
	State       string       `json:"state"`
	Reason      *string      `json:"reason"`
	Observation *Observation `json:"observation"`
	FreshUntil  *string      `json:"freshUntil"`
	EvaluatedAt string       `json:"evaluatedAt"`
}
type Gap struct {
	ID          string `json:"id"          dynamodbav:"id"`
	MonitorID   string `json:"monitorId"   dynamodbav:"monitorId"`
	FromDueAt   string `json:"fromDueAt"   dynamodbav:"fromDueAt"`
	ToDueAt     string `json:"toDueAt"     dynamodbav:"toDueAt"`
	MissedCount int    `json:"missedCount" dynamodbav:"missedCount"`
	Reason      string `json:"reason"      dynamodbav:"reason"`
	RecordedAt  string `json:"recordedAt"  dynamodbav:"recordedAt"`
}

// The persistence sort layout has nine fractional digits; the API contract
// presents scheduled instants to millisecond precision without changing keys.
func apiTime(value string) string {
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return Stamp(t)
}

func (o Observation) MarshalJSON() ([]byte, error) {
	type stored Observation
	var due *string
	if o.DueAt != nil {
		value := apiTime(*o.DueAt)
		due = &value
	}
	return json.Marshal(struct {
		stored
		DueAt *string `json:"dueAt"`
	}{stored: stored(o), DueAt: due})
}

func (g Gap) MarshalJSON() ([]byte, error) {
	type stored Gap
	return json.Marshal(struct {
		stored
		FromDueAt string `json:"fromDueAt"`
		ToDueAt   string `json:"toDueAt"`
	}{stored: stored(g), FromDueAt: apiTime(g.FromDueAt), ToDueAt: apiTime(g.ToDueAt)})
}

const (
	NotCountedPaused        = "paused"
	NotCountedArchived      = "archived"
	NotCountedConfigChanged = "config_changed"
	NotCountedOlder         = "older_than_current"
	NotCountedLeaseLost     = "lease_lost"
)

func AllowedIntervals(minimum int) []int {
	allowed := make([]int, 0, 7)
	for _, n := range []int{10, 15, 30, 60, 300, 600, 900} {
		if n >= minimum {
			allowed = append(allowed, n)
		}
	}
	return allowed
}

func ValidateInterval(n, minimum int, fields Fields) {
	for _, v := range AllowedIntervals(minimum) {
		if v == n {
			return
		}
	}
	values := AllowedIntervals(minimum)
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = fmt.Sprint(v)
	}
	fields.Add(
		"intervalSeconds",
		"invalid_value",
		"intervalSeconds must be one of "+strings.Join(parts, ", "),
	)
}

func GridSlot(id string, interval int, at time.Time) time.Time {
	if interval == 0 {
		interval = DefaultIntervalSeconds
	}
	step := int64(interval) * int64(time.Second)
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	offset := (int64(h.Sum32()) % (int64(interval) * 1000)) * int64(time.Millisecond)
	ns := at.UTC().UnixNano()
	// Floor division is required before the Unix epoch too.
	k := (ns - offset) / step
	if ns-offset < 0 && (ns-offset)%step != 0 {
		k--
	}
	return time.Unix(0, k*step+offset).UTC()
}

func SlotsBetween(id string, interval int, previous, latest time.Time) (time.Time, time.Time, int) {
	step := time.Duration(interval) * time.Second
	first := GridSlot(id, interval, previous).Add(step)
	last := GridSlot(id, interval, latest).Add(-step)
	if !first.Before(GridSlot(id, interval, latest)) {
		return time.Time{}, time.Time{}, 0
	}
	return first, last, int(last.Sub(first)/step) + 1
}

func Evaluate(m Monitor, now time.Time) Status {
	result := Status{EvaluatedAt: Stamp(now)}
	if m.Evidence != nil {
		obs := m.Evidence.Observation
		result.Observation = &obs
	}
	switch {
	case m.Lifecycle == "archived":
		result.State = "archived"
	case m.Lifecycle == "paused":
		result.State = "paused"
	case m.Evidence == nil:
		result.State = "unknown"
		r := "no_checks"
		result.Reason = &r
	case m.Evidence.ConfigVersion < m.ConfigVersion:
		result.State = "unknown"
		r := "config_changed"
		result.Reason = &r
	default:
		done, err := time.Parse(time.RFC3339Nano, m.Evidence.CompletedAt)
		if err != nil {
			result.State = "unknown"
			r := "no_checks"
			result.Reason = &r
			break
		}
		interval := m.IntervalSeconds
		if interval == 0 {
			interval = DefaultIntervalSeconds
		}
		until := done.Add(
			time.Duration(
				2*interval,
			)*time.Second + time.Duration(
				m.Check.DeadlineMs,
			)*time.Millisecond,
		)
		stamp := Stamp(until)
		result.FreshUntil = &stamp
		if now.After(until) {
			result.State = "stale"
		} else {
			result.State = m.Evidence.Outcome
		}
	}
	return result
}

func WithStatus(m Monitor, now time.Time) Monitor {
	if m.IntervalSeconds == 0 {
		m.IntervalSeconds = DefaultIntervalSeconds
	}
	if m.ScheduledThrough == "" {
		m.LegacyCursor = true
		m.ScheduledThrough = GridSlot(
			m.ID,
			m.IntervalSeconds,
			now,
		).Format("2006-01-02T15:04:05.000000000Z")
	}
	m.Status = Evaluate(m, now)
	return m
}
