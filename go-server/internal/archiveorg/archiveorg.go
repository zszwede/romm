// Package archiveorg parses archive.org source URLs and enumerates the ROM
// files they expose, ported from backend/handler/virtual/archive_org.py. It is
// the discovery half of the virtual-download feature: given a source URL it
// yields (name, source_url, size) games to create as virtual roms.
//
// Accepted URL shapes:
//   - https://archive.org/download/<id>                   item download root
//   - https://archive.org/download/<id>/<file>            one file in an item
//   - https://archive.org/download/<id>/<zip>/            contents of a zip
//   - .../view_archive.php?archive=/NN/items/<id>/<zip>   contents of a zip
//
// Item files come from the public metadata API (JSON, no auth); a zip's members
// come from view_archive.php (HTML). This package does the parsing only; the
// HTTP fetching and virtual-rom creation are wired by the caller.
package archiveorg

import (
	"errors"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var (
	archiveExtensions   = map[string]bool{"zip": true, "7z": true, "rar": true}
	alwaysSkipExtensions = map[string]bool{
		"torrent": true, "sqlite": true, "xml": true, "json": true,
		"jpg": true, "jpeg": true, "png": true, "gif": true, "bmp": true, "webp": true,
	}
	alwaysSkipBasenames = map[string]bool{"images.7z": true, "images.zip": true}
)

// MultiGameArchiveMinBytes: a lone archive at an item root is auto-enumerated as
// a multi-game pack only when at least this big; a smaller single-game zip is
// kept as one game.
const MultiGameArchiveMinBytes = 64 * 1024 * 1024

// ParsedSource is a normalised archive.org source.
type ParsedSource struct {
	Identifier  string
	ArchivePath string // a zip whose members should be listed via view_archive
	FilePath    string // a single file in the item
}

// VirtualGame is one discovered ROM.
type VirtualGame struct {
	Name      string `json:"name"`
	SourceURL string `json:"source_url"`
	Size      *int64 `json:"size"`
}

var (
	reViewArchive = regexp.MustCompile(`archive=/\d+/items/([^/]+)/(.+)$`)
	reDownload    = regexp.MustCompile(`^/download/([^/]+)(?:/(.+))?$`)
)

// ErrBadURL is returned for a URL that isn't a recognised archive.org source.
var ErrBadURL = errors.New("archiveorg: unrecognised source URL")

// ParseSourceURL normalises any accepted archive.org URL.
func ParseSourceURL(raw string) (ParsedSource, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ParsedSource{}, err
	}
	// view_archive.php?archive=/NN/items/<id>/<zip>
	if strings.HasSuffix(u.Path, "/view_archive.php") {
		arc := u.Query().Get("archive")
		m := reViewArchive.FindStringSubmatch("archive=" + arc)
		if m == nil {
			return ParsedSource{}, ErrBadURL
		}
		return ParsedSource{Identifier: unquote(m[1]), ArchivePath: m[2]}, nil
	}
	// /download/<id>[/<rest>]
	if m := reDownload.FindStringSubmatch(u.Path); m != nil {
		id := unquote(m[1])
		rest := m[2]
		if rest == "" {
			return ParsedSource{Identifier: id}, nil
		}
		return classify(id, strings.TrimRight(rest, "/")), nil
	}
	return ParsedSource{}, ErrBadURL
}

func classify(identifier, rest string) ParsedSource {
	if rest == "" {
		return ParsedSource{Identifier: identifier}
	}
	if isArchive(rest) {
		return ParsedSource{Identifier: identifier, ArchivePath: rest}
	}
	return ParsedSource{Identifier: identifier, FilePath: rest}
}

// GamesFromMetadata turns an archive.org metadata document's "files" into games.
// If the item holds a single large archive at its root, it returns that archive
// path (and no games) so the caller can enumerate its members instead.
func GamesFromMetadata(meta map[string]any, identifier string, romExts map[string]bool) ([]VirtualGame, string) {
	files, _ := meta["files"].([]any)
	var originals []map[string]any
	for _, fi := range files {
		f, ok := fi.(map[string]any)
		if !ok {
			continue
		}
		if s, _ := f["source"].(string); s != "original" {
			continue
		}
		if name, _ := f["name"].(string); name != "" {
			originals = append(originals, f)
		}
	}
	var kept []map[string]any
	for _, f := range originals {
		name, _ := f["name"].(string)
		if keep(name, romExts) {
			kept = append(kept, f)
		}
	}
	// A single large archive at the root -> enumerate it instead.
	if len(kept) == 1 {
		name, _ := kept[0]["name"].(string)
		if isArchive(name) && fileSize(kept[0]) >= MultiGameArchiveMinBytes {
			return nil, name
		}
	}
	games := make([]VirtualGame, 0, len(kept))
	for _, f := range kept {
		name, _ := f["name"].(string)
		base := name
		if i := strings.LastIndex(name, "/"); i >= 0 {
			base = name[i+1:]
		}
		g := VirtualGame{Name: base, SourceURL: downloadURL(identifier, encodePath(name))}
		if sz := fileSize(f); sz > 0 {
			g.Size = &sz
		}
		games = append(games, g)
	}
	return games, ""
}

var reMemberRow = regexp.MustCompile(`(?s)<a href="(//archive\.org/download/[^"]+)">([^<]*)</a>.*?<td id="size">(\d+)`)

// GamesFromArchiveListing parses a view_archive.php HTML listing of a zip's members.
func GamesFromArchiveListing(listingHTML string, romExts map[string]bool) []VirtualGame {
	games := []VirtualGame{}
	for _, m := range reMemberRow.FindAllStringSubmatch(listingHTML, -1) {
		href, display, sizeStr := m[1], m[2], m[3]
		name := html.UnescapeString(display)
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		name = strings.TrimSpace(name)
		if name == "" || !keep(name, romExts) {
			continue
		}
		g := VirtualGame{Name: name, SourceURL: "https:" + href}
		if sz, err := strconv.ParseInt(sizeStr, 10, 64); err == nil && sz > 0 {
			g.Size = &sz
		}
		games = append(games, g)
	}
	return games
}

// --- helpers ---

func extension(name string) string {
	base := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		base = name[i+1:]
	}
	if i := strings.LastIndex(base, "."); i > 0 {
		return strings.ToLower(base[i+1:])
	}
	return ""
}

func isArchive(name string) bool { return archiveExtensions[extension(name)] }

// keep reports whether a file name is a ROM worth listing.
func keep(name string, romExts map[string]bool) bool {
	base := name
	if i := strings.LastIndex(name, "/"); i >= 0 {
		base = name[i+1:]
	}
	base = strings.ToLower(base)
	if strings.HasPrefix(base, "__ia") || alwaysSkipBasenames[base] {
		return false
	}
	ext := extension(name)
	if ext == "" || alwaysSkipExtensions[ext] {
		return false
	}
	if archiveExtensions[ext] {
		return true
	}
	if len(romExts) == 0 {
		return true
	}
	return romExts[ext]
}

func downloadURL(identifier, path string) string {
	return "https://archive.org/download/" + identifier + "/" + path
}

func encodePath(path string) string {
	// Encode each segment but keep the separators, matching urllib.quote(path).
	parts := strings.Split(path, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

func fileSize(f map[string]any) int64 {
	switch s := f["size"].(type) {
	case string:
		n, _ := strconv.ParseInt(s, 10, 64)
		return n
	case float64:
		return int64(s)
	}
	return 0
}

func unquote(s string) string {
	if dec, err := url.PathUnescape(s); err == nil {
		return dec
	}
	return s
}
