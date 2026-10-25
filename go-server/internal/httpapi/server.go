// Package httpapi is the HTTP layer. Phase 2 adds a platforms list, a rom
// detail endpoint, and a token-protected rom_user write, on top of the Phase-1
// heartbeat/roms. Bearer tokens are the same HS256 access tokens the Python
// backend issues (verified via internal/auth). Only the stdlib net/http router
// (Go 1.22 pattern matching) is used.
package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/achievements"
	"github.com/zszwede/romm-go/internal/auth"
	"github.com/zszwede/romm-go/internal/config"
	"github.com/zszwede/romm-go/internal/deviceauth"
	"github.com/zszwede/romm-go/internal/metadata"
	"github.com/zszwede/romm-go/internal/netplay"
	"github.com/zszwede/romm-go/internal/oidc"
	"github.com/zszwede/romm-go/internal/scan"
	"github.com/zszwede/romm-go/internal/socketio"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/internal/virtual"
	"github.com/zszwede/romm-go/internal/worker"
	"github.com/zszwede/romm-go/web"
)

// VirtualService is the merge-service orchestration the API needs (an interface
// so handlers can be faked in tests).
type VirtualService interface {
	Check(ctx context.Context, urls []string) ([]virtual.Availability, error)
	Dispatch(ctx context.Context, j virtual.Job) (string, error)
}

// AchievementService fetches a user's RetroAchievements progress for a game.
type AchievementService interface {
	UserGameProgress(ctx context.Context, gameID int, user string) (achievements.Progress, error)
}

// Version reported by the heartbeat. Distinct from the Python app's so it's
// obvious which backend answered during the migration.
const Version = "go-0.2.0"

