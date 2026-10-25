package httpapi

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/zszwede/romm-go/internal/store"
	"golang.org/x/crypto/bcrypt"
)

func serverWithUser(t *testing.T, role, plainPassword string) (*Server, *fakeStore) {
	t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(plainPassword), bcrypt.DefaultCost)
	if err != nil {
		t.Fatal(err)
	}
	fs := &fakeStore{
		userID: 1,
		authUser: &store.UserRecord{
			ID: 1, Username: "xiam", Role: role, Enabled: true,
			AvatarPath: "", HashedPassword: string(hash),
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		},
	}
	s := New(Deps{Store: fs, AuthSecret: testSecret})
	return s, fs
}

func basicAuthHeader(user, pass string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
}

func TestSession_SignVerifyRoundTrip(t *testing.T) {
	s, _ := serverWithUser(t, "admin", "pw")
	tok := s.signSession("xiam", "admin", time.Now().Add(time.Hour))
	u, role, ok := s.verifySession(tok)
	if !ok || u != "xiam" || role != "admin" {
		t.Fatalf("round-trip = %q %q %v", u, role, ok)
	}
	// Tamper -> reject.
	if _, _, ok := s.verifySession(tok + "x"); ok {
		t.Error("accepted tampered session")
	}
	// Expired -> reject.
	exp := s.signSession("xiam", "admin", time.Now().Add(-time.Minute))
	if _, _, ok := s.verifySession(exp); ok {
		t.Error("accepted expired session")
	}
}

func TestLogin_SetsCookiesAndMeWorks(t *testing.T) {
	s, _ := serverWithUser(t, "admin", "s3cret")

	// Wrong password -> 401.
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.Header.Set("Authorization", basicAuthHeader("xiam", "wrong"))
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatalf("wrong password = %d, want 401", w.Code)
	}

	// Correct password -> 200 + session cookie.
	r = httptest.NewRequest("POST", "/api/login", nil)
	r.Header.Set("Authorization", basicAuthHeader("xiam", "s3cret"))
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("login = %d %s", w.Code, w.Body.String())
	}
	var sessionVal string
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			sessionVal = c.Value
		}
	}
	if sessionVal == "" {
		t.Fatal("no session cookie set")
	}

	// /users/me with the cookie -> the admin, with full scopes.
	r = httptest.NewRequest("GET", "/api/users/me", nil)
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: sessionVal})
	w = httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("me = %d %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	if m["username"] != "xiam" || m["role"] != "admin" {
		t.Fatalf("me body: %v", m)
	}
	scopes := toStrings(m["oauth_scopes"])
	hasUsersWrite := false
	for _, sc := range scopes {
		if sc == "users.write" {
			hasUsersWrite = true
		}
	}
	if !hasUsersWrite {
		t.Errorf("admin missing users.write scope: %v", scopes)
	}
}

func TestMe_Unauthorized(t *testing.T) {
	s, _ := serverWithUser(t, "admin", "pw")
	if w := do(s, "GET", "/api/users/me", "", ""); w.Code != 401 {
		t.Errorf("me without session = %d, want 401", w.Code)
	}
}

func TestLogout_ClearsCookie(t *testing.T) {
	s, _ := serverWithUser(t, "admin", "pw")
	w := do(s, "POST", "/api/logout", "", "")
	if w.Code != 200 {
		t.Fatalf("logout = %d", w.Code)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("logout did not clear session cookie")
	}
}

func TestConfigAndHeartbeat_Shape(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(s, "GET", "/api/heartbeat", "", "")
	if w.Code != 200 {
		t.Fatalf("heartbeat = %d", w.Code)
	}
	hb := decodeBody(t, w.Body.Bytes())
	for _, k := range []string{"SYSTEM", "METADATA_SOURCES", "FILESYSTEM", "EMULATION", "FRONTEND", "OIDC", "TASKS"} {
		if _, ok := hb[k]; !ok {
			t.Errorf("heartbeat missing %s", k)
		}
	}
	w = do(s, "GET", "/api/config", "", "")
	if w.Code != 200 {
		t.Fatalf("config = %d", w.Code)
	}
	cfg := decodeBody(t, w.Body.Bytes())
	if _, ok := cfg["EJS_DEFAULT_CORES"]; !ok {
		t.Error("config missing EJS_DEFAULT_CORES")
	}
}
