package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Music playlists (game soundtracks), user-scoped. All bearer-protected.

func (s *Server) musicUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return 0, false
	}
	return uid, true
}

func (s *Server) listPlaylists(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.musicUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, err := s.st.ListPlaylists(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createPlaylist(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.musicUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id, err := s.st.CreatePlaylist(ctx, uid, body.Name, body.Description, body.IsPublic)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name})
}

func (s *Server) deletePlaylist(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.musicUser(w, r)
	if !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	gone, err := s.st.DeletePlaylist(ctx, uid, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listTracks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.musicUser(w, r); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, err := s.st.ListPlaylistTracks(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) addTrack(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.musicUser(w, r); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	var body struct {
		RomFileID int64 `json:"rom_file_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.RomFileID <= 0 {
		writeErr(w, http.StatusBadRequest, "rom_file_id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tid, err := s.st.AddTrack(ctx, id, body.RomFileID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "add failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": tid, "playlist_id": id, "rom_file_id": body.RomFileID})
}

func (s *Server) removeTrack(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.musicUser(w, r); !ok {
		return
	}
	tid := int64(atoiDefault(r.PathValue("tid"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.RemoveTrack(ctx, tid); err != nil {
		writeErr(w, http.StatusInternalServerError, "remove failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
