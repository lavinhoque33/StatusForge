package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type (
	heartbeatStore interface {
		AuthenticateHeartbeat(context.Context, string, string) (monitor.Monitor, error)
		RecordHeartbeat(
			context.Context,
			string,
			string,
			heartbeat.Report,
			time.Time,
		) (store.ReportResult, error)
		HeartbeatToken(context.Context, string, bool, time.Time) (string, *heartbeat.Token, error)
		PatchHeartbeat(
			context.Context,
			string,
			int,
			*string,
			*heartbeat.Schedule,
			*incident.Policy,
			time.Time,
		) (monitor.Monitor, error)
	}
	bucket struct {
		tokens float64
		at     time.Time
	}
	heartbeatLimiters struct {
		mu       sync.Mutex
		monitors map[string]bucket
		remote   map[string]bucket
	}
)

func newHeartbeatLimiters() *heartbeatLimiters {
	return &heartbeatLimiters{monitors: map[string]bucket{}, remote: map[string]bucket{}}
}

func (l *heartbeatLimiters) allow(collection map[string]bucket, key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := collection[key]
	if !ok {
		b = bucket{tokens: 5, at: now}
	}
	elapsed := now.Sub(b.at).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed
		if b.tokens > 5 {
			b.tokens = 5
		}
	}
	b.at = now
	if b.tokens < 1 {
		collection[key] = b
		return false
	}
	b.tokens--
	collection[key] = b
	return true
}

func (s *monitorAPI) createHeartbeat(
	w http.ResponseWriter,
	r *http.Request,
	name *string,
	hasCheck bool,
	schedule *heartbeat.Schedule,
	hasHTTPInterval bool,
	policy *incident.Policy,
) {
	fields := monitor.Fields{}
	value := ""
	if name == nil {
		fields.Add("name", "required", "name is required")
	} else {
		value = monitor.ValidateName(*name, fields)
	}
	if hasCheck {
		fields.Add("check", "unexpected", "check is not valid for heartbeat monitors")
	}
	if hasHTTPInterval {
		fields.Add(
			"intervalSeconds",
			"unexpected",
			"intervalSeconds is not valid for heartbeat monitors",
		)
	}
	if schedule == nil {
		fields.Add("heartbeat", "required", "heartbeat schedule is required")
	} else {
		s.validateHeartbeatSchedule(*schedule, fields)
	}
	p := incident.Policy{OpenAfter: 1, RecoverAfter: 1}
	if policy != nil {
		p = *policy
	}
	validateIncidentPolicy(p, fields)
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	now := s.now()
	m := monitor.New(value, monitor.Check{}, now)
	m.Kind = "heartbeat"
	m.IntervalSeconds = 0
	m.Heartbeat = &heartbeat.Configuration{
		Schedule:   *schedule,
		IngestPath: "/ingest/heartbeats/" + m.ID,
	}
	expect := heartbeat.Expect(now, *schedule)
	m.Expectation = &expect
	m.IncidentPolicy = p
	token, e := heartbeat.Generate()
	if e != nil {
		apiError(w, 503, "store_unavailable")
		return
	}
	m.Heartbeat.Token = &heartbeat.Token{
		Hash:      heartbeat.Hash(token),
		Hint:      heartbeat.Hint(token),
		CreatedAt: monitor.Stamp(now),
	}
	if e = s.store.Create(r.Context(), m); e != nil {
		s.failure(w, e)
		return
	}
	payload, e := json.Marshal(monitor.WithStatus(m, now))
	if e != nil {
		apiError(w, 503, "store_unavailable")
		return
	}
	var body map[string]any
	if json.Unmarshal(payload, &body) != nil {
		apiError(w, 503, "store_unavailable")
		return
	}
	body["issuedToken"] = token
	w.Header().Set("Location", "/api/monitors/"+m.ID)
	writeJSON(w, 201, body)
}

func (s *monitorAPI) validateHeartbeatSchedule(value heartbeat.Schedule, fields monitor.Fields) {
	if !heartbeat.Allowed(value.IntervalSeconds, heartbeat.Intervals(s.minInterval)) {
		fields.Add("heartbeat.intervalSeconds", "not_allowed", "interval is not allowed")
	}
	if !heartbeat.Allowed(value.GraceSeconds, heartbeat.Graces(s.minInterval)) {
		fields.Add("heartbeat.graceSeconds", "not_allowed", "grace is not allowed")
	}
	if value.GraceSeconds > value.IntervalSeconds {
		fields.Add("heartbeat.graceSeconds", "exceeds_interval", "grace exceeds interval")
	}
}

