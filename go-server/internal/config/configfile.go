package config

import (
	"errors"
	"io/fs"
	"os"

	"gopkg.in/yaml.v3"
)

// ConfigData is the parsed RomM config.yml. It mirrors the sections the Python
// ConfigManager reads and feeds the GET /api/config response. Dict-shaped
// fields the frontend only round-trips (controls, settings, platform formats)
// are kept as `any` so arbitrary user content survives unchanged.
type ConfigData struct {
	// Mounted reports whether a config file was found and read. Writable and
	// ParseError mirror the two companion flags the frontend shows a banner for.
	Mounted    bool
	Writable   bool
	ParseError string

	Converto struct {
		DownloadConversionEnabled bool           `yaml:"download_conversion_enabled"`
		ScanMetadata              bool           `yaml:"scan_metadata"`
		CacheTTLHours             int            `yaml:"cache_ttl_hours"`
		CacheMaxSizeGB            int            `yaml:"cache_max_size_gb"`
		PlatformFormats           map[string]any `yaml:"platform_formats"`
	} `yaml:"converto"`

	EmulatorJS struct {
		AutoSaveSync      bool           `yaml:"auto_save_sync"`
		CacheLimit        *int           `yaml:"cache_limit"`
		Controls          map[string]any `yaml:"controls"`
		Debug             bool           `yaml:"debug"`
		DefaultCores      map[string]any `yaml:"default_cores"`
		DisableAutoUnload bool           `yaml:"disable_auto_unload"`
		DisableBatchBoot  bool           `yaml:"disable_batch_bootup"`
		Settings          map[string]any `yaml:"settings"`
		Netplay           struct {
			Enabled    bool  `yaml:"enabled"`
			ICEServers []any `yaml:"ice_servers"`
		} `yaml:"netplay"`
	} `yaml:"emulatorjs"`

	Exclude struct {
		Platforms []string `yaml:"platforms"`
		Roms      struct {
			MultiFile struct {
				Names []string `yaml:"names"`
				Parts struct {
					Extensions []string `yaml:"extensions"`
					Names      []string `yaml:"names"`
				} `yaml:"parts"`
			} `yaml:"multi_file"`
			SingleFile struct {
				Extensions []string `yaml:"extensions"`
				Names      []string `yaml:"names"`
			} `yaml:"single_file"`
		} `yaml:"roms"`
	} `yaml:"exclude"`

	Filesystem struct {
		SkipHashCalculation bool `yaml:"skip_hash_calculation"`
	} `yaml:"filesystem"`

	Scan struct {
		Gamelist struct {
			Export bool `yaml:"export"`
			Media  struct {
				Image     string `yaml:"image"`
				Thumbnail string `yaml:"thumbnail"`
			} `yaml:"media"`
		} `yaml:"gamelist"`
		Media   []string `yaml:"media"`
		Pegasus struct {
			Export bool `yaml:"export"`
		} `yaml:"pegasus"`
		Priority struct {
			Artwork  []string `yaml:"artwork"`
			Cover    []string `yaml:"cover"`
			Language []string `yaml:"language"`
			Metadata []string `yaml:"metadata"`
			Region   []string `yaml:"region"`
		} `yaml:"priority"`
	} `yaml:"scan"`

	System struct {
		Platforms map[string]string `yaml:"platforms"`
		Versions  map[string]string `yaml:"versions"`
	} `yaml:"system"`
}

// LoadConfigFile reads and parses the config.yml at path. A missing file is not
// an error: it returns a ConfigData with Mounted=false so the frontend shows
// its "config file not mounted" banner, exactly like the Python app. A present
// but malformed file returns Mounted=true with ParseError set.
func LoadConfigFile(path string) *ConfigData {
	cd := &ConfigData{}
	if path == "" {
		return cd
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return cd // not mounted
		}
		cd.Mounted = true
		cd.ParseError = err.Error()
		return cd
	}
	cd.Mounted = true
	cd.Writable = isWritable(path)
	if err := yaml.Unmarshal(raw, cd); err != nil {
		cd.ParseError = err.Error()
	}
	return cd
}

func isWritable(path string) bool {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}

// Code-constant exclusion defaults and conversion targets. These live in the
// Python app's source (not config.yml); copied verbatim so the settings UI
// shows the same baseline the container does.
var (
	defaultExcludedPlatformDirs = []string{"@eaDir", "assets", "__MACOSX", "#recycle", "$RECYCLE.BIN", ".Trash-*", ".stfolder", ".Spotlight-V100", ".fseventsd", ".DocumentRevisions-V100", "System Volume Information"}
	defaultExcludedMultiDirs    = []string{"#recycle", "$RECYCLE.BIN", ".DocumentRevisions-V100", ".Spotlight-V100", ".Trash-*", ".fseventsd", ".stfolder", "3dboxes", "@eaDir", "System Volume Information", "__MACOSX", "assets", "backcovers", "bezels", "covers", "fanart", "images", "manuals", "marquees", "miximages", "miximages_v2", "physicalmedia", "screenshots", "thumbnails", "titlescreens", "videos"}
	defaultExcludedFiles        = []string{".DS_Store", ".localized", ".Trashes", ".stfolder", "@SynoResource", "*:Zone.Identifier", "gamelist.xml", "metadata.pegasus.txt"}
	defaultExcludedExtensions   = []string{"db", "tmp", "bak", "lock", "log", "cache", "crdownload", "assembling"}
	convertoLibraryTargets      = map[string][]string{
		"3ds": {"cci", "cia", "z3ds"}, "dc": {"chd"}, "ngc": {"iso", "rvz"},
		"ps2": {"chd", "cso", "iso", "zso"}, "psp": {"chd", "cso", "iso", "zso"},
		"psx": {"chd"}, "saturn": {"chd"}, "segacd": {"chd"},
		"switch": {"nsp", "nsz", "xci", "xcz"}, "switch-2": {"nsp", "nsz", "xci", "xcz"},
		"wii": {"iso", "rvz", "wbfs"}, "xbox360": {"zar"},
	}
)

