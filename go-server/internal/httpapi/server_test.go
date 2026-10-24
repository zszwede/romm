package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zszwede/romm-go/internal/metadata"
	"github.com/zszwede/romm-go/internal/scan"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/internal/virtual"
	"github.com/zszwede/romm-go/internal/worker"
)

type fakeVirtual struct {
	gotURLs []string
	gotJob  virtual.Job
}

func (f *fakeVirtual) Check(_ context.Context, urls []string) ([]virtual.Availability, error) {
	f.gotURLs = urls
	return []virtual.Availability{{URL: urls[0], Available: true, Checked: true, Filename: "x.rar"}}, nil
}
func (f *fakeVirtual) Dispatch(_ context.Context, j virtual.Job) (string, error) {
	f.gotJob = j
	return "job-1", nil
}

type fakeProvider struct{}

func (fakeProvider) Name() string { return "fake" }
func (fakeProvider) Search(_ context.Context, q string) ([]metadata.Game, error) {
	return []metadata.Game{{Provider: "fake", ProviderID: 1, Name: "Match for " + q}}, nil
}

const testSecret = "unit-test-secret"

type fakeStore struct {
	counts    store.Counts
	platforms []store.Platform
	roms      map[int64]*store.RomDetail
	userID    int64
	dirs      []scan.PlatformDir

	mu               sync.Mutex
	gotUID, gotRomID int64
	gotProps         store.RomUserProps
	propsCalls       int
	upserts          []scan.Record

	locPath, locName string
	locFound         bool
}

func (f *fakeStore) Counts(context.Context) (store.Counts, error) { return f.counts, nil }
func (f *fakeStore) ListRoms(context.Context, int64, int) ([]store.Rom, error) {
	return []store.Rom{{ID: 1, Name: "A"}}, nil
}
func (f *fakeStore) ListPlatforms(context.Context) ([]store.Platform, error) { return f.platforms, nil }
func (f *fakeStore) GetRom(_ context.Context, id int64) (*store.RomDetail, error) {
	return f.roms[id], nil
}
func (f *fakeStore) GetRomLocation(context.Context, int64) (string, string, bool, error) {
	return f.locPath, f.locName, f.locFound, nil
}
func (f *fakeStore) UserIDByUsername(context.Context, string) (int64, error) { return f.userID, nil }
func (f *fakeStore) SetRomUserProps(_ context.Context, uid, romID int64, p store.RomUserProps) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotUID, f.gotRomID, f.gotProps, f.propsCalls = uid, romID, p, f.propsCalls+1
	return 1, nil
}
func (f *fakeStore) ListPlatformDirs(context.Context) ([]scan.PlatformDir, error) { return f.dirs, nil }
func (f *fakeStore) ListCollections(context.Context) ([]store.Collection, error) {
	return []store.Collection{{ID: 1, Name: "Favorites", IsFavorite: true}}, nil
}
func (f *fakeStore) Upsert(_ context.Context, r scan.Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.upserts = append(f.upserts, r)
	return nil
}
func (f *fakeStore) EnrichFromGamelist(context.Context, int64, string, string, string) error {
	return nil
}
func (f *fakeStore) upsertCount() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.upserts) }

