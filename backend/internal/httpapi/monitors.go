package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
	"github.com/lavinhoque33/statusforge/backend/internal/webui"
)

type MonitorStore interface {
	Create(context.Context, monitor.Monitor) error
	Get(context.Context, string) (monitor.Monitor, error)
	List(context.Context) ([]monitor.Monitor, error)
	PatchInterval(
		context.Context,
		string,
		int,
		*string,
		*monitor.Check,
		*int,
		time.Time,
	) (monitor.Monitor, error)
	Lifecycle(context.Context, string, string, time.Time) (monitor.Monitor, error)
	ClaimManual(context.Context, string, time.Time) (monitor.Monitor, string, error)
	RecordResult(context.Context, monitor.Observation, string) (monitor.Observation, error)
	Observations(context.Context, string, int) ([]monitor.Observation, error)
	Gaps(context.Context, string, int) ([]monitor.Gap, error)
}
type CheckRunner interface {
	Run(context.Context, monitor.Monitor) monitor.Observation
}
type monitorAPI struct {
	store       MonitorStore
	runner      CheckRunner
	policy      *targetpolicy.Policy
	logger      *slog.Logger
	now         func() time.Time
	minInterval int
	limiters    *heartbeatLimiters
}

func NewMonitorRouter(
	logger *slog.Logger,
	deps []Dependency,
	readinessTimeout time.Duration,
	now func() time.Time,
	s MonitorStore,
	r CheckRunner,
	p *targetpolicy.Policy,
	minIntervals ...int,
) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if readinessTimeout <= 0 {
		readinessTimeout = DefaultReadinessTimeout
	}
	minInterval := 60
	if len(minIntervals) > 0 {
		minInterval = minIntervals[0]
	}
	mux := newMux(logger, now)
	mux.Use(deletionGuard(s))
	mux.Get("/api/health/live", handleLive)
	mux.Get("/api/health/ready", handleReady(logger, deps, readinessTimeout, now))
	mux.Get("/api/system/liveness", func(w http.ResponseWriter, r *http.Request) {
		if v, ok := s.(interface {
			Liveness(context.Context) (heartbeat.Liveness, error)
		}); ok {
			live, e := v.Liveness(r.Context())
			if e == nil {
				writeJSON(w, 200, live)
				return
			}
		}
		apiError(w, 503, "store_unavailable")
	})
	// In-process scheduler coverage; no table access, so it stays cheap and
	// available while the Overview is slow under load.
	mux.Get("/api/system/scheduler", func(w http.ResponseWriter, r *http.Request) {
		coverage := store.SchedulerCoverage{State: "unknown"}
		if v, ok := s.(interface {
			SchedulerCoverage(time.Time) store.SchedulerCoverage
		}); ok {
			coverage = v.SchedulerCoverage(now())
		}
		writeJSON(w, 200, coverage)
	})
	mux.Get("/api/system", func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.(interface {
			System(context.Context) (store.SystemStatus, error)
		})
		if !ok {
			apiError(w, 503, "store_unavailable")
			return
		}
		status, err := v.System(r.Context())
		if err != nil {
			apiError(w, 503, "store_unavailable")
			return
		}
		writeJSON(w, 200, status)
	})
	a := &monitorAPI{
		store:       s,
		runner:      r,
		policy:      p,
		logger:      logger,
		now:         now,
		minInterval: minInterval,
	}
	a.limiters = newHeartbeatLimiters()
	mux.Get("/api/intervals", a.intervals)
	mux.Get("/api/applications", a.listApplications)
	mux.Post("/api/applications", a.createApplication)
	mux.Get("/api/applications/{id}", a.getApplication)
	mux.Patch("/api/applications/{id}", a.renameApplication)
	mux.Post("/api/applications/{id}/archive", a.archiveApplication)
	mux.Get("/api/applications/{id}/deletion", a.applicationDeletion)
	mux.Post("/api/applications/{id}/deletion", a.startApplicationDeletion)
	mux.Post("/api/applications/{id}/token", a.applicationToken)
	mux.Delete("/api/applications/{id}/token", a.applicationToken)
	mux.Get("/api/applications/{id}/deployments", a.listDeployments)
	mux.Post("/api/applications/{id}/deployments", a.manualDeployment)
	mux.Post("/ingest/applications/{id}/deployments", a.ingestDeployment)
	mux.Put("/api/monitors/{id}/application", a.setMonitorApplication)
	mux.Get("/api/overview", a.overview)
	mux.Get("/api/monitors", a.list)
	mux.Post("/api/monitors", a.create)
	mux.Get("/api/monitors/{id}", a.get)
	mux.Get("/api/monitors/{id}/summary", a.summary)
	mux.Patch("/api/monitors/{id}", a.patch)
	mux.Post("/api/monitors/{id}/lifecycle", a.lifecycle)
	mux.Get("/api/monitors/{id}/deletion", a.monitorDeletion)
	mux.Post("/api/monitors/{id}/deletion", a.startMonitorDeletion)
	mux.Post("/api/monitors/{id}/checks", a.check)
	mux.Post("/api/monitors/{id}/heartbeat/token", a.heartbeatToken)
	mux.Delete("/api/monitors/{id}/heartbeat/token", a.heartbeatRevoke)
	mux.Post("/ingest/heartbeats/{id}", a.ingestHeartbeat)
	mux.Get("/api/monitors/{id}/observations", a.observations)
	mux.Get("/api/monitors/{id}/maintenance", a.maintenanceList)
	mux.Post("/api/monitors/{id}/maintenance", a.maintenanceCreate)
	mux.Post("/api/monitors/{id}/maintenance/{windowId}/cancel", a.maintenanceCancel)
	mux.Get("/api/monitors/{id}/gaps", a.gaps)
	mux.Get("/api/incidents", a.incidents)
	mux.Get("/api/notifications/attention", a.attention)
	mux.Get("/api/monitors/{id}/incidents", a.monitorIncidents)
	mux.Get("/api/monitors/{id}/incidents/{incidentId}", a.incidentDetail)
	mux.Post("/api/monitors/{id}/incidents/{incidentId}/notifications/{noteKey}/retry", a.retry)
	web := webui.Handler()
	mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
		if isAPIPath(r.URL.Path) || r.URL.Path == "/ingest" ||
			strings.HasPrefix(r.URL.Path, "/ingest/") {
			jsonErrorHandler(http.StatusNotFound, errorNotFound)(w, r)
			return
		}
		web.ServeHTTP(w, r)
	})
	return mux
}

