package checker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/monitor"
	"github.com/lavinhoque33/statusforge/backend/internal/targetpolicy"
)

func TestOutcomeAgainstLoopback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/status":
			w.WriteHeader(500)
		case "/redirect":
			http.Redirect(w, r, "/status", 302)
		case "/large":
			fmt.Fprint(w, strings.Repeat("x", 65537))
		case "/headers":
			time.Sleep(1200 * time.Millisecond)
		case "/body":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			time.Sleep(1200 * time.Millisecond)
			fmt.Fprint(w, "late")
		default:
			fmt.Fprint(w, "ok")
		}
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	p, _ := targetpolicy.Parse(host)
	runner := New(p, time.Now)
	for _, tc := range []struct {
		path, outcome, reason string
		status                int
		truncated             bool
	}{{"/", "healthy", "ok", 200, false}, {"/status", "failing", "wrong_status", 500, false}, {"/redirect", "failing", "wrong_status", 302, false}, {"/large", "healthy", "ok", 200, true}, {"/headers", "failing", "timeout", 0, false}, {"/body", "failing", "timeout", 200, false}} {
		t.Run(tc.path, func(t *testing.T) {
			m := monitor.Monitor{
				ID:            "m",
				ConfigVersion: 1,
				Check: monitor.Check{
					URL:            server.URL + tc.path,
					Method:         "GET",
					ExpectedStatus: 200,
					DeadlineMs:     1000,
					MaxBodyBytes:   65536,
				},
			}
			o := runner.Run(t.Context(), m)
			if o.Outcome != tc.outcome || o.Reason != tc.reason || o.BodyTruncated != tc.truncated {
				t.Fatalf("outcome=%+v", o)
			}
			if tc.status == 0 && o.ObservedStatus != nil ||
				tc.status != 0 && (o.ObservedStatus == nil || *o.ObservedStatus != tc.status) {
				t.Fatalf("status=%v, expected %d", o.ObservedStatus, tc.status)
			}
		})
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	p, _ = targetpolicy.Parse(addr)
	o := New(
		p,
		time.Now,
	).Run(t.Context(), monitor.Monitor{Check: monitor.Check{URL: "http://" + addr, Method: "GET", ExpectedStatus: 200, DeadlineMs: 1000, MaxBodyBytes: 65536}})
	if o.Reason != "connection_refused" {
		t.Fatalf("refusal: %+v", o)
	}
}

func TestCheckLogHasNumericStatusWithoutTargetURL(t *testing.T) {
	var buf bytes.Buffer
	status := 200
	Log(slog.New(slog.NewJSONHandler(&buf, nil)), monitor.Observation{
		MonitorID: "monitor", ID: "observation", ConfigVersion: 1,
		Request: monitor.Check{URL: "http://127.0.0.1:8090/?secret=hidden"},
		Outcome: "healthy", Reason: "ok", ObservedStatus: &status,
	})
	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["observed_status"] != float64(200) || strings.Contains(buf.String(), "hidden") {
		t.Fatalf("unsafe or incorrect check log: %s", buf.String())
	}
	buf.Reset()
	Log(slog.New(slog.NewJSONHandler(&buf, nil)), monitor.Observation{
		MonitorID: "monitor", ID: "refused", Outcome: "checker_problem", Reason: "refused_by_policy",
	})
	entry = nil
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatal(err)
	}
	if _, present := entry["observed_status"]; present {
		t.Fatalf("status unexpectedly logged without response: %s", buf.String())
	}
}
