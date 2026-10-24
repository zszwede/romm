package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zszwede/romm-go/internal/auth"
	"github.com/zszwede/romm-go/internal/oidc"
)

// --- fake issuer (httptest) ---

type oidcFakeIssuer struct {
	srv *httptest.Server
	key *rsa.PrivateKey
	kid string
}

func newOIDCFakeIssuer(t *testing.T, idClaims func(host string) jwt.MapClaims) *oidcFakeIssuer {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	fi := &oidcFakeIssuer{key: key, kid: "k1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": base, "authorization_endpoint": base + "/authorize",
			"token_endpoint": base + "/token", "jwks_uri": base + "/jwks",
			"claims_supported": []string{"sub", "email", "email_verified"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := fi.key.PublicKey
		var eb []byte
		for e := pub.E; e > 0; e >>= 8 {
			eb = append([]byte{byte(e & 0xff)}, eb...)
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "use": "sig", "kid": fi.kid,
			"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(eb),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		c := idClaims("http://" + r.Host)
		if c["iss"] == nil {
			c["iss"] = "http://" + r.Host
		}
		if c["exp"] == nil {
			c["exp"] = time.Now().Add(time.Hour).Unix()
		}
		tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
		tok.Header["kid"] = fi.kid
		idt, _ := tok.SignedString(fi.key)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "ac", "token_type": "Bearer", "id_token": idt})
	})
	fi.srv = httptest.NewServer(mux)
	t.Cleanup(fi.srv.Close)
	return fi
}

// --- fake user store ---

type fakeUsers struct {
	byID   map[int64]*oidc.User
	nextID int64
}

func newFakeUsers(seed ...*oidc.User) *fakeUsers {
	u := &fakeUsers{byID: map[int64]*oidc.User{}, nextID: 1}
	for _, s := range seed {
		if s.ID == 0 {
			s.ID = u.nextID
		}
		u.nextID = s.ID + 1
		cp := *s
		u.byID[s.ID] = &cp
	}
	return u
}