func apiError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func fieldsError(w http.ResponseWriter, f monitor.Fields) {
	writeJSON(w, 400, map[string]any{"error": "validation_failed", "fields": f})
}

func decode(w http.ResponseWriter, r *http.Request, out any, paths map[string]string) bool {
	b, err := io.ReadAll(io.LimitReader(r.Body, 65537))
	if err != nil {
		apiError(w, 400, "invalid_json")
		return false
	}
	if len(b) > 65536 {
		apiError(w, 413, "body_too_large")
		return false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(b, &fields) != nil || fields == nil {
		apiError(w, 400, "invalid_json")
		return false
	}
	for k, v := range fields {
		if k == "check" {
			var nested map[string]json.RawMessage
			if json.Unmarshal(v, &nested) == nil {
				for child := range nested {
					key := "check." + child
					if !strings.Contains(
						"|url|method|expectedStatus|deadlineMs|maxBodyBytes|",
						"|"+child+"|",
					) {
						f := monitor.Fields{}
						f.Add(key, "unknown_field", "unknown field")
						fieldsError(w, f)
						return false
					}
				}
			}
		}
		if k == "heartbeat" {
			var nested map[string]json.RawMessage
			if json.Unmarshal(v, &nested) == nil {
				for child := range nested {
					if child != "intervalSeconds" && child != "graceSeconds" {
						f := monitor.Fields{}
						f.Add("heartbeat."+child, "unknown_field", "unknown field")
						fieldsError(w, f)
						return false
					}
				}
			}
		}
		if _, ok := paths[k]; !ok {
			f := monitor.Fields{}
			f.Add(k, "unknown_field", "unknown field")
			fieldsError(w, f)
			return false
		}
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(out) != nil {
		apiError(w, 400, "invalid_json")
		return false
	}
	return true
}

func (s *monitorAPI) failure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, 404, "monitor_not_found")
	case errors.Is(err, store.ErrVersionConflict):
		apiError(w, 409, "version_conflict")
	case errors.Is(err, store.ErrInvalidTransition):
		apiError(w, 409, "invalid_transition")
	case errors.Is(err, store.ErrArchived):
		apiError(w, 409, "archived")
	case errors.Is(err, store.ErrLeaseHeld):
		apiError(w, 409, "check_in_progress")
	case errors.Is(err, store.ErrNotEligible):
		apiError(w, 409, "not_supported")
	default:
		if r.Context().Err() == nil {
			s.logger.Error("store unavailable", "reason", "dependency_failure")
		}
		apiError(w, 503, "store_unavailable")
	}
}