func newTestServer(t *testing.T) (*Server, *fakeStore) {
	t.Helper()
	fs := &fakeStore{
		counts:    store.Counts{Roms: 3, Platforms: 2},
		platforms: []store.Platform{{ID: 1, Name: "Switch", RomCount: 2}},
		roms:      map[int64]*store.RomDetail{7: {ID: 7, Name: "Zelda", PlatformID: 1}},
		userID:    42,
		dirs:      []scan.PlatformDir{{ID: 28, FsSlug: "switch"}},
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "switch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "switch", "Zelda.nsp"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	q := worker.New(context.Background(), 2, 16)
	s := New(Deps{
		Store:      fs,
		AuthSecret: testSecret,
		Queue:      q,
		ScanRoot:   root,
		Metadata:   fakeProvider{},
	})
	return s, fs
}

func token(t *testing.T, secret, typ, iss string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "xiam", "iss": iss, "type": typ,
		"scopes": "roms.read roms.write", "exp": time.Now().Add(time.Hour).Unix(),
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
	s, _ := newTestServer(t)
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
	s, _ := newTestServer(t)
	w := do(s, "GET", "/api/platforms", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Switch") {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
}

func TestCollections(t *testing.T) {
	s, _ := newTestServer(t)
	w := do(s, "GET", "/api/collections", "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Favorites") {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
}

func TestGetRom(t *testing.T) {
	s, _ := newTestServer(t)
	if w := do(s, "GET", "/api/roms/7", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Zelda") {
		t.Errorf("found: code %d body %s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/roms/999", "", ""); w.Code != 404 {
		t.Errorf("missing: want 404 got %d", w.Code)
	}
}

func TestSetRomProps_AuthRequired(t *testing.T) {
	s, fs := newTestServer(t)
	if w := do(s, "PUT", "/api/roms/7/props", "", `{"status":"finished"}`); w.Code != 401 {
		t.Errorf("no token: want 401 got %d", w.Code)
	}
	if w := do(s, "PUT", "/api/roms/7/props", token(t, "wrong", "access", "romm:oauth"), `{"status":"x"}`); w.Code != 401 {
		t.Errorf("bad sig: want 401 got %d", w.Code)
	}
	if w := do(s, "PUT", "/api/roms/7/props", token(t, testSecret, "refresh", "romm:oauth"), `{"status":"x"}`); w.Code != 401 {
		t.Errorf("refresh token: want 401 got %d", w.Code)
	}
	if fs.propsCalls != 0 {
		t.Errorf("store written %d times while unauthorized", fs.propsCalls)
	}
}

func TestSetRomProps_OK(t *testing.T) {
	s, fs := newTestServer(t)
	w := do(s, "PUT", "/api/roms/7/props", token(t, testSecret, "access", "romm:oauth"),
		`{"status":"finished","backlogged":true}`)
	if w.Code != 200 {
		t.Fatalf("want 200 got %d body %s", w.Code, w.Body.String())
	}
	if fs.propsCalls != 1 || fs.gotUID != 42 || fs.gotRomID != 7 {
		t.Fatalf("calls=%d uid=%d rom=%d", fs.propsCalls, fs.gotUID, fs.gotRomID)
	}
	if fs.gotProps.Status == nil || *fs.gotProps.Status != "finished" {
		t.Errorf("status = %v", fs.gotProps.Status)
	}
	if fs.gotProps.Backlogged == nil || !*fs.gotProps.Backlogged {
		t.Errorf("backlogged not set")
	}
}

func TestScan_AuthAndRun(t *testing.T) {
	s, fs := newTestServer(t)
	// Unauthenticated -> 401.
	if w := do(s, "POST", "/api/scan", "", ""); w.Code != 401 {
		t.Fatalf("no token: want 401 got %d", w.Code)
	}
	// Authenticated -> 202 + job id.
	w := do(s, "POST", "/api/scan", token(t, testSecret, "access", "romm:oauth"), "")
	if w.Code != 202 {
		t.Fatalf("want 202 got %d body %s", w.Code, w.Body.String())
	}
	var resp struct {
		JobID int64 `json:"job_id"`
	}
	json.Unmarshal(w.Body.Bytes(), &resp)
	if resp.JobID == 0 {
		t.Fatal("no job id")
	}
	// Wait for the scan job to finish, then assert the temp-dir rom was upserted.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sw := do(s, "GET", "/api/scan/"+itoa(resp.JobID), "", "")
		if strings.Contains(sw.Body.String(), `"state":"done"`) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if fs.upsertCount() != 1 {
		t.Fatalf("upserts = %d, want 1", fs.upsertCount())
	}
	if fs.upserts[0].FsName != "Zelda.nsp" || fs.upserts[0].PlatformID != 28 {
		t.Errorf("upsert record wrong: %+v", fs.upserts[0])
	}
}

func TestSearchMetadata(t *testing.T) {
	s, _ := newTestServer(t)
	if w := do(s, "GET", "/api/metadata/search?q=zelda", "", ""); w.Code != 401 {
		t.Errorf("no token: want 401 got %d", w.Code)
	}
	tok := token(t, testSecret, "access", "romm:oauth")
	if w := do(s, "GET", "/api/metadata/search", tok, ""); w.Code != 400 {
		t.Errorf("missing q: want 400 got %d", w.Code)
	}
	w := do(s, "GET", "/api/metadata/search?q=zelda", tok, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Match for zelda") {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
}

func TestVirtual_CheckAndDispatch(t *testing.T) {
	fv := &fakeVirtual{}
	s := New(Deps{Store: &fakeStore{userID: 1}, AuthSecret: testSecret, Virtual: fv})
	tok := token(t, testSecret, "access", "romm:oauth")

	// Auth required.
	if w := do(s, "POST", "/api/virtual/check", "", `{"urls":["u1"]}`); w.Code != 401 {
		t.Errorf("check no token: want 401 got %d", w.Code)
	}
	// Check.
	w := do(s, "POST", "/api/virtual/check", tok, `{"urls":["u1","u2"]}`)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "x.rar") {
		t.Fatalf("check: code %d body %s", w.Code, w.Body.String())
	}
	if len(fv.gotURLs) != 2 {
		t.Errorf("check urls = %v", fv.gotURLs)
	}
	// Dispatch validation (missing links -> 400).
	if w := do(s, "POST", "/api/virtual/dispatch", tok, `{"system":"switch","name":"G.nsp"}`); w.Code != 400 {
		t.Errorf("dispatch missing links: want 400 got %d", w.Code)
	}
	// Dispatch ok.
	w = do(s, "POST", "/api/virtual/dispatch", tok,
		`{"system":"switch","name":"G.nsp","rom_id":35950,"platform_id":28,"links":[{"url":"https://1fichier.com/?x","category":"base"}]}`)
	if w.Code != 202 || !strings.Contains(w.Body.String(), "job-1") {
		t.Fatalf("dispatch: code %d body %s", w.Code, w.Body.String())
	}
	if fv.gotJob.RomID != 35950 || fv.gotJob.System != "switch" || len(fv.gotJob.Links) != 1 {
		t.Errorf("dispatched job wrong: %+v", fv.gotJob)
	}
}

func TestSPA_DiskWebRootAndFallback(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!doctype html><title>RomM</title>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Deps{Store: &fakeStore{}, WebRoot: root})

	// Real asset is served from disk.
	if w := do(s, "GET", "/assets/app.js", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "console.log") {
		t.Errorf("asset: code %d body %q", w.Code, w.Body.String())
	}
	// Deep client route with no file -> index.html (SPA fallback).
	if w := do(s, "GET", "/rom/35950", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "<title>RomM</title>") {
		t.Errorf("spa fallback: code %d body %q", w.Code, w.Body.String())
	}
	// index itself.
	if w := do(s, "GET", "/", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "RomM") {
		t.Errorf("index: code %d", w.Code)
	}
}

func TestRomContent(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "library", "roms", "test")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "g.bin"), []byte("DATA123"), 0o644); err != nil {
		t.Fatal(err)
	}
	fs := &fakeStore{locPath: "roms/test", locName: "g.bin", locFound: true}
	s := New(Deps{Store: fs, AuthSecret: testSecret, ScanRoot: filepath.Join(tmp, "library", "roms")})
	tok := token(t, testSecret, "access", "romm:oauth")

	// Auth required.
	if w := do(s, "GET", "/api/roms/7/content", "", ""); w.Code != 401 {
		t.Fatalf("no token: want 401 got %d", w.Code)
	}
	// Full content.
	w := do(s, "GET", "/api/roms/7/content", tok, "")
	if w.Code != 200 || w.Body.String() != "DATA123" {
		t.Fatalf("content: code %d body %q", w.Code, w.Body.String())
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "g.bin") {
		t.Errorf("content-disposition = %q", cd)
	}
	// Range request -> 206 partial.
	r := httptest.NewRequest("GET", "/api/roms/7/content", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("Range", "bytes=0-1")
	rw := httptest.NewRecorder()
	s.Handler().ServeHTTP(rw, r)
	if rw.Code != 206 || rw.Body.String() != "DA" {
		t.Errorf("range: code %d body %q", rw.Code, rw.Body.String())
	}
	// Missing rom -> 404.
	fs.locFound = false
	if w := do(s, "GET", "/api/roms/9/content", tok, ""); w.Code != 404 {
		t.Errorf("missing: want 404 got %d", w.Code)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
