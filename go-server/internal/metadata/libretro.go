package metadata

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Libretro resolves box-art covers from the public libretro-thumbnails server
// (thumbnails.libretro.com), matching a ROM's filename against a platform's
// Named_Boxarts listing. It carries no credentials (the server is public) and
// is used to enrich manual-match results with an alternative cover, mirroring
// the Python search endpoint.
type Libretro struct {
	root string
	hc   *http.Client

	mu    sync.Mutex
	cache map[string]listingEntry // system -> cached boxart filenames
}

type listingEntry struct {
	files   []string
	fetched time.Time
}

const libretroListingTTL = 6 * time.Hour

func NewLibretro() *Libretro {
	return &Libretro{
		root:  "https://thumbnails.libretro.com",
		hc:    &http.Client{Timeout: 20 * time.Second},
		cache: map[string]listingEntry{},
	}
}

func (l *Libretro) Name() string { return "libretro" }

// LibretroCover is a matched box-art cover. ID is the sha1 of the matched
// filename (what the Python app stores as libretro_id).
type LibretroCover struct {
	ID       string
	CoverURL string
}

// CoverFor returns a libretro box-art URL for a ROM filename on a platform, or
// nil when the platform is unmapped or no art matches.
func (l *Libretro) CoverFor(ctx context.Context, fsName, platformSlug string) *LibretroCover {
	system, ok := libretroSystems[strings.ToLower(platformSlug)]
	if !ok {
		return nil
	}
	files, err := l.listing(ctx, system)
	if err != nil || len(files) == 0 {
		return nil
	}
	matched := matchArt(fsName, files)
	if matched == "" {
		return nil
	}
	u := l.root + "/" + url.PathEscape(system) + "/Named_Boxarts/" + url.PathEscape(matched)
	// url.PathEscape escapes spaces as %20, which libretro expects.
	sum := sha1.Sum([]byte(matched))
	return &LibretroCover{ID: hex.EncodeToString(sum[:]), CoverURL: u}
}

// listing fetches (and caches) a system's Named_Boxarts directory listing.
func (l *Libretro) listing(ctx context.Context, system string) ([]string, error) {
	l.mu.Lock()
	if e, ok := l.cache[system]; ok && time.Since(e.fetched) < libretroListingTTL {
		l.mu.Unlock()
		return e.files, nil
	}
	l.mu.Unlock()

	u := l.root + "/" + url.PathEscape(system) + "/Named_Boxarts/?F=2"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RomM/go")
	resp, err := l.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// Cache an absent system as empty so we don't refetch it each rom.
		l.store(system, nil)
		return nil, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	files := parseAnchors(string(body))
	l.store(system, files)
	return files, nil
}

func (l *Libretro) store(system string, files []string) {
	l.mu.Lock()
	l.cache[system] = listingEntry{files: files, fetched: time.Now()}
	l.mu.Unlock()
}

var anchorRE = regexp.MustCompile(`href="([^"?/][^"]*\.png)"`)

// parseAnchors extracts .png filenames from an Apache autoindex page, matching
// the Python _AnchorHrefParser (hrefs to files, not parent/sort links).
func parseAnchors(html string) []string {
	var out []string
	for _, m := range anchorRE.FindAllStringSubmatch(html, -1) {
		name := m[1]
		if strings.Contains(name, "/") {
			continue
		}
		if dec, err := url.PathUnescape(name); err == nil {
			name = dec
		}
		out = append(out, name)
	}
	return out
}

var parenTagRE = regexp.MustCompile(`\([^)]*\)`)

// matchArt finds the box-art filename for a ROM name: exact (case-insensitive,
// extension-stripped) first, then a tag-stripped containment fallback. Libretro
// replaces '&' with '_' in its filenames.
func matchArt(fsName string, files []string) string {
	cleaned := strings.ReplaceAll(fsName, "&", "_")
	target := stripExt(cleaned)
	targetLower := strings.ToLower(target)

	for _, f := range files {
		if strings.ToLower(stripExt(f)) == targetLower {
			return f
		}
	}
	// Fallback: compare with parenthetical tags removed on both sides.
	q := normalizeLibretro(target)
	if q == "" {
		return ""
	}
	best := ""
	bestLen := 1 << 30
	for _, f := range files {
		cand := normalizeLibretro(stripExt(f))
		if cand == q {
			return f
		}
		// Prefer the shortest filename that starts with the query, so
		// "Zelda" doesn't lose to a longer unrelated title.
		if strings.HasPrefix(cand, q) && len(cand) < bestLen {
			best = f
			bestLen = len(cand)
		}
	}
	return best
}

