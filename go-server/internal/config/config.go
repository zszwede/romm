// Package config loads the server configuration from the environment, using
// the same variable names the Python RomM container uses so the Go server can
// be pointed at the existing stack (DB on pve2, the ES-DE library paths) with
// no new configuration.
package config

import (
	"os"
	"path/filepath"
	"strings"
)

// defaultConfigFile is BasePath/config/config.yml when BasePath is set, else "".
func defaultConfigFile(basePath string) string {
	if basePath == "" {
		return ""
	}
	return filepath.Join(basePath, "config", "config.yml")
}

// defaultExternalBins is an "external-bins" folder beside the running executable
// (where RomM's bundled tools like switch-tool live); falls back to "./external-bins".
func defaultExternalBins() string {
	if exe, err := os.Executable(); err == nil {
		return filepath.Join(filepath.Dir(exe), "external-bins")
	}
	return "external-bins"
}

// defaultSwitchKeys is <BasePath>/resources/switch/prod.keys (the app resources
// folder), else "" when BasePath is unset.
func defaultSwitchKeys(basePath string) string {
	if basePath == "" {
		return ""
	}
	return filepath.Join(basePath, "resources", "switch", "prod.keys")
}

type Config struct {
	Addr     string // listen address, e.g. ":8088"
	DBHost   string
	DBPort   string
	DBUser   string
	DBPass   string
	DBName   string
	BasePath string // ROMM_BASE_PATH: the state dir (library/, resources/, config/)
	// ConfigFile is the path to RomM's config.yml (exclusions, scan priorities,
	// EmulatorJS + converto settings, platform bindings). Defaults to
	// BasePath/config/config.yml; set ROMM_CONFIG_FILE when the file is reached
	// at a different host path than the container sees (the ES-DE config dir is a
	// bind — e.g. the host reads /home/xiam/romm/config/config.yml). A missing
	// file is reported to the UI as "not mounted".
	ConfigFile string
	// AuthSecret is ROMM_AUTH_SECRET_KEY: the HS256 HMAC key the Python app signs
	// access tokens with. Reused verbatim so tokens issued by the existing
	// backend verify here unchanged.
	AuthSecret string

	IGDBClientID     string
	IGDBClientSecret string
	RAAPIKey         string // RETROACHIEVEMENTS_API_KEY

	// ScreenScraper credentials (this install's primary metadata source).
	// SSDevID/SSDevPassword are the software (dev) credentials RomM registers as
	// "romm"; SSUser/SSPassword are the user account that raises the scrape quota.
	SSDevID       string // SCREENSCRAPER_DEV_ID
	SSDevPassword string // SCREENSCRAPER_DEV_PASSWORD
	SSUser        string // SCREENSCRAPER_USER
	SSPassword    string // SCREENSCRAPER_PASSWORD

	// Hasheous (hash identification) and Libretro (box-art covers) are public,
	// keyless sources, enabled by default like the Python app. Set the matching
	// *_API_ENABLED env to a falsey value to turn one off.
	HasheousEnabled bool
	LibretroEnabled bool

	// MergeService* point at the RommDownloader merge service for the
	// virtual-download workflow. (The Python app keeps these in its Downloads
	// config; the Go server reads them from the environment.)
	MergeServiceURL   string
	MergeServiceToken string

	// WebRoot: directory with the built frontend (Vue dist + player assets).
	// Empty => serve the embedded placeholder shell.
	WebRoot string

	// LibraryRomsDir: the directory the roms' fs_path is relative to (its parent,
	// since fs_path is like "roms/<platform>"). Defaults to BASE/library. Set
	// ROMM_LIBRARY_ROMS_DIR to the ES-DE roms root when the library is served
	// through an overlay the container sees but the host does not (e.g.
	// /media/8TB/Retro/roms). The scanner + content serving both use it.
	LibraryRomsDir string

	// DownloaderConfig: path to a RommDownloader config.yaml. When set, the
	// virtual-download pipeline runs IN-PROCESS (absorbed). When empty, the
	// server dispatches to an external merge service if MergeServiceURL is set.
	DownloaderConfig string

	// ExternalBins is the folder holding RomM's bundled external tool binaries
	// (the Switch switch-tool lives here as `switch-tool`). The in-process
	// downloader points its Switch steps at it, overriding the container paths in
	// config.yaml. Defaults to an `external-bins` dir beside the executable; set
	// ROMM_EXTERNAL_BINS to relocate it.
	ExternalBins string
	// SwitchKeys is the path to the Switch prod.keys. Defaults to
	// <BASE_PATH>/resources/switch/prod.keys (the app resources folder); set
	// ROMM_SWITCH_KEYS to point elsewhere.
	SwitchKeys string
	// DownloaderWorkDir / DownloaderStateDir override the config.yaml's work_dir
	// and state_dir (which are container paths like /data/rommdl/*). Set
	// ROMM_DOWNLOADER_WORK_DIR / ROMM_DOWNLOADER_STATE_DIR to host-writable dirs.
	DownloaderWorkDir  string
	DownloaderStateDir string

	// OIDC config (OIDC_* env vars). Login endpoints activate when Enabled.
	OIDC OIDCConfig
}