// DataStore is the slice of the store the API needs. An interface so handlers
// can be unit-tested against a fake without a database.
type DataStore interface {
	Counts(ctx context.Context) (store.Counts, error)
	ListRoms(ctx context.Context, platformID int64, limit int) ([]store.Rom, error)
	ListPlatforms(ctx context.Context) ([]store.Platform, error)
	GetRom(ctx context.Context, id int64) (*store.RomDetail, error)
	GetRomLocation(ctx context.Context, id int64) (fsPath, fsName string, found bool, err error)
	GetRomRAID(ctx context.Context, id int64) (raID int, found bool, err error)
	UserIDByUsername(ctx context.Context, username string) (int64, error)
	SetRomUserProps(ctx context.Context, userID, romID int64, p store.RomUserProps) (int, error)
	ListPlatformDirs(ctx context.Context) ([]scan.PlatformDir, error)
	Upsert(ctx context.Context, r scan.Record) error
	EnrichFromGamelist(ctx context.Context, platformID int64, fsName, name, summary string) error
	ListCollections(ctx context.Context) ([]store.Collection, error)
	ListAssets(ctx context.Context, kind string, romID, userID int64) ([]store.Asset, error)
	InsertAsset(ctx context.Context, kind string, a store.Asset) (int64, error)
	AssetLocation(ctx context.Context, kind string, id int64) (filePath, fileName string, found bool, err error)
	DeleteAsset(ctx context.Context, kind string, id int64) error
	UpdateAsset(ctx context.Context, kind string, id int64, up store.AssetUpdate) error
	GetAssetByID(ctx context.Context, kind string, id int64) (*store.Asset, error)
	BulkDeleteAssets(ctx context.Context, kind string, ids []int64) ([]int64, error)
	ListPlaylists(ctx context.Context, userID int64) ([]store.Playlist, error)
	CreatePlaylist(ctx context.Context, userID int64, name, description string, isPublic bool) (int64, error)
	DeletePlaylist(ctx context.Context, userID, id int64) (bool, error)
	ListPlaylistTracks(ctx context.Context, playlistID int64) ([]store.Track, error)
	AddTrack(ctx context.Context, playlistID, romFileID int64) (int64, error)
	RemoveTrack(ctx context.Context, trackID int64) error
	RegisterDeviceToken(ctx context.Context, in store.DeviceTokenInput) (string, error)
	GetUserAuth(ctx context.Context, username string) (*store.UserRecord, error)
	GetUserByID(ctx context.Context, id int64) (*store.UserRecord, error)
	ListUsers(ctx context.Context) ([]store.UserRecord, error)
	TouchLogin(ctx context.Context, id int64) error
	GetStats(ctx context.Context) (store.Stats, error)
	RandomRomID(ctx context.Context) (int64, error)
	GetRomFull(ctx context.Context, id int64) (*store.RomFull, error)
	ListRomsFull(ctx context.Context, f store.RomFilter, orderBy, orderDir string, limit, offset int) ([]store.RomFull, error)
	CountRoms(ctx context.Context, f store.RomFilter) (int64, error)
	RomIDIndex(ctx context.Context, f store.RomFilter, orderBy, orderDir string) ([]int64, map[string]int, error)
	GetRomUser(ctx context.Context, userID, romID int64) (*store.RomUser, error)
	CreateDevice(ctx context.Context, userID int64, in store.DeviceCreate) (*store.Device, error)
	GetDevice(ctx context.Context, userID int64, id string) (*store.Device, error)
	ListDevices(ctx context.Context, userID int64) ([]store.Device, error)
	DeleteDevice(ctx context.Context, userID int64, id string) (bool, error)
	CreateCollection(ctx context.Context, userID int64, name, description string, isPublic, isFavorite bool, romIDs []int64) (*store.Collection, error)
	GetCollection(ctx context.Context, userID, id int64) (*store.Collection, error)
	AddRomsToCollection(ctx context.Context, userID, id int64, romIDs []int64) (*store.Collection, error)
	RemoveRomsFromCollection(ctx context.Context, userID, id int64, romIDs []int64) (*store.Collection, error)
	DeleteCollection(ctx context.Context, userID, id int64) (bool, error)
	CreateLocalUser(ctx context.Context, username, hashedPassword, email, role string, enabled bool) (*store.UserRecord, error)
	AdminUpdateUser(ctx context.Context, id int64, up store.AdminUserUpdate) (*store.UserRecord, error)
	DeleteUser(ctx context.Context, id int64) (bool, error)
	SetDownloadStatus(ctx context.Context, romID int64, status string) error
	DownloadQueue(ctx context.Context) ([]store.QueuedRom, error)
	MergeRoms(ctx context.Context, romIDs []int64) (*store.MergeResult, error)
	ExcludeRom(ctx context.Context, romID int64) error
	ListVirtualSources(ctx context.Context, platformID int64) ([]store.VirtualSource, error)
	UpdateVirtualSource(ctx context.Context, id int64, up store.VirtualSourceUpdate) (*store.VirtualSource, error)
	DeleteVirtualSource(ctx context.Context, id int64) (bool, error)
	CreateVirtualSource(ctx context.Context, platformID int64, url string) (*store.VirtualSource, error)
	CreateVirtualRom(ctx context.Context, platformID int64, fsSlug string, g store.VirtualGameInput) (bool, error)
	ListEnabledSources(ctx context.Context, platformID int64) ([]string, error)
	ListRomNotes(ctx context.Context, romID, userID int64) ([]store.Note, error)
	SetRomNote(ctx context.Context, userID, romID int64, markdown string, isPublic bool) (*store.Note, error)
	ClearRomNote(ctx context.Context, userID, romID int64) error
	SetRomManual(ctx context.Context, romID int64, relPath string) error
	ListClientTokens(ctx context.Context, userID int64) ([]store.ClientToken, error)
	CreateClientToken(ctx context.Context, userID int64, name, hashedToken, scopes string, expiresAt *time.Time) (*store.ClientToken, error)
	DeleteClientToken(ctx context.Context, userID, id int64) (bool, error)
	GetPlatform(ctx context.Context, id int64) (*store.Platform, error)
	GetPlatformSourceIDs(ctx context.Context, platformID int64) (*store.PlatformSourceIDs, error)
	RomMirrorURLs(ctx context.Context, romID int64) ([]string, error)
	ListRomFiles(ctx context.Context, romID int64) ([]store.RomFile, error)
	ListSiblings(ctx context.Context, romID, userID int64) ([]store.SiblingRom, error)
	GetRomMetadata(ctx context.Context, romID int64) (*store.RomMetadata, error)
	RomCollections(ctx context.Context, romID int64) ([]store.Collection, error)
	UnidentifiedHashedRoms(ctx context.Context, limit int) ([]store.HashedRom, error)
	SetHashMatchIDs(ctx context.Context, romID int64, igdb, hasheous, tgdb *int64) error
	UpdatePlatformCustomName(ctx context.Context, id int64, customName string) (*store.Platform, error)
	InsertScreenshot(ctx context.Context, sc store.Screenshot) (int64, error)
	ListScreenshots(ctx context.Context, romID, userID int64) ([]store.Screenshot, error)
	ScreenshotLocation(ctx context.Context, id int64) (filePath, fileName string, found bool, err error)
	DeleteScreenshot(ctx context.Context, id int64) error
}

// Deps are the server's collaborators. Passed as a struct so new ones can be
// added without churning every call site.
type Deps struct {
	Store        DataStore
	AuthSecret   string
	Queue        *worker.Queue
	ScanRoot     string // ES-DE roms root, e.g. ROMM_BASE_PATH/library/roms
	Metadata     metadata.Provider
	// MetadataSS, when set, is the ScreenScraper provider (this install's primary
	// source). It is platform-scoped, so it is held separately from Metadata.
	MetadataSS metadata.PlatformSearcher
	// Libretro, when set, enriches manual-match results with box-art covers from
	// the public libretro-thumbnails server.
	Libretro *metadata.Libretro
	// Hasheous, when set, identifies ROMs by file hash during a hashed scan.
	Hasheous *metadata.Hasheous
	Virtual      VirtualService
	Achievements AchievementService
	// WebRoot is a directory holding the built frontend (the real Vue dist +
	// EmulatorJS/Ruffle/js-dos player assets). When set and present it is served
	// from disk; otherwise the embedded placeholder shell is used. (The heavy
	// player assets are ~400MB, impractical to embed in the executable.)
	WebRoot string
	// AssetsRoot: directory for uploaded saves/states (ROMM_BASE_PATH/assets).
	AssetsRoot string
	// ResourcesRoot: directory for cover/screenshot/manual resources
	// (ROMM_BASE_PATH/resources), served at /assets/romm/resources/.
	ResourcesRoot string
	// OIDC, when non-nil, enables the OpenID Connect login endpoints. OIDCUsers
	// is the user store it provisions against (the *store.Store satisfies it).
	OIDC      *oidc.Provider
	OIDCUsers oidc.UserStore
	// ConfigFile is the parsed RomM config.yml, serving GET /api/config. When
	// nil the endpoint reports the file as not mounted with empty defaults.
	ConfigFile *config.ConfigData
	// DevUser, when non-empty, DISABLES auth: every request is treated as this
	// (admin) user. Development convenience only — never on an internet-facing
	// instance. Set via ROMM_DEV_NO_AUTH.
	DevUser string
}

