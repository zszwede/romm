package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/zszwede/romm-go/internal/achievements"
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

	assets    []store.Asset
	assetKind string

	raID      int
	playlists []store.Playlist
	tracks    []store.Track

	regCalls  int
	lastReg   store.DeviceTokenInput
	regDevID  string

	authUser *store.UserRecord
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
func (f *fakeStore) GetRomRAID(context.Context, int64) (int, bool, error) {
	return f.raID, f.raID > 0, nil
}
func (f *fakeStore) ListPlaylists(context.Context, int64) ([]store.Playlist, error) {
	return f.playlists, nil
}
func (f *fakeStore) CreatePlaylist(_ context.Context, userID int64, name, desc string, pub bool) (int64, error) {
	id := int64(len(f.playlists) + 1)
	f.playlists = append(f.playlists, store.Playlist{ID: id, Name: name, Description: desc, IsPublic: pub, UserID: userID})
	return id, nil
}
func (f *fakeStore) DeletePlaylist(_ context.Context, _, id int64) (bool, error) {
	for i, p := range f.playlists {
		if p.ID == id {
			f.playlists = append(f.playlists[:i], f.playlists[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}
func (f *fakeStore) ListPlaylistTracks(context.Context, int64) ([]store.Track, error) {
	return f.tracks, nil
}
func (f *fakeStore) AddTrack(_ context.Context, playlistID, romFileID int64) (int64, error) {
	id := int64(len(f.tracks) + 1)
	f.tracks = append(f.tracks, store.Track{ID: id, PlaylistID: playlistID, RomFileID: romFileID, Position: len(f.tracks)})
	return id, nil
}
func (f *fakeStore) RemoveTrack(context.Context, int64) error                { return nil }
func (f *fakeStore) RegisterDeviceToken(_ context.Context, in store.DeviceTokenInput) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.regCalls++
	f.lastReg = in
	if f.regDevID == "" {
		f.regDevID = in.DeviceID
	}
	return f.regDevID, nil
}
func (f *fakeStore) GetUserAuth(_ context.Context, username string) (*store.UserRecord, error) {
	if f.authUser != nil && f.authUser.Username == username {
		return f.authUser, nil
	}
	return nil, nil
}
func (f *fakeStore) GetUserByID(_ context.Context, id int64) (*store.UserRecord, error) {
	if f.authUser != nil && f.authUser.ID == id {
		return f.authUser, nil
	}
	return nil, nil
}
func (f *fakeStore) ListUsers(context.Context) ([]store.UserRecord, error) {
	if f.authUser != nil {
		return []store.UserRecord{*f.authUser}, nil
	}
	return []store.UserRecord{}, nil
}
func (f *fakeStore) TouchLogin(context.Context, int64) error { return nil }
func (f *fakeStore) GetStats(context.Context) (store.Stats, error) {
	return store.Stats{Platforms: 2, Roms: 3}, nil
}
func (f *fakeStore) RandomRomID(context.Context) (int64, error) { return 7, nil }
func (f *fakeStore) GetRomFull(_ context.Context, id int64) (*store.RomFull, error) {
	if id == 7 {
		return &store.RomFull{ID: 7, Name: toNullStr("Zelda"), PlatformID: 1, PlatformSlug: "switch", PlatformName: "Switch", FsName: "Zelda.nsp", FsPath: "roms/switch"}, nil
	}
	return nil, nil
}
func (f *fakeStore) ListRomsFull(context.Context, store.RomFilter, string, string, int, int) ([]store.RomFull, error) {
	return []store.RomFull{{ID: 7, Name: toNullStr("Zelda"), PlatformID: 1, PlatformSlug: "switch", PlatformName: "Switch", FsName: "Zelda.nsp", FsPath: "roms/switch"}}, nil
}
func (f *fakeStore) CountRoms(context.Context, store.RomFilter) (int64, error) { return 1, nil }
func (f *fakeStore) RomIDIndex(context.Context, store.RomFilter, string, string) ([]int64, map[string]int, error) {
	return []int64{7}, map[string]int{"Z": 0}, nil
}
func (f *fakeStore) GetRomUser(context.Context, int64, int64) (*store.RomUser, error) {
	return nil, nil
}

func toNullStr(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func (f *fakeStore) CreateDevice(_ context.Context, userID int64, in store.DeviceCreate) (*store.Device, error) {
	n := in.Name
	return &store.Device{ID: "dev-1", UserID: userID, Name: &n, SyncMode: "api", SyncEnabled: true}, nil
}
func (f *fakeStore) GetDevice(_ context.Context, userID int64, id string) (*store.Device, error) {
	if id == "dev-1" {
		return &store.Device{ID: "dev-1", UserID: userID, SyncMode: "api"}, nil
	}
	return nil, nil
}
func (f *fakeStore) ListDevices(context.Context, int64) ([]store.Device, error) {
	return []store.Device{}, nil
}
func (f *fakeStore) DeleteDevice(_ context.Context, _ int64, id string) (bool, error) {
	return id == "dev-1", nil
}
func (f *fakeStore) CreateCollection(_ context.Context, userID int64, name, desc string, pub, fav bool, romIDs []int64) (*store.Collection, error) {
	if romIDs == nil {
		romIDs = []int64{}
	}
	return &store.Collection{ID: 10, Name: name, Description: desc, IsPublic: pub, IsFavorite: fav, UserID: userID, RomIDs: romIDs}, nil
}
func (f *fakeStore) GetCollection(_ context.Context, userID, id int64) (*store.Collection, error) {
	return &store.Collection{ID: id, Name: "Favorites", IsFavorite: true, UserID: userID, RomIDs: []int64{}}, nil
}
func (f *fakeStore) AddRomsToCollection(_ context.Context, userID, id int64, romIDs []int64) (*store.Collection, error) {
	return &store.Collection{ID: id, Name: "Favorites", IsFavorite: true, UserID: userID, RomIDs: romIDs}, nil
}
func (f *fakeStore) RemoveRomsFromCollection(_ context.Context, userID, id int64, _ []int64) (*store.Collection, error) {
	return &store.Collection{ID: id, Name: "Favorites", IsFavorite: true, UserID: userID, RomIDs: []int64{}}, nil
}
func (f *fakeStore) DeleteCollection(_ context.Context, _ int64, id int64) (bool, error) {
	return id == 10, nil
}
func (f *fakeStore) CreateLocalUser(_ context.Context, username, _, email, role string, enabled bool) (*store.UserRecord, error) {
	e := email
	return &store.UserRecord{ID: 2, Username: username, Email: &e, Role: role, Enabled: enabled}, nil
}
func (f *fakeStore) AdminUpdateUser(_ context.Context, id int64, up store.AdminUserUpdate) (*store.UserRecord, error) {
	u := &store.UserRecord{ID: id, Username: "xiam", Role: "admin", Enabled: true}
	if up.Role != nil {
		u.Role = *up.Role
	}
	if up.Enabled != nil {
		u.Enabled = *up.Enabled
	}
	return u, nil
}
func (f *fakeStore) DeleteUser(_ context.Context, id int64) (bool, error) { return id == 2, nil }
func (f *fakeStore) SetDownloadStatus(context.Context, int64, string) error { return nil }
func (f *fakeStore) ExcludeRom(context.Context, int64) error { return nil }
func (f *fakeStore) ListVirtualSources(context.Context, int64) ([]store.VirtualSource, error) { return []store.VirtualSource{}, nil }
func (f *fakeStore) UpdateVirtualSource(_ context.Context, id int64, _ store.VirtualSourceUpdate) (*store.VirtualSource, error) { return &store.VirtualSource{ID:id, Enabled:true}, nil }
func (f *fakeStore) DeleteVirtualSource(_ context.Context, id int64) (bool, error) { return id>0, nil }
func (f *fakeStore) ListRomNotes(context.Context, int64, int64) ([]store.Note, error) { return []store.Note{}, nil }
func (f *fakeStore) SetRomNote(_ context.Context, uid, rid int64, md string, pub bool) (*store.Note, error) { return &store.Note{ID:1, RomID:rid, UserID:uid, RawMarkdown:md, IsPublic:pub}, nil }
func (f *fakeStore) ClearRomNote(context.Context, int64, int64) error { return nil }
func (f *fakeStore) InsertScreenshot(context.Context, store.Screenshot) (int64, error) { return 1, nil }
func (f *fakeStore) ListScreenshots(context.Context, int64, int64) ([]store.Screenshot, error) { return []store.Screenshot{}, nil }
func (f *fakeStore) ScreenshotLocation(context.Context, int64) (string, string, bool, error) { return "", "", false, nil }
func (f *fakeStore) DeleteScreenshot(context.Context, int64) error { return nil }
func (f *fakeStore) GetPlatform(_ context.Context, id int64) (*store.Platform, error) { return &store.Platform{ID:id, Name:"Switch", DisplayName:"Switch", Slug:"switch"}, nil }
func (f *fakeStore) UpdatePlatformCustomName(_ context.Context, id int64, cn string) (*store.Platform, error) { return &store.Platform{ID:id, Name:"Switch", DisplayName:cn, Slug:"switch"}, nil }
func (f *fakeStore) DownloadQueue(context.Context) ([]store.QueuedRom, error) {
	return []store.QueuedRom{{RomID: 5, PlatformID: 1, Name: "x.nsp", Status: "wanted"}}, nil
}
func (f *fakeStore) MergeRoms(_ context.Context, ids []int64) (*store.MergeResult, error) {
	if len(ids) < 2 {
		return nil, store.ErrMergeTooFew
	}
	return &store.MergeResult{KeeperID: ids[0], AbsorbedIDs: ids[1:]}, nil
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
func (f *fakeStore) ListAssets(context.Context, string, int64, int64) ([]store.Asset, error) {
	return f.assets, nil
}
func (f *fakeStore) InsertAsset(_ context.Context, kind string, a store.Asset) (int64, error) {
	f.assetKind = kind
	a.ID = int64(len(f.assets) + 1)
	f.assets = append(f.assets, a)
	return a.ID, nil
}
func (f *fakeStore) AssetLocation(context.Context, string, int64) (string, string, bool, error) {
	if len(f.assets) == 0 {
		return "", "", false, nil
	}
	a := f.assets[len(f.assets)-1]
	return a.FilePath, a.FileName, true, nil
}
func (f *fakeStore) DeleteAsset(context.Context, string, int64) error {
	if len(f.assets) > 0 {
		f.assets = f.assets[:len(f.assets)-1]
	}
	return nil
}
func (f *fakeStore) UpdateAsset(context.Context, string, int64, store.AssetUpdate) error { return nil }
func (f *fakeStore) GetAssetByID(_ context.Context, _ string, id int64) (*store.Asset, error) {
	return &store.Asset{ID: id, RomID: 7, FileName: "s.srm"}, nil
}
func (f *fakeStore) BulkDeleteAssets(_ context.Context, _ string, ids []int64) ([]int64, error) {
	return ids, nil
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
	sys, ok := body["SYSTEM"].(map[string]any)
	if !ok || sys["VERSION"] != Version {
		t.Errorf("heartbeat SYSTEM wrong: %v", body["SYSTEM"])
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
	tok := token(t, testSecret, "access", "romm:oauth")
	if w := do(s, "GET", "/api/roms/7", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Zelda") {
		t.Errorf("found: code %d body %s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/roms/999", tok, ""); w.Code != 404 {
		t.Errorf("missing: want 404 got %d", w.Code)
	}
	if w := do(s, "GET", "/api/roms/7", "", ""); w.Code != 401 {
		t.Errorf("no auth: want 401 got %d", w.Code)
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

type fakeAch struct{ user string }

func (f *fakeAch) UserGameProgress(_ context.Context, gameID int, user string) (achievements.Progress, error) {
	f.user = user
	return achievements.Progress{GameID: gameID, Title: "Game", Total: 50, Earned: 7}, nil
}

func TestAchievements(t *testing.T) {
	fs := &fakeStore{raID: 14402}
	s := New(Deps{Store: fs, AuthSecret: testSecret, Achievements: &fakeAch{}})
	tok := token(t, testSecret, "access", "romm:oauth")

	if w := do(s, "GET", "/api/roms/7/achievements?user=xiam", "", ""); w.Code != 401 {
		t.Errorf("no token: want 401 got %d", w.Code)
	}
	if w := do(s, "GET", "/api/roms/7/achievements", tok, ""); w.Code != 400 {
		t.Errorf("missing user: want 400 got %d", w.Code)
	}
	w := do(s, "GET", "/api/roms/7/achievements?user=xiam", tok, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"earned":7`) || !strings.Contains(w.Body.String(), `"total":50`) {
		t.Fatalf("code %d body %s", w.Code, w.Body.String())
	}
	// Rom without an RA id -> 404.
	fs.raID = 0
	if w := do(s, "GET", "/api/roms/8/achievements?user=xiam", tok, ""); w.Code != 404 {
		t.Errorf("no ra id: want 404 got %d", w.Code)
	}
}

func TestMusicPlaylists(t *testing.T) {
	fs := &fakeStore{userID: 42}
	s := New(Deps{Store: fs, AuthSecret: testSecret})
	tok := token(t, testSecret, "access", "romm:oauth")

	if w := do(s, "POST", "/api/music/playlists", "", `{"name":"x"}`); w.Code != 401 {
		t.Errorf("no token: want 401 got %d", w.Code)
	}
	if w := do(s, "POST", "/api/music/playlists", tok, `{}`); w.Code != 400 {
		t.Errorf("missing name: want 400 got %d", w.Code)
	}
	// Create -> list.
	if w := do(s, "POST", "/api/music/playlists", tok, `{"name":"Chiptunes","is_public":true}`); w.Code != 201 {
		t.Fatalf("create: code %d body %s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/music/playlists", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "Chiptunes") {
		t.Fatalf("list: code %d body %s", w.Code, w.Body.String())
	}
	// Add a track -> list tracks.
	if w := do(s, "POST", "/api/music/playlists/1/tracks", tok, `{"rom_file_id":555}`); w.Code != 201 {
		t.Fatalf("add track: code %d body %s", w.Code, w.Body.String())
	}
	if w := do(s, "GET", "/api/music/playlists/1/tracks", tok, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "555") {
		t.Fatalf("list tracks: code %d body %s", w.Code, w.Body.String())
	}
	// Delete playlist.
	if w := do(s, "DELETE", "/api/music/playlists/1", tok, ""); w.Code != 204 {
		t.Errorf("delete: code %d", w.Code)
	}
	if w := do(s, "DELETE", "/api/music/playlists/999", tok, ""); w.Code != 404 {
		t.Errorf("delete missing: want 404 got %d", w.Code)
	}
}

func TestSaveSync(t *testing.T) {
	tmp := t.TempDir()
	fs := &fakeStore{userID: 42}
	s := New(Deps{Store: fs, AuthSecret: testSecret, AssetsRoot: tmp})
	tok := token(t, testSecret, "access", "romm:oauth")

	// Upload a save (multipart).
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "mario.srm")
	fw.Write([]byte("SAVEDATA"))
	mw.WriteField("emulator", "mgba")
	mw.Close()
	r := httptest.NewRequest("POST", "/api/roms/7/saves", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.Header.Set("Authorization", "Bearer "+tok)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("upload: code %d body %s", w.Code, w.Body.String())
	}
	data, err := os.ReadFile(filepath.Join(tmp, "saves", "user-42", "rom-7", "mario.srm"))
	if err != nil || string(data) != "SAVEDATA" {
		t.Fatalf("stored file = %q err=%v", data, err)
	}
	if fs.assetKind != "saves" || len(fs.assets) != 1 || fs.assets[0].SizeBytes != 8 || fs.assets[0].Emulator != "mgba" {
		t.Fatalf("inserted asset wrong: kind=%s %+v", fs.assetKind, fs.assets)
	}

	// List, download, delete.
	if lw := do(s, "GET", "/api/roms/7/saves", tok, ""); lw.Code != 200 || !strings.Contains(lw.Body.String(), "mario.srm") {
		t.Errorf("list: code %d body %s", lw.Code, lw.Body.String())
	}
	if cw := do(s, "GET", "/api/saves/1/content", tok, ""); cw.Code != 200 || cw.Body.String() != "SAVEDATA" {
		t.Errorf("content: code %d body %q", cw.Code, cw.Body.String())
	}
	if dw := do(s, "DELETE", "/api/saves/1", tok, ""); dw.Code != 204 {
		t.Errorf("delete: code %d", dw.Code)
	}
	if _, err := os.Stat(filepath.Join(tmp, "saves", "user-42", "rom-7", "mario.srm")); !os.IsNotExist(err) {
		t.Error("file not removed on delete")
	}
	// Auth required.
	if w := do(s, "POST", "/api/roms/7/saves", "", ""); w.Code != 401 {
		t.Errorf("no token upload: want 401 got %d", w.Code)
	}

	// The frontend's actual contract: POST /api/saves?rom_id=7 (top-level route,
	// rom id in the query, not the path).
	var buf2 bytes.Buffer
	mw2 := multipart.NewWriter(&buf2)
	fw2, _ := mw2.CreateFormFile("file", "zelda.state")
	fw2.Write([]byte("STATE"))
	mw2.Close()
	r2 := httptest.NewRequest("POST", "/api/states?rom_id=7&emulator=mgba", &buf2)
	r2.Header.Set("Content-Type", mw2.FormDataContentType())
	r2.Header.Set("Authorization", "Bearer "+tok)
	w2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w2, r2)
	if w2.Code != 201 {
		t.Fatalf("top-level state upload: code %d body %s", w2.Code, w2.Body.String())
	}
	// Response is a StateSchema with a download_path and rom_id.
	m := decodeBody(t, w2.Body.Bytes())
	if m["rom_id"] != float64(7) || m["file_name"] != "zelda.state" {
		t.Fatalf("state response wrong: %v", m)
	}
	if dp, _ := m["download_path"].(string); !strings.Contains(dp, "/api/states/") {
		t.Errorf("download_path wrong: %v", m["download_path"])
	}
	if _, err := os.ReadFile(filepath.Join(tmp, "states", "user-42", "rom-7", "zelda.state")); err != nil {
		t.Errorf("state file not stored: %v", err)
	}
	// List via the top-level query route returns a bare array.
	if lw := do(s, "GET", "/api/states?rom_id=7", tok, ""); lw.Code != 200 || lw.Body.Bytes()[0] != '[' {
		t.Errorf("top-level list not a bare array: code %d body %s", lw.Code, lw.Body.String())
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