// OIDCConfig mirrors the backend's OIDC_* environment variables.
type OIDCConfig struct {
	Enabled           bool
	AllowRegistration bool
	ClientID          string
	ClientSecret      string
	RedirectURI       string
	MetadataURL       string
	ProviderURL       string
	UsernameAttribute string
	ClaimRoles        string
	RoleViewer        []string
	RoleEditor        []string
	RoleAdmin         []string
}

// FromEnv reads the configuration, matching the container's env var names.
func FromEnv() Config {
	return Config{
		Addr:              env("ROMM_GO_ADDR", ":8088"),
		DBHost:            env("DB_HOST", "127.0.0.1"),
		DBPort:            env("DB_PORT", "3306"),
		DBUser:            env("DB_USER", "romm-user"),
		DBPass:            os.Getenv("DB_PASSWD"),
		DBName:            env("DB_NAME", "romm"),
		BasePath:          os.Getenv("ROMM_BASE_PATH"),
		ConfigFile:        env("ROMM_CONFIG_FILE", defaultConfigFile(os.Getenv("ROMM_BASE_PATH"))),
		AuthSecret:        os.Getenv("ROMM_AUTH_SECRET_KEY"),
		IGDBClientID:      os.Getenv("IGDB_CLIENT_ID"),
		IGDBClientSecret:  os.Getenv("IGDB_CLIENT_SECRET"),
		RAAPIKey:          os.Getenv("RETROACHIEVEMENTS_API_KEY"),
		SSDevID:           os.Getenv("SCREENSCRAPER_DEV_ID"),
		SSDevPassword:     os.Getenv("SCREENSCRAPER_DEV_PASSWORD"),
		SSUser:            os.Getenv("SCREENSCRAPER_USER"),
		SSPassword:        os.Getenv("SCREENSCRAPER_PASSWORD"),
		HasheousEnabled:   boolEnvDefault("HASHEOUS_API_ENABLED", true),
		LibretroEnabled:   boolEnvDefault("LIBRETRO_API_ENABLED", true),
		MergeServiceURL:   os.Getenv("MERGE_SERVICE_URL"),
		MergeServiceToken: os.Getenv("MERGE_SERVICE_TOKEN"),
		WebRoot:           os.Getenv("ROMM_WEB_ROOT"),
		LibraryRomsDir:    os.Getenv("ROMM_LIBRARY_ROMS_DIR"),
		DownloaderConfig:  os.Getenv("ROMM_DOWNLOADER_CONFIG"),
		ExternalBins:       env("ROMM_EXTERNAL_BINS", defaultExternalBins()),
		SwitchKeys:         env("ROMM_SWITCH_KEYS", defaultSwitchKeys(os.Getenv("ROMM_BASE_PATH"))),
		DownloaderWorkDir:  os.Getenv("ROMM_DOWNLOADER_WORK_DIR"),
		DownloaderStateDir: os.Getenv("ROMM_DOWNLOADER_STATE_DIR"),
		OIDC: OIDCConfig{
			Enabled:           boolEnv("OIDC_ENABLED"),
			AllowRegistration: boolEnvDefault("OIDC_ALLOW_REGISTRATION", true),
			ClientID:          os.Getenv("OIDC_CLIENT_ID"),
			ClientSecret:      os.Getenv("OIDC_CLIENT_SECRET"),
			RedirectURI:       os.Getenv("OIDC_REDIRECT_URI"),
			MetadataURL:       os.Getenv("OIDC_SERVER_METADATA_URL"),
			ProviderURL:       os.Getenv("OIDC_SERVER_APPLICATION_URL"),
			UsernameAttribute: env("OIDC_USERNAME_ATTRIBUTE", "preferred_username"),
			ClaimRoles:        os.Getenv("OIDC_CLAIM_ROLES"),
			RoleViewer:        setEnv("OIDC_ROLE_VIEWER"),
			RoleEditor:        setEnv("OIDC_ROLE_EDITOR"),
			RoleAdmin:         setEnv("OIDC_ROLE_ADMIN"),
		},
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func boolEnv(key string) bool { return boolEnvDefault(key, false) }

func boolEnvDefault(key string, def bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if raw == "" {
		return def
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// setEnv parses a comma/space-separated env var into a slice (matching the
// backend's _get_env_set).
func setEnv(key string) []string {
	raw := os.Getenv(key)
	if raw == "" {
		return nil
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}
