package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/heartbeat"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type applicationStore interface {
	Applications(context.Context) ([]store.Application, error)
	Application(context.Context, string) (store.Application, error)
	CreateApplication(context.Context, string, time.Time) (store.Application, string, error)
	RenameApplication(context.Context, string, string, time.Time) (store.Application, error)
	ArchiveApplication(context.Context, string, time.Time) (store.Application, error)
	ApplicationToken(context.Context, string, bool, time.Time) (string, *heartbeat.Token, error)
	AuthenticateApplication(context.Context, string, string) (store.Application, error)
	SetApplication(context.Context, string, string) (monitor.Monitor, error)
	PutDeployment(context.Context, string, string, store.Marker) (store.Marker, error)
	Deployments(context.Context, string, int) ([]store.Marker, error)
}

func (s *monitorAPI) apps(w http.ResponseWriter) applicationStore {
	v, ok := s.store.(applicationStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return nil
	}
	return v
}

func (s *monitorAPI) appFailure(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, store.ErrNotFound):
		apiError(w, 404, "application_not_found")
	case errors.Is(e, store.ErrDuplicateName):
		apiError(w, 409, "duplicate_name")
	case errors.Is(e, store.ErrDuplicateDeployment):
		apiError(w, 409, "duplicate_deployment")
	default:
		s.failure(w, e)
	}
}

func (s *monitorAPI) listApplications(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	a, e := db.Applications(r.Context())
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"applications": a})
}

func (s *monitorAPI) getApplication(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	a, e := db.Application(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 200, a)
}

func (s *monitorAPI) createApplication(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	var req struct {
		Name *string `json:"name"`
	}
	if !decode(w, r, &req, map[string]string{"name": ""}) {
		return
	}
	f := monitor.Fields{}
	if req.Name == nil {
		f.Add("name", "required", "name is required")
	} else {
		*req.Name = monitor.ValidateName(*req.Name, f)
	}
	if len(f) > 0 {
		fieldsError(w, f)
		return
	}
	a, token, e := db.CreateApplication(r.Context(), *req.Name, s.now())
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 201, struct {
		store.Application
		IssuedToken string `json:"issuedToken"`
	}{a, token})
}

func (s *monitorAPI) renameApplication(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	var req struct {
		Name *string `json:"name"`
	}
	if !decode(w, r, &req, map[string]string{"name": ""}) {
		return
	}
	f := monitor.Fields{}
	if req.Name == nil {
		f.Add("name", "required", "name is required")
	} else {
		*req.Name = monitor.ValidateName(*req.Name, f)
	}
	if len(f) > 0 {
		fieldsError(w, f)
		return
	}
	a, e := db.RenameApplication(r.Context(), chi.URLParam(r, "id"), *req.Name, s.now())
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 200, a)
}

func (s *monitorAPI) archiveApplication(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	a, e := db.ArchiveApplication(r.Context(), chi.URLParam(r, "id"), s.now())
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 200, a)
}

func (s *monitorAPI) applicationToken(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	revoke := r.Method == http.MethodDelete
	value, token, e := db.ApplicationToken(r.Context(), chi.URLParam(r, "id"), revoke, s.now())
	if e != nil {
		s.appFailure(w, e)
		return
	}
	if revoke {
		w.WriteHeader(204)
		return
	}
	writeJSON(
		w,
		201,
		map[string]string{"token": value, "hint": token.Hint, "createdAt": token.CreatedAt},
	)
}

func (s *monitorAPI) setMonitorApplication(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	var req struct {
		ApplicationID *string `json:"applicationId"`
	}
	if !decode(w, r, &req, map[string]string{"applicationId": ""}) {
		return
	}
	id := ""
	if req.ApplicationID != nil {
		id = *req.ApplicationID
	}
	m, e := db.SetApplication(r.Context(), chi.URLParam(r, "id"), id)
	if e != nil {
		if errors.Is(e, store.ErrNotFound) {
			if _, monitorErr := s.store.Get(r.Context(), chi.URLParam(r, "id")); errors.Is(
				monitorErr,
				store.ErrNotFound,
			) {
				s.failure(w, monitorErr)
				return
			} else if monitorErr != nil {
				s.failure(w, monitorErr)
				return
			}
		}
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 200, monitor.WithStatus(m, s.now()))
}

func (s *monitorAPI) listDeployments(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	limit, ok := incidentLimit(w, r)
	if !ok {
		return
	}
	items, e := db.Deployments(r.Context(), chi.URLParam(r, "id"), limit)
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"deployments": items})
}

var deploymentIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,100}$`)

func validMarkerBody(raw []byte, now time.Time) (store.Marker, monitor.Fields, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || object == nil {
		return store.Marker{}, nil, errors.New("invalid_json")
	}
	f := monitor.Fields{}
	for key := range object {
		switch key {
		case "version", "description", "link", "deployedAt", "deploymentId":
		default:
			f.Add(key, "unknown_field", "unknown field")
		}
	}
	var in struct {
		Version      *string `json:"version"`
		Description  *string `json:"description"`
		Link         *string `json:"link"`
		DeployedAt   *string `json:"deployedAt"`
		DeploymentID *string `json:"deploymentId"`
	}
	if json.Unmarshal(raw, &in) != nil {
		return store.Marker{}, nil, errors.New("invalid_json")
	}
	m := store.Marker{
		Description:  in.Description,
		Link:         in.Link,
		DeployedAt:   in.DeployedAt,
		DeploymentID: in.DeploymentID,
	}
	if in.Version == nil || strings.TrimSpace(*in.Version) == "" {
		f.Add("version", "required", "version is required")
	} else {
		m.Version = *in.Version
		if utf8.RuneCountInString(m.Version) > 100 {
			f.Add("version", "too_long", "version must contain at most 100 characters")
		}
		for _, r := range m.Version {
			if unicode.IsControl(r) {
				f.Add("version", "invalid_value", "version cannot contain control characters")
				break
			}
		}
	}
	if in.Description != nil {
		if utf8.RuneCountInString(*in.Description) > 500 {
			f.Add("description", "too_long", "description must contain at most 500 characters")
		}
		for _, r := range *in.Description {
			if unicode.IsControl(r) {
				f.Add(
					"description",
					"invalid_value",
					"description cannot contain control characters",
				)
				break
			}
		}
	}
	if in.Link != nil {
		u, e := url.Parse(*in.Link)
		if len(*in.Link) > 500 {
			f.Add("link", "too_long", "link must contain at most 500 characters")
		} else if e != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Opaque != "" {
			f.Add("link", "invalid_value", "link must be an absolute http or https URL without userinfo")
		}
	}
	if in.DeploymentID != nil && !deploymentIDPattern.MatchString(*in.DeploymentID) {
		f.Add(
			"deploymentId",
			"invalid_value",
			"deploymentId must use 1–100 letters, digits, dots, underscores, colons or hyphens",
		)
	}
	if in.DeployedAt != nil {
		t, e := time.Parse(time.RFC3339Nano, *in.DeployedAt)
		if e != nil {
			f.Add("deployedAt", "invalid_value", "deployedAt must be RFC 3339")
		} else if t.After(now.Add(time.Minute)) || t.Before(now.Add(-30*24*time.Hour)) {
			f.Add("deployedAt", "out_of_range", "deployedAt must be within 30 days before and 60 seconds after receipt")
		} else {
			stamp := monitor.Stamp(t)
			m.DeployedAt = &stamp
		}
	}
	return m, f, nil
}

func (s *monitorAPI) manualDeployment(w http.ResponseWriter, r *http.Request) {
	db := s.apps(w)
	if db == nil {
		return
	}
	body, e := heartbeat.ReadBody(r.Body)
	if e != nil {
		if e.Error() == "too_large" {
			apiError(w, 413, "too_large")
		} else {
			apiError(w, 400, "invalid_json")
		}
		return
	}
	m, f, e := validMarkerBody(body, s.now())
	if e != nil {
		apiError(w, 400, "invalid_json")
		return
	}
	if len(f) > 0 {
		fieldsError(w, f)
		return
	}
	m.Source = "manual"
	m.ReportedAt = monitor.Stamp(s.now())
	m, e = db.PutDeployment(r.Context(), chi.URLParam(r, "id"), "", m)
	if e != nil {
		s.appFailure(w, e)
		return
	}
	writeJSON(w, 201, m)
}

func (s *monitorAPI) ingestDeployment(w http.ResponseWriter, r *http.Request) {
	body, e := heartbeat.ReadBody(r.Body)
	if e != nil {
		if e.Error() == "too_large" {
			apiError(w, 413, "too_large")
		} else {
			apiError(w, 400, "invalid_json")
		}
		return
	}
	db := s.apps(w)
	if db == nil {
		return
	}
	id := chi.URLParam(r, "id")
	token := ""
	if header := r.Header.Get("Authorization"); strings.HasPrefix(header, "Bearer ") {
		token = strings.TrimPrefix(header, "Bearer ")
	}
	var a store.Application
	var authErr error
	if token != "" {
		a, authErr = db.AuthenticateApplication(r.Context(), id, token)
	} else {
		authErr = store.ErrNotEligible
	}
	if errors.Is(authErr, store.ErrUnavailable) {
		apiError(w, 503, "store_unavailable")
		return
	}
	now := s.now()
	if authErr != nil {
		remote, _, e := net.SplitHostPort(r.RemoteAddr)
		if e != nil {
			remote = r.RemoteAddr
		}
		if !s.limiters.allow(s.limiters.remote, remote, now) {
			w.Header().Set("Retry-After", "1")
			apiError(w, 429, "rate_limited")
		} else {
			apiError(w, 401, "unauthorized")
		}
		return
	}
	if !s.limiters.allow(s.limiters.applications, id, now) {
		w.Header().Set("Retry-After", "1")
		apiError(w, 429, "rate_limited")
		return
	}
	if a.ArchivedAt != nil {
		apiError(w, 410, "archived")
		return
	}
	if len(body) > 0 &&
		!strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		apiError(w, 400, "invalid_json")
		return
	}
	m, f, e := validMarkerBody(body, now)
	if e != nil {
		apiError(w, 400, "invalid_json")
		return
	}
	if len(f) > 0 {
		fieldsError(w, f)
		return
	}
	m.Source = "ingest"
	m.ReportedAt = monitor.Stamp(now)
	m, e = db.PutDeployment(r.Context(), id, a.Token.Hash, m)
	if e != nil {
		switch {
		case errors.Is(e, store.ErrDuplicateDeployment):
			writeJSON(w, 200, map[string]bool{"accepted": true, "duplicate": true})
		case errors.Is(e, store.ErrArchived):
			apiError(w, 410, "archived")
		case errors.Is(e, store.ErrNotEligible):
			apiError(w, 401, "unauthorized")
		default:
			apiError(w, 503, "store_unavailable")
		}
		return
	}
	writeJSON(w, 202, map[string]any{"accepted": true, "duplicate": false, "marker": m})
}
