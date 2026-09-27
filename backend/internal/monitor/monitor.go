package monitor

import (
	"crypto/rand"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const MaxBodyBytes = 65536

type Check struct {
	URL            string `json:"url"            dynamodbav:"url"`
	Method         string `json:"method"         dynamodbav:"method"`
	ExpectedStatus int    `json:"expectedStatus" dynamodbav:"expectedStatus"`
	DeadlineMs     int    `json:"deadlineMs"     dynamodbav:"deadlineMs"`
	MaxBodyBytes   int    `json:"maxBodyBytes"   dynamodbav:"maxBodyBytes"`
}
type Monitor struct {
	ID            string `json:"id"                   dynamodbav:"monitorId"`
	Name          string `json:"name"                 dynamodbav:"name"`
	Lifecycle     string `json:"lifecycle"            dynamodbav:"lifecycle"`
	ConfigVersion int    `json:"configVersion"        dynamodbav:"configVersion"`
	Check         Check  `json:"check"                dynamodbav:"check"`
	CreatedAt     string `json:"createdAt"            dynamodbav:"createdAt"`
	UpdatedAt     string `json:"updatedAt"            dynamodbav:"updatedAt"`
	PausedAt      string `json:"pausedAt,omitempty"   dynamodbav:"pausedAt,omitempty"`
	ArchivedAt    string `json:"archivedAt,omitempty" dynamodbav:"archivedAt,omitempty"`
}
type Observation struct {
	ID             string `json:"id"                       dynamodbav:"observationId"`
	MonitorID      string `json:"monitorId"                dynamodbav:"monitorId"`
	ConfigVersion  int    `json:"configVersion"            dynamodbav:"configVersion"`
	InitiatedBy    string `json:"initiatedBy"              dynamodbav:"initiatedBy"`
	Request        Check  `json:"request"                  dynamodbav:"request"`
	StartedAt      string `json:"startedAt"                dynamodbav:"startedAt"`
	CompletedAt    string `json:"completedAt"              dynamodbav:"completedAt"`
	DurationMs     int64  `json:"durationMs"               dynamodbav:"durationMs"`
	Outcome        string `json:"outcome"                  dynamodbav:"outcome"`
	Reason         string `json:"reason"                   dynamodbav:"reason"`
	ObservedStatus *int   `json:"observedStatus,omitempty" dynamodbav:"observedStatus,omitempty"`
	BodyBytesRead  int    `json:"bodyBytesRead"            dynamodbav:"bodyBytesRead"`
	BodyTruncated  bool   `json:"bodyTruncated"            dynamodbav:"bodyTruncated"`
}
type ListItem struct {
	Monitor
	LastObservation *Observation `json:"lastObservation"`
}
type FieldError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
type Fields map[string]FieldError

func (f Fields) Add(path, code, message string) { f[path] = FieldError{code, message} }

func Stamp(
	t time.Time,
) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func ValidateName(name string, fields Fields) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "":
		fields.Add("name", "required", "name is required")
	case utf8.RuneCountInString(name) > 100:
		fields.Add("name", "too_long", "name must contain at most 100 characters")
	default:
		for _, r := range name {
			if unicode.IsControl(r) {
				fields.Add("name", "invalid_value", "name cannot contain control characters")
				break
			}
		}
	}
	return name
}

func ValidateCheck(c *Check, fields Fields) {
	if c.Method != "GET" {
		fields.Add("check.method", "invalid_value", "method must be GET")
	}
	if c.ExpectedStatus < 100 || c.ExpectedStatus > 599 {
		fields.Add("check.expectedStatus", "out_of_range", "expectedStatus must be 100–599")
	}
	if c.DeadlineMs < 1000 || c.DeadlineMs > 30000 {
		fields.Add("check.deadlineMs", "out_of_range", "deadlineMs must be 1000–30000")
	}
	c.MaxBodyBytes = MaxBodyBytes
}

func New(name string, c Check, now time.Time) Monitor {
	stamp := Stamp(now)
	return Monitor{
		ID:            rand.Text(),
		Name:          name,
		Lifecycle:     "active",
		ConfigVersion: 1,
		Check:         c,
		CreatedAt:     stamp,
		UpdatedAt:     stamp,
	}
}

func (m Monitor) Patch(name *string, check *Check, now time.Time) Monitor {
	if name != nil {
		m.Name = *name
	}
	if check != nil && *check != m.Check {
		m.Check = *check
		m.ConfigVersion++
	}
	m.UpdatedAt = Stamp(now)
	return m
}

func (m Monitor) Transition(action string, now time.Time) (Monitor, bool) {
	stamp := Stamp(now)
	switch action {
	case "pause":
		if m.Lifecycle != "active" {
			return m, false
		}
		m.Lifecycle = "paused"
		m.PausedAt = stamp
	case "resume":
		if m.Lifecycle != "paused" {
			return m, false
		}
		m.Lifecycle = "active"
		m.PausedAt = ""
	case "archive":
		if m.Lifecycle == "archived" {
			return m, false
		}
		m.Lifecycle = "archived"
		m.PausedAt = ""
		m.ArchivedAt = stamp
	default:
		return m, false
	}
	m.UpdatedAt = stamp
	return m, true
}
