package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/checker"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

type MonitorStore interface {
	Create(context.Context, monitor.Monitor) error
	Get(context.Context, string) (monitor.Monitor, error)
	List(context.Context) ([]monitor.Monitor, error)
	Patch(context.Context, string, int, *string, *monitor.Check, time.Time) (monitor.Monitor, error)
	Lifecycle(context.Context, string, string, time.Time) (monitor.Monitor, error)
	PutObservation(context.Context, monitor.Observation) error
	Observations(context.Context, string, int) ([]monitor.Observation, error)
}
type CheckRunner interface {
	Run(context.Context, monitor.Monitor) monitor.Observation
}
type monitorAPI struct {
	store  MonitorStore
	runner CheckRunner
	policy *targetpolicy.Policy
	logger *slog.Logger
	now    func() time.Time
	mu     sync.Mutex
	busy   map[string]bool
}

func NewMonitorRouter(
	logger *slog.Logger,
	deps []Dependency,
	readinessTimeout time.Duration,
	now func() time.Time,
	s MonitorStore,
	r CheckRunner,
	p *targetpolicy.Policy,
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
	mux := newMux(logger, now)
	mux.Get("/api/health/live", handleLive)
	mux.Get("/api/health/ready", handleReady(logger, deps, readinessTimeout, now))
	a := &monitorAPI{
		store:  s,
		runner: r,
		policy: p,
		logger: logger,
		now:    now,
		busy:   map[string]bool{},
	}
	mux.Get("/api/monitors", a.list)
	mux.Post("/api/monitors", a.create)
	mux.Get("/api/monitors/{id}", a.get)
	mux.Patch("/api/monitors/{id}", a.patch)
	mux.Post("/api/monitors/{id}/lifecycle", a.lifecycle)
	mux.Post("/api/monitors/{id}/checks", a.check)
	mux.Get("/api/monitors/{id}/observations", a.observations)
	mux.NotFound(jsonErrorHandler(http.StatusNotFound, errorNotFound))
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

func (s *monitorAPI) failure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		apiError(w, 404, "monitor_not_found")
	case errors.Is(err, store.ErrVersionConflict):
		apiError(w, 409, "version_conflict")
	case errors.Is(err, store.ErrInvalidTransition):
		apiError(w, 409, "invalid_transition")
	case errors.Is(err, store.ErrArchived):
		apiError(w, 409, "archived")
	default:
		s.logger.Error("store unavailable", "reason", "dependency_failure")
		apiError(w, 503, "store_unavailable")
	}
}

func (s *monitorAPI) list(w http.ResponseWriter, r *http.Request) {
	ms, err := s.store.List(r.Context())
	if err != nil {
		s.failure(w, err)
		return
	}
	items := make([]monitor.ListItem, 0, len(ms))
	for _, m := range ms {
		obs, e := s.store.Observations(r.Context(), m.ID, 1)
		if e != nil {
			s.failure(w, e)
			return
		}
		item := monitor.ListItem{Monitor: m}
		if len(obs) > 0 {
			item.LastObservation = &obs[0]
		}
		items = append(items, item)
	}
	writeJSON(w, 200, map[string]any{"monitors": items})
}

func (s *monitorAPI) create(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  *string `json:"name"`
		Check *struct {
			URL            *string `json:"url"`
			Method         *string `json:"method"`
			ExpectedStatus *int    `json:"expectedStatus"`
			DeadlineMs     *int    `json:"deadlineMs"`
			MaxBodyBytes   *int    `json:"maxBodyBytes"`
		} `json:"check"`
	}
	if !decode(w, r, &req, map[string]string{"name": "", "check": ""}) {
		return
	}
	fields := monitor.Fields{}
	name := ""
	if req.Name != nil {
		name = monitor.ValidateName(*req.Name, fields)
	} else {
		fields.Add("name", "required", "name is required")
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
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	m := monitor.New(name, c, s.now())
	if err := s.store.Create(r.Context(), m); err != nil {
		s.failure(w, err)
		return
	}
	w.Header().Set("Location", "/api/monitors/"+m.ID)
	writeJSON(w, 201, m)
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
		s.failure(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *monitorAPI) patch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Expected *int    `json:"expectedConfigVersion"`
		Name     *string `json:"name"`
		Check    *struct {
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
		map[string]string{"expectedConfigVersion": "", "name": "", "check": ""},
	) {
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
			s.failure(w, err)
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
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	m, err := s.store.Patch(r.Context(), chi.URLParam(r, "id"), *req.Expected, req.Name, c, s.now())
	if err != nil {
		s.failure(w, err)
		return
	}
	writeJSON(w, 200, m)
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
		s.failure(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *monitorAPI) observations(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			f := monitor.Fields{}
			f.Add("limit", "out_of_range", "limit must be 1–200")
			fieldsError(w, f)
			return
		}
		limit = n
	}
	id := chi.URLParam(r, "id")
	if _, err := s.store.Get(r.Context(), id); err != nil {
		s.failure(w, err)
		return
	}
	obs, err := s.store.Observations(r.Context(), id, limit)
	if err != nil {
		s.failure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"observations": obs})
}

func (s *monitorAPI) check(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	m, err := s.store.Get(r.Context(), id)
	if err != nil {
		s.failure(w, err)
		return
	}
	if m.Lifecycle == "archived" {
		apiError(w, 409, "archived")
		return
	}
	s.mu.Lock()
	if s.busy[id] {
		s.mu.Unlock()
		apiError(w, 409, "check_in_progress")
		return
	}
	s.busy[id] = true
	s.mu.Unlock()
	type result struct {
		o   monitor.Observation
		err error
	}
	done := make(chan result, 1)
	go func() {
		defer func() { s.mu.Lock(); delete(s.busy, id); s.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(m.Check.DeadlineMs)*time.Millisecond+5*time.Second,
		)
		defer cancel()
		o := s.runner.Run(ctx, m)
		checker.Log(s.logger, o)
		done <- result{o, s.store.PutObservation(ctx, o)}
	}()
	select {
	case value := <-done:
		if value.err != nil {
			s.failure(w, value.err)
			return
		}
		writeJSON(w, 201, value.o)
	case <-r.Context().Done():
		return
	case <-time.After(time.Duration(m.Check.DeadlineMs)*time.Millisecond + 5*time.Second):
		s.failure(w, store.ErrUnavailable)
	}
}
