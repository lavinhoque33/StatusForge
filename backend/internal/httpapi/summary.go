package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
	"github.com/lavinhoque33/statusforge/backend/internal/summary"
)

type dailyStore interface {
	Summary(context.Context, monitor.Monitor, string, time.Time) (summary.Result, error)
	Overview(context.Context, time.Time) (store.OverviewResult, error)
}

func (s *monitorAPI) summary(w http.ResponseWriter, r *http.Request) {
	window := r.URL.Query().Get("window")
	if window == "" && !r.URL.Query().Has("window") {
		window = "24h"
	}
	if window != "24h" && window != "7d" {
		fields := monitor.Fields{}
		fields.Add("window", "invalid_value", "window must be 24h or 7d")
		fieldsError(w, fields)
		return
	}
	m, e := s.store.Get(r.Context(), chi.URLParam(r, "id"))
	if e != nil {
		s.failure(w, r, e)
		return
	}
	if m.Kind == "heartbeat" {
		db, ok := s.store.(interface {
			HeartbeatSummary(context.Context, monitor.Monitor, string, time.Time) (summary.HeartbeatResult, error)
		})
		if !ok {
			apiError(w, 503, "store_unavailable")
			return
		}
		result, err := db.HeartbeatSummary(r.Context(), m, window, s.now())
		if err != nil {
			s.failure(w, r, err)
			return
		}
		writeJSON(w, 200, result)
		return
	}
	db, ok := s.store.(dailyStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return
	}
	result, e := db.Summary(r.Context(), m, window, s.now())
	if e != nil {
		s.failure(w, r, e)
		return
	}
	writeJSON(w, 200, result)
}

func (s *monitorAPI) overview(w http.ResponseWriter, r *http.Request) {
	db, ok := s.store.(dailyStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return
	}
	result, e := db.Overview(r.Context(), s.now())
	if e != nil {
		s.failure(w, r, e)
		return
	}
	writeJSON(w, 200, result)
}
