package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/incident"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type incidentStore interface {
	ListIncidents(context.Context, string, string, int, time.Time) ([]store.Incident, error)
	IncidentDetail(
		context.Context,
		string,
		string,
		time.Time,
	) (store.Incident, []store.Event, []monitor.Gap, []store.Notification, error)
	RetryNotification(
		context.Context,
		string,
		string,
		string,
		time.Time,
	) (store.Notification, error)
	Attention(context.Context, int) ([]store.Notification, error)
}

func validateIncidentPolicy(p incident.Policy, f monitor.Fields) {
	if p.OpenAfter < 1 || p.OpenAfter > 5 {
		f.Add("incidentPolicy.openAfter", "out_of_range", "openAfter must be 1–5")
	}
	if p.RecoverAfter < 1 || p.RecoverAfter > 5 {
		f.Add("incidentPolicy.recoverAfter", "out_of_range", "recoverAfter must be 1–5")
	}
}

func (s *monitorAPI) incidentStore(w http.ResponseWriter) incidentStore {
	v, ok := s.store.(incidentStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return nil
	}
	return v
}

func incidentLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 50, true
	}
	n, e := strconv.Atoi(raw)
	if e != nil || n < 1 || n > 200 {
		f := monitor.Fields{}
		f.Add("limit", "out_of_range", "limit must be 1–200")
		fieldsError(w, f)
		return 0, false
	}
	return n, true
}
func (s *monitorAPI) incidents(w http.ResponseWriter, r *http.Request) { s.listIncidents(w, r, "") }
func (s *monitorAPI) monitorIncidents(w http.ResponseWriter, r *http.Request) {
	s.listIncidents(w, r, chi.URLParam(r, "id"))
}

func (s *monitorAPI) listIncidents(w http.ResponseWriter, r *http.Request, mid string) {
	db := s.incidentStore(w)
	if db == nil {
		return
	}
	limit, ok := incidentLimit(w, r)
	if !ok {
		return
	}
	state := "all"
	if mid == "" {
		state = r.URL.Query().Get("state")
		if state == "" {
			state = "all"
		}
		if state != "all" && state != "open" && state != "resolved" {
			f := monitor.Fields{}
			f.Add("state", "invalid_value", "state must be open, resolved, or all")
			fieldsError(w, f)
			return
		}
	}
	items, e := db.ListIncidents(r.Context(), mid, state, limit, s.now())
	if e != nil {
		s.failure(w, r, e)
		return
	}
	writeJSON(w, 200, map[string]any{"incidents": items})
}

func (s *monitorAPI) incidentDetail(w http.ResponseWriter, r *http.Request) {
	db := s.incidentStore(w)
	if db == nil {
		return
	}
	in, events, gaps, notes, e := db.IncidentDetail(
		r.Context(),
		chi.URLParam(r, "id"),
		chi.URLParam(r, "incidentId"),
		s.now(),
	)
	if e != nil {
		if errors.Is(e, store.ErrIncidentNotFound) {
			apiError(w, 404, "incident_not_found")
		} else {
			s.failure(w, r, e)
		}
		return
	}
	nearby := []store.NearbyMarker{}
	if deployments, ok := s.store.(interface {
		NearbyDeployments(context.Context, string, time.Time, *time.Time, time.Time) ([]store.NearbyMarker, error)
	}); ok {
		appID := ""
		if in.ApplicationID != nil {
			appID = *in.ApplicationID
		} else {
			m, err := s.store.Get(r.Context(), in.MonitorID)
			if err != nil {
				s.failure(w, r, err)
				return
			}
			appID = m.ApplicationID
		}
		opened, _ := time.Parse(time.RFC3339Nano, in.OpenedAt)
		var resolved *time.Time
		if in.ResolvedAt != nil {
			at, _ := time.Parse(time.RFC3339Nano, *in.ResolvedAt)
			resolved = &at
		}
		var err error
		nearby, err = deployments.NearbyDeployments(r.Context(), appID, opened, resolved, s.now())
		if err != nil {
			s.failure(w, r, err)
			return
		}
	}
	writeJSON(
		w,
		200,
		map[string]any{
			"incident":          in,
			"events":            events,
			"gaps":              gaps,
			"notifications":     notes,
			"nearbyDeployments": nearby,
		},
	)
}

func (s *monitorAPI) retry(w http.ResponseWriter, r *http.Request) {
	db := s.incidentStore(w)
	if db == nil {
		return
	}
	nk := chi.URLParam(r, "noteKey")
	if strings.HasPrefix(nk, "reminder-") && len(nk) == 13 {
		nk = "reminder#" + strings.TrimPrefix(nk, "reminder-")
	} else if nk != "opened" && nk != "resolved" {
		apiError(w, 404, "notification_not_found")
		return
	}
	n, e := db.RetryNotification(
		r.Context(),
		chi.URLParam(r, "id"),
		chi.URLParam(r, "incidentId"),
		nk,
		s.now(),
	)
	if e != nil {
		switch {
		case errors.Is(e, store.ErrNotificationNotFound):
			apiError(w, 404, "notification_not_found")
		case errors.Is(e, store.ErrNotFailed):
			apiError(w, 409, "not_failed")
		default:
			s.failure(w, r, e)
		}
		return
	}
	writeJSON(w, 202, n)
}

func (s *monitorAPI) attention(w http.ResponseWriter, r *http.Request) {
	db := s.incidentStore(w)
	if db == nil {
		return
	}
	limit, ok := incidentLimit(w, r)
	if !ok {
		return
	}
	notes, e := db.Attention(r.Context(), limit)
	if e != nil {
		s.failure(w, r, e)
		return
	}
	writeJSON(w, 200, map[string]any{"notifications": notes})
}
