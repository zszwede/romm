// Package httpapi is the HTTP layer. Phase 2 adds a platforms list, a rom
// detail endpoint, and a token-protected rom_user write, on top of the Phase-1
// heartbeat/roms. Bearer tokens are the same HS256 access tokens the Python
// backend issues (verified via internal/auth). Only the stdlib net/http router
// (Go 1.22 pattern matching) is used.
package httpapi

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/auth"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/web"
)

// Version reported by the heartbeat. Distinct from the Python app's so it's
// obvious which backend answered during the migration.
const Version = "go-0.2.0"

// DataStore is the slice of the store the API needs. An interface so handlers
// can be unit-tested against a fake without a database.
type DataStore interface {
	Counts(ctx context.Context) (store.Counts, error)
	ListRoms(ctx context.Context, platformID int64, limit int) ([]store.Rom, error)
	ListPlatforms(ctx context.Context) ([]store.Platform, error)
	GetRom(ctx context.Context, id int64) (*store.RomDetail, error)
	UserIDByUsername(ctx context.Context, username string) (int64, error)
	SetRomUserProps(ctx context.Context, userID, romID int64, p store.RomUserProps) (int, error)
}

type Server struct {
	st     DataStore
	secret string
	mux    *http.ServeMux
}

func New(st DataStore, authSecret string) *Server {
	s := &Server{st: st, secret: authSecret, mux: http.NewServeMux()}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return logging(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/heartbeat", s.heartbeat)
	s.mux.HandleFunc("GET /api/platforms", s.listPlatforms)
	s.mux.HandleFunc("GET /api/roms", s.listRoms)
	s.mux.HandleFunc("GET /api/roms/{id}", s.getRom)
	s.mux.HandleFunc("PUT /api/roms/{id}/props", s.setRomProps)

	sub, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("embed static: %v", err)
	}
	s.mux.Handle("GET /", http.FileServer(http.FS(sub)))
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	counts, err := s.st.Counts(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "db unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"SYSTEM":  map[string]any{"VERSION": Version, "BACKEND": "go"},
		"LIBRARY": counts,
	})
}

func (s *Server) listPlatforms(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	platforms, err := s.st.ListPlatforms(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": platforms})
}

func (s *Server) listRoms(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
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

func (s *Server) getRom(w http.ResponseWriter, r *http.Request) {
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rom, err := s.st.GetRom(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if rom == nil {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}
	writeJSON(w, http.StatusOK, rom)
}

func (s *Server) setRomProps(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var props store.RomUserProps
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&props); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "user lookup failed")
		return
	}
	if uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return
	}
	n, err := s.st.SetRomUserProps(ctx, uid, id, props)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// authUser extracts and verifies the Bearer access token, if any.
func (s *Server) authUser(r *http.Request) (*auth.Claims, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return nil, false
	}
	c, err := auth.Verify(strings.TrimPrefix(h, "Bearer "), s.secret)
	if err != nil {
		return nil, false
	}
	return c, true
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
	if s == "" {
		return def
	}
	n := 0
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
