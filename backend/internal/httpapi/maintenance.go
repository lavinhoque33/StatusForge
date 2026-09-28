package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type maintenanceStore interface {
	ListMaintenance(context.Context, string, int, time.Time) ([]monitor.MaintenanceWindow, error)
	CreateMaintenance(
		context.Context,
		string,
		time.Time,
		time.Time,
		string,
		time.Time,
	) (monitor.MaintenanceWindow, error)
	CancelMaintenance(context.Context, string, string, time.Time) (monitor.MaintenanceWindow, error)
}

func (s *monitorAPI) maintenanceStore(w http.ResponseWriter) maintenanceStore {
	db, ok := s.store.(maintenanceStore)
	if !ok {
		apiError(w, 503, "store_unavailable")
		return nil
	}
	return db
}

func (s *monitorAPI) maintenanceFailure(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrOverlaps):
		field := monitor.Fields{}
		field.Add("startAt", "overlaps", "window overlaps an existing window")
		fieldsError(w, field)
	case errors.Is(err, store.ErrTooManyWindows):
		field := monitor.Fields{}
		field.Add("startAt", "too_many_windows", "at most 10 upcoming windows")
		fieldsError(w, field)
	case errors.Is(err, store.ErrWindowClosed):
		apiError(w, 409, "window_closed")
	case errors.Is(err, store.ErrWindowNotFound):
		apiError(w, 404, "window_not_found")
	default:
		s.failure(w, r, err)
	}
}

func (s *monitorAPI) maintenanceList(w http.ResponseWriter, r *http.Request) {
	db := s.maintenanceStore(w)
	if db == nil {
		return
	}
	limit, ok := incidentLimit(w, r)
	if !ok {
		return
	}
	windows, err := db.ListMaintenance(r.Context(), chi.URLParam(r, "id"), limit, s.now())
	if err != nil {
		s.maintenanceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, map[string]any{"windows": windows})
}

func (s *monitorAPI) maintenanceCreate(w http.ResponseWriter, r *http.Request) {
	db := s.maintenanceStore(w)
	if db == nil {
		return
	}
	var req struct {
		StartAt *string `json:"startAt"`
		EndAt   *string `json:"endAt"`
		Note    *string `json:"note"`
	}
	if !decode(w, r, &req, map[string]string{"startAt": "", "endAt": "", "note": ""}) {
		return
	}
	fields := monitor.Fields{}
	var start, end time.Time
	if req.StartAt == nil {
		fields.Add("startAt", "required", "startAt is required")
	} else {
		var e error
		start, e = time.Parse(time.RFC3339Nano, *req.StartAt)
		if e != nil {
			fields.Add("startAt", "invalid_value", "startAt must be RFC 3339")
		}
	}
	if req.EndAt == nil {
		fields.Add("endAt", "required", "endAt is required")
	} else {
		var e error
		end, e = time.Parse(time.RFC3339Nano, *req.EndAt)
		if e != nil {
			fields.Add("endAt", "invalid_value", "endAt must be RFC 3339")
		}
	}
	rawStart := start
	rawDuration := end.Sub(start)
	// Persisted boundaries and observation start times have millisecond precision.
	start = start.UTC().Truncate(time.Millisecond)
	end = end.UTC().Truncate(time.Millisecond)
	now := s.now()
	if !rawStart.IsZero() && rawStart.Before(now.Add(-5*time.Minute)) {
		fields.Add(
			"startAt",
			"out_of_range",
			"startAt must not be more than five minutes in the past",
		)
	}
	if !start.IsZero() && !end.IsZero() {
		if !end.After(start) || rawDuration > 7*24*time.Hour {
			fields.Add("endAt", "out_of_range", "endAt must be after startAt and within seven days")
		}
	}
	note := ""
	if req.Note != nil {
		note = *req.Note
		if utf8.RuneCountInString(note) > 200 {
			fields.Add("note", "out_of_range", "note must be at most 200 characters")
		}
	}
	if len(fields) > 0 {
		fieldsError(w, fields)
		return
	}
	window, err := db.CreateMaintenance(r.Context(), chi.URLParam(r, "id"), start, end, note, now)
	if err != nil {
		s.maintenanceFailure(w, r, err)
		return
	}
	writeJSON(w, 201, window)
}

func (s *monitorAPI) maintenanceCancel(w http.ResponseWriter, r *http.Request) {
	db := s.maintenanceStore(w)
	if db == nil {
		return
	}
	window, err := db.CancelMaintenance(
		r.Context(),
		chi.URLParam(r, "id"),
		chi.URLParam(r, "windowId"),
		s.now(),
	)
	if err != nil {
		s.maintenanceFailure(w, r, err)
		return
	}
	writeJSON(w, 200, window)
}