type Server struct {
	st          DataStore
	secret      string
	queue       *worker.Queue
	scanRoot    string
	libraryRoot string // parent of scanRoot; roms resolve as <libraryRoot>/<fs_path>/<fs_name>
	meta        metadata.Provider
	ss          metadata.PlatformSearcher
	libretro    *metadata.Libretro
	hasheous    *metadata.Hasheous
	virt        VirtualService
	ach         AchievementService
	assetsRoot    string
	resourcesRoot string
	static      fs.FS
	flows       *deviceauth.Store
	oidc        *oidc.Provider
	oidcUsers   oidc.UserStore
	configFile  *config.ConfigData
	devUser     string
	netplay     *netplay.Hub
	sockets     *socketio.Hub
	uploads     *uploadManager
	mux         *http.ServeMux
}

func New(d Deps) *Server {
	s := &Server{
		st:         d.Store,
		secret:     d.AuthSecret,
		queue:      d.Queue,
		scanRoot:   d.ScanRoot,
		meta:       d.Metadata,
		ss:         d.MetadataSS,
		libretro:   d.Libretro,
		hasheous:   d.Hasheous,
		virt:       d.Virtual,
		ach:           d.Achievements,
		assetsRoot:    d.AssetsRoot,
		resourcesRoot: d.ResourcesRoot,
		flows:      deviceauth.NewStore(),
		oidc:       d.OIDC,
		oidcUsers:  d.OIDCUsers,
		configFile: d.ConfigFile,
		devUser:    d.DevUser,
		mux:        http.NewServeMux(),
	}
	// Netplay signaling hub. The room store + relay logic live here; the
	// Socket.IO wire transport that drives open/join is the one unbuilt seam,
	// so for now the hub is fed only by tests and the REST list reflects it.
	s.netplay = netplay.NewHub(netplay.NewRoomStore(), netplayNoopEmitter{}, denyAllAuthz{})
	s.sockets = socketio.NewHub()
	// The frontend's Scan page triggers a scan by emitting a "scan" socket
	// event and watches scan:* events for progress.
	s.sockets.On("scan", s.handleScanEvent)
	s.uploads = newUploadManager(filepath.Join(os.TempDir(), "romm-go-uploads"))
	if d.ScanRoot != "" {
		s.libraryRoot = filepath.Dir(d.ScanRoot)
	}
	s.static = resolveStatic(d.WebRoot)
	s.routes()
	return s
}

// resolveStatic serves the real frontend from WebRoot when it exists, else the
// embedded placeholder shell.
func resolveStatic(webRoot string) fs.FS {
	if webRoot != "" {
		if st, err := os.Stat(filepath.Join(webRoot, "index.html")); err == nil && !st.IsDir() {
			log.Printf("serving frontend from disk: %s", webRoot)
			return os.DirFS(webRoot)
		}
		log.Printf("WebRoot %q has no index.html; using embedded shell", webRoot)
	}
	sub, err := fs.Sub(web.Static, "static")
	if err != nil {
		log.Fatalf("embed static: %v", err)
	}
	return sub
}

