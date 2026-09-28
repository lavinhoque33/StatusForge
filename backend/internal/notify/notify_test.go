package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func TestClassifyDeliveryOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{{"accepted", func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); w.WriteHeader(204) }, "delivered"}, {"rejected", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(400) }, "rejected"}, {"receiver_error", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) }, "http_error"}, {"closed_after_body", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		conn, _, e := w.(http.Hijacker).Hijack()
		if e == nil {
			conn.Close()
		}
	}, "outcome_unknown"}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			policy, e := PolicyForURL(srv.URL)
			if e != nil {
				t.Fatal(e)
			}
			worker := Worker{URL: srv.URL, Policy: policy}
			result, _, _ := worker.Send(
				context.Background(),
				store.Notification{
					ID:      "id:opened",
					Payload: `{"schema":"statusforge.notification.v1"}`,
				},
			)
			if result != tc.want {
				t.Fatalf("got %s want %s", result, tc.want)
			}
		})
	}
}

func TestTimeoutBeforeWrite(t *testing.T) {
	srv := httptest.NewServer(
		http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) { t.Fatal("request should not arrive") },
		),
	)
	defer srv.Close()
	policy, e := PolicyForURL(srv.URL)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	worker := Worker{URL: srv.URL, Policy: policy}
	result, _, _ := worker.Send(ctx, store.Notification{ID: "id:opened", Payload: "{}"})
	if result != "timeout" {
		t.Fatalf("got %s want timeout", result)
	}
}
