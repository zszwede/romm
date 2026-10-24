// Package config loads the server configuration from the environment, using
// the same variable names the Python RomM container uses so the Go server can
// be pointed at the existing stack (DB on pve2, the ES-DE library paths) with
// no new configuration.
package config

import "os"

type Config struct {
	Addr     string // listen address, e.g. ":8088"
	DBHost   string
	DBPort   string
	DBUser   string
	DBPass   string
	DBName   string
	BasePath string // ROMM_BASE_PATH: the state dir (library/, resources/, config/)
	// AuthSecret is ROMM_AUTH_SECRET_KEY: the HS256 HMAC key the Python app signs
	// access tokens with. Reused verbatim so tokens issued by the existing
	// backend verify here unchanged.
	AuthSecret string

	IGDBClientID     string
	IGDBClientSecret string
	RAAPIKey         string // RETROACHIEVEMENTS_API_KEY

	// MergeService* point at the RommDownloader merge service for the
	// virtual-download workflow. (The Python app keeps these in its Downloads
	// config; the Go server reads them from the environment.)
	MergeServiceURL   string
	MergeServiceToken string

	// WebRoot: directory with the built frontend (Vue dist + player assets).
	// Empty => serve the embedded placeholder shell.
	WebRoot string

	// DownloaderConfig: path to a RommDownloader config.yaml. When set, the
	// virtual-download pipeline runs IN-PROCESS (absorbed). When empty, the
	// server dispatches to an external merge service if MergeServiceURL is set.
	DownloaderConfig string
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
		AuthSecret:        os.Getenv("ROMM_AUTH_SECRET_KEY"),
		IGDBClientID:      os.Getenv("IGDB_CLIENT_ID"),
		IGDBClientSecret:  os.Getenv("IGDB_CLIENT_SECRET"),
		RAAPIKey:          os.Getenv("RETROACHIEVEMENTS_API_KEY"),
		MergeServiceURL:   os.Getenv("MERGE_SERVICE_URL"),
		MergeServiceToken: os.Getenv("MERGE_SERVICE_TOKEN"),
		WebRoot:           os.Getenv("ROMM_WEB_ROOT"),
		DownloaderConfig:  os.Getenv("ROMM_DOWNLOADER_CONFIG"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