func (s *Server) Handler() http.Handler { return logging(s.mux) }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/heartbeat", s.heartbeatFull)
	s.mux.HandleFunc("GET /api/platforms", s.listPlatforms)
	s.mux.HandleFunc("GET /api/platforms/supported", s.supportedPlatforms)
	s.mux.HandleFunc("GET /api/platforms/filesystem", s.filesystemPlatforms)
	s.mux.HandleFunc("GET /api/platforms/{id}", s.getPlatform)
	s.mux.HandleFunc("PUT /api/platforms/{id}", s.updatePlatform)
	s.mux.HandleFunc("POST /api/tasks/run/{name}", s.runTask)
	s.mux.HandleFunc("GET /api/screenshots", s.listScreenshots)
	s.mux.HandleFunc("POST /api/screenshots", s.uploadScreenshot)
	s.mux.HandleFunc("GET /api/screenshots/{id}/content", s.screenshotContent)
	s.mux.HandleFunc("DELETE /api/screenshots/{id}", s.deleteScreenshot)
	s.mux.HandleFunc("GET /api/music/favorites", s.musicFavorites)
	s.mux.HandleFunc("POST /api/music/favorites", s.musicFavorites)
	s.mux.HandleFunc("DELETE /api/music/favorites", s.musicFavorites)
	s.mux.HandleFunc("GET /api/roms", s.listRoms)
	s.mux.HandleFunc("GET /api/roms/{id}", s.getRom)
	s.mux.HandleFunc("GET /api/roms/{id}/content", s.getRomContent)
	// The frontend builds /api/roms/{id}/content/{fs_name} (filename segment for
	// a nice download name + player path); serve both the same way.
	s.mux.HandleFunc("GET /api/roms/{id}/content/{name...}", s.getRomContent)
	s.mux.HandleFunc("GET /api/roms/{id}/achievements", s.getAchievements)
	s.mux.HandleFunc("GET /api/roms/{id}/similar", s.getSimilarRoms)
	s.mux.HandleFunc("POST /api/roms/{id}/manuals", s.uploadManual)
	s.mux.HandleFunc("DELETE /api/roms/{id}/manuals", s.deleteManual)
	s.mux.HandleFunc("GET /api/roms/{id}/notes", s.listRomNotes)
	s.mux.HandleFunc("POST /api/roms/{id}/notes", s.setRomNote)
	s.mux.HandleFunc("PUT /api/roms/{id}/notes/{note_id}", s.setRomNote)
	s.mux.HandleFunc("DELETE /api/roms/{id}/notes/{note_id}", s.deleteRomNote)
	s.mux.HandleFunc("GET /api/collections", s.listCollections)
	s.mux.HandleFunc("POST /api/collections", s.createCollection)
	s.mux.HandleFunc("GET /api/collections/{id}", s.getCollection)
	s.mux.HandleFunc("DELETE /api/collections/{id}", s.deleteCollection)
	s.mux.HandleFunc("POST /api/collections/{id}/roms", s.addRomsToCollection)
	s.mux.HandleFunc("DELETE /api/collections/{id}/roms", s.removeRomsFromCollection)
	s.mux.HandleFunc("PUT /api/roms/{id}/props", s.setRomProps)
	// Chunked ROM upload. The chunk PUT goes through a general 2-segment matcher
	// because PUT /api/roms/upload/{id} would conflict with the more-specific
	// PUT /api/roms/{id}/props in the stdlib mux; props stays specific and wins,
	// and this general pattern dispatches the "upload" prefix.
	s.mux.HandleFunc("POST /api/roms/upload/start", s.uploadStart)
	s.mux.HandleFunc("POST /api/roms/upload/{id}/cancel", s.uploadCancel)
	s.mux.HandleFunc("POST /api/roms/upload/{id}/complete", s.uploadComplete)
	s.mux.HandleFunc("PUT /api/roms/{seg}/{id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("seg") == "upload" {
			s.uploadChunk(w, r)
			return
		}
		writeErr(w, http.StatusNotFound, "not found")
	})

	s.mux.HandleFunc("POST /api/scan", s.triggerScan)
	s.mux.HandleFunc("GET /api/scan", s.listScans)
	s.mux.HandleFunc("GET /api/scan/{id}", s.scanStatus)
	s.mux.HandleFunc("GET /api/metadata/search", s.searchMetadata)
	s.mux.HandleFunc("GET /api/search/roms", s.searchRoms)
	s.mux.HandleFunc("POST /api/virtual/check", s.virtualCheck)
	s.mux.HandleFunc("POST /api/virtual/dispatch", s.virtualDispatch)
	s.mux.HandleFunc("GET /api/virtual/queued", s.virtualQueued)
	s.mux.HandleFunc("POST /api/virtual/merge", s.virtualMerge)
	s.mux.HandleFunc("POST /api/virtual/roms/{id}/exclude", s.virtualExclude)
	s.mux.HandleFunc("GET /api/virtual/sources", s.virtualSources)
	s.mux.HandleFunc("POST /api/virtual/assign", s.virtualAssign)
	s.mux.HandleFunc("POST /api/virtual/platforms/{id}/sync", s.virtualSyncPlatform)
	s.mux.HandleFunc("PATCH /api/virtual/sources/{id}", s.virtualUpdateSource)
	s.mux.HandleFunc("DELETE /api/virtual/sources/{id}", s.virtualDeleteSource)
	s.mux.HandleFunc("POST /api/virtual/roms/{id}/mark-download", s.virtualMarkDownload)
	s.mux.HandleFunc("DELETE /api/virtual/roms/{id}/mark-download", s.virtualUnmarkDownload)

	// Device authorization flow (RFC 8628-style). init/token are open; the
	// approval endpoints require the user's bearer token.
	s.mux.HandleFunc("POST /api/auth/device/init", s.deviceInit)
	s.mux.HandleFunc("GET /api/auth/device/pending/{user_code}", s.devicePending)
	s.mux.HandleFunc("POST /api/auth/device/approve", s.deviceApprove)
	s.mux.HandleFunc("POST /api/auth/device/deny", s.deviceDeny)
	s.mux.HandleFunc("POST /api/auth/device/token", s.deviceToken)

	// OpenID Connect login (authorization-code flow). Active only when an OIDC
	// provider is configured; otherwise both return 404.
	s.mux.HandleFunc("GET /api/auth/login/openid", s.oidcLogin)
	s.mux.HandleFunc("GET /api/auth/oauth/openid", s.oidcCallback)

	// Netplay: list joinable rooms for a ROM.
	s.mux.HandleFunc("GET /api/netplay/list", s.netplayList)

	// Socket.IO real-time transport (scan progress, live events).
	s.mux.HandleFunc("GET /ws/socket.io/", s.sockets.Handler)

	// Sync devices.
	s.mux.HandleFunc("POST /api/devices", s.registerDevice)
	s.mux.HandleFunc("GET /api/devices", s.listDevices)
	s.mux.HandleFunc("GET /api/devices/online", s.onlineDevices)
	s.mux.HandleFunc("GET /api/devices/{id}", s.getDevice)
	s.mux.HandleFunc("DELETE /api/devices/{id}", s.deleteDevice)

	// Session auth + identity.
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /api/logout", s.logout)
	s.mux.HandleFunc("GET /api/users/me", s.usersMe)
	s.mux.HandleFunc("GET /api/users", s.listUsers)
	s.mux.HandleFunc("GET /api/client-tokens", s.listClientTokens)
	s.mux.HandleFunc("POST /api/client-tokens", s.createClientToken)
	s.mux.HandleFunc("DELETE /api/client-tokens/{id}", s.deleteClientToken)
	s.mux.HandleFunc("POST /api/users", s.createUser)
	s.mux.HandleFunc("POST /api/users/invite-link", s.inviteLink)
	s.mux.HandleFunc("GET /api/users/{id}", s.getUser)
	s.mux.HandleFunc("PUT /api/users/{id}", s.updateUser)
	s.mux.HandleFunc("DELETE /api/users/{id}", s.deleteUser)

	// Frontend bootstrap.
	s.mux.HandleFunc("GET /api/config", s.getConfig)
	s.mux.HandleFunc("GET /api/config/downloads_settings", s.getDownloadsSettings)
	s.mux.HandleFunc("GET /api/permissions/me", s.getPermissions)
	s.mux.HandleFunc("GET /api/permissions/catalog", s.permissionsCatalog)
	s.mux.HandleFunc("GET /api/permissions/groups", s.permissionGroups)
	s.mux.HandleFunc("GET /api/permissions/users/{id}", s.userPermissions)
	s.mux.HandleFunc("PUT /api/permissions/users/{id}", s.userPermissions)
	s.mux.HandleFunc("POST /api/permissions/hidden", s.hiddenEntities)
	s.mux.HandleFunc("DELETE /api/permissions/hidden", s.hiddenEntities)
	s.mux.HandleFunc("POST /api/users/{id}/ra/refresh", s.raRefresh)
	s.mux.HandleFunc("GET /api/stats", s.getStats)
	s.mux.HandleFunc("GET /api/notifications", s.getNotifications)
	s.mux.HandleFunc("GET /api/recommendations", s.getRecommendations)
	s.mux.HandleFunc("GET /api/collections/smart", s.getSmartCollections)
	s.mux.HandleFunc("GET /api/collections/virtual", s.getVirtualCollections)
	s.mux.HandleFunc("GET /api/tasks/status", s.getTasksStatus)
	s.mux.HandleFunc("GET /api/streaming/config", s.getStreamingConfig)
	s.mux.HandleFunc("GET /api/streaming/sessions/joinable", s.streamingJoinable)
	s.mux.HandleFunc("GET /api/streaming/states/{id}", s.streamingStatePicks)
	s.mux.HandleFunc("GET /api/streaming/sessions/{platform}/status", s.streamingSessionStatus)
	s.mux.HandleFunc("GET /api/roms/random", s.getRandomRom)
	s.mux.HandleFunc("GET /api/firmware", s.getFirmware)
	s.mux.HandleFunc("GET /api/logs", s.getLogs)
	s.mux.HandleFunc("GET /api/audit-events", s.getAuditEvents)

	// Music playlists.
	s.mux.HandleFunc("GET /api/music/playlists", s.listPlaylists)
	s.mux.HandleFunc("POST /api/music/playlists", s.createPlaylist)
	s.mux.HandleFunc("DELETE /api/music/playlists/{id}", s.deletePlaylist)
	s.mux.HandleFunc("GET /api/music/playlists/{id}/tracks", s.listTracks)
	s.mux.HandleFunc("POST /api/music/playlists/{id}/tracks", s.addTrack)
	s.mux.HandleFunc("DELETE /api/music/tracks/{tid}", s.removeTrack)

	// Save/state sync: identical routes for both kinds. The frontend uploads to
	// the top-level /api/{saves,states}?rom_id= and lists the same way; the
	// per-rom routes are kept for API clients.
	for _, kind := range []string{"saves", "states"} {
		k := kind
		s.mux.HandleFunc("GET /api/roms/{id}/"+k, func(w http.ResponseWriter, r *http.Request) { s.listAssets(w, r, k) })
		s.mux.HandleFunc("POST /api/roms/{id}/"+k, func(w http.ResponseWriter, r *http.Request) { s.uploadAsset(w, r, k) })
		s.mux.HandleFunc("GET /api/"+k, func(w http.ResponseWriter, r *http.Request) { s.listAssets(w, r, k) })
		s.mux.HandleFunc("POST /api/"+k, func(w http.ResponseWriter, r *http.Request) { s.uploadAsset(w, r, k) })
		s.mux.HandleFunc("GET /api/"+k+"/{id}/content", func(w http.ResponseWriter, r *http.Request) { s.assetContent(w, r, k) })
		s.mux.HandleFunc("DELETE /api/"+k+"/{id}", func(w http.ResponseWriter, r *http.Request) { s.deleteAsset(w, r, k) })
		s.mux.HandleFunc("POST /api/"+k+"/delete", func(w http.ResponseWriter, r *http.Request) { s.bulkDeleteAssets(w, r, k) })
		for _, f := range []string{"visibility", "favorite", "file-name", "labels"} {
			field := f
			s.mux.HandleFunc("PUT /api/"+k+"/{id}/"+field, func(w http.ResponseWriter, r *http.Request) { s.editAsset(w, r, k, field) })
		}
	}
	// Presence heartbeat (no persistence needed in a single-user setup).
	s.mux.HandleFunc("GET /api/activity", s.activityEntries)
	s.mux.HandleFunc("GET /api/activity/rom/{id}", s.activityEntries)
	s.mux.HandleFunc("POST /api/activity/heartbeat", s.activityHeartbeat)
	s.mux.HandleFunc("DELETE /api/activity/heartbeat", s.activityHeartbeat)

	// Static resources (covers, screenshots, manuals) served from disk. A guard
	// refuses sensitive files (Switch prod.keys may live under resources/switch/
	// per ROMM_SWITCH_KEYS) so they can never be fetched over HTTP.
	if s.resourcesRoot != "" {
		fsrv := http.StripPrefix("/assets/romm/resources/", http.FileServer(http.Dir(s.resourcesRoot)))
		s.mux.Handle("GET /assets/romm/resources/", guardSensitiveFiles(fsrv))
	}

	s.mux.Handle("GET /", s.spaHandler())
}

