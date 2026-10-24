package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zszwede/romm-go/internal/store"
)

const testSecret = "unit-test-secret"

// fakeStore implements DataStore for handler tests, no DB.
type fakeStore struct {
	counts    store.Counts
	platforms []store.Platform
	roms      map[int64]*store.RomDetail
	userID    int64

	gotUID, gotRomID int64
	gotProps         store.RomUserProps
	propsCalls       int
}

func (f *fakeStore) Counts(context.Context) (store.Counts, error) { return f.counts, nil }
func (f *fakeStore) ListRoms(context.Context, int64, int) ([]store.Rom, error) {
	return []store.Rom{{ID: 1, Name: "A"}}, nil
}
func (f *fakeStore) ListPlatforms(context.Context) ([]store.Platform, error) {
	return f.platforms, nil
}
func (f *fakeStore) GetRom(_ context.Context, id int64) (*store.RomDetail, error) {
	return f.roms[id], nil
}
func (f *fakeStore) UserIDByUsername(context.Context, string) (int64, error) {
	return f.userID, nil
}
func (f *fakeStore) SetRomUserProps(_ context.Context, uid, romID int64, p store.RomUserProps) (int, error) {
	f.gotUID, f.gotRomID, f.gotProps, f.propsCalls = uid, romID, p, f.propsCalls+1
	return 1, nil
}

func newTestServer() (*Server, *fakeStore) {
	fs := &fakeStore{
		counts:    store.Counts{Roms: 3, Platforms: 2},
		platforms: []store.Platform{{ID: 1, Name: "Switch", RomCount: 2}},
		roms:      map[int64]*store.RomDetail{7: {ID: 7, Name: "Zelda", PlatformID: 1}},
		userID:    42,
	}
	return New(fs, testSecret), fs
}

func token(t *testing.T, secret, typ, iss string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "xiam", "iss": iss, "type": typ,
		"scopes": "roms.read roms.write",
		"exp":    time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func do(s *Server, method, path, auth, body string) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if auth != "" {
		r.Header.Set("Authorization", "Bearer "+auth)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestHeartbeat(t *testing.T) {
	s, _ := newTestServer()
	w := do(s, "GET", "/api/heartbeat", "", "")
	if w.Code != 200 {
		t.Fatalf("code %d", w.Code)
	}
	var body map[string]any
	json.Unmarshal(w.Body.Bytes(), &body)
	if body["LIBRARY"].(map[string]any)["roms"].(float64) != 3 {
		t.Errorf("roms count wrong: %v", body["LIBRARY"])
	}
}

func TestPlatforms(t *testing.T) {
	s, _ := newTestServer()
	w := do(s, "GET", "/api/platforms", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Switch") {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
}

func TestGetRom(t *testing.T) {
	s, _ := newTestServer()
	if w := do(s, "GET", "/api/roms/7", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Zelda") {
		t.Errorf("found: code %d body %s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/roms/999", "", ""); w.Code != 404 {
		t.Errorf("missing: want 404 got %d", w.Code)
	}
}

func TestSetRomProps_AuthRequired(t *testing.T) {
	s, fs := newTestServer()
	// No token.
	if w := do(s, "PUT", "/api/roms/7/props", "", `{"status":"finished"}`); w.Code != 401 {
		t.Errorf("no token: want 401 got %d", w.Code)
	}
	// Token signed with the wrong secret.
	if w := do(s, "PUT", "/api/roms/7/props", token(t, "wrong", "access", "romm:oauth"), `{"status":"finished"}`); w.Code != 401 {
		t.Errorf("bad sig: want 401 got %d", w.Code)
	}
	// Refresh token (wrong type) must not authorize a write.
	if w := do(s, "PUT", "/api/roms/7/props", token(t, testSecret, "refresh", "romm:oauth"), `{"status":"finished"}`); w.Code != 401 {
		t.Errorf("refresh token: want 401 got %d", w.Code)
	}
	if fs.propsCalls != 0 {
		t.Errorf("store written %d times while unauthorized", fs.propsCalls)
	}
}

func TestSetRomProps_OK(t *testing.T) {
	s, fs := newTestServer()
	status := "finished"
	w := do(s, "PUT", "/api/roms/7/props", token(t, testSecret, "access", "romm:oauth"),
		`{"status":"finished","backlogged":true}`)
	if w.Code != 200 {
		t.Fatalf("want 200 got %d body %s", w.Code, w.Body.String())
	}
	if fs.propsCalls != 1 {
		t.Fatalf("store called %d times", fs.propsCalls)
	}
	if fs.gotUID != 42 || fs.gotRomID != 7 {
		t.Errorf("uid=%d romID=%d, want 42/7", fs.gotUID, fs.gotRomID)
	}
	if fs.gotProps.Status == nil || *fs.gotProps.Status != status {
		t.Errorf("status prop = %v, want %q", fs.gotProps.Status, status)
	}
	if fs.gotProps.Backlogged == nil || !*fs.gotProps.Backlogged {
		t.Errorf("backlogged prop not set true: %v", fs.gotProps.Backlogged)
	}
}
