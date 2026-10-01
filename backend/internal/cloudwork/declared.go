package cloudwork

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
)

// MonitorsVariable declares the cloud monitors.
const MonitorsVariable = "STATUSFORGE_CLOUD_MONITORS"

// CloudIntervals are the allowed cloud intervals; scheduler precision is 60 s.
var CloudIntervals = []int{60, 120, 300, 600, 900, 3600}

// Declaration is one declared monitor.
type Declaration struct {
	Key             string
	Name            string
	URL             string
	IntervalSeconds int
	ExpectedStatus  int
	DeadlineMs      int
}

// Check is the monitor check the declaration describes.
func (d Declaration) Check() monitor.Check {
	return monitor.Check{
		URL:            d.URL,
		Method:         "GET",
		ExpectedStatus: d.ExpectedStatus,
		DeadlineMs:     d.DeadlineMs,
		MaxBodyBytes:   monitor.MaxBodyBytes,
	}
}

// URLPolicy is the cloud destination policy as the planner uses it.
type URLPolicy interface {
	// Validate is the static check: a field error code and message, or "".
	Validate(raw string) (string, string)
	// CheckResolved also resolves the host; every address must be public.
	CheckResolved(ctx context.Context, raw string) error
}

// DeclarationError names the entry index and field, never the value.
type DeclarationError struct {
	Index int
	Field string
	Code  string
}

func (e DeclarationError) Error() string {
	if e.Index < 0 {
		return fmt.Sprintf("%s: %s", MonitorsVariable, e.Code)
	}
	return fmt.Sprintf("%s[%d].%s: %s", MonitorsVariable, e.Index, e.Field, e.Code)
}

type declarationJSON struct {
	Key             *string `json:"key"`
	Name            *string `json:"name"`
	URL             *string `json:"url"`
	IntervalSeconds *int    `json:"intervalSeconds"`
	ExpectedStatus  *int    `json:"expectedStatus"`
	DeadlineMs      *int    `json:"deadlineMs"`
}

// ParseDeclarations validates the declared monitors: keys, name and check
// rules, cloud intervals, and the static destination policy. expectedStatus
// and deadlineMs default as in the API (200, 10000 ms).
func ParseDeclarations(raw string, policy URLPolicy) ([]Declaration, error) {
	var entries []json.RawMessage
	if json.Unmarshal([]byte(raw), &entries) != nil || entries == nil {
		return nil, DeclarationError{
			Index: -1,
			Code:  "must be a JSON array of monitor declarations",
		}
	}
	result := make([]Declaration, 0, len(entries))
	seen := map[string]bool{}
	for i, entry := range entries {
		var in declarationJSON
		decoder := json.NewDecoder(bytes.NewReader(entry))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&in) != nil {
			return nil, DeclarationError{i, "entry", "must be an object with known fields only"}
		}
		d, err := declaration(i, in, policy)
		if err != nil {
			return nil, err
		}
		if seen[d.Key] {
			return nil, DeclarationError{i, "key", "duplicate"}
		}
		seen[d.Key] = true
		result = append(result, d)
	}
	return result, nil
}

func declaration(i int, in declarationJSON, policy URLPolicy) (Declaration, error) {
	for _, required := range []struct {
		field   string
		present bool
	}{
		{"key", in.Key != nil},
		{"name", in.Name != nil},
		{"url", in.URL != nil},
		{"intervalSeconds", in.IntervalSeconds != nil},
	} {
		if !required.present {
			return Declaration{}, DeclarationError{i, required.field, "required"}
		}
	}
	if !validKey(*in.Key) {
		return Declaration{}, DeclarationError{i, "key", "must be 1–64 characters of [a-z0-9-]"}
	}
	fields := monitor.Fields{}
	name := monitor.ValidateName(*in.Name, fields)
	d := Declaration{
		Key:             *in.Key,
		Name:            name,
		URL:             *in.URL,
		IntervalSeconds: *in.IntervalSeconds,
		ExpectedStatus:  200,
		DeadlineMs:      10000,
	}
	if in.ExpectedStatus != nil {
		d.ExpectedStatus = *in.ExpectedStatus
	}
	if in.DeadlineMs != nil {
		d.DeadlineMs = *in.DeadlineMs
	}
	check := d.Check()
	monitor.ValidateCheck(&check, fields)
	if code, _ := policy.Validate(d.URL); code != "" {
		fields.Add("url", code, "")
	}
	if !slices.Contains(CloudIntervals, d.IntervalSeconds) {
		fields.Add("intervalSeconds", "invalid_value", "")
	}
	if len(fields) > 0 {
		paths := make([]string, 0, len(fields))
		for path := range fields {
			paths = append(paths, path)
		}
		sort.Strings(paths)
		return Declaration{}, DeclarationError{
			Index: i,
			Field: strings.TrimPrefix(paths[0], "check."),
			Code:  fields[paths[0]].Code,
		}
	}
	return d, nil
}