// guardSensitiveFiles blocks HTTP access to secret files that may sit in the
// resources tree (Switch prod.keys and any *.keys), returning 404 before the
// file server can read them.
func guardSensitiveFiles(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lower := strings.ToLower(path.Clean(r.URL.Path))
		if strings.HasSuffix(lower, ".keys") || strings.HasSuffix(lower, "prod.keys") ||
			strings.Contains(lower, "/resources/switch/") {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// spaHandler serves static files, falling back to index.html for unknown paths
// so the Vue client router handles deep links (e.g. /rom/35950).
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServer(http.FS(s.static))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := s.static.Open(p); err == nil {
			_ = f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path with no file extension -> SPA route: serve index.html.
		if idx, err := s.static.Open("index.html"); err == nil {
			defer idx.Close()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.Copy(w, idx)
			return
		}
		http.NotFound(w, r)
	})
}

func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	counts, err := s.st.Counts(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "db unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"SYSTEM":  map[string]any{"VERSION": Version, "BACKEND": "go"},
		"LIBRARY": counts,
	})
}

func (s *Server) listPlatforms(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	platforms, err := s.st.ListPlatforms(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// The frontend expects a bare array (list[PlatformSchema]).
	if platforms == nil {
		platforms = []store.Platform{}
	}
	writeJSON(w, http.StatusOK, platforms)
}

func (s *Server) listRoms(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	q := r.URL.Query()
	uid, _ := s.st.UserIDByUsername(ctx, claims.Username)
	filter := store.RomFilter{
		PlatformIDs:    parseIDList(q.Get("platform_ids")),
		ExcludeVirtual: q.Get("virtual") == "false",
		Search:         q.Get("search_term"),
		GroupByMetaID:  q.Get("group_by_meta_id") == "true",
		UserID:         uid,
	}
	if s.configFile != nil {
		filter.RegionPriority = s.configFile.Scan.Priority.Region
	}
	// Back-compat singular param.
	if pid := int64(atoiDefault(q.Get("platform_id"), 0)); pid > 0 {
		filter.PlatformIDs = append(filter.PlatformIDs, pid)
	}
	orderBy, orderDir := q.Get("order_by"), q.Get("order_dir")
	limit := atoiDefault(q.Get("limit"), 72)
	offset := atoiDefault(q.Get("offset"), 0)
	roms, err := s.st.ListRomsFull(ctx, filter, orderBy, orderDir, limit, offset)
	if err != nil {
		log.Printf("listRoms: %v", err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	items := make([]map[string]any, 0, len(roms))
	for i := range roms {
		items = append(items, romJSON(&roms[i], nil, uid, false, nil))
	}
	resp := map[string]any{"items": items, "limit": limit, "offset": offset}

	// Sidecars default to ON (the frontend omits the flag unless disabling it).
	if q.Get("with_total") != "false" {
		if total, err := s.st.CountRoms(ctx, filter); err == nil {
			resp["total"] = total
		}
	}
	if q.Get("with_rom_id_index") != "false" || q.Get("with_char_index") != "false" {
		if ids, charIdx, err := s.st.RomIDIndex(ctx, filter, orderBy, orderDir); err == nil {
			if q.Get("with_rom_id_index") != "false" {
				resp["rom_id_index"] = ids
			}
			if q.Get("with_char_index") != "false" {
				resp["char_index"] = charIdx
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseIDList parses a comma-separated id list ("4,7,9") into []int64.
func parseIDList(s string) []int64 {
	if s == "" {
		return nil
	}
	var out []int64
	for _, part := range strings.Split(s, ",") {
		if n := int64(atoiDefault(strings.TrimSpace(part), 0)); n > 0 {
			out = append(out, n)
		}
	}
	return out
}

// getRomContent streams a rom's file (range-capable, for downloads and the
// in-browser players). Bearer-protected — it serves the user's library.
func (s *Server) getRomContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.libraryRoot == "" {
		writeErr(w, http.StatusServiceUnavailable, "library root not configured")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	fsPath, fsName, found, err := s.st.GetRomLocation(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}
	// Resolve and confine to the library root (defence against traversal in the
	// stored path).
	full := filepath.Join(s.libraryRoot, filepath.FromSlash(fsPath), fsName)
	clean := filepath.Clean(full)
	if clean != s.libraryRoot && !strings.HasPrefix(clean, s.libraryRoot+string(os.PathSeparator)) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	f, err := os.Open(clean)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file missing")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "file missing")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(fsName))
	http.ServeContent(w, r, fsName, st.ModTime(), f) // handles Range + If-Modified-Since
}

// getAchievements returns a user's RetroAchievements progress for a rom.
// ?user=<ra_username> is required.
func (s *Server) getAchievements(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.ach == nil {
		writeErr(w, http.StatusServiceUnavailable, "retroachievements not configured")
		return
	}
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	if user == "" {
		writeErr(w, http.StatusBadRequest, "user required")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	raID, found, err := s.st.GetRomRAID(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "rom has no RetroAchievements id")
		return
	}
	prog, err := s.ach.UserGameProgress(ctx, raID, user)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "retroachievements error")
		return
	}
	writeJSON(w, http.StatusOK, prog)
}

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	cols, err := s.st.ListCollections(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	// The frontend expects a bare array (list[CollectionSchema]).
	if cols == nil {
		cols = []store.Collection{}
	}
	writeJSON(w, http.StatusOK, cols)
}

func (s *Server) getRom(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rom, err := s.st.GetRomFull(ctx, id)
	if err != nil {
		log.Printf("getRomFull(%d): %v", id, err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if rom == nil {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}
	uid, _ := s.st.UserIDByUsername(ctx, claims.Username)
	var ru *store.RomUser
	if uid > 0 {
		ru, _ = s.st.GetRomUser(ctx, uid, id)
	}
	// Mirror URLs feed source_urls (every host a virtual rom can be fetched from).
	if rom.IsVirtual {
		rom.Mirrors, _ = s.st.RomMirrorURLs(ctx, id)
	}
	// Detail extras: files (Files tab), siblings (versions), provider metadata
	// (Metadata + Media tabs). Each is best-effort — a failed load leaves that
	// section empty rather than failing the whole page.
	extras := &romExtras{}
	if s.configFile != nil {
		extras.HostOrder = s.configFile.Downloads.DefaultHostOrder
	}
	extras.Files, _ = s.st.ListRomFiles(ctx, id)
	extras.Siblings, _ = s.st.ListSiblings(ctx, id, uid)
	extras.Meta, _ = s.st.GetRomMetadata(ctx, id)
	extras.Collections, _ = s.st.RomCollections(ctx, id)
	if uid > 0 {
		extras.Saves, _ = s.st.ListAssets(ctx, "saves", id, uid)
		extras.States, _ = s.st.ListAssets(ctx, "states", id, uid)
		extras.Screenshots, _ = s.st.ListScreenshots(ctx, id, uid)
	}
	writeJSON(w, http.StatusOK, romJSON(rom, ru, uid, true, extras))
}

func (s *Server) setRomProps(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	var props store.RomUserProps
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&props); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "user lookup failed")
		return
	}
	if uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return
	}
	n, err := s.st.SetRomUserProps(ctx, uid, id, props)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"updated": n})
}

// triggerScan enqueues a background library scan. ?hash=0 skips hashing for a
// fast inventory pass. Requires a valid bearer token.
// handleScanEvent runs a library scan requested over the socket and emits the
// scan:* progress/completion events the frontend watches. Enqueued on the
// worker so a long scan doesn't block, exactly like POST /api/scan.
func (s *Server) handleScanEvent(args []json.RawMessage) {
	if s.queue == nil || s.scanRoot == "" {
		s.sockets.Emit("scan:done_ko", map[string]any{"message": "scanning not configured"})
		return
	}
	root := s.scanRoot
	s.queue.Enqueue("library-scan", func(ctx context.Context) error {
		s.sockets.Emit("scan:scanning_platform", map[string]any{})
		dirs, err := s.st.ListPlatformDirs(ctx)
		if err != nil {
			s.sockets.Emit("scan:done_ko", map[string]any{"message": err.Error()})
			return err
		}
		if _, err := scan.Platforms(ctx, root, dirs, s.st, true); err != nil {
			s.sockets.Emit("scan:done_ko", map[string]any{"message": err.Error()})
			return err
		}
		// Identify still-unmatched roms by file hash via Hasheous (bounded).
		s.identifyByHash(ctx)
		s.sockets.Emit("scan:done", map[string]any{})
		return nil
	})
}

// identifyByHash runs a bounded Hasheous pass over unidentified, hashed roms,
// recording the IGDB/TheGamesDB ids it resolves. Best-effort: an unreachable
// Hasheous or a single failed lookup never aborts the scan. No-op when Hasheous
// is disabled. It only touches roms with no id yet, so it never re-queries the
// already-identified library.
func (s *Server) identifyByHash(ctx context.Context) {
	if s.hasheous == nil {
		return
	}
	cands, err := s.st.UnidentifiedHashedRoms(ctx, 500)
	if err != nil || len(cands) == 0 {
		return
	}
	matched := 0
	for _, c := range cands {
		if ctx.Err() != nil {
			return
		}
		m, err := s.hasheous.LookupByHash(ctx, c.MD5, c.SHA1, c.CRC)
		if err != nil || m == nil {
			continue
		}
		if err := s.st.SetHashMatchIDs(ctx, c.ID, m.IgdbID, m.HasheousID, m.TgdbID); err == nil {
			matched++
		}
	}
	if matched > 0 {
		log.Printf("hasheous: identified %d/%d unmatched roms by hash", matched, len(cands))
	}
}

func (s *Server) triggerScan(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.queue == nil || s.scanRoot == "" {
		writeErr(w, http.StatusServiceUnavailable, "scanning not configured")
		return
	}
	doHash := r.URL.Query().Get("hash") != "0"
	root := s.scanRoot
	id := s.queue.Enqueue("library-scan", func(ctx context.Context) error {
		dirs, err := s.st.ListPlatformDirs(ctx)
		if err != nil {
			return err
		}
		_, err = scan.Platforms(ctx, root, dirs, s.st, doHash)
		return err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

func (s *Server) listScans(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": s.queue.List()})
}

func (s *Server) scanStatus(w http.ResponseWriter, r *http.Request) {
	if s.queue == nil {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	job, ok := s.queue.Get(id)
	if !ok {
		writeErr(w, http.StatusNotFound, "no such job")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

// searchMetadata searches the configured provider by name. Requires auth
// (it hits a rate-limited external API).
func (s *Server) searchMetadata(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.meta == nil {
		writeErr(w, http.StatusServiceUnavailable, "no metadata provider configured")
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeErr(w, http.StatusBadRequest, "q required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	games, err := s.meta.Search(ctx, q)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "provider error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider": s.meta.Name(), "items": games})
}

func (s *Server) virtualCheck(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.virt == nil {
		writeErr(w, http.StatusServiceUnavailable, "merge service not configured")
		return
	}
	var body struct {
		URLs []string `json:"urls"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || len(body.URLs) == 0 {
		writeErr(w, http.StatusBadRequest, "urls required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	res, err := s.virt.Check(ctx, body.URLs)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "merge service error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

func (s *Server) virtualDispatch(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.virt == nil {
		writeErr(w, http.StatusServiceUnavailable, "merge service not configured")
		return
	}
	var job virtual.Job
	var body struct {
		System     string         `json:"system"`
		Name       string         `json:"name"`
		Links      []virtual.Link `json:"links"`
		RomID      int64          `json:"rom_id"`
		PlatformID int64          `json:"platform_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&body); err != nil ||
		body.System == "" || body.Name == "" || len(body.Links) == 0 {
		writeErr(w, http.StatusBadRequest, "system, name and links required")
		return
	}
	job = virtual.Job{
		System: body.System, Name: body.Name, Links: body.Links,
		RomID: body.RomID, PlatformID: body.PlatformID,
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	id, err := s.virt.Dispatch(ctx, job)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "dispatch failed")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"job_id": id})
}

// authUser extracts and verifies the Bearer access token, if any.
func (s *Server) authUser(r *http.Request) (*auth.Claims, bool) {
	// DEV ONLY: ROMM_DEV_NO_AUTH makes every request an admin (no login needed),
	// so frequent rebuilds during development don't bounce the browser to login.
	// Never enable on an internet-facing instance.
	if s.devUser != "" {
		return &auth.Claims{Username: s.devUser, Scopes: auth.ScopesForRole("admin")}, true
	}
	// Bearer JWT (API clients), else the web-UI session cookie.
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		if c, err := auth.Verify(strings.TrimPrefix(h, "Bearer "), s.secret); err == nil {
			return c, true
		}
	}
	return s.sessionClaims(r)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

func atoiDefault(s string, def int) int {
	if s == "" {
		return def
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	return n
}

func logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start).Round(time.Millisecond))
	})
}