func (s *monitorAPI) list(w http.ResponseWriter, r *http.Request) {
	ms, err := s.store.List(r.Context())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	items := make([]monitor.Monitor, 0, len(ms))
	for _, m := range ms {
		items = append(items, monitor.WithStatus(m, s.now()))
	}
	writeJSON(w, 200, map[string]any{"monitors": items})
}

func (s *monitorAPI) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name            *string             `json:"name"`
		Kind            *string             `json:"kind"`
		Heartbeat       *heartbeat.Schedule `json:"heartbeat"`
		IntervalSeconds *int                `json:"intervalSeconds"`
		IncidentPolicy  *incident.Policy    `json:"incidentPolicy"`
		Check           *struct {
			URL            *string `json:"url"`
			Method         *string `json:"method"`
			ExpectedStatus *int    `json:"expectedStatus"`
			DeadlineMs     *int    `json:"deadlineMs"`
			MaxBodyBytes   *int    `json:"maxBodyBytes"`
		} `json:"check"`
	}
	if !decode(
		w,
		r,
		&req,
		map[string]string{
			"name":            "",
			"check":           "",
			"intervalSeconds": "",
			"incidentPolicy":  "",
			"kind":            "",
			"heartbeat":       "",
		},
	) {
		return
	}
	if req.Kind != nil && *req.Kind == "heartbeat" {
		s.createHeartbeat(
			w,
			r,
			req.Name,
			req.Check != nil,
			req.Heartbeat,
			req.IntervalSeconds != nil,
			req.IncidentPolicy,
		)
		return
	}
	fields := monitor.Fields{}
	name := ""
	if req.Name != nil {
		name = monitor.ValidateName(*req.Name, fields)
	} else {
		fields.Add("name", "required", "name is required")
	}
	if req.Kind != nil && *req.Kind != "http" {
		fields.Add("kind", "invalid_value", "kind must be http or heartbeat")
	}
	if req.Heartbeat != nil {
		fields.Add("heartbeat", "unexpected", "heartbeat is not valid for HTTP monitors")
	}
	c := monitor.Check{
		Method:         "GET",
		ExpectedStatus: 200,
		DeadlineMs:     10000,
		MaxBodyBytes:   monitor.MaxBodyBytes,
	}
	if req.Check == nil {
		fields.Add("check.url", "required", "URL is required")
	} else {
		if req.Check.URL == nil {
			fields.Add("check.url", "required", "URL is required")
		} else {
			c.URL = *req.Check.URL
			s.validateURL(c.URL, fields)
		}
		if req.Check.Method != nil {
			c.Method = *req.Check.Method
		}
		if req.Check.ExpectedStatus != nil {
			c.ExpectedStatus = *req.Check.ExpectedStatus
		}
		if req.Check.DeadlineMs != nil {
			c.DeadlineMs = *req.Check.DeadlineMs
		}
		monitor.ValidateCheck(&c, fields)
	}
	interval := monitor.DefaultIntervalSeconds
	if req.IntervalSeconds != nil {
		interval = *req.IntervalSeconds
	}
	monitor.ValidateInterval(interval, s.minInterval, fields)
	policy := incident.Policy{OpenAfter: 2, RecoverAfter: 2}
	if req.IncidentPolicy != nil {
		policy = *req.IncidentPolicy
	}
	validateIncidentPolicy(policy, fields)
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	m := monitor.New(name, c, s.now())
	m.IncidentPolicy = policy
	m.IntervalSeconds = interval
	if err := s.store.Create(r.Context(), m); err != nil {
		s.failure(w, r, err)
		return
	}
	w.Header().Set("Location", "/api/monitors/"+m.ID)
	writeJSON(w, 201, monitor.WithStatus(m, s.now()))
}

