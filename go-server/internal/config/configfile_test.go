package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleYAML = `
converto:
  cache_max_size_gb: 20
  cache_ttl_hours: 24
  download_conversion_enabled: false
  scan_metadata: true
emulatorjs:
  auto_save_sync: true
  debug: false
  netplay:
    enabled: false
    ice_servers: []
exclude:
  platforms:
    - amiga500
    - "$RECYCLE.BIN"
  roms:
    multi_file:
      names: ["Disc A.zip"]
      parts:
        extensions: ["tmp", "log"]
        names: [".DS_Store"]
    single_file:
      extensions: ["bak"]
      names: ["desktop.ini"]
filesystem:
  skip_hash_calculation: true
scan:
  gamelist:
    export: false
    media:
      image: screenshot
      thumbnail: box2d
  media: ["box2d", "screenshot"]
  pegasus:
    export: false
  priority:
    artwork: ["gamelist", "ss", "igdb"]
    cover: ["gamelist", "ss"]
    language: ["en"]
    metadata: ["gamelist", "igdb"]
    region: ["wor", "eu", "us", "ss"]
system:
  platforms:
    amiga500: amiga
  versions: {}
`

func TestLoadConfigFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yml")
	if err := os.WriteFile(path, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	cd := LoadConfigFile(path)
	if !cd.Mounted {
		t.Fatal("expected Mounted")
	}
	if cd.ParseError != "" {
		t.Fatalf("parse error: %s", cd.ParseError)
	}
	resp := cd.APIResponse(true)

	if resp["CONFIG_FILE_MOUNTED"] != true {
		t.Error("CONFIG_FILE_MOUNTED should be true")
	}
	if got := resp["EXCLUDED_PLATFORMS"].([]string); len(got) != 2 || got[0] != "amiga500" {
		t.Errorf("EXCLUDED_PLATFORMS = %v", got)
	}
	if got := resp["EXCLUDED_SINGLE_EXT"].([]string); len(got) != 1 || got[0] != "bak" {
		t.Errorf("EXCLUDED_SINGLE_EXT = %v", got)
	}
	if got := resp["EXCLUDED_MULTI_PARTS_FILES"].([]string); len(got) != 1 || got[0] != ".DS_Store" {
		t.Errorf("EXCLUDED_MULTI_PARTS_FILES = %v", got)
	}
	if resp["SKIP_HASH_CALCULATION"] != true {
		t.Error("SKIP_HASH_CALCULATION should be true")
	}
	if got := resp["SCAN_METADATA_PRIORITY"].([]string); len(got) != 2 || got[1] != "igdb" {
		t.Errorf("SCAN_METADATA_PRIORITY = %v", got)
	}
	// SCAN_ARTWORK_PRIORITY_OVERRIDES keys the cover list under url_cover.
	ov := resp["SCAN_ARTWORK_PRIORITY_OVERRIDES"].(map[string]any)
	if cover, ok := ov["url_cover"].([]string); !ok || len(cover) != 2 {
		t.Errorf("url_cover override = %v", ov["url_cover"])
	}
	if resp["GAMELIST_MEDIA_THUMBNAIL"] != "box2d" {
		t.Errorf("GAMELIST_MEDIA_THUMBNAIL = %v", resp["GAMELIST_MEDIA_THUMBNAIL"])
	}
	bind := resp["PLATFORMS_BINDING"].(map[string]string)
	if bind["amiga500"] != "amiga" {
		t.Errorf("PLATFORMS_BINDING = %v", bind)
	}
	// Code-constant defaults must always be present and non-empty.
	if len(resp["DEFAULT_EXCLUDED_EXTENSIONS"].([]string)) == 0 {
		t.Error("DEFAULT_EXCLUDED_EXTENSIONS empty")
	}
	if len(resp["CONVERTO_LIBRARY_TARGETS"].(map[string][]string)) == 0 {
		t.Error("CONVERTO_LIBRARY_TARGETS empty")
	}
}

func TestLoadConfigFile_Missing(t *testing.T) {
	cd := LoadConfigFile(filepath.Join(t.TempDir(), "nope.yml"))
	if cd.Mounted {
		t.Error("missing file should report not mounted")
	}
	resp := cd.APIResponse(false)
	if resp["CONFIG_FILE_MOUNTED"] != false {
		t.Error("CONFIG_FILE_MOUNTED should be false for a missing file")
	}
	// The response shape is still complete so the frontend never hits a nil key.
	if _, ok := resp["EXCLUDED_PLATFORMS"].([]string); !ok {
		t.Error("EXCLUDED_PLATFORMS missing from empty response")
	}
}
