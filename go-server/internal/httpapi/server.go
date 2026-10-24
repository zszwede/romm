// Package httpapi is the HTTP layer. Phase 1 exposes a heartbeat and a
// read-only roms endpoint, and serves the embedded static shell. It uses only
// the stdlib net/http router (Go 1.22+ pattern matching) to keep the
// dependency surface minimal.
package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/web"
)

// Version reported by the heartbeat. Distinct from the Python app's so it's
// obvious which backend answered during the migration.
const Version = "go-0.1.0"

type Server struct {
	st  *store.Store
	mux *http.ServeMux
}

func New(st *store.Store) *Server {
	s := &Server{st: st, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return logging(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/heartbeat", s.heartbeat)
	s.mux.HandleFunc("GET /api/roms", s.listRoms)

	// Static SPA shell (embedded). Phase 1 ships a placeholder; a later phase
	// embeds the real built Vue dist and falls back to index.html for client
	// routes.
	sub, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("embed static: %v", err)
	}
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	// Generous: a cold connection to a remote DB plus the COUNT(*)s can take a
	// few seconds on the first hit. (Counts get cached in a later phase.)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	counts, err := s.st.Counts(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "db unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"SYSTEM": map[string]any{"VERSION": Version, "BACKEND": "go"},
		"LIBRARY": counts,
	})
}

func (s *Server) listRoms(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	platformID := int64(atoiDefault(r.URL.Query().Get("platform_id"), 0))
	limit := atoiDefault(r.URL.Query().Get("limit"), 100)
	roms, err := s.st.ListRoms(ctx, platformID, limit)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": roms, "limit": limit})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

func atoiDefault(s string, def int) int {
	n := 0
	if s == "" {
		return def
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
