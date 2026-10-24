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
	"github.com/zszwede/romm-go/internal/metadata"
	"github.com/zszwede/romm-go/internal/scan"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/internal/worker"
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
	ListPlatformDirs(ctx context.Context) ([]scan.PlatformDir, error)
	Upsert(ctx context.Context, r scan.Record) error
}

// Deps are the server's collaborators. Passed as a struct so new ones can be
// added without churning every call site.
type Deps struct {
	Store      DataStore
	AuthSecret string
	Queue      *worker.Queue
	ScanRoot   string // ES-DE roms root, e.g. ROMM_BASE_PATH/library/roms
	Metadata   metadata.Provider
}

type Server struct {
	st       DataStore
	secret   string
	queue    *worker.Queue
	scanRoot string
	meta     metadata.Provider
	mux      *http.ServeMux
}

func New(d Deps) *Server {
	s := &Server{
		st:       d.Store,
		secret:   d.AuthSecret,
		queue:    d.Queue,
		scanRoot: d.ScanRoot,
		meta:     d.Metadata,
		mux:      http.NewServeMux(),
	}
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
	s.mux.HandleFunc("POST /api/scan", s.triggerScan)
	s.mux.HandleFunc("GET /api/scan", s.listScans)
	s.mux.HandleFunc("GET /api/scan/{id}", s.scanStatus)
	s.mux.HandleFunc("GET /api/metadata/search", s.searchMetadata)

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

// triggerScan enqueues a background library scan. ?hash=0 skips hashing for a
// fast inventory pass. Requires a valid bearer token.
func (s *Server) triggerScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.queue == nil || s.scanRoot == "" {
		writeErr(w, http.StatusServiceUnavailable, "scanning not configured")
		return
	}
	doHash := r.URL.Query().Get("hash") != "0"
	root := s.scanRoot
	id := s.queue.Enqueue("library-scan", func(ctx context.Context) error {
		dirs, err := s.st.ListPlatformDirs(ctx)
		if err != nil {
			return err
		}
		_, err = scan.Platforms(ctx, root, dirs, s.st, doHash)
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

func (s *Server) listScans(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.queue.List()})
}

func (s *Server) scanStatus(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	job, ok := s.queue.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// searchMetadata searches the configured provider by name. Requires auth
// (it hits a rate-limited external API).
func (s *Server) searchMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.meta == nil {
		writeErr(w, http.StatusServiceUnavailable, "no metadata provider configured")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	games, err := s.meta.Search(ctx, q)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "provider error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": s.meta.Name(), "items": games})
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