func stripExt(s string) string {
	if i := strings.LastIndex(s, "."); i >= 0 {
		return s[:i]
	}
	return s
}

func normalizeLibretro(s string) string {
	s = parenTagRE.ReplaceAllString(s, "")
	return strings.ToLower(strings.TrimSpace(s))
}

// libretroSystems maps RomM platform slugs to libretro-thumbnails system
// directory names (from the Python LIBRETRO_PLATFORM_LIST).
var libretroSystems = map[string]string{
	"adventure-vision": "Entex - Adventure Vision",
	"amiga":            "Commodore - Amiga",
	"amiga-cd32":       "Commodore - Amiga",
	"acpc":             "Amstrad - CPC",
	"atari2600":        "Atari - 2600",
	"atari5200":        "Atari - 5200",
	"atari7800":        "Atari - 7800",
	"atari-st":         "Atari - ST",
	"jaguar":           "Atari - Jaguar",
	"atarijaguar":      "Atari - Jaguar",
	"lynx":             "Atari - Lynx",
	"wonderswan":       "Bandai - WonderSwan",
	"wonderswan-color": "Bandai - WonderSwan Color",
	"colecovision":     "Coleco - ColecoVision",
	"c64":              "Commodore - 64",
	"vic-20":           "Commodore - VIC-20",
	"doom":             "DOOM",
	"dos":              "DOS",
	"fairchild-channel-f": "Fairchild - Channel F",
	"vectrex":             "GCE - Vectrex",
	"odyssey-2":           "Magnavox - Odyssey2",
	"intellivision":       "Mattel - Intellivision",
	"msx":                 "Microsoft - MSX",
	"msx2":                "Microsoft - MSX2",
	"xbox":                "Microsoft - XBOX",
	"pc-8800-series":      "NEC - PC Engine - TurboGrafx 16",
	"pc-fx":               "NEC - PC-FX",
	"pc-9800-series":      "NEC - PC-98",
	"supergrafx":          "NEC - PC Engine SuperGrafx",
	"tg16":                "NEC - PC Engine - TurboGrafx 16",
	"turbografx-cd":       "NEC - PC Engine CD - TurboGrafx-CD",
	"fds":                 "Nintendo - Family Computer Disk System",
	"gb":                  "Nintendo - Game Boy",
	"gba":                 "Nintendo - Game Boy Advance",
	"gbc":                 "Nintendo - Game Boy Color",
	"ngc":                 "Nintendo - GameCube",
	"n64":                 "Nintendo - Nintendo 64",
	"64dd":                "Nintendo - Nintendo 64DD",
	"3ds":                 "Nintendo - Nintendo 3DS",
	"nds":                 "Nintendo - Nintendo DS",
	"nes":                 "Nintendo - Nintendo Entertainment System",
	"famicom":             "Nintendo - Nintendo Entertainment System",
	"pokemon-mini":        "Nintendo - Pokemon Mini",
	"satellaview":         "Nintendo - Satellaview",
	"sufami-turbo":        "Nintendo - Sufami Turbo",
	"snes":                "Nintendo - Super Nintendo Entertainment System",
	"sfam":                "Nintendo - Super Nintendo Entertainment System",
	"virtualboy":          "Nintendo - Virtual Boy",
	"wii":                 "Nintendo - Wii",
	"wiiu":                "Nintendo - Wii U",
	"rpg-maker":           "RPG Maker",
	"scummvm":             "ScummVM",
	"sega32":              "Sega - 32X",
	"dc":                  "Sega - Dreamcast",
	"gamegear":            "Sega - Game Gear",
	"genesis":             "Sega - Mega Drive - Genesis",
	"segacd":              "Sega - Mega-CD - Sega CD",
	"sms":                 "Sega - Master System - Mark III",
	"sg1000":              "Sega - SG-1000",
	"saturn":              "Sega - Saturn",
	"x1":                  "Sharp - X1",
	"sharp-x68000":        "Sharp - X68000",
	"zx81":                "Sinclair - ZX 81",
	"zxs":                 "Sinclair - ZX Spectrum",
	"neogeoaes":           "SNK - Neo Geo",
	"neogeomvs":           "SNK - Neo Geo",
	"neo-geo-cd":          "SNK - Neo Geo CD",
	"neo-geo-pocket":      "SNK - Neo Geo Pocket",
	"neo-geo-pocket-color": "SNK - Neo Geo Pocket Color",
	"psx":                  "Sony - PlayStation",
	"ps2":                  "Sony - PlayStation 2",
	"psp":                  "Sony - PlayStation Portable",
	"tic-80":               "TIC-80",
	"tomy-tutor":           "Tomy - Tutor",
	"supervision":          "Watara - Supervision",
}
