package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type deletionStore interface {
	StartMonitorDeletion(context.Context, string, string, time.Time) (monitor.Deletion, error)
	MonitorDeletion(context.Context, string) (monitor.Deletion, error)
	StartApplicationDeletion(context.Context, string, string, time.Time) (monitor.Deletion, error)
	ApplicationDeletion(context.Context, string) (monitor.Deletion, error)
}

func (s *monitorAPI) deletionStore(w http.ResponseWriter) deletionStore {
	v, ok := s.store.(deletionStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return nil
	}
	return v
}

func (s *monitorAPI) deletionError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, store.ErrNotFound):
		apiError(w, 404, "not_found")
	case errors.Is(e, store.ErrNotArchived):
		apiError(w, 409, "not_archived")
	case errors.Is(e, store.ErrNameMismatch):
		f := monitor.Fields{}
		f.Add("confirmName", "mismatch", "name does not match")
		fieldsError(w, f)
	default:
		apiError(w, 503, "store_unavailable")
	}
}

func (s *monitorAPI) startDeletion(w http.ResponseWriter, r *http.Request, application bool) {
	db := s.deletionStore(w)
	if db == nil {
		return
	}
	var req struct {
		ConfirmName string `json:"confirmName"`
	}
	if !decode(w, r, &req, map[string]string{"confirmName": ""}) {
		return
	}
	if req.ConfirmName == "" {
		f := monitor.Fields{}
		f.Add("confirmName", "required", "name is required")
		fieldsError(w, f)
		return
	}
	id := chi.URLParam(r, "id")
	var state monitor.Deletion
	var e error
	if application {
		state, e = db.StartApplicationDeletion(r.Context(), id, req.ConfirmName, s.now())
	} else {
		state, e = db.StartMonitorDeletion(r.Context(), id, req.ConfirmName, s.now())
	}
	if e != nil {
		s.deletionError(w, e)
		return
	}
	writeJSON(w, 202, state)
}

func (s *monitorAPI) startMonitorDeletion(w http.ResponseWriter, r *http.Request) {
	s.startDeletion(w, r, false)
}

func (s *monitorAPI) startApplicationDeletion(w http.ResponseWriter, r *http.Request) {
	s.startDeletion(w, r, true)
}

func (s *monitorAPI) getDeletion(w http.ResponseWriter, r *http.Request, application bool) {
	db := s.deletionStore(w)
	if db == nil {
		return
	}
	var state monitor.Deletion
	var e error
	if application {
		state, e = db.ApplicationDeletion(r.Context(), chi.URLParam(r, "id"))
	} else {
		state, e = db.MonitorDeletion(r.Context(), chi.URLParam(r, "id"))
	}
	if e != nil {
		s.deletionError(w, e)
		return
	}
	writeJSON(w, 200, state)
}

func (s *monitorAPI) monitorDeletion(w http.ResponseWriter, r *http.Request) {
	s.getDeletion(w, r, false)
}

func (s *monitorAPI) applicationDeletion(w http.ResponseWriter, r *http.Request) {
	s.getDeletion(w, r, true)
}
