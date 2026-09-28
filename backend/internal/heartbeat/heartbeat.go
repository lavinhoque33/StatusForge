// Package heartbeat owns receipt validation, schedule arithmetic and credential primitives.
package heartbeat

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

var runID = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)

type (
	Schedule struct {
		IntervalSeconds int `json:"intervalSeconds" dynamodbav:"intervalSeconds"`
		GraceSeconds    int `json:"graceSeconds"    dynamodbav:"graceSeconds"`
	}
	Token struct {
		Hash      string `json:"-"         dynamodbav:"hash"`
		Hint      string `json:"hint"      dynamodbav:"hint"`
		CreatedAt string `json:"createdAt" dynamodbav:"createdAt"`
	}
	Configuration struct {
		Schedule
		Token        *Token  `json:"token"        dynamodbav:"token,omitempty"`
		IngestPath   string  `json:"ingestPath"   dynamodbav:"-"`
		LastReportAt *string `json:"lastReportAt" dynamodbav:"lastReportAt,omitempty"`
	}
	Expectation struct {
		Reference      string `json:"-"         dynamodbav:"reference"`
		DueAt          string `json:"dueAt"     dynamodbav:"dueAt"`
		LateAt         string `json:"lateAt"    dynamodbav:"lateAt"`
		MissingAt      string `json:"missingAt" dynamodbav:"missingAt"`
		StaleAt        string `json:"staleAt"   dynamodbav:"staleAt"`
		LastFinishedAt string `json:"-"         dynamodbav:"lastFinishedAt,omitempty"`
	}
	Report struct {
		Status     string  `json:"-"          dynamodbav:"-"`
		RunID      *string `json:"runId"      dynamodbav:"runId,omitempty"`
		FinishedAt *string `json:"finishedAt" dynamodbav:"finishedAt,omitempty"`
		DurationMs *int64  `json:"durationMs" dynamodbav:"durationMs,omitempty"`
		ExitCode   *int64  `json:"exitCode"   dynamodbav:"exitCode,omitempty"`
		Message    *string `json:"message"    dynamodbav:"message,omitempty"`
		Late       bool    `json:"late"       dynamodbav:"late"`
	}
	Outage struct {
		From string `json:"from" dynamodbav:"from"`
		To   string `json:"to"   dynamodbav:"to"`
	}
	Liveness struct {
		AliveAt string   `json:"aliveAt" dynamodbav:"aliveAt"`
		Outages []Outage `json:"outages" dynamodbav:"outages"`
	}
	FieldError struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
)

var livenessSeconds atomic.Int64

func SetLivenessInterval(seconds int) { livenessSeconds.Store(int64(seconds)) }
func LivenessInterval() int64 {
	n := livenessSeconds.Load()
	if n == 0 {
		return 10
	}
	return n
}

func Intervals(min int) []int {
	all := []int{300, 900, 1800, 3600, 21600, 43200, 86400, 604800}
	if min < 60 {
		all = append([]int{10, 15, 30}, all...)
	}
	return all
}

func Graces(min int) []int {
	all := []int{60, 300, 900, 1800, 3600, 21600}
	if min < 60 {
		all = append([]int{5, 10}, all...)
	}
	return all
}

func Allowed(v int, options []int) bool {
	for _, n := range options {
		if n == v {
			return true
		}
	}
	return false
}
func Stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
func Expect(reference time.Time, schedule Schedule) Expectation {
	due := reference.Add(time.Duration(schedule.IntervalSeconds) * time.Second)
	missing := due.Add(time.Duration(schedule.GraceSeconds) * time.Second)
	return Expectation{
		Reference: Stamp(reference),
		DueAt:     Stamp(due),
		LateAt:    Stamp(due),
		MissingAt: Stamp(missing),
		StaleAt:   Stamp(missing.Add(time.Duration(3*LivenessInterval()+5) * time.Second)),
	}
}

func Change(previous Expectation, schedule Schedule, now time.Time) Expectation {
	reference, e := time.Parse(time.RFC3339Nano, previous.Reference)
	if e != nil {
		reference = now
	}
	next := Expect(reference, schedule)
	missing, _ := time.Parse(time.RFC3339Nano, next.MissingAt)
	if missing.Before(now) {
		next = Expect(now.Add(-time.Duration(schedule.IntervalSeconds)*time.Second), schedule)
		next.Reference = Stamp(now)
	}
	next.LastFinishedAt = previous.LastFinishedAt
	return next
}

