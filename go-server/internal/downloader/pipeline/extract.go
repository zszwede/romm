package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/fsutil"
)

// extract unpacks archives and passes everything else through unchanged — a
// download that is already a plain rom is a no-op. Extraction is pure-Go (no
// external 7z/unar binary): zip, 7z, rar, tar and gz/xz/bz2/zst streams.
// Multi-volume sets (.partN.rar, .rar+.r00, .7z.001, .zip+.z01) are unpacked
// once from their first volume.
type extract struct {
	keepArchives bool
	recursive    bool
	password     string
	deleteInputs bool
	timeout      time.Duration
	never        []*regexp.Regexp // names never treated as archives
}

// default for extract.exclude: rom formats whose first bytes may look like an
// archive magic by chance (XCI starts with a random RSA signature)
var defaultNeverExtract = []string{`(?i)\.(nsp|xci|nsz|xcz|iso|chd|cso|rvz|wbfs|wua|wux)$`}

func newExtract(c config.Step) (Step, error) {
	pats := c.Exclude
	if len(pats) == 0 {
		pats = defaultNeverExtract
	}
	never, err := compileAll(pats)
	if err != nil {
		return nil, err
	}
	return &extract{
		never:        never,
		keepArchives: c.KeepArchives,
		recursive:    boolOr(c.Recursive, true),
		password:     c.Password,
		deleteInputs: boolOr(c.DeleteInputs, true),
		timeout:      c.Timeout,
	}, nil
}

var (
	reRarPart1   = regexp.MustCompile(`(?i)\.part0*1\.rar$`)
	reRarPartN   = regexp.MustCompile(`(?i)\.part\d+\.rar$`)
	reRarOld     = regexp.MustCompile(`(?i)\.r\d{2,3}$`)
	reZipSplit   = regexp.MustCompile(`(?i)\.z\d{2,3}$`)
	reNumbered1  = regexp.MustCompile(`(?i)\.(7z|zip|rar|tar)\.0*1$`)
	reNumberedN  = regexp.MustCompile(`(?i)\.(7z|zip|rar|tar)\.\d+$`)
	reArchiveExt = regexp.MustCompile(`(?i)\.(rar|zip|7z|gz|tgz|xz|bz2|zst|tar)$`)
	// rePartNum matches a "PartN" multi-volume name (RommDownloader splits large
	// games into "... Part1"/"Part2", often with no .rar extension): a separator,
	// "part", leading zeros, then the volume number, and an optional .rar.
	rePartNum = regexp.MustCompile(`(?i)[ ._-]part0*(\d+)(\.rar)?$`)
)

// classify: "first" = feed to 7z, "volume" = part of a set, "" = not an archive.
func (e *extract) classify(path string) string {
	n := filepath.Base(path)
	if matchAny(e.never, n) {
		return ""
	}
	switch {
	case reRarPart1.MatchString(n), reNumbered1.MatchString(n):
		return "first"
	case reRarPartN.MatchString(n), reRarOld.MatchString(n), reZipSplit.MatchString(n), reNumberedN.MatchString(n):
		return "volume"
	}
	// "PartN" naming without a standard extension (e.g. "... - Part2"): a RAR
	// multi-volume set. Part 1 is the first volume; the rest are continuations
	// (which carry the RAR signature too, so they'd otherwise look like separate
	// archives and fail to extract standalone). Gate on the archive magic so a
	// non-archive "Part2" is not misclassified.
	if m := rePartNum.FindStringSubmatch(n); m != nil && fsutil.IsArchive(path) {
		if m[1] == "1" {
			return "first"
		}
		return "volume"
	}
	switch {
	case fsutil.IsArchive(path):
		return "first"
	case reArchiveExt.MatchString(n):
		// archive by name but unrecognised magic: let 7z try
		return "first"
	}
	return ""
}

func (e *extract) Run(ctx context.Context, job *Job, dir string, files []string) ([]string, error) {
	current := files
	var kept []string // archives passed through (keep_archives)
	for depth := 0; depth < 4; depth++ {
		var out, firsts, consumed []string
		for _, f := range current {
			switch e.classify(f) {
			case "first":
				firsts = append(firsts, f)
				consumed = append(consumed, f)
			case "volume":
				consumed = append(consumed, f)
			default:
				out = append(out, f)
			}
		}
		if len(firsts) == 0 {
			if len(consumed) > 0 {
				return nil, fmt.Errorf("archive volumes without a first volume: %s", baseNames(consumed))
			}
			return append(current, kept...), nil
		}
		for i, a := range firsts {
			dest := filepath.Join(dir, fmt.Sprintf("d%d-%d", depth, i))
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return nil, err
			}
			job.Log.Info("extracting", "archive", filepath.Base(a))
			runErr := func() error {
				c := ctx
				if e.timeout > 0 {
					var cancel context.CancelFunc
					c, cancel = context.WithTimeout(ctx, e.timeout)
					defer cancel()
				}
				return extractArchive(c, a, dest, e.password)
			}()
			got, err := fsutil.ListFiles(dest)
			if err != nil {
				return nil, err
			}
			// A partial failure (a junk ".URL" shortcut in a scene pack, say) is
			// tolerated as long as the archive still yielded a real, non-empty
			// file; the filter step keeps the wanted ROM. Only an archive that
			// produced nothing usable is a hard error — and then the source is
			// kept so a retry can re-extract without re-downloading.
			if !hasNonEmpty(got) {
				// Keep the source archive (we return before the delete below) so
				// a retry can re-extract without re-downloading.
				if runErr != nil {
					return nil, fmt.Errorf("%s: extraction produced no usable files: %w", filepath.Base(a), runErr)
				}
				return nil, fmt.Errorf("%s: extraction produced no usable files", filepath.Base(a))
			}
			if runErr != nil {
				job.Log.Warn("extract reported errors but produced files",
					"archive", filepath.Base(a), "err", runErr)
			}
			out = append(out, got...)
		}
		if e.keepArchives {
			kept = append(kept, consumed...)
		} else if e.deleteInputs {
			fsutil.RemoveAll(consumed)
		}
		if !e.recursive {
			return append(out, kept...), nil
		}
		current = out
	}
	return nil, fmt.Errorf("archives nested too deep")
}

// hasNonEmpty reports whether any of paths is a non-empty (>0 byte) file — the
// signal that an extraction actually yielded usable content rather than the
// 0-byte stubs 7z leaves on an "Unsupported Method" RAR.
func hasNonEmpty(paths []string) bool {
	for _, p := range paths {
		if fi, err := os.Stat(p); err == nil && fi.Size() > 0 {
			return true
		}
	}
	return false
}

func baseNames(paths []string) string {
	n := make([]string, len(paths))
	for i, p := range paths {
		n[i] = filepath.Base(p)
	}
	return strings.Join(n, ", ")
}
