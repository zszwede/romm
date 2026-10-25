package httpapi

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/auth"
	"github.com/zszwede/romm-go/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// Simple username/password session auth for the web UI (the deployment is a
// trusted home-lab, not internet-facing, and the user asked for simple auth
// rather than the OAuth/OIDC flow). POST /api/login takes HTTP Basic creds,
// verifies the bcrypt hash, and sets a signed session cookie; every later
// request is authorized by that cookie (or, for API clients, a bearer JWT).

const (
	sessionCookie = "romm_session"
	csrfCookie    = "romm_csrftoken"
	sessionTTL    = 14 * 24 * time.Hour
)

// signSession builds "b64(username|role|exp).b64(hmac)" signed with the secret.
func (s *Server) signSession(username, role string, exp time.Time) string {
	payload := username + "|" + role + "|" + strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// verifySession returns the username and role from a valid, unexpired cookie.
func (s *Server) verifySession(cookie string) (username, role string, ok bool) {
	parts := strings.SplitN(cookie, ".", 2)
	if len(parts) != 2 || s.secret == "" {
		return "", "", false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", false
	}
	wantMAC, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", false
	}
	mac := hmac.New(sha256.New, []byte(s.secret))
	mac.Write(payload)
	if !hmac.Equal(mac.Sum(nil), wantMAC) {
		return "", "", false
	}
	fields := strings.SplitN(string(payload), "|", 3)
	if len(fields) != 3 {
		return "", "", false
	}
	exp, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || time.Now().Unix() >= exp {
		return "", "", false
	}
	return fields[0], fields[1], true
}

// sessionClaims resolves a session cookie to auth.Claims (scopes from role), so
// authUser can accept a cookie just like a bearer token.
func (s *Server) sessionClaims(r *http.Request) (*auth.Claims, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil, false
	}
	username, role, ok := s.verifySession(c.Value)
	if !ok {
		return nil, false
	}
	return &auth.Claims{Username: username, Scopes: auth.ScopesForRole(role)}, true
}

func newCSRFToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *Server) setAuthCookies(w http.ResponseWriter, username, role string) {
	exp := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: s.signSession(username, role, exp),
		Path: "/", Expires: exp, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	// Readable by JS (js-cookie) so the frontend can echo it as x-csrftoken.
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: newCSRFToken(),
		Path: "/", Expires: exp, SameSite: http.SameSiteLaxMode,
	})
}

func clearAuthCookie(w http.ResponseWriter, name string, httpOnly bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: httpOnly})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok || username == "" {
		writeErr(w, http.StatusUnauthorized, "credentials required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, err := s.st.GetUserAuth(ctx, username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "login failed")
		return
	}
	if u == nil || u.HashedPassword == "" ||
		bcrypt.CompareHashAndPassword([]byte(u.HashedPassword), []byte(password)) != nil {
		writeErr(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
	if !u.Enabled {
		writeErr(w, http.StatusForbidden, "user account is disabled")
		return
	}
	_ = s.st.TouchLogin(ctx, u.ID)
	s.setAuthCookies(w, u.Username, u.Role)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	clearAuthCookie(w, sessionCookie, true)
	clearAuthCookie(w, csrfCookie, false)
	writeJSON(w, http.StatusOK, nil)
}

func (s *Server) usersMe(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	u, err := s.st.GetUserAuth(ctx, claims.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if u == nil {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return
	}
	writeJSON(w, http.StatusOK, userSchema(u, deviceIDFromSession(r)))
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope("users.read") {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	users, err := s.st.ListUsers(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(users))
	for i := range users {
		out = append(out, userSchema(&users[i], ""))
	}
	writeJSON(w, http.StatusOK, out)
}

// userSchema builds the UserSchema JSON the frontend expects.
func userSchema(u *store.UserRecord, deviceID string) map[string]any {
	return map[string]any{
		"id":                  u.ID,
		"username":            u.Username,
		"email":               u.Email,
		"enabled":             u.Enabled,
		"role":                u.Role,
		"permission_group_id": u.PermissionGroupID,
		"oauth_scopes":        auth.ScopesForRole(u.Role),
		"avatar_path":         u.AvatarPath,
		"last_login":          u.LastLogin,
		"last_active":         u.LastActive,
		"ra_username":         u.RAUsername,
		"ra_progression":      nil,
		"ui_settings":         nil,
		"current_device_id":   nilIfEmpty(deviceID),
		"created_at":          u.CreatedAt,
		"updated_at":          u.UpdatedAt,
	}
}

func deviceIDFromSession(r *http.Request) string { return "" } // sessions here carry no device id

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// csrfOK enforces the double-submit CSRF check on mutating requests: the
// x-csrftoken header must equal the romm_csrftoken cookie. Lenient (home-lab):
// requests without the cookie set yet are allowed through.
func csrfOK(r *http.Request) bool {
	if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
		return true
	}
	c, err := r.Cookie(csrfCookie)
	if err != nil || c.Value == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(r.Header.Get("x-csrftoken"))) == 1
}
