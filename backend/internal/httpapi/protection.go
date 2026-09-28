package httpapi

import (
	"bytes"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

const maxAPIBytes = 64 << 10

// localProtection rejects DNS rebinding on every route and browser cross-site writes.
// A literal null Origin is not a loopback origin and is rejected.
func localProtection(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackAuthority(r.Host) {
			apiError(w, http.StatusMisdirectedRequest, "host_not_allowed")
			return
		}
		if isAPIPath(r.URL.Path) &&
			(r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete) {
			if origin := r.Header.Get("Origin"); origin != "" && !loopbackOrigin(origin) ||
				strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
				apiError(w, http.StatusForbidden, "origin_not_allowed")
				return
			}
			if r.Body != nil {
				// Read one extra byte so unknown-length/chunked bodies obey the same bound.
				body, err := io.ReadAll(io.LimitReader(r.Body, maxAPIBytes+1))
				_ = r.Body.Close()
				if len(body) > maxAPIBytes {
					apiError(w, http.StatusRequestEntityTooLarge, "too_large")
					return
				}
				if err != nil {
					apiError(w, http.StatusBadRequest, "invalid_json")
					return
				}
				if len(body) != 0 {
					media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
					if err != nil || !strings.EqualFold(media, "application/json") {
						apiError(w, http.StatusUnsupportedMediaType, "unsupported_media_type")
						return
					}
				}
				r.Body = io.NopCloser(bytes.NewReader(body))
			}
		}
		next.ServeHTTP(w, r)
	})
}

func loopbackAuthority(authority string) bool {
	if authority == "[::1]" {
		return true
	}
	host := authority
	if strings.Contains(authority, ":") {
		var err error
		host, _, err = net.SplitHostPort(authority)
		if err != nil {
			return false
		}
	}
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

func loopbackOrigin(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" ||
		u.RawQuery != "" ||
		u.Fragment != "" {
		return false
	}
	return loopbackAuthority(u.Host)
}
