package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalProtection(t *testing.T) {
	cases := []struct {
		name, method, path, host, origin, fetchSite, media, body string
		status                                                   int
	}{
		{
			name:   "localhost",
			method: "GET",
			path:   "/api/health/live",
			host:   "localhost:8080",
			status: 204,
		},
		{name: "ipv6", method: "GET", path: "/api/health/live", host: "[::1]:8080", status: 204},
		{name: "ipv6 no port", method: "GET", path: "/", host: "[::1]", status: 204},
		{
			name:   "foreign host API",
			method: "GET",
			path:   "/api/health/live",
			host:   "evil.example",
			status: 421,
		},
		{name: "foreign host app", method: "GET", path: "/", host: "evil.example", status: 421},
		{
			name:   "local origin",
			method: "POST",
			path:   "/api/example",
			host:   "127.0.0.1:8080",
			origin: "http://localhost:5173",
			status: 204,
		},
		{
			name:   "foreign origin",
			method: "POST",
			path:   "/api/example",
			host:   "localhost",
			origin: "https://evil.example",
			status: 403,
		},
		{
			name:   "opaque origin",
			method: "POST",
			path:   "/api/example",
			host:   "localhost",
			origin: "null",
			status: 403,
		},
		{
			name:      "cross site",
			method:    "DELETE",
			path:      "/api/example",
			host:      "localhost",
			fetchSite: "cross-site",
			status:    403,
		},
		{
			name:   "wrong media",
			method: "POST",
			path:   "/api/example",
			host:   "localhost",
			media:  "text/plain",
			body:   "{}",
			status: 415,
		},
		{
			name:   "empty missing media",
			method: "POST",
			path:   "/api/example",
			host:   "localhost",
			status: 204,
		},
		{
			name:   "limit allowed",
			method: "POST",
			path:   "/api/example",
			host:   "localhost",
			media:  "application/json; charset=utf-8",
			body:   strings.Repeat("x", 64<<10),
			status: 204,
		},
		{
			name:   "limit exceeded",
			method: "POST",
			path:   "/api/example",
			host:   "localhost",
			media:  "application/json",
			body:   strings.Repeat("x", (64<<10)+1),
			status: 413,
		},
		{
			name:   "ingest unchanged",
			method: "POST",
			path:   "/ingest/example",
			host:   "localhost",
			media:  "text/plain",
			body:   strings.Repeat("x", (64<<10)+1),
			status: 204,
		},
	}
	handler := localProtection(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }),
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Host = tc.host
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				req.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			if tc.media != "" {
				req.Header.Set("Content-Type", tc.media)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != tc.status {
				t.Fatalf("status %d, expected %d: %s", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status >= 400 {
				data, _ := io.ReadAll(rec.Body)
				if !strings.Contains(string(data), `"error":`) {
					t.Fatalf("missing JSON error: %s", data)
				}
			}
		})
	}
}