func Inside(at time.Time, outages []Outage) bool {
	for _, o := range outages {
		from, e1 := time.Parse(time.RFC3339Nano, o.From)
		to, e2 := time.Parse(time.RFC3339Nano, o.To)
		if e1 == nil && e2 == nil && at.After(from) && !at.After(to) {
			return true
		}
	}
	return false
}

// OverlappingOutage returns the end of the latest outage that could have
// prevented a report in any collapsed window. A previous judged deadline
// bounds the oldest window even when the last counted report is much older.
func OverlappingOutage(
	reference, oldestDue, latestMissing time.Time,
	interval time.Duration,
	outages []Outage,
) (time.Time, bool) {
	start := oldestDue.Add(-interval)
	if reference.After(start) {
		start = reference
	}
	var end time.Time
	for _, outage := range outages {
		from, fromErr := time.Parse(time.RFC3339Nano, outage.From)
		to, toErr := time.Parse(time.RFC3339Nano, outage.To)
		if fromErr == nil && toErr == nil && to.After(start) && from.Before(latestMissing) &&
			to.After(end) {
			end = to
		}
	}
	return end, !end.IsZero()
}

func Generate() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sfh_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), nil
}

func GenerateDeployment() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "sfd_" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b[:]), nil
}

func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func Hint(token string) string {
	if len(token) < 4 {
		return token
	}
	return token[len(token)-4:]
}

func Matches(token, hash string) bool {
	provided := sha256.Sum256([]byte(token))
	expected, e := hex.DecodeString(hash)
	return e == nil && len(expected) == len(provided) &&
		subtle.ConstantTimeCompare(provided[:], expected) == 1
}

func Validate(raw []byte, received time.Time) (Report, map[string]FieldError, error) {
	var report Report
	if len(raw) == 0 {
		return report, nil, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return report, nil, errors.New("invalid_json")
	}
	fields := map[string]FieldError{}
	for name := range object {
		switch name {
		case "status", "runId", "finishedAt", "durationMs", "exitCode", "message":
		default:
			fields[name] = FieldError{"unknown_field", "unknown field"}
		}
	}
	if len(fields) > 0 {
		return report, fields, nil
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		return report, nil, errors.New("invalid_json")
	}
	if value, ok := object["status"]; ok {
		if err := json.Unmarshal(value, &report.Status); err != nil {
			return report, nil, errors.New("invalid_json")
		}
	}
	if report.Status != "" && report.Status != "success" && report.Status != "failure" {
		fields["status"] = FieldError{"invalid_value", "status must be success or failure"}
	}
	if report.RunID != nil && !runID.MatchString(*report.RunID) {
		fields["runId"] = FieldError{"invalid_value", "invalid run ID"}
	}
	if report.FinishedAt != nil {
		t, e := time.Parse(time.RFC3339Nano, *report.FinishedAt)
		if e != nil || t.After(received.Add(time.Minute)) || t.Before(received.Add(-24*time.Hour)) {
			fields["finishedAt"] = FieldError{"out_of_range", "finishedAt outside allowed range"}
		}
	}
	if report.DurationMs != nil && (*report.DurationMs < 0 || *report.DurationMs > 604800000) {
		fields["durationMs"] = FieldError{"out_of_range", "duration outside allowed range"}
	}
	if report.ExitCode != nil && (*report.ExitCode < -2147483648 || *report.ExitCode > 2147483647) {
		fields["exitCode"] = FieldError{"out_of_range", "exit code outside allowed range"}
	}
	if report.Message != nil {
		if len([]rune(*report.Message)) > 200 ||
			strings.IndexFunc(*report.Message, unicode.IsControl) >= 0 {
			fields["message"] = FieldError{
				"invalid_value",
				"message must be at most 200 characters without controls",
			}
		}
	}
	return report, fields, nil
}

func ReadBody(reader io.Reader) ([]byte, error) {
	body, e := io.ReadAll(io.LimitReader(reader, 4097))
	if e != nil {
		return nil, e
	}
	if len(body) > 4096 {
		return nil, fmt.Errorf("too_large")
	}
	return body, nil
}