// APIResponse builds the GET /api/config body. Authenticated controls whether
// the parse error (which may contain the file path) and the netplay ICE servers
// (which may carry credentials) are included — matching the Python endpoint.
func (c *ConfigData) APIResponse(authenticated bool) map[string]any {
	str := func(s []string) []string {
		if s == nil {
			return []string{}
		}
		return s
	}
	parseErr := any(nil)
	if authenticated && c.ParseError != "" {
		parseErr = c.ParseError
	}
	ice := []any{}
	if authenticated && c.EmulatorJS.Netplay.ICEServers != nil {
		ice = c.EmulatorJS.Netplay.ICEServers
	}
	nonNilMap := func(m map[string]any) map[string]any {
		if m == nil {
			return map[string]any{}
		}
		return m
	}
	bind := func(m map[string]string) map[string]string {
		if m == nil {
			return map[string]string{}
		}
		return m
	}

	// SCAN_ARTWORK_PRIORITY_OVERRIDES keys the cover-art priority under the
	// url_cover field the frontend overrides per-source.
	overrides := map[string]any{}
	if len(c.Scan.Priority.Cover) > 0 {
		overrides["url_cover"] = c.Scan.Priority.Cover
	}

	targets := make(map[string][]string, len(convertoLibraryTargets))
	for k, v := range convertoLibraryTargets {
		targets[k] = v
	}

	return map[string]any{
		"CONFIG_FILE_MOUNTED":     c.Mounted,
		"CONFIG_FILE_WRITABLE":    c.Writable,
		"CONFIG_FILE_PARSE_ERROR": parseErr,

		"EXCLUDED_PLATFORMS":         str(c.Exclude.Platforms),
		"EXCLUDED_SINGLE_EXT":        str(c.Exclude.Roms.SingleFile.Extensions),
		"EXCLUDED_SINGLE_FILES":      str(c.Exclude.Roms.SingleFile.Names),
		"EXCLUDED_MULTI_FILES":       str(c.Exclude.Roms.MultiFile.Names),
		"EXCLUDED_MULTI_PARTS_EXT":   str(c.Exclude.Roms.MultiFile.Parts.Extensions),
		"EXCLUDED_MULTI_PARTS_FILES": str(c.Exclude.Roms.MultiFile.Parts.Names),

		"DEFAULT_EXCLUDED_PLATFORM_DIRS":   defaultExcludedPlatformDirs,
		"DEFAULT_EXCLUDED_MULTI_FILE_DIRS": defaultExcludedMultiDirs,
		"DEFAULT_EXCLUDED_FILES":           defaultExcludedFiles,
		"DEFAULT_EXCLUDED_EXTENSIONS":      defaultExcludedExtensions,

		"PLATFORMS_BINDING":  bind(c.System.Platforms),
		"PLATFORMS_VERSIONS": bind(c.System.Versions),

		"SKIP_HASH_CALCULATION": c.Filesystem.SkipHashCalculation,

		"EJS_DEBUG":                 c.EmulatorJS.Debug,
		"EJS_CACHE_LIMIT":           c.EmulatorJS.CacheLimit,
		"EJS_DISABLE_AUTO_UNLOAD":   c.EmulatorJS.DisableAutoUnload,
		"EJS_DISABLE_BATCH_BOOTUP":  c.EmulatorJS.DisableBatchBoot,
		"EJS_ENABLE_AUTO_SAVE_SYNC": c.EmulatorJS.AutoSaveSync,
		"EJS_NETPLAY_ENABLED":       c.EmulatorJS.Netplay.Enabled,
		"EJS_NETPLAY_ICE_SERVERS":   ice,
		"EJS_DEFAULT_CORES":         nonNilMap(c.EmulatorJS.DefaultCores),
		"EJS_SETTINGS":              nonNilMap(c.EmulatorJS.Settings),
		"EJS_CONTROLS":              nonNilMap(c.EmulatorJS.Controls),

		"SCAN_METADATA_PRIORITY":          str(c.Scan.Priority.Metadata),
		"SCAN_ARTWORK_PRIORITY":           str(c.Scan.Priority.Artwork),
		"SCAN_ARTWORK_PRIORITY_OVERRIDES": overrides,
		"SCAN_REGION_PRIORITY":            str(c.Scan.Priority.Region),
		"SCAN_LANGUAGE_PRIORITY":          str(c.Scan.Priority.Language),
		"SCAN_MEDIA":                      str(c.Scan.Media),

		"GAMELIST_AUTO_EXPORT_ON_SCAN": c.Scan.Gamelist.Export,
		"GAMELIST_MEDIA_THUMBNAIL":     c.Scan.Gamelist.Media.Thumbnail,
		"GAMELIST_MEDIA_IMAGE":         c.Scan.Gamelist.Media.Image,
		"PEGASUS_AUTO_EXPORT_ON_SCAN":  c.Scan.Pegasus.Export,

		"CONVERTO": map[string]any{
			"download_conversion_enabled": c.Converto.DownloadConversionEnabled,
			"scan_metadata":               c.Converto.ScanMetadata,
			"cache_ttl_hours":             c.Converto.CacheTTLHours,
			"cache_max_size_gb":           c.Converto.CacheMaxSizeGB,
			"platform_formats":            nonNilMap(c.Converto.PlatformFormats),
		},
		"CONVERTO_LIBRARY_TARGETS": targets,
	}
}
