package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/deviceauth"
	"github.com/zszwede/romm-go/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Admin user management (Administration → Users).

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "users.write"); !ok {
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Email    string `json:"email"`
		Role     string `json:"role"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.Username == "" || body.Password == "" {
		writeErr(w, http.StatusBadRequest, "username and password required")
		return
	}
	role := body.Role
	if role != "admin" {
		role = "user"
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "hash failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, err := s.st.CreateLocalUser(ctx, body.Username, string(hash), body.Email, role, true)
	if err != nil || u == nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, userSchema(u, ""))
}

func (s *Server) getUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "users.read"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, err := s.st.GetUserByID(ctx, id)
	if err != nil || u == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, userSchema(u, ""))
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "users.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if err := r.ParseMultipartForm(16 << 20); err != nil {
		_ = r.ParseForm()
	}
	var up store.AdminUserUpdate
	if v := r.FormValue("email"); v != "" {
		up.Email = &v
	}
	if v := r.FormValue("role"); v != "" {
		up.Role = &v
	}
	if v := r.FormValue("enabled"); v != "" {
		b := truthy(v)
		up.Enabled = &b
	}
	if v := r.FormValue("password"); v != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(v), bcrypt.DefaultCost)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "hash failed")
			return
		}
		h := string(hash)
		up.HashedPassword = &h
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, err := s.st.AdminUpdateUser(ctx, id, up)
	if err != nil || u == nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, userSchema(u, ""))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "users.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	gone, err := s.st.DeleteUser(ctx, id)
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

// inviteLink returns a signup token for the given role (admin "Invite link").
func (s *Server) inviteLink(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "users.write"); !ok {
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"token": deviceauth.GenerateClientToken(),
		"url":   nil,
	})
}
