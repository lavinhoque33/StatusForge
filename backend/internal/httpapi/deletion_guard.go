package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/lavinhoque33/statusforge/backend/internal/store"
)

func deletionGuard(s MonitorStore) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				next.ServeHTTP(w, r)
				return
			}
			path := r.URL.Path
			for _, root := range []string{"/api/monitors/", "/ingest/heartbeats/"} {
				if strings.HasPrefix(path, root) {
					id := strings.Split(strings.TrimPrefix(path, root), "/")[0]
					if strings.HasSuffix(path, "/deletion") {
						break
					}
					m, e := s.Get(r.Context(), id)
					if e == nil && m.Deletion != nil {
						apiError(w, 409, "deleting")
						return
					}
					break
				}
			}
			for _, root := range []string{"/api/applications/", "/ingest/applications/"} {
				if strings.HasPrefix(path, root) {
					id := strings.Split(strings.TrimPrefix(path, root), "/")[0]
					if strings.HasSuffix(path, "/deletion") {
						break
					}
					if db, ok := s.(interface {
						Application(context.Context, string) (store.Application, error)
					}); ok {
						a, e := db.Application(r.Context(), id)
						if e == nil && a.Deletion != nil {
							apiError(w, 409, "deleting")
							return
						}
					}
					break
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
