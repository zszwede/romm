package httpapi

import (
	"context"
	"net/http"
	"time"
)

// getPermissions serves GET /api/permissions/me. For the single admin user this
// install has, the frontend short-circuits on is_admin, so the grant list need
// not be enumerated.
func (s *Server) getPermissions(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	isAdmin := claims.HasScope("users.write") // admins hold the full set
	writeJSON(w, http.StatusOK, map[string]any{
		"is_admin": isAdmin,
		"grants":   []any{},
		"hidden":   map[string]any{"platforms": []any{}, "roms": []any{}},
	})
}

// getConfig serves GET /api/config with safe home-lab defaults for every key the
// ConfigResponse carries. Scan/exclusion lists are empty (nothing filtered);
// EmulatorJS is on with no per-core overrides.
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"CONFIG_FILE_MOUNTED":                false,
		"CONFIG_FILE_WRITABLE":               false,
		"CONFIG_FILE_PARSE_ERROR":            nil,
		"EXCLUDED_PLATFORMS":                 []string{},
		"EXCLUDED_SINGLE_EXT":                []string{},
		"EXCLUDED_SINGLE_FILES":              []string{},
		"EXCLUDED_MULTI_FILES":               []string{},
		"EXCLUDED_MULTI_PARTS_EXT":           []string{},
		"EXCLUDED_MULTI_PARTS_FILES":         []string{},
		"DEFAULT_EXCLUDED_PLATFORM_DIRS":     []string{},
		"DEFAULT_EXCLUDED_MULTI_FILE_DIRS":   []string{},
		"DEFAULT_EXCLUDED_FILES":             []string{},
		"DEFAULT_EXCLUDED_EXTENSIONS":        []string{},
		"PLATFORMS_BINDING":                  map[string]string{},
		"PLATFORMS_VERSIONS":                 map[string]string{},
		"SKIP_HASH_CALCULATION":              false,
		"EJS_DEBUG":                          false,
		"EJS_CACHE_LIMIT":                    nil,
		"EJS_DISABLE_AUTO_UNLOAD":            false,
		"EJS_DISABLE_BATCH_BOOTUP":           false,
		"EJS_ENABLE_AUTO_SAVE_SYNC":          true,
		"EJS_NETPLAY_ENABLED":                false,
		"EJS_NETPLAY_ICE_SERVERS":            []any{},
		"EJS_DEFAULT_CORES":                  map[string]string{},
		"EJS_SETTINGS":                       map[string]any{},
		"EJS_CONTROLS":                       map[string]any{},
		"SCAN_METADATA_PRIORITY":             []string{},
		"SCAN_ARTWORK_PRIORITY":              []string{},
		"SCAN_ARTWORK_PRIORITY_OVERRIDES":    map[string]any{},
		"SCAN_REGION_PRIORITY":               []string{},
		"SCAN_LANGUAGE_PRIORITY":             []string{},
		"SCAN_MEDIA":                         []string{},
		"GAMELIST_AUTO_EXPORT_ON_SCAN":       false,
		"GAMELIST_MEDIA_THUMBNAIL":           "",
		"GAMELIST_MEDIA_IMAGE":               "",
		"PEGASUS_AUTO_EXPORT_ON_SCAN":        false,
		"CONVERTO":                           map[string]any{},
		"CONVERTO_LIBRARY_TARGETS":           map[string]any{},
	})
}

// heartbeatBody builds the full HeartbeatResponse shape the frontend reads on
// load. Capability flags reflect what this server actually has wired.
func (s *Server) heartbeatBody(ctx context.Context) map[string]any {
	fsPlatforms := []string{}
	if dirs, err := s.st.ListPlatformDirs(ctx); err == nil {
		for _, d := range dirs {
			fsPlatforms = append(fsPlatforms, d.FsSlug)
		}
	}
	igdb := s.meta != nil
	ra := s.ach != nil
	oidcEnabled := s.oidc != nil

	return map[string]any{
		"SYSTEM": map[string]any{
			"VERSION":           Version,
			"GIT_BRANCH":        "golang-rewrite",
			"SHOW_SETUP_WIZARD": false,
		},
		"METADATA_SOURCES": map[string]any{
			"ANY_SOURCE_ENABLED":      igdb || ra,
			"IGDB_API_ENABLED":        igdb,
			"SS_API_ENABLED":          false,
			"SS_DEV_CREDENTIALS_SET":  false,
			"MOBY_API_ENABLED":        false,
			"STEAMGRIDDB_API_ENABLED": false,
			"RA_API_ENABLED":          ra,
			"LAUNCHBOX_API_ENABLED":   false,
			"HASHEOUS_API_ENABLED":    false,
			"PLAYMATCH_API_ENABLED":   false,
			"TGDB_API_ENABLED":        false,
			"FLASHPOINT_API_ENABLED":  false,
			"HLTB_API_ENABLED":        false,
			"DEMOZOO_API_ENABLED":     false,
			"POUET_API_ENABLED":       false,
			"CSDB_API_ENABLED":        false,
			"STEAM_API_ENABLED":       false,
			"LIBRETRO_API_ENABLED":    false,
		},
		"FILESYSTEM": map[string]any{
			"FS_PLATFORMS":                 fsPlatforms,
			"TITLE_ID_EXTRACTION_ENABLED":  false,
		},
		"EMULATION": map[string]any{
			"DISABLE_EMULATOR_JS": false,
			"DISABLE_RUFFLE_RS":   false,
			"DISABLE_JSDOS":       false,
			"DISABLE_PICO8":       false,
			"DISABLE_EASYRPG":     false,
		},
		"FRONTEND": map[string]any{
			"DISABLE_USERPASS_LOGIN": false,
			"DISABLE_LOGS_VIEWER":    false,
			"YOUTUBE_BASE_URL":       "https://www.youtube.com",
		},
		"OIDC": map[string]any{
			"ENABLED":             oidcEnabled,
			"AUTOLOGIN":           false,
			"PROVIDER":            "",
			"RP_INITIATED_LOGOUT": false,
		},
		"NOTIFICATIONS": map[string]any{
			"EMAIL_ENABLED":       false,
			"EMAILS_RESET_LINKS":  false,
		},
		"DEVICE_INSTALL": map[string]any{
			"ENABLED":                  false,
			"EXCLUDED_PLATFORM_SLUGS":  []string{},
		},
		"CONVERTO": map[string]any{"ENABLED": false},
		"TASKS": map[string]any{
			"ENABLE_SCHEDULED_RESCAN":                      false,
			"SCHEDULED_RESCAN_CRON":                        "",
			"ENABLE_SCHEDULED_UPDATE_SWITCH_TITLEDB":       false,
			"SCHEDULED_UPDATE_SWITCH_TITLEDB_CRON":         "",
			"ENABLE_SCHEDULED_UPDATE_LAUNCHBOX_METADATA":   false,
			"SCHEDULED_UPDATE_LAUNCHBOX_METADATA_CRON":     "",
			"ENABLE_SCHEDULED_CONVERT_IMAGES_TO_WEBP":      false,
			"SCHEDULED_CONVERT_IMAGES_TO_WEBP_CRON":        "",
		},
	}
}

func (s *Server) heartbeatFull(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.heartbeatBody(ctx))
}
