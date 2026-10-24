package pipeline

import (
	"bufio"
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

// Nintendo Switch steps, driven by the switch-tool binary
// (/mnt/retroEMU/other/switch-tools): it reads Title IDs from the NCA headers
// and merges base + update + DLC into one NSP without re-encrypting.

var reSwitchRom = regexp.MustCompile(`(?i)\.(nsp|xci|nsz|xcz)$`)
var reSwitchCompressed = regexp.MustCompile(`(?i)\.(nsz|xcz)$`)

type switchBase struct {
	bin, keys    string
	timeout      time.Duration
	deleteInputs bool
}

func newSwitchBase(c config.Step) (switchBase, error) {
	bin := c.Binary
	if bin == "" {
		bin = "switch-tool"
	}
	if _, err := lookPath(bin); err != nil {
		return switchBase{}, err
	}
	if c.Keys == "" {
		return switchBase{}, fmt.Errorf("keys (prod.keys path) is required")
	}
	if _, err := os.Stat(c.Keys); err != nil {
		return switchBase{}, fmt.Errorf("keys: %w", err)
	}
	return switchBase{bin: bin, keys: c.Keys, timeout: c.Timeout, deleteInputs: boolOr(c.DeleteInputs, true)}, nil
}

// stage moves the rom files into one flat directory with unique names, since
// switch-tool reports files by base name only.
func stage(dir string, files []string) (map[string]string, []string, error) {
	in := filepath.Join(dir, "in")
	if err := os.MkdirAll(in, 0o755); err != nil {
		return nil, nil, err
	}
	byName := map[string]string{}
	var staged []string
	for _, f := range files {
		dst := fsutil.UniquePath(filepath.Join(in, filepath.Base(f)))
		if err := fsutil.Move(f, dst); err != nil {
			return nil, nil, err
		}
		byName[filepath.Base(dst)] = dst
		staged = append(staged, dst)
	}
	return byName, staged, nil
}

// partitionEmpty splits paths into non-empty files and empty/unreadable ones
// (0-byte or failed stat), so failed sub-link downloads never reach the merge.
func partitionEmpty(files []string) (nonEmpty, empty []string) {
	for _, f := range files {
		if fi, err := os.Stat(f); err != nil || fi.Size() == 0 {
			empty = append(empty, f)
		} else {
			nonEmpty = append(nonEmpty, f)
		}
	}
	return
}

func splitRoms(files []string, re *regexp.Regexp) (roms, other []string) {
	for _, f := range files {
		if re.MatchString(filepath.Base(f)) {
			roms = append(roms, f)
		} else {
			other = append(other, f)
		}
	}
	return
}

// singleOutput returns the only finished output file in dir.
func singleOutput(dir string) (string, error) {
	got, err := fsutil.ListFiles(dir)
	if err != nil {
		return "", err
	}
	var res []string
	for _, g := range got {
		if !strings.HasSuffix(g, ".tmp") {
			res = append(res, g)
		}
	}
	if len(res) != 1 {
		return "", fmt.Errorf("expected 1 output file, found %d", len(res))
	}
	return res[0], nil
}

// ---------------------------------------------------------------------------
// switch_decompress: .nsz/.xcz -> .nsp. Other files pass through.
// ---------------------------------------------------------------------------

type switchDecompress struct{ switchBase }

func newSwitchDecompress(c config.Step) (Step, error) {
	b, err := newSwitchBase(c)
	return &switchDecompress{b}, err
}

func (s *switchDecompress) Run(ctx context.Context, job *Job, dir string, files []string) ([]string, error) {
	comp, out := splitRoms(files, reSwitchCompressed)
	for i, f := range comp {
		od := filepath.Join(dir, fmt.Sprintf("out-%d", i))
		if err := os.MkdirAll(od, 0o755); err != nil {
			return nil, err
		}
		job.Log.Info("decompressing", "file", filepath.Base(f))
		so, se, err := runCmd(ctx, job.Log, s.timeout, nil, s.bin, "decompress", "--keys", s.keys, "--out", od, f)
		if err != nil {
			return nil, err
		}
		if strings.Contains(se, "failed") {
			return nil, fmt.Errorf("decompress %s: %s", filepath.Base(f), tail(strings.TrimSpace(se), 400))
		}
		res, err := singleOutput(od)
		if err != nil {
			return nil, fmt.Errorf("decompress %s: %w: %s", filepath.Base(f), err, tail(so, 400))
		}
		out = append(out, res)
		if s.deleteInputs {
			os.Remove(f)
		}
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// switch_merge: group the roms by base title id (switch-tool scan) and merge
// every group of 2+ files into one NSP (switch-tool merge). A game that is
// already one file passes through unchanged.
// ---------------------------------------------------------------------------

type switchMerge struct {
	switchBase
	allowLoose bool
}

func newSwitchMerge(c config.Step) (Step, error) {
	b, err := newSwitchBase(c)
	return &switchMerge{switchBase: b, allowLoose: boolOr(c.AllowLoose, true)}, err
}

// scanResult is the parsed "switch-tool scan" report.
type scanResult struct {
	groups map[string][]string // base id -> file names (split games)
	loose  []string            // update/DLC files without their base
}

var (
	reScanBase  = regexp.MustCompile(`^base ([0-9A-Fa-f]{16}):$`)
	reScanFile  = regexp.MustCompile(`^\s+\[[^\]]*\]\s+[\d.]+G  (.+)$`)
	reScanLoose = regexp.MustCompile(`^\s+\[[^\]]*\]\s(.+)  \(base [0-9A-Fa-f]{16} absent\)$`)
)

func parseScan(out string) scanResult {
	res := scanResult{groups: map[string][]string{}}
	section, base := "", ""
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "=== ") && strings.Contains(line, "merge candidates"):
			section = "split"
		case strings.HasPrefix(line, "=== ") && strings.Contains(line, "loose"):
			section = "loose"
		case strings.HasPrefix(line, "=== "):
			section = ""
		case section == "split":
			if m := reScanBase.FindStringSubmatch(line); m != nil {
				base = strings.ToUpper(m[1])
			} else if m := reScanFile.FindStringSubmatch(line); m != nil && base != "" {
				res.groups[base] = append(res.groups[base], m[1])
			}
		case section == "loose":
			if m := reScanLoose.FindStringSubmatch(line); m != nil {
				res.loose = append(res.loose, m[1])
			}
		}
	}
	return res
}