func (s *monitorAPI) validateURL(u string, fields monitor.Fields) {
	code, msg := s.policy.Validate(u)
	if code != "" {
		fields.Add("check.url", code, msg)
	}
}

func (s *monitorAPI) get(w http.ResponseWriter, r *http.Request) {
	m, err := s.store.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.failure(w, r, err)
		return
	}
	writeJSON(w, 200, monitor.WithStatus(m, s.now()))
}

func (s *monitorAPI) patch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expected        *int                `json:"expectedConfigVersion"`
		Name            *string             `json:"name"`
		IntervalSeconds *int                `json:"intervalSeconds"`
		IncidentPolicy  *incident.Policy    `json:"incidentPolicy"`
		Kind            *string             `json:"kind"`
		Heartbeat       *heartbeat.Schedule `json:"heartbeat"`
		Check           *struct {
			URL            *string `json:"url"`
			Method         *string `json:"method"`
			ExpectedStatus *int    `json:"expectedStatus"`
			DeadlineMs     *int    `json:"deadlineMs"`
			MaxBodyBytes   *int    `json:"maxBodyBytes"`
		} `json:"check"`
	}
	if !decode(
		w,
		r,
		&req,
		map[string]string{
			"expectedConfigVersion": "",
			"name":                  "",
			"check":                 "",
			"intervalSeconds":       "",
			"incidentPolicy":        "",
			"kind":                  "", "heartbeat": "",
		},
	) {
		return
	}
	base, lookupErr := s.store.Get(r.Context(), chi.URLParam(r, "id"))
	if lookupErr != nil {
		s.failure(w, r, lookupErr)
		return
	}
	if base.Kind == "heartbeat" || req.Heartbeat != nil || req.Kind != nil {
		s.patchHeartbeat(
			w,
			r,
			req.Expected,
			req.Name,
			req.Check != nil,
			req.IntervalSeconds != nil,
			req.Kind,
			req.Heartbeat,
			req.IncidentPolicy,
		)
		return
	}
	fields := monitor.Fields{}
	if req.Expected == nil {
		fields.Add("expectedConfigVersion", "required", "version is required")
	} else if *req.Expected < 1 {
		fields.Add("expectedConfigVersion", "out_of_range", "version must be positive")
	}
	if req.Name != nil {
		name := monitor.ValidateName(*req.Name, fields)
		req.Name = &name
	}
	var c *monitor.Check
	if req.Check != nil {
		base, err := s.store.Get(r.Context(), chi.URLParam(r, "id"))
		if err != nil {
			s.failure(w, r, err)
			return
		}
		value := base.Check
		if req.Check.URL != nil {
			value.URL = *req.Check.URL
		}
		s.validateURL(value.URL, fields)
		if req.Check.Method != nil {
			value.Method = *req.Check.Method
		}
		if req.Check.ExpectedStatus != nil {
			value.ExpectedStatus = *req.Check.ExpectedStatus
		}
		if req.Check.DeadlineMs != nil {
			value.DeadlineMs = *req.Check.DeadlineMs
		}
		monitor.ValidateCheck(&value, fields)
		c = &value
	}
	if req.IntervalSeconds != nil {
		monitor.ValidateInterval(*req.IntervalSeconds, s.minInterval, fields)
	}
	if req.IncidentPolicy != nil {
		validateIncidentPolicy(*req.IncidentPolicy, fields)
	}
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	var m monitor.Monitor
	var err error
	if req.IncidentPolicy != nil {
		if policyStore, ok := s.store.(interface {
			PatchIntervalPolicy(context.Context, string, int, *string, *monitor.Check, *int, *incident.Policy, time.Time) (monitor.Monitor, error)
		}); ok {
			m, err = policyStore.PatchIntervalPolicy(
				r.Context(),
				chi.URLParam(r, "id"),
				*req.Expected,
				req.Name,
				c,
				req.IntervalSeconds,
				req.IncidentPolicy,
				s.now(),
			)
		} else {
			apiError(w, 503, "store_unavailable")
			return
		}
	} else {
		m, err = s.store.PatchInterval(r.Context(), chi.URLParam(r, "id"), *req.Expected, req.Name, c, req.IntervalSeconds, s.now())
	}
	if err != nil {
		s.failure(w, r, err)
		return
	}
	writeJSON(w, 200, monitor.WithStatus(m, s.now()))
}

