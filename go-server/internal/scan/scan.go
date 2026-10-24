// Package scan walks the ES-DE library and turns rom files into Records for
// the store to upsert. Phase-3 scope: top-level files under each platform's
// folder (roms/<fs_slug>/*), hashing each. Multi-file rom folders, ES-DE
// gamelist.xml metadata ingest, and media association are layered on next.
package scan

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zszwede/romm-go/internal/hash"
)

// Record is one discovered rom file.
type Record struct {
	PlatformID   int64
	FsPath       string // relative dir stored on the rom, e.g. "roms/switch"
	FsName       string // file name with extension
	FsNameNoExt  string
	FsNameNoTags string
	FsExtension  string // without the leading dot
	SizeBytes    int64
	Digests      hash.Digests
}

// Sink receives discovered roms (implemented by the store).
type Sink interface {
	Upsert(ctx context.Context, r Record) error
}

// PlatformDir maps a platform id to its on-disk folder name.
type PlatformDir struct {
	ID     int64
	FsSlug string
}

type Result struct {
	Found    int `json:"found"`
	Upserted int `json:"upserted"`
	Skipped  int `json:"skipped"`
}

var tagRE = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)

// skip names that are not roms.
func skipName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	lower := strings.ToLower(name)
	switch lower {
	case "gamelist.xml", "media", "miximages", "systeminfo.txt":
		return true
	}
	return false
}

func stripTags(s string) string {
	return strings.Join(strings.Fields(tagRE.ReplaceAllString(s, " ")), " ")
}

// Platforms scans each platform's folder under root. When doHash is false the
// files are discovered and sized but not hashed (fast inventory).
func Platforms(ctx context.Context, root string, plats []PlatformDir, sink Sink, doHash bool) (Result, error) {
	var res Result
	for _, p := range plats {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		dir := filepath.Join(root, p.FsSlug)
		entries, err := os.ReadDir(dir)
		if err != nil {
			// A platform with no folder on disk is fine; keep going.
			if os.IsNotExist(err) {
				continue
			}
			return res, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || skipName(name) {
				res.Skipped++
				continue
			}
			info, err := e.Info()
			if err != nil {
				res.Skipped++
				continue
			}
			res.Found++
			ext := strings.TrimPrefix(filepath.Ext(name), ".")
			noExt := strings.TrimSuffix(name, filepath.Ext(name))
			rec := Record{
				PlatformID:   p.ID,
				FsPath:       filepath.Join("roms", p.FsSlug),
				FsName:       name,
				FsNameNoExt:  noExt,
				FsNameNoTags: stripTags(noExt),
				FsExtension:  ext,
				SizeBytes:    info.Size(),
			}
			if doHash {
				d, err := hash.File(filepath.Join(dir, name))
				if err != nil {
					res.Skipped++
					continue
				}
				rec.Digests = d
			}
			if err := sink.Upsert(ctx, rec); err != nil {
				return res, err
			}
			res.Upserted++
		}
	}
	return res, nil
}
