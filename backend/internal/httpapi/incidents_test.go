package httpapi

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

type incidentAPIStore struct {
	*fakeMonitorStore
	retryResult error
}

func (s *incidentAPIStore) ListIncidents(
	context.Context,
	string,
	string,
	int,
	time.Time,
) ([]store.Incident, error) {
	return []store.Incident{}, nil
}

func (s *incidentAPIStore) IncidentDetail(
	context.Context,
	string,
	string,
	time.Time,
) (store.Incident, []store.Event, []monitor.Gap, []store.Notification, error) {
	return store.Incident{}, nil, nil, nil, store.ErrIncidentNotFound
}

func (s *incidentAPIStore) RetryNotification(
	context.Context,
	string,
	string,
	string,
	time.Time,
) (store.Notification, error) {
	return store.Notification{}, s.retryResult
}

func (s *incidentAPIStore) Attention(context.Context, int) ([]store.Notification, error) {
	return []store.Notification{}, nil
}

func TestIncidentRouteStatuses(t *testing.T) {
	s := &incidentAPIStore{fakeMonitorStore: &fakeMonitorStore{}, retryResult: store.ErrNotFailed}
	handler := NewMonitorRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		time.Second,
		time.Now,
		s,
		nil,
		nil,
	)
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/api/incidents?state=all", 200}, {"GET", "/api/incidents?limit=201", 400}, {"GET", "/api/incidents?state=invalid", 400}, {"GET", "/api/monitors/m/incidents/i", 404}, {"GET", "/api/notifications/attention", 200}, {"POST", "/api/monitors/m/incidents/i/notifications/opened/retry", 409}, {"POST", "/api/monitors/m/incidents/i/notifications/reminder-0001/retry", 409}, {"POST", "/api/monitors/m/incidents/i/notifications/reminder#0001/retry", 404}} {
		t.Run(tc.path, func(t *testing.T) {
			request := httptest.NewRequest(tc.method, tc.path, strings.NewReader(""))
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			if recorder.Code != tc.code {
				t.Fatalf("got %d want %d: %s", recorder.Code, tc.code, recorder.Body.String())
			}
		})
	}
}
