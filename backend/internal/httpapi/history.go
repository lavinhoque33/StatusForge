package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type historyStore interface {
	HistoryObservations(
		context.Context,
		string,
		int,
		store.HistoryFilter,
	) (store.HistoryPage[monitor.Observation], error)
	HistoryGaps(context.Context, string, int, string) (store.HistoryPage[monitor.Gap], error)
}

func historyLimit(r *http.Request, fields monitor.Fields) int {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 200 {
			fields.Add("limit", "out_of_range", "limit must be 1–200")
		} else {
			limit = n
		}
	}
	return limit
}

func historyFilter(r *http.Request, fields monitor.Fields) store.HistoryFilter {
	q := r.URL.Query()
	f := store.HistoryFilter{Before: q.Get("before")}
	if q.Has("before") && f.Before == "" {
		fields.Add("before", "invalid_value", "invalid cursor")
	}
	if q.Has("outcome") {
		allowed := map[string]bool{"healthy": true, "failing": true, "checker_problem": true}
		seen := map[string]bool{}
		for _, v := range strings.Split(q.Get("outcome"), ",") {
			if !allowed[v] || seen[v] {
				fields.Add("outcome", "invalid_value", "unknown or repeated outcome")
				break
			}
			seen[v] = true
			f.Outcomes = append(f.Outcomes, v)
		}
	}
	for _, name := range []string{"counted", "maintenance"} {
		if !q.Has(name) {
			continue
		}
		raw := q.Get(name)
		if raw != "true" && raw != "false" {
			fields.Add(name, "invalid_value", name+" must be true or false")
			continue
		}
		value := raw == "true"
		if name == "counted" {
			f.Counted = &value
		} else {
			f.Maintenance = &value
		}
	}
	for _, name := range []string{"from", "to"} {
		if !q.Has(name) {
			continue
		}
		t, err := time.Parse(time.RFC3339Nano, q.Get(name))
		if err != nil {
			fields.Add(name, "invalid_value", name+" must be RFC 3339")
			continue
		}
		t = t.UTC()
		if name == "from" {
			f.From = &t
		} else {
			f.To = &t
		}
	}
	if f.From != nil && f.To != nil && f.From.After(*f.To) {
		fields.Add("from", "invalid_value", "from must not be after to")
	}
	return f
}

func historyFailure(w http.ResponseWriter, r *http.Request, err error, s *monitorAPI) {
	if errors.Is(err, store.ErrInvalidCursor) {
		fields := monitor.Fields{}
		fields.Add("before", "invalid_value", "invalid cursor")
		fieldsError(w, fields)
		return
	}
	s.failure(w, r, err)
}
