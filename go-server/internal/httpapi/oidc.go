package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/auth"
	"github.com/zszwede/romm-go/internal/deviceauth"
	"github.com/zszwede/romm-go/internal/oidc"
)

// OIDC login endpoints, ported from backend/endpoints/auth.py (login_via_openid
// + auth_openid). The Python app parked the result in a server-side session;
// the Go rewrite is bearer/JWT, so on success it mints a RomM access token the
// rest of the API already trusts (auth.Sign) and returns it as JSON. The one
// seam that still needs a live IdP + the SPA is the browser redirect handoff;
// everything the server does — redirect build, code exchange, ID-token
// verification, user provisioning — is exercised in tests against a fake issuer.

const (
	oidcStateCookie = "romm_oidc_state"
	oidcNonceCookie = "romm_oidc_nonce"
	oidcTokenTTL    = 14 * 24 * time.Hour
)

// Scopes granted to a minted token by role. Minimal, role-shaped mapping: a
// user gets read + their own write; an admin additionally gets the write
// scopes. (The Python side derived scopes from the role's permission group;
// this is the rewrite's simpler equivalent.)
var (
	oidcUserScopes = []string{
		"me.read", "me.write",
		"roms.read", "roms.user.read", "roms.user.write",
		"platforms.read", "assets.read", "assets.write",
		"collections.read", "collections.write",
		"playlists.read", "playlists.write",
	}
	oidcAdminExtraScopes = []string{
		"roms.write", "platforms.write", "users.read", "users.write",
		"firmware.read", "firmware.write", "tasks.run", "logs.read",
		"devices.read", "devices.write",
	}
)

func scopesForRole(role string) []string {
	out := append([]string(nil), oidcUserScopes...)
	if role == oidc.RoleAdmin {
		out = append(out, oidcAdminExtraScopes...)
	}
	return out
}

func (s *Server) oidcLogin(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil {
		writeErr(w, http.StatusNotFound, "oidc not configured")
		return
	}
	state := deviceauth.GenerateDeviceCode()
	nonce := deviceauth.GenerateDeviceCode()

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	url, err := s.oidc.AuthCodeURL(ctx, state, nonce)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "oidc discovery failed")
		return
	}
	setFlowCookie(w, oidcStateCookie, state)
	setFlowCookie(w, oidcNonceCookie, nonce)
	http.Redirect(w, r, url, http.StatusFound)
}

func (s *Server) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if s.oidc == nil || s.oidcUsers == nil {
		writeErr(w, http.StatusNotFound, "oidc not configured")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		writeErr(w, http.StatusBadRequest, "missing code or state")
		return
	}
	// CSRF: the state echoed back must match the one we set before redirecting.
	if c, err := r.Cookie(oidcStateCookie); err != nil || c.Value != state {
		writeErr(w, http.StatusBadRequest, "state mismatch")
		return
	}
	var nonce string
	if c, err := r.Cookie(oidcNonceCookie); err == nil {
		nonce = c.Value
	}
	clearFlowCookie(w, oidcStateCookie)
	clearFlowCookie(w, oidcNonceCookie)

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()

	tok, err := s.oidc.Exchange(ctx, code)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "token exchange failed")
		return
	}
	claims, err := s.oidc.VerifyIDToken(ctx, tok.IDToken, nonce)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "id token rejected")
		return
	}
	// Pull userinfo only if the ID token didn't carry the email.
	if claims.Email() == "" {
		claims, _ = s.oidc.FetchUserinfo(ctx, tok.AccessToken, claims)
	}
	meta, err := s.oidc.Discover(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "oidc discovery failed")
		return
	}

	user, err := s.oidc.MapUser(ctx, s.oidcUsers, meta, claims)
	if err != nil {
		writeErr(w, oidcErrStatus(err), err.Error())
		return
	}

	access, err := auth.Sign(s.secret, user.Username, scopesForRole(user.Role), oidcTokenTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token mint failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access,
		"token_type":   "bearer",
		"user": map[string]any{
			"id":       user.ID,
			"username": user.Username,
			"email":    user.Email,
			"role":     user.Role,
		},
	})
}

// oidcErrStatus maps provisioning errors to HTTP status codes, matching the
// Python endpoint's HTTPException codes.
func oidcErrStatus(err error) int {
	switch {
	case errors.Is(err, oidc.ErrEmailMissing),
		errors.Is(err, oidc.ErrEmailUnverified),
		errors.Is(err, oidc.ErrUsernameMissing):
		return http.StatusBadRequest
	case errors.Is(err, oidc.ErrNoRoles),
		errors.Is(err, oidc.ErrRegistrationDisabled),
		errors.Is(err, oidc.ErrIdentityMismatch),
		errors.Is(err, oidc.ErrUserDisabled):
		return http.StatusForbidden
	case errors.Is(err, oidc.ErrUsernameTaken):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func setFlowCookie(w http.ResponseWriter, name, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   600,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearFlowCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
}
