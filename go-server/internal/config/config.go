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
}

// FromEnv reads the configuration, matching the container's env var names.
func FromEnv() Config {
	return Config{
		Addr:             env("ROMM_GO_ADDR", ":8088"),
		DBHost:           env("DB_HOST", "127.0.0.1"),
		DBPort:           env("DB_PORT", "3306"),
		DBUser:           env("DB_USER", "romm-user"),
		DBPass:           os.Getenv("DB_PASSWD"),
		DBName:           env("DB_NAME", "romm"),
		BasePath:         os.Getenv("ROMM_BASE_PATH"),
		AuthSecret:       os.Getenv("ROMM_AUTH_SECRET_KEY"),
		IGDBClientID:     os.Getenv("IGDB_CLIENT_ID"),
		IGDBClientSecret: os.Getenv("IGDB_CLIENT_SECRET"),
	}
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
