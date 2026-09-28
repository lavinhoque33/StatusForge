package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; object-src 'none'; base-uri 'none'"

// Handler serves the release web build. The ordinary Go build has no bundled UI.
func Handler() http.Handler {
	var index []byte
	if assets != nil {
		index, _ = fs.ReadFile(assets, "index.html")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", csp)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if assets == nil || index == nil {
			http.Error(w, "Web UI not embedded; run make build", http.StatusServiceUnavailable)
			return
		}
		requested := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if requested == "" {
			requested = "index.html"
		}
		file, err := assets.Open(requested)
		if err != nil || isDirectory(file) {
			if file != nil {
				_ = file.Close()
			}
			// Missing asset URLs must not return HTML under a script or stylesheet MIME type.
			if strings.HasPrefix(requested, "assets/") || path.Ext(requested) != "" {
				http.NotFound(w, r)
				return
			}
			requested = "index.html"
		} else {
			_ = file.Close()
		}
		if requested == "index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
			return
		}
		if strings.HasPrefix(requested, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		r.URL.Path = "/" + requested
		http.FileServer(http.FS(assets)).ServeHTTP(w, r)
	})
}

func isDirectory(file fs.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.IsDir()
}
