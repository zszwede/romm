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

// extract unpacks archives (any format 7z reads) and passes everything else
// through unchanged - a download that is already a plain rom is a no-op.
// Multi-volume sets (.partN.rar, .rar+.r00, .7z.001, .zip+.z01) are unpacked
// once from their first volume.
type extract struct {
	bin          string
	rarFallback  string // extractor for RARs 7z can't decode ("unar"); "" if absent
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
	bin := c.Binary
	if bin == "" {
		bin = "7z"
	}
	if _, err := lookPath(bin); err != nil {
		return nil, err
	}
	pats := c.Exclude
	if len(pats) == 0 {
		pats = defaultNeverExtract
	}
	never, err := compileAll(pats)
	if err != nil {
		return nil, err
	}
	// Some scene RARs (e.g. Ziperto) use a RAR method 7-Zip cannot decode
	// ("Unsupported Method") and silently yields 0-byte output. The official
	// unar/The Unarchiver handles every RAR method, so use it as a fallback
	// when present. Optional: absence just disables the fallback.
	rarFallback := ""
	if _, err := lookPath("unar"); err == nil {
		rarFallback = "unar"
	}
	return &extract{
		never:        never,
		bin:          bin,
		rarFallback:  rarFallback,
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
	reRarAny     = regexp.MustCompile(`(?i)(\.part\d+)?\.rar$`)
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
			// "-p" (possibly empty) keeps 7z from prompting for a password
			args := []string{"x", "-y", "-bd", "-p" + e.password, "-o" + dest, a}
			runErr := func() error {
				_, _, err := runCmd(ctx, job.Log, e.timeout, nil, e.bin, args...)
				return err
			}()
			got, err := fsutil.ListFiles(dest)
			if err != nil {
				return nil, err
			}
			// 7z exits non-zero for partial failures too — e.g. a junk ".URL"
			// ad shortcut inside scene packs that uses a method 7z can't decode.
			// Tolerate that only while the archive still yielded a real (non-
			// empty) file; the filter step keeps the wanted ROM. But some RARs
			// use a method 7z can't decode *at all*, leaving every output 0-byte
			// (got != empty, yet nothing usable) — retry those with unar before
			// giving up, and never delete the source archive on failure.
			if !hasNonEmpty(got) && e.rarFallback != "" && isRar(a) {
				job.Log.Warn("7z produced no usable files; retrying with unar",
					"archive", filepath.Base(a), "err", runErr)
				fsutil.RemoveAll(got)
				if _, _, uerr := runCmd(ctx, job.Log, e.timeout, nil, e.rarFallback,
					"-quiet", "-force-overwrite", "-output-directory", dest, a); uerr != nil {
					job.Log.Warn("unar reported errors", "archive", filepath.Base(a), "err", uerr)
				}
				if got, err = fsutil.ListFiles(dest); err != nil {
					return nil, err
				}
				runErr = nil // unar is authoritative for RARs
			}
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

func isRar(path string) bool { return reRarAny.MatchString(path) }

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
