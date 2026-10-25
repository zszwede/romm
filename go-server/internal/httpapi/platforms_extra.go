package httpapi

import (
	"context"
	"net/http"
	"time"
)

// Platform single/edit + supported/filesystem lists, plus tasks/run and the
// jukebox favorites endpoints.

func (s *Server) getPlatform(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p, err := s.st.GetPlatform(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if p == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) updatePlatform(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "platforms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		_ = r.ParseForm()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	p, err := s.st.UpdatePlatformCustomName(ctx, id, r.FormValue("custom_name"))
	if err != nil || p == nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// supportedPlatforms / filesystemPlatforms: the frontend uses these for the
// "scan an unadded folder" flow. Empty is a valid answer (nothing extra to add).
func (s *Server) supportedPlatforms(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

func (s *Server) filesystemPlatforms(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// runTask runs a named task. "scan*" maps to the library scanner; others are
// accepted as no-ops (scheduled jobs the single-user setup doesn't need).
func (s *Server) runTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "tasks.run"); !ok {
		return
	}
	name := r.PathValue("name")
	if name == "scan" || name == "scan_library" {
		s.handleScanEvent(nil)
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": name, "status": "started"})
}

// Jukebox favorites. None in this build; shapes kept valid for the store.
func (s *Server) musicFavorites(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.musicUser(w, r); !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
	case http.MethodPost:
		writeJSON(w, http.StatusOK, map[string]any{"added": 0})
	case http.MethodDelete:
		writeJSON(w, http.StatusOK, map[string]any{"removed": 0})
	}
}
