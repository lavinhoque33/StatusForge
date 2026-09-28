// Package incident implements counted-evidence transitions and notification intent.
package incident

import (
	"encoding/json"
	"fmt"
	"time"
)

type Policy struct {
	OpenAfter    int `json:"openAfter"    dynamodbav:"openAfter"`
	RecoverAfter int `json:"recoverAfter" dynamodbav:"recoverAfter"`
}

func (p Policy) Defaults() Policy {
	if p.OpenAfter == 0 {
		p.OpenAfter = 2
	}
	if p.RecoverAfter == 0 {
		p.RecoverAfter = 2
	}
	return p
}

type (
	Evidence struct {
		Kind                string  `json:"kind"           dynamodbav:"kind,omitempty"`
		ObservationID       string  `json:"observationId"  dynamodbav:"observationId"`
		StartedAt           string  `json:"startedAt"      dynamodbav:"startedAt"`
		InitiatedBy         string  `json:"initiatedBy"    dynamodbav:"initiatedBy"`
		Outcome             string  `json:"outcome"        dynamodbav:"outcome"`
		Reason              string  `json:"reason"         dynamodbav:"reason"`
		ObservedStatus      *int    `json:"observedStatus" dynamodbav:"observedStatus"`
		ConfigVersion       int     `json:"configVersion"  dynamodbav:"configVersion"`
		MaintenanceWindowID *string `json:"-"              dynamodbav:"-"`
	}
	Evaluation struct {
		Revision   int        `json:"revision"   dynamodbav:"revision"`
		FailRun    []Evidence `json:"failRun"    dynamodbav:"failRun"`
		HealthyRun []Evidence `json:"healthyRun" dynamodbav:"healthyRun"`
	}
	Open struct {
		ID             string `json:"id"       dynamodbav:"id"`
		OpenedAt       string `json:"openedAt" dynamodbav:"openedAt"`
		NextReminderAt string `json:"-"        dynamodbav:"nextReminderAt"`
		ReminderSeq    int    `json:"-"        dynamodbav:"reminderSeq"`
	}
	Transition struct {
		Kind     string
		Evidence []Evidence
		Last     *Evidence
	}
)

func (e Evidence) MarshalJSON() ([]byte, error) {
	type stored Evidence
	if e.Kind == "" {
		e.Kind = "http_check"
	}
	return json.Marshal(stored(e))
}

func Evaluate(
	policy Policy,
	current Evaluation,
	open *Open,
	evidence Evidence,
	now time.Time,
) (Evaluation, *Transition) {
	p := policy.Defaults()
	next := current
	next.Revision++
	if evidence.MaintenanceWindowID != nil {
		if open != nil {
			return next, &Transition{Kind: "maintenance"}
		}
		return next, nil
	}
	if evidence.Outcome == "checker_problem" {
		if open != nil {
			return next, &Transition{Kind: "checker_problem", Last: &evidence}
		}
		return next, nil
	}
	if evidence.Outcome == "failing" {
		next.HealthyRun = nil
		if open != nil {
			return next, &Transition{Kind: "extend", Last: &evidence}
		}
		next.FailRun = append(append([]Evidence(nil), current.FailRun...), evidence)
		if len(next.FailRun) > 5 {
			next.FailRun = next.FailRun[len(next.FailRun)-5:]
		}
		if len(next.FailRun) >= p.OpenAfter {
			run := next.FailRun
			next.FailRun = nil
			return next, &Transition{Kind: "opened", Evidence: run}
		}
		return next, nil
	}
	if evidence.Outcome == "healthy" {
		next.FailRun = nil
		if open == nil {
			return next, nil
		}
		next.HealthyRun = append(append([]Evidence(nil), current.HealthyRun...), evidence)
		if len(next.HealthyRun) > 5 {
			next.HealthyRun = next.HealthyRun[len(next.HealthyRun)-5:]
		}
		if len(next.HealthyRun) >= p.RecoverAfter {
			run := next.HealthyRun
			next.HealthyRun = nil
			return next, &Transition{Kind: "resolved", Evidence: run}
		}
	}
	return next, nil
}

func Clear(e Evaluation) Evaluation {
	return Evaluation{Revision: e.Revision + 1, FailRun: []Evidence{}, HealthyRun: []Evidence{}}
}

func NextSlot(opened time.Time, interval time.Duration, now time.Time) time.Time {
	if interval <= 0 {
		return now
	}
	n := int64(0)
	if !now.Before(opened) {
		n = int64(now.Sub(opened) / interval)
	}
	return opened.Add(time.Duration(n+1) * interval)
}
func ReminderKey(seq int) string { return fmt.Sprintf("reminder#%04d", seq) }

var Results = map[string]bool{
	"in_flight":          true,
	"delivered":          true,
	"http_error":         true,
	"rejected":           true,
	"timeout":            true,
	"connection_refused": true,
	"connection_error":   true,
	"outcome_unknown":    true,
	"refused_by_policy":  true,
	"process_stopped":    true,
}

func RetryAt(start time.Time, attempts int, schedule []time.Duration) (time.Time, bool) {
	if attempts < 1 || attempts > len(schedule) {
		return time.Time{}, false
	}
	return start.Add(schedule[attempts-1]), true
}

type (
	PayloadMonitor struct {
		ID   string  `json:"id"`
		Name string  `json:"name"`
		URL  *string `json:"url"`
		Kind string  `json:"kind"`
	}
	PayloadIncident struct {
		ID           string  `json:"id"`
		OpenedAt     string  `json:"openedAt"`
		ResolvedAt   *string `json:"resolvedAt"`
		Resolution   *string `json:"resolution"`
		FailureCount int     `json:"failureCount"`
		Path         string  `json:"path"`
	}
)

func Payload(
	id, kind string,
	seq *int,
	mon PayloadMonitor,
	inc PayloadIncident,
	evidence []Evidence,
	created string,
) (string, error) {
	if evidence == nil {
		evidence = []Evidence{}
	}
	v := struct {
		Schema      string          `json:"schema"`
		ID          string          `json:"id"`
		Kind        string          `json:"kind"`
		ReminderSeq *int            `json:"reminderSeq"`
		Monitor     PayloadMonitor  `json:"monitor"`
		Incident    PayloadIncident `json:"incident"`
		Evidence    []Evidence      `json:"evidence"`
		CreatedAt   string          `json:"createdAt"`
	}{"statusforge.notification.v1", id, kind, seq, mon, inc, evidence, created}
	b, err := json.Marshal(v)
	return string(b), err
}
