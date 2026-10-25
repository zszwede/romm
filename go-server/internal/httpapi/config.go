package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/config"
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

// getConfig serves GET /api/config from the parsed RomM config.yml (exclusion
// lists, scan priorities, EmulatorJS + converto settings, platform bindings).
// When no config file is wired, an empty ConfigData still produces the full
// response shape with CONFIG_FILE_MOUNTED=false so the frontend shows its
// "config not mounted" banner rather than erroring on a missing key.
func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	cd := s.configFile
	if cd == nil {
		cd = &config.ConfigData{}
	}
	_, authed := s.authUser(r)
	writeJSON(w, http.StatusOK, cd.APIResponse(authed))
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
	ss := s.ss != nil
	ssDevCreds := false
	if c, ok := s.ss.(interface{ HasDevCredentials() bool }); ok {
		ssDevCreds = c.HasDevCredentials()
	}
	hasheous := s.hasheous != nil
	libretro := s.libretro != nil
	oidcEnabled := s.oidc != nil

	return map[string]any{
		"SYSTEM": map[string]any{
			"VERSION":           Version,
			"GIT_BRANCH":        "golang-rewrite",
			"SHOW_SETUP_WIZARD": false,
		},
		"METADATA_SOURCES": map[string]any{
			"ANY_SOURCE_ENABLED":      igdb || ra || ss || hasheous || libretro,
			"IGDB_API_ENABLED":        igdb,
			"SS_API_ENABLED":          ss,
			"SS_DEV_CREDENTIALS_SET":  ssDevCreds,
			"MOBY_API_ENABLED":        false,
			"STEAMGRIDDB_API_ENABLED": false,
			"RA_API_ENABLED":          ra,
			"LAUNCHBOX_API_ENABLED":   false,
			"HASHEOUS_API_ENABLED":    hasheous,
			"PLAYMATCH_API_ENABLED":   false,
			"TGDB_API_ENABLED":        false,
			"FLASHPOINT_API_ENABLED":  false,
			"HLTB_API_ENABLED":        false,
			"DEMOZOO_API_ENABLED":     false,
			"POUET_API_ENABLED":       false,
			"CSDB_API_ENABLED":        false,
			"STEAM_API_ENABLED":       false,
			"LIBRETRO_API_ENABLED":    libretro,
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
