package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/deviceauth"
)

// Personal API tokens (Settings → Client API Tokens).

func (s *Server) listClientTokens(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	tokens, err := s.st.ListClientTokens(ctx, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, tokens)
}

func (s *Server) createClientToken(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	var body struct {
		Name      string   `json:"name"`
		Scopes    []string `json:"scopes"`
		ExpiresIn string   `json:"expires_in"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.Name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	expiresAt, okExp := parseExpiry(body.ExpiresIn)
	if !okExp {
		writeErr(w, http.StatusUnprocessableEntity, "invalid expires_in")
		return
	}
	raw := deviceauth.GenerateClientToken()
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	t, err := s.st.CreateClientToken(ctx, uid, body.Name, deviceauth.HashClientToken(raw), strings.Join(body.Scopes, " "), expiresAt)
	if err != nil || t == nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	// ClientTokenCreateSchema = the token row plus the one-time raw token.
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": t.ID, "user_id": t.UserID, "name": t.Name, "scopes": t.Scopes,
		"expires_at": t.ExpiresAt, "last_used_at": nil, "device_id": nil,
		"created_at": t.CreatedAt, "token": raw,
	})
}

func (s *Server) deleteClientToken(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	gone, err := s.st.DeleteClientToken(ctx, uid, id)
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