func (f *fakeUsers) GetUserByOIDCIdentity(_ context.Context, iss, sub string) (*oidc.User, error) {
	for _, u := range f.byID {
		if iss != "" && sub != "" && u.OIDCIssuer == iss && u.OIDCSub == sub {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (f *fakeUsers) GetUserByEmail(_ context.Context, email string) (*oidc.User, error) {
	for _, u := range f.byID {
		if strings.EqualFold(u.Email, email) {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (f *fakeUsers) GetUserByUsername(_ context.Context, name string) (*oidc.User, error) {
	for _, u := range f.byID {
		if u.Username == name {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (f *fakeUsers) AddUser(_ context.Context, nu oidc.NewUser) (*oidc.User, error) {
	id := f.nextID
	f.nextID++
	u := &oidc.User{ID: id, Username: nu.Username, Email: nu.Email, Role: nu.Role,
		OIDCIssuer: nu.OIDCIssuer, OIDCSub: nu.OIDCSub, Enabled: nu.Enabled}
	f.byID[id] = u
	cp := *u
	return &cp, nil
}
func (f *fakeUsers) UpdateUser(_ context.Context, id int64, up oidc.UserUpdates) (*oidc.User, error) {
	u := f.byID[id]
	if up.Email != nil {
		u.Email = *up.Email
	}
	if up.Role != nil {
		u.Role = *up.Role
	}
	if up.OIDCIssuer != nil {
		u.OIDCIssuer = *up.OIDCIssuer
	}
	if up.OIDCSub != nil {
		u.OIDCSub = *up.OIDCSub
	}
	cp := *u
	return &cp, nil
}

// --- server builder ---

func oidcServer(t *testing.T, fi *oidcFakeIssuer, users oidc.UserStore, allowReg bool, claimRoles string, roleAdmin []string) *Server {
	t.Helper()
	p := oidc.NewProvider(oidc.Config{
		Enabled: true, AllowRegistration: allowReg,
		ClientID: "romm-client", ClientSecret: "shh",
		RedirectURI: "https://romm.local/api/auth/oauth/openid",
		MetadataURL: fi.srv.URL + "/.well-known/openid-configuration",
		ClaimRoles:  claimRoles, RoleAdmin: roleAdmin,
	})
	return New(Deps{Store: &fakeStore{userID: 1}, AuthSecret: testSecret, OIDC: p, OIDCUsers: users})
}

func TestOIDCLogin_Redirects(t *testing.T) {
	fi := newOIDCFakeIssuer(t, func(string) jwt.MapClaims { return jwt.MapClaims{} })
	s := oidcServer(t, fi, newFakeUsers(), true, "", nil)

	w := do(s, "GET", "/api/auth/login/openid", "", "")
	if w.Code != http.StatusFound {
		t.Fatalf("login code = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "/authorize") || !strings.Contains(loc, "client_id=romm-client") {
		t.Fatalf("bad Location: %s", loc)
	}
	var sawState bool
	for _, c := range w.Result().Cookies() {
		if c.Name == oidcStateCookie && c.Value != "" {
			sawState = true
		}
	}
	if !sawState {
		t.Error("state cookie not set")
	}
}

func TestOIDCLogin_NotConfigured(t *testing.T) {
	s, _ := newTestServer(t) // no OIDC
	if w := do(s, "GET", "/api/auth/login/openid", "", ""); w.Code != 404 {
		t.Errorf("login without provider = %d, want 404", w.Code)
	}
	if w := do(s, "GET", "/api/auth/oauth/openid?code=x&state=y", "", ""); w.Code != 404 {
		t.Errorf("callback without provider = %d, want 404", w.Code)
	}
}

// callbackWithCookies drives the callback with the state/nonce cookies set.
func callbackWithCookies(s *Server, state, nonce string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/api/auth/oauth/openid?code=thecode&state="+state, nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: state})
	if nonce != "" {
		r.AddCookie(&http.Cookie{Name: oidcNonceCookie, Value: nonce})
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestOIDCCallback_ProvisionsUserAndMintsToken(t *testing.T) {
	fi := newOIDCFakeIssuer(t, func(host string) jwt.MapClaims {
		return jwt.MapClaims{
			"sub": "sub-1", "email": "new.user@example.com", "email_verified": true,
			"preferred_username": "new.user", "aud": "romm-client", "nonce": "NONCE1",
		}
	})
	users := newFakeUsers()
	s := oidcServer(t, fi, users, true, "", nil)

	w := callbackWithCookies(s, "STATE1", "NONCE1")
	if w.Code != 200 {
		t.Fatalf("callback = %d %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	tokStr, _ := m["access_token"].(string)
	claims, err := auth.Verify(tokStr, testSecret)
	if err != nil {
		t.Fatalf("minted token did not verify: %v", err)
	}
	if claims.Username != "new-user" { // sanitized from new.user
		t.Errorf("token sub = %q, want new-user", claims.Username)
	}
	if !claims.HasScope("me.read") || !claims.HasScope("roms.user.write") {
		t.Errorf("token missing baseline scopes: %v", claims.Scopes)
	}
	if len(users.byID) != 1 {
		t.Errorf("user not provisioned (have %d)", len(users.byID))
	}
}

func TestOIDCCallback_StateMismatch(t *testing.T) {
	fi := newOIDCFakeIssuer(t, func(string) jwt.MapClaims { return jwt.MapClaims{"aud": "romm-client"} })
	s := oidcServer(t, fi, newFakeUsers(), true, "", nil)

	r := httptest.NewRequest("GET", "/api/auth/oauth/openid?code=c&state=SENT", nil)
	r.AddCookie(&http.Cookie{Name: oidcStateCookie, Value: "DIFFERENT"})
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 400 {
		t.Fatalf("state mismatch = %d, want 400", w.Code)
	}
}

func TestOIDCCallback_RegistrationDisabled(t *testing.T) {
	fi := newOIDCFakeIssuer(t, func(host string) jwt.MapClaims {
		return jwt.MapClaims{
			"sub": "s", "email": "nope@example.com", "email_verified": true,
			"preferred_username": "nope", "aud": "romm-client", "nonce": "N",
		}
	})
	s := oidcServer(t, fi, newFakeUsers(), false, "", nil) // registration off, empty store

	w := callbackWithCookies(s, "S", "N")
	if w.Code != 403 {
		t.Fatalf("registration disabled = %d, want 403 (body %s)", w.Code, w.Body.String())
	}
}

func TestOIDCCallback_AdminRoleGrantsWriteScopes(t *testing.T) {
	fi := newOIDCFakeIssuer(t, func(host string) jwt.MapClaims {
		return jwt.MapClaims{
			"sub": "admin-sub", "email": "admin@example.com", "email_verified": true,
			"preferred_username": "admin", "aud": "romm-client", "nonce": "N",
			"groups": []any{"romm-admins"},
		}
	})
	s := oidcServer(t, fi, newFakeUsers(), true, "groups", []string{"romm-admins"})

	w := callbackWithCookies(s, "S", "N")
	if w.Code != 200 {
		t.Fatalf("admin callback = %d %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	claims, err := auth.Verify(m["access_token"].(string), testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if !claims.HasScope("users.write") || !claims.HasScope("roms.write") {
		t.Errorf("admin token missing write scopes: %v", claims.Scopes)
	}
	u := m["user"].(map[string]any)
	if u["role"] != "admin" {
		t.Errorf("role = %v, want admin", u["role"])
	}
}