func (s *switchMerge) Run(ctx context.Context, job *Job, dir string, files []string) ([]string, error) {
	roms, out := splitRoms(files, reSwitchRom)
	// Drop 0-byte rom files: these are sub-links (base/update/DLC) that failed
	// to download. switch-tool scan reads every staged file and aborts the whole
	// job on an empty one ("failed to fill whole buffer"), so skip them and merge
	// whatever valid files did arrive.
	roms, empty := partitionEmpty(roms)
	if len(empty) > 0 {
		job.Log.Warn("skipping empty rom files (failed sub-link downloads)", "files", baseNames(empty))
	}
	if len(roms) == 0 {
		return nil, fmt.Errorf("no non-empty rom files to merge (%d empty, downloads failed)", len(empty))
	}
	if len(roms) < 2 {
		return append(out, roms...), nil // nothing to merge; deliver the single valid rom
	}
	byName, staged, err := stage(dir, roms)
	if err != nil {
		return nil, err
	}
	so, se, err := runCmd(ctx, job.Log, s.timeout, nil, s.bin, append([]string{"scan", "--keys", s.keys}, staged...)...)
	if err != nil {
		return nil, err
	}
	if strings.Contains(se, "ERR ") {
		return nil, fmt.Errorf("scan could not read some files: %s", tail(strings.TrimSpace(se), 600))
	}
	scan := parseScan(so)
	if len(scan.loose) > 0 {
		if !s.allowLoose {
			return nil, fmt.Errorf("updates/DLC without their base game: %s", strings.Join(scan.loose, ", "))
		}
		job.Log.Warn("passing through updates/DLC without base game", "files", scan.loose)
	}

	merged := map[string]bool{}
	i := 0
	for base, names := range scan.groups {
		var srcs []string
		for _, n := range names {
			p, ok := byName[n]
			if !ok {
				return nil, fmt.Errorf("scan reported unknown file %q", n)
			}
			srcs = append(srcs, p)
		}
		od := filepath.Join(dir, fmt.Sprintf("out-%d", i))
		i++
		if err := os.MkdirAll(od, 0o755); err != nil {
			return nil, err
		}
		job.Log.Info("merging", "base", base, "files", names)
		mo, me, err := runCmd(ctx, job.Log, s.timeout, nil, s.bin, append([]string{"merge", "--keys", s.keys, "--out", od}, srcs...)...)
		if err != nil {
			return nil, err
		}
		// switch-tool exits 0 even when a merge fails: check its report
		if strings.Contains(me, "failed") || strings.Contains(mo, "verify: FAILED") {
			return nil, fmt.Errorf("merge %s failed: %s", base, tail(strings.TrimSpace(me+"\n"+mo), 600))
		}
		if !strings.Contains(mo, "verify: OK") {
			return nil, fmt.Errorf("merge %s: no successful verify in output: %s", base, tail(strings.TrimSpace(mo), 600))
		}
		res, err := singleOutput(od)
		if err != nil {
			return nil, fmt.Errorf("merge %s: %w", base, err)
		}
		job.Log.Info("merged", "base", base, "output", filepath.Base(res))
		out = append(out, res)
		for _, p := range srcs {
			merged[p] = true
		}
	}
	for _, p := range staged {
		if !merged[p] {
			out = append(out, p) // complete single-file games and loose files
		} else if s.deleteInputs {
			os.Remove(p)
		}
	}
	return out, nil
}
