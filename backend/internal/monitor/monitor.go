package monitor

import (
	"crypto/rand"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
)

const MaxBodyBytes = 65536

type Check struct {
	URL            string `json:"url"            dynamodbav:"url"`
	Method         string `json:"method"         dynamodbav:"method"`
	ExpectedStatus int    `json:"expectedStatus" dynamodbav:"expectedStatus"`
	DeadlineMs     int    `json:"deadlineMs"     dynamodbav:"deadlineMs"`
	MaxBodyBytes   int    `json:"maxBodyBytes"   dynamodbav:"maxBodyBytes"`
}
type Deletion struct {
	State        string `json:"state"        dynamodbav:"state"`
	RequestedAt  string `json:"requestedAt"  dynamodbav:"requestedAt"`
	UpdatedAt    string `json:"updatedAt"    dynamodbav:"updatedAt"`
	RemovedItems int    `json:"removedItems" dynamodbav:"removedItems"`
}

type Monitor struct {
	ID               string                   `json:"id"                   dynamodbav:"monitorId"`
	ApplicationID    string                   `json:"applicationId"        dynamodbav:"applicationId,omitempty"`
	Name             string                   `json:"name"                 dynamodbav:"name"`
	Lifecycle        string                   `json:"lifecycle"            dynamodbav:"lifecycle"`
	ConfigVersion    int                      `json:"configVersion"        dynamodbav:"configVersion"`
	IntervalSeconds  int                      `json:"intervalSeconds"      dynamodbav:"intervalSeconds"`
	Kind             string                   `json:"kind"                 dynamodbav:"kind,omitempty"`
	Heartbeat        *heartbeat.Configuration `json:"heartbeat"            dynamodbav:"heartbeat,omitempty"`
	Expectation      *heartbeat.Expectation   `json:"expectation"          dynamodbav:"expectation,omitempty"`
	IncidentPolicy   incident.Policy          `json:"incidentPolicy"       dynamodbav:"incidentPolicy"`
	Evaluation       incident.Evaluation      `json:"-"                    dynamodbav:"evaluation"`
	Maintenance      Maintenance              `json:"maintenance"          dynamodbav:"maintenance"`
	OpenIncident     *incident.Open           `json:"openIncident"         dynamodbav:"openIncident,omitempty"`
	ScheduledThrough string                   `json:"-"                    dynamodbav:"scheduledThrough,omitempty"`
	LegacyCursor     bool                     `json:"-"                    dynamodbav:"-"`
	Lease            *Lease                   `json:"-"                    dynamodbav:"lease,omitempty"`
	LastClaimAt      string                   `json:"-"                    dynamodbav:"lastClaimAt,omitempty"`
	Evidence         *Evidence                `json:"-"                    dynamodbav:"status,omitempty"`
	Status           Status                   `json:"status"               dynamodbav:"-"`
	Check            Check                    `json:"check"                dynamodbav:"check"`
	CreatedAt        string                   `json:"createdAt"            dynamodbav:"createdAt"`
	UpdatedAt        string                   `json:"updatedAt"            dynamodbav:"updatedAt"`
	PausedAt         string                   `json:"pausedAt,omitempty"   dynamodbav:"pausedAt,omitempty"`
	ArchivedAt       string                   `json:"archivedAt,omitempty" dynamodbav:"archivedAt,omitempty"`
	Deletion         *Deletion                `json:"deletion"             dynamodbav:"deletion,omitempty"`
	// DeclaredKey is the stable key of a cloud-declared monitor (M7 §3.1);
	// empty for monitors created through the API.
	DeclaredKey string `json:"-"                    dynamodbav:"declaredKey,omitempty"`
}
type Observation struct {
	Kind                string            `json:"kind"                     dynamodbav:"kind,omitempty"`
	Report              *heartbeat.Report `json:"report"                   dynamodbav:"report,omitempty"`
	ID                  string            `json:"id"                       dynamodbav:"observationId"`
	MonitorID           string            `json:"monitorId"                dynamodbav:"monitorId"`
	ConfigVersion       int               `json:"configVersion"            dynamodbav:"configVersion"`
	InitiatedBy         string            `json:"initiatedBy"              dynamodbav:"initiatedBy"`
	Trigger             *string           `json:"trigger"                  dynamodbav:"trigger,omitempty"`
	DueAt               *string           `json:"dueAt"                    dynamodbav:"dueAt,omitempty"`
	Counted             bool              `json:"counted"                  dynamodbav:"counted"`
	NotCountedReason    *string           `json:"notCountedReason"         dynamodbav:"notCountedReason,omitempty"`
	MaintenanceWindowID *string           `json:"maintenanceWindowId"      dynamodbav:"maintenanceWindowId,omitempty"`
	Request             Check             `json:"request"                  dynamodbav:"request"`
	StartedAt           string            `json:"startedAt"                dynamodbav:"startedAt"`
	CompletedAt         string            `json:"completedAt"              dynamodbav:"completedAt"`
	DurationMs          int64             `json:"durationMs"               dynamodbav:"durationMs"`
	Outcome             string            `json:"outcome"                  dynamodbav:"outcome"`
	Reason              string            `json:"reason"                   dynamodbav:"reason"`
	ObservedStatus      *int              `json:"observedStatus,omitempty" dynamodbav:"observedStatus,omitempty"`
	BodyBytesRead       int               `json:"bodyBytesRead"            dynamodbav:"bodyBytesRead"`
	BodyTruncated       bool              `json:"bodyTruncated"            dynamodbav:"bodyTruncated"`
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
		ID:              rand.Text(),
		Name:            name,
		Lifecycle:       "active",
		IncidentPolicy:  incident.Policy{OpenAfter: 2, RecoverAfter: 2},
		ConfigVersion:   1,
		Kind:            "http",
		Check:           c,
		CreatedAt:       stamp,
		UpdatedAt:       stamp,
		IntervalSeconds: 300,
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
		if m.Kind == "heartbeat" {
			m.Expectation = nil
		}
	case "resume":
		if m.Lifecycle != "paused" {
			return m, false
		}
		m.Lifecycle = "active"
		m.PausedAt = ""
		if m.Kind == "heartbeat" && m.Heartbeat != nil {
			expectation := heartbeat.Expect(now, m.Heartbeat.Schedule)
			m.Expectation = &expectation
		}
	case "archive":
		if m.Lifecycle == "archived" {
			return m, false
		}
		m.Lifecycle = "archived"
		m.PausedAt = ""
		m.ArchivedAt = stamp
		if m.Kind == "heartbeat" {
			m.Expectation = nil
		}
	default:
		return m, false
	}
	m.UpdatedAt = stamp
	return m, true
}
