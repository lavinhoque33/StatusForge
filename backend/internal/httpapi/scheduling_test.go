package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

func TestSchedulingRoutes(t *testing.T) {
	p, _ := targetpolicy.Parse("127.0.0.1:8090")
	f := &fakeMonitorStore{}
	router := NewMonitorRouter(
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		nil,
		time.Second,
		time.Now,
		f,
		nil,
		p,
		10,
	)
	send := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "localhost:8080"
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := send(http.MethodGet, "/api/intervals", "")
	if w.Code != 200 {
		t.Fatalf("intervals %d", w.Code)
	}
	var intervals struct {
		IntervalSeconds []int `json:"intervalSeconds"`
		Default         int   `json:"defaultIntervalSeconds"`
	}
	if json.Unmarshal(w.Body.Bytes(), &intervals) != nil || len(intervals.IntervalSeconds) != 7 ||
		intervals.Default != 300 {
		t.Fatalf("intervals %s", w.Body.String())
	}
	w = send(
		http.MethodPost,
		"/api/monitors",
		`{"name":"sample","intervalSeconds":10,"check":{"url":"http://127.0.0.1:8090/healthy"}}`,
	)
	if w.Code != 201 {
		t.Fatalf("create %d %s", w.Code, w.Body.String())
	}
	var created monitor.Monitor
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil ||
		created.IntervalSeconds != 10 ||
		created.Status.State != "unknown" {
		t.Fatalf("create %+v %v", created, err)
	}
	w = send(http.MethodGet, "/api/monitors/"+created.ID+"/gaps", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"gaps":[]`) {
		t.Fatalf("gaps %d %s", w.Code, w.Body.String())
	}
	for _, tc := range []struct {
		path string
		code int
	}{{"/api/monitors/not-here/gaps", 404}, {"/api/monitors/" + created.ID + "/gaps?limit=201", 400}} {
		w = send(http.MethodGet, tc.path, "")
		if w.Code != tc.code {
			t.Fatalf("%s -> %d", tc.path, w.Code)
		}
	}
	for _, tc := range []struct{ query, field string }{
		{"outcome=unknown", "outcome"},
		{"outcome=healthy,unknown", "outcome"},
		{"counted=maybe", "counted"},
		{"maintenance=1", "maintenance"},
		{"from=2026-09-29T00:00:00Z&to=2026-09-28T00:00:00Z", "from"},
		{"before=", "before"},
	} {
		w = send(http.MethodGet, "/api/monitors/"+created.ID+"/observations?"+tc.query, "")
		var body struct {
			Error  string         `json:"error"`
			Fields monitor.Fields `json:"fields"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil ||
			w.Code != 400 || body.Error != "validation_failed" || body.Fields[tc.field].Code == "" {
			t.Fatalf("%s: %d %s %v", tc.query, w.Code, w.Body.String(), err)
		}
	}
}
