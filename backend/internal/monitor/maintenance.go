package monitor

import (
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/incident"
)

// Maintenance stores the bounded claim-time index; presentation is derived on read.
type Maintenance struct {
	Windows  []incident.WindowRef `json:"-"      dynamodbav:"windows"`
	ActiveID string               `json:"-"      dynamodbav:"activeId,omitempty"`
	Active   *MaintenanceWindow   `json:"active" dynamodbav:"-"`
	Next     *MaintenanceWindow   `json:"next"   dynamodbav:"-"`
}

type MaintenanceWindow struct {
	ID          string  `json:"id"          dynamodbav:"windowId"`
	MonitorID   string  `json:"monitorId"   dynamodbav:"monitorId"`
	StartAt     string  `json:"startAt"     dynamodbav:"startAt"`
	EndAt       string  `json:"endAt"       dynamodbav:"endAt"`
	Note        string  `json:"note"        dynamodbav:"note"`
	CreatedAt   string  `json:"createdAt"   dynamodbav:"createdAt"`
	CancelledAt *string `json:"cancelledAt" dynamodbav:"cancelledAt,omitempty"`
	State       string  `json:"state"       dynamodbav:"-"`
}

func (w MaintenanceWindow) WithState(at time.Time) MaintenanceWindow {
	switch {
	case w.CancelledAt != nil:
		w.State = "cancelled"
	case ContainsWindow(w, at):
		w.State = "active"
	case w.StartAt > Stamp(at):
		w.State = "scheduled"
	default:
		w.State = "ended"
	}
	return w
}

func ContainsWindow(w MaintenanceWindow, at time.Time) bool {
	return incident.Contains(
		incident.WindowRef{WindowID: w.ID, StartAt: w.StartAt, EndAt: w.EndAt},
		at,
	) &&
		w.CancelledAt == nil
}
