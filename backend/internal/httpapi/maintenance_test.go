package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type maintenanceAPIStore struct {
	*fakeMonitorStore
	createErr, cancelErr error
}

func (s *maintenanceAPIStore) ListMaintenance(
	context.Context,
	string,
	int,
	time.Time,
) ([]monitor.MaintenanceWindow, error) {
	return []monitor.MaintenanceWindow{}, nil
}

func (s *maintenanceAPIStore) CreateMaintenance(
	context.Context,
	string,
	time.Time,
	time.Time,
	string,
	time.Time,
) (monitor.MaintenanceWindow, error) {
	return monitor.MaintenanceWindow{}, s.createErr
}

func (s *maintenanceAPIStore) CancelMaintenance(
	context.Context,
	string,
	string,
	time.Time,
) (monitor.MaintenanceWindow, error) {
	return monitor.MaintenanceWindow{}, s.cancelErr
}

func TestMaintenanceAPIValidation(t *testing.T) {
	now := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	s := &maintenanceAPIStore{fakeMonitorStore: &fakeMonitorStore{}}
	handler := NewMonitorRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		time.Second,
		func() time.Time { return now },
		s,
		nil,
		nil,
	)
	cases := []struct {
		name, method, path, body string
		createErr, cancelErr     error
		status                   int
		field, code              string
	}{
		{
			name:      "overlap",
			method:    "POST",
			path:      "/api/monitors/m/maintenance",
			body:      `{"startAt":"2026-09-28T10:00:00Z","endAt":"2026-09-28T11:00:00Z"}`,
			createErr: store.ErrOverlaps,
			status:    400,
			field:     "startAt",
			code:      "overlaps",
		},
		{
			name:      "limit",
			method:    "POST",
			path:      "/api/monitors/m/maintenance",
			body:      `{"startAt":"2026-09-28T10:00:00Z","endAt":"2026-09-28T11:00:00Z"}`,
			createErr: store.ErrTooManyWindows,
			status:    400,
			field:     "startAt",
			code:      "too_many_windows",
		},
		{
			name:   "duration",
			method: "POST",
			path:   "/api/monitors/m/maintenance",
			body:   `{"startAt":"2026-09-28T10:00:00Z","endAt":"2026-10-06T10:00:00Z"}`,
			status: 400,
			field:  "endAt",
			code:   "out_of_range",
		},
		{
			name:   "past",
			method: "POST",
			path:   "/api/monitors/m/maintenance",
			body:   `{"startAt":"2026-09-28T09:54:59Z","endAt":"2026-09-28T10:00:00Z"}`,
			status: 400,
			field:  "startAt",
			code:   "out_of_range",
		},
		{
			name:   "fractional boundary collapses at millisecond precision",
			method: "POST",
			path:   "/api/monitors/m/maintenance",
			body:   `{"startAt":"2026-09-28T10:00:00.000000001Z","endAt":"2026-09-28T10:00:00.000000002Z"}`,
			status: 400,
			field:  "endAt",
			code:   "out_of_range",
		},
		{
			name:      "archived",
			method:    "POST",
			path:      "/api/monitors/m/maintenance",
			body:      `{"startAt":"2026-09-28T10:00:00Z","endAt":"2026-09-28T11:00:00Z"}`,
			createErr: store.ErrArchived,
			status:    409,
			code:      "archived",
		},
		{
			name:      "closed",
			method:    "POST",
			path:      "/api/monitors/m/maintenance/id/cancel",
			cancelErr: store.ErrWindowClosed,
			status:    409,
			code:      "window_closed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s.createErr = tc.createErr
			s.cancelErr = tc.cancelErr
			rec := httptest.NewRecorder()
			handler.ServeHTTP(
				rec,
				httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body)),
			)
			if rec.Code != tc.status {
				t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
			}
			var payload struct {
				Error  string `json:"error"`
				Fields map[string]struct {
					Code string `json:"code"`
				} `json:"fields"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
				t.Fatal(err)
			}
			if tc.field != "" {
				if payload.Fields[tc.field].Code != tc.code {
					t.Fatalf("field: %s", rec.Body.String())
				}
			} else if payload.Error != tc.code {
				t.Fatalf("error: %s", rec.Body.String())
			}
		})
	}
}
