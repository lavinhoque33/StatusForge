package webui

import (
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestHistoryAndAssetCaching(t *testing.T) {
	previous := assets
	assets = fstest.MapFS{
		"index.html":        {Data: []byte("<html>release</html>")},
		"assets/app-123.js": {Data: []byte("console.log('ok')")},
	}
	t.Cleanup(func() { assets = previous })
	cases := []struct {
		path        string
		status      int
		cache, body string
	}{
		{"/", 200, "no-cache", "release"},
		{"/monitors/abc", 200, "no-cache", "release"},
		{"/assets/app-123.js", 200, "immutable", "console.log"},
		{"/assets/missing.js", 404, "", "404"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest("GET", tc.path, nil)
			rec := httptest.NewRecorder()
			Handler().ServeHTTP(rec, req)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.body) ||
				!strings.Contains(rec.Header().Get("Cache-Control"), tc.cache) {
				t.Fatalf(
					"status=%d cache=%q body=%q",
					rec.Code,
					rec.Header().Get("Cache-Control"),
					rec.Body.String(),
				)
			}
			if !strings.Contains(
				rec.Header().Get("Content-Security-Policy"),
				"frame-ancestors 'none'",
			) {
				t.Fatal("missing CSP")
			}
		})
	}
}