func validKey(key string) bool {
	if key == "" || len(key) > 64 {
		return false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// DeclaredID is the monitor ID of the generation-th monitor declared under
// key. It is deterministic, so two overlapping passes cannot both create the
// same declaration: the store's create is conditional on the ID being new.
func DeclaredID(key string, generation int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "statusforge-declared#%s#%d", key, generation))
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:16])
}

// ReconcileStore is the monitor save path the reconciliation uses.
type ReconcileStore interface {
	List(context.Context) ([]monitor.Monitor, error)
	Create(context.Context, monitor.Monitor) error
	PatchInterval(
		ctx context.Context,
		id string,
		expected int,
		name *string,
		check *monitor.Check,
		interval *int,
		now time.Time,
	) (monitor.Monitor, error)
	Lifecycle(ctx context.Context, id, action string, now time.Time) (monitor.Monitor, error)
}

// ReconcileReport counts the writes one reconciliation made.
type ReconcileReport struct{ Created, Updated, Archived int }

type reconcilePlan struct {
	create  []Declaration
	update  []Declaration
	archive []monitor.Monitor
	// saves holds the keys about to be created or updated.
	saves   map[string]bool
	current map[string]monitor.Monitor
	// generations counts every stored monitor per key; the next create's ID
	// uses it, so a re-declared key never reuses an archived monitor's ID.
	generations map[string]int
}

// Reconcile makes the stored declared monitors match decls through the
// existing save paths: create (queues a create run), PATCH (bumps the
// version and queues a config_change run when the check changes), and
// archive. Every URL about to be saved is resolved and checked first, so a
// refused declaration fails the pass before any write. Unchanged
// declarations write nothing.
func Reconcile(
	ctx context.Context,
	st ReconcileStore,
	policy URLPolicy,
	decls []Declaration,
	now time.Time,
) (ReconcileReport, error) {
	var report ReconcileReport
	monitors, err := st.List(ctx)
	if err != nil {
		return report, err
	}
	plan := planReconcile(decls, monitors)
	for i, d := range decls {
		if !plan.saves[d.Key] {
			continue
		}
		if err := policy.CheckResolved(ctx, d.URL); err != nil {
			return report, DeclarationError{i, "url", "refused_by_policy"}
		}
	}
	for _, d := range plan.create {
		m := monitor.New(d.Name, d.Check(), now)
		m.ID = DeclaredID(d.Key, plan.generations[d.Key])
		m.IntervalSeconds = d.IntervalSeconds
		m.DeclaredKey = d.Key
		if err := st.Create(ctx, m); err != nil {
			return report, fmt.Errorf("create declared monitor %s: %w", d.Key, err)
		}
		report.Created++
	}
	for _, d := range plan.update {
		m := plan.current[d.Key]
		name, check, interval := d.Name, d.Check(), d.IntervalSeconds
		_, err := st.PatchInterval(ctx, m.ID, m.ConfigVersion, &name, &check, &interval, now)
		if err != nil {
			return report, fmt.Errorf("update declared monitor %s: %w", d.Key, err)
		}
		report.Updated++
	}
	for _, m := range plan.archive {
		if _, err := st.Lifecycle(ctx, m.ID, "archive", now); err != nil {
			return report, fmt.Errorf("archive declared monitor %s: %w", m.DeclaredKey, err)
		}
		report.Archived++
	}
	return report, nil
}

func planReconcile(decls []Declaration, monitors []monitor.Monitor) reconcilePlan {
	plan := reconcilePlan{
		saves:       map[string]bool{},
		current:     map[string]monitor.Monitor{},
		generations: map[string]int{},
	}
	var extra []monitor.Monitor
	for _, m := range monitors {
		if m.DeclaredKey == "" || m.Kind == "heartbeat" {
			continue
		}
		plan.generations[m.DeclaredKey]++
		if m.Lifecycle == "archived" || m.Deletion != nil {
			continue
		}
		if _, dup := plan.current[m.DeclaredKey]; dup {
			// List is oldest first: keep the oldest, archive any later copy.
			extra = append(extra, m)
			continue
		}
		plan.current[m.DeclaredKey] = m
	}
	declared := map[string]bool{}
	for _, d := range decls {
		declared[d.Key] = true
		m, ok := plan.current[d.Key]
		switch {
		case !ok:
			plan.create = append(plan.create, d)
			plan.saves[d.Key] = true
		case m.Name != d.Name || m.Check != d.Check() || m.IntervalSeconds != d.IntervalSeconds:
			plan.update = append(plan.update, d)
			plan.saves[d.Key] = true
		}
	}
	for _, m := range plan.current {
		if !declared[m.DeclaredKey] {
			plan.archive = append(plan.archive, m)
		}
	}
	sort.Slice(plan.archive, func(i, j int) bool { return plan.archive[i].ID < plan.archive[j].ID })
	plan.archive = append(plan.archive, extra...)
	return plan
}
