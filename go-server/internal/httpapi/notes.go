package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Per-user rom notes (the detail Notes tab). Stored on rom_user.

func (s *Server) listRomNotes(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	romID := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	notes, err := s.st.ListRomNotes(ctx, romID, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, notes)
}

func (s *Server) setRomNote(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	romID := int64(atoiDefault(r.PathValue("id"), 0))
	var body struct {
		RawMarkdown string `json:"raw_markdown"`
		IsPublic    bool   `json:"is_public"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	n, err := s.st.SetRomNote(ctx, uid, romID, body.RawMarkdown, body.IsPublic)
	if err != nil || n == nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	writeJSON(w, http.StatusOK, n)
}

func (s *Server) deleteRomNote(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	romID := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.ClearRomNote(ctx, uid, romID); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) noteUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
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
