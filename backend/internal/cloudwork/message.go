// Package cloudwork holds the cloud planner pass and worker batch handlers,
// the work message format, and the acknowledgement mapping (ADR 0008 D1, D2).
// It imports aws-lambda-go's event types only; the SDK clients live in
// hosteddynamo and the destination policy in cloudtargetpolicy.
package cloudwork

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

// MessageVersion is the only accepted "v".
const MessageVersion = 1

// Message points at one durable work item; the row decides eligibility.
type Message struct {
	V         int    `json:"v"`
	MonitorID string `json:"monitorId"`
	DueAt     string `json:"dueAt"`
}

var errInvalidMessage = errors.New("invalid_message")

// Encode renders the message body.
func (m Message) Encode() string {
	b, _ := json.Marshal(Message{V: MessageVersion, MonitorID: m.MonitorID, DueAt: m.DueAt})
	return string(b)
}

// ParseMessage accepts exactly {"v":1,"monitorId","dueAt"} with a canonical
// work stamp.
func ParseMessage(body string) (Message, error) {
	var m Message
	decoder := json.NewDecoder(bytes.NewReader([]byte(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&m) != nil || decoder.More() {
		return Message{}, errInvalidMessage
	}
	if m.V != MessageVersion || !validMonitorID(m.MonitorID) || !store.ValidWorkStamp(m.DueAt) {
		return Message{}, errInvalidMessage
	}
	return m, nil
}

func validMonitorID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if (r < 'A' || r > 'Z') && (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}