func (s *monitorAPI) lifecycle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Action *string `json:"action"`
	}
	if !decode(w, r, &req, map[string]string{"action": ""}) {
		return
	}
	if req.Action == nil {
		f := monitor.Fields{}
		f.Add("action", "required", "action is required")
		fieldsError(w, f)
		return
	}
	if *req.Action != "pause" && *req.Action != "resume" && *req.Action != "archive" {
		f := monitor.Fields{}
		f.Add("action", "invalid_value", "unknown action")
		fieldsError(w, f)
		return
	}
	m, err := s.store.Lifecycle(r.Context(), chi.URLParam(r, "id"), *req.Action, s.now())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	writeJSON(w, 200, monitor.WithStatus(m, s.now()))
}

func (s *monitorAPI) observations(w http.ResponseWriter, r *http.Request) {
	fields := monitor.Fields{}
	limit := historyLimit(r, fields)
	filter := historyFilter(r, fields)
	if len(fields) != 0 {
		fieldsError(w, fields)
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(r.Context(), id); err != nil {
		s.failure(w, r, err)
		return
	}
	db, ok := s.store.(historyStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return
	}
	page, err := db.HistoryObservations(r.Context(), id, limit, filter)
	if err != nil {
		historyFailure(w, r, err, s)
		return
	}
	writeJSON(
		w,
		200,
		map[string]any{
			"observations":    page.Items,
			"nextCursor":      page.NextCursor,
			"searchedThrough": page.SearchedThrough,
		},
	)
}

func (s *monitorAPI) check(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if m, e := s.store.Get(r.Context(), id); e == nil && m.Kind == "heartbeat" {
		apiError(w, 409, "not_supported")
		return
	}
	m, token, err := s.store.ClaimManual(r.Context(), id, s.now())
	if err != nil {
		s.failure(w, r, err)
		return
	}
	type result struct {
		o   monitor.Observation
		err error
	}
	done := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(m.Check.DeadlineMs)*time.Millisecond+5*time.Second,
		)
		defer cancel()
		o := s.runner.Run(ctx, m)
		o.InitiatedBy = "manual"
		o.Trigger = nil
		o.DueAt = nil
		o, err := s.store.RecordResult(ctx, o, token)
		if err == nil {
			checker.Log(s.logger, o)
		}
		done <- result{o, err}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			s.failure(w, r, value.err)
			return
		}
		writeJSON(w, 201, value.o)
	case <-r.Context().Done():
		return
	case <-time.After(time.Duration(m.Check.DeadlineMs)*time.Millisecond + 5*time.Second):
		s.failure(w, r, store.ErrUnavailable)
	}
}

func (s *monitorAPI) intervals(w http.ResponseWriter, _ *http.Request) {
	writeJSON(
		w,
		200,
		map[string]any{
			"intervalSeconds":        monitor.AllowedIntervals(s.minInterval),
			"defaultIntervalSeconds": monitor.DefaultIntervalSeconds,
			"heartbeat": map[string]any{
				"intervalSeconds":        heartbeat.Intervals(s.minInterval),
				"graceSeconds":           heartbeat.Graces(s.minInterval),
				"defaultIntervalSeconds": 3600,
				"defaultGraceSeconds":    900,
			},
		},
	)
}

func (s *monitorAPI) gaps(w http.ResponseWriter, r *http.Request) {
	fields := monitor.Fields{}
	limit := historyLimit(r, fields)
	before := r.URL.Query().Get("before")
	if r.URL.Query().Has("before") && before == "" {
		fields.Add("before", "invalid_value", "invalid cursor")
	}
	if len(fields) != 0 {
		fieldsError(w, fields)
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(r.Context(), id); err != nil {
		s.failure(w, r, err)
		return
	}
	db, ok := s.store.(historyStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return
	}
	page, err := db.HistoryGaps(r.Context(), id, limit, before)
	if err != nil {
		historyFailure(w, r, err, s)
		return
	}
	writeJSON(
		w,
		200,
		map[string]any{
			"gaps":            page.Items,
			"nextCursor":      page.NextCursor,
			"searchedThrough": page.SearchedThrough,
		},
	)
}