func (s *monitorAPI) patchHeartbeat(
	w http.ResponseWriter,
	r *http.Request,
	version *int,
	name *string,
	hasCheck, hasHTTPInterval bool,
	kind *string,
	schedule *heartbeat.Schedule,
	policy *incident.Policy,
) {
	fields := monitor.Fields{}
	if version == nil || *version < 1 {
		fields.Add("expectedConfigVersion", "required", "version is required")
	}
	if name != nil {
		v := monitor.ValidateName(*name, fields)
		name = &v
	}
	if hasCheck {
		fields.Add("check", "unexpected", "check is not valid for heartbeat monitors")
	}
	if hasHTTPInterval {
		fields.Add(
			"intervalSeconds",
			"unexpected",
			"intervalSeconds is not valid for heartbeat monitors",
		)
	}
	if kind != nil {
		fields.Add("kind", "immutable", "kind cannot change")
	}
	if schedule != nil {
		s.validateHeartbeatSchedule(*schedule, fields)
	}
	if policy != nil {
		validateIncidentPolicy(*policy, fields)
	}
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	v, ok := s.store.(heartbeatStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return
	}
	m, e := v.PatchHeartbeat(
		r.Context(),
		chi.URLParam(r, "id"),
		*version,
		name,
		schedule,
		policy,
		s.now(),
	)
	if e != nil {
		s.failure(w, e)
		return
	}
	writeJSON(w, 200, monitor.WithStatus(m, s.now()))
}

func (s *monitorAPI) heartbeatToken(w http.ResponseWriter, r *http.Request) {
	s.tokenChange(w, r, false)
}

func (s *monitorAPI) heartbeatRevoke(w http.ResponseWriter, r *http.Request) {
	s.tokenChange(w, r, true)
}

func (s *monitorAPI) tokenChange(w http.ResponseWriter, r *http.Request, revoke bool) {
	v, ok := s.store.(heartbeatStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return
	}
	token, metadata, e := v.HeartbeatToken(r.Context(), chi.URLParam(r, "id"), revoke, s.now())
	if e != nil {
		s.failure(w, e)
		return
	}
	if revoke {
		w.WriteHeader(204)
		return
	}
	writeJSON(
		w,
		201,
		map[string]string{"token": token, "hint": metadata.Hint, "createdAt": metadata.CreatedAt},
	)
}

func (s *monitorAPI) ingestHeartbeat(w http.ResponseWriter, r *http.Request) {
	body, e := heartbeat.ReadBody(r.Body)
	if e != nil {
		if e.Error() == "too_large" {
			apiError(w, 413, "too_large")
		} else {
			apiError(w, 400, "invalid_json")
		}
		return
	}
	id := chi.URLParam(r, "id")
	token := ""
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		token = strings.TrimPrefix(header, "Bearer ")
	}
	v, ok := s.store.(heartbeatStore)
	var m monitor.Monitor
	var authErr error
	if ok && token != "" {
		m, authErr = v.AuthenticateHeartbeat(r.Context(), id, token)
	} else {
		authErr = store.ErrNotEligible
	}
	if errors.Is(authErr, store.ErrUnavailable) {
		apiError(w, 503, "store_unavailable")
		return
	}
	now := s.now()
	if authErr != nil {
		remote, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			remote = r.RemoteAddr
		}
		if !s.limiters.allow(s.limiters.remote, remote, now) {
			w.Header().Set("Retry-After", "1")
			apiError(w, 429, "rate_limited")
			return
		}
		apiError(w, 401, "unauthorized")
		return
	}
	if !s.limiters.allow(s.limiters.monitors, id, now) {
		w.Header().Set("Retry-After", "1")
		apiError(w, 429, "rate_limited")
		return
	}
	if m.Lifecycle == "archived" {
		apiError(w, 410, "archived")
		return
	}
	if len(body) > 0 &&
		!strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		apiError(w, 400, "invalid_json")
		return
	}
	report, fields, e := heartbeat.Validate(body, now)
	if e != nil {
		apiError(w, 400, "invalid_json")
		return
	}
	if len(fields) > 0 {
		f := monitor.Fields{}
		for path, issue := range fields {
			f.Add(path, issue.Code, issue.Message)
		}
		fieldsError(w, f)
		return
	}
	result, e := v.RecordHeartbeat(r.Context(), id, m.Heartbeat.Token.Hash, report, now)
	if e != nil {
		if errors.Is(e, store.ErrArchived) {
			apiError(w, 410, "archived")
		} else if errors.Is(e, store.ErrNotEligible) {
			apiError(w, 401, "unauthorized")
		} else {
			apiError(w, 503, "store_unavailable")
		}
		return
	}
	if result.Duplicate {
		writeJSON(w, 200, map[string]bool{"accepted": true, "duplicate": true})
		return
	}
	writeJSON(w, 202, result)
}
