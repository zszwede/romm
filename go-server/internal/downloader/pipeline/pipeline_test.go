package pipeline

import (
	"context"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zszwede/romm-go/internal/downloader/config"
)

// real "switch-tool scan" report layout
const scanOut = `scanned 4 files

=== 1 games split across files (merge candidates) ===

base 0100ABCD00010000:
   [1xBASE                ]   1.20G  Game [0100ABCD00010000][v0].nsp
   [1xUPDATE              ]   0.10G  Game [0100ABCD00010800][v65536].nsp

=== 1 loose update/DLC files with no base present ===
   [1xDLC      ] Other DLC [0100FFFF00011001].nsp  (base 0100FFFF00010000 absent)

=== 1 games already complete in one file ===
`

func TestParseScan(t *testing.T) {
	r := parseScan(scanOut)
	g := r.groups["0100ABCD00010000"]
	if len(r.groups) != 1 || len(g) != 2 || g[0] != "Game [0100ABCD00010000][v0].nsp" || g[1] != "Game [0100ABCD00010800][v65536].nsp" {
		t.Fatalf("groups: %#v", r.groups)
	}
	if len(r.loose) != 1 || r.loose[0] != "Other DLC [0100FFFF00011001].nsp" {
		t.Fatalf("loose: %#v", r.loose)
	}
}

func testJob(t *testing.T) *Job {
	return &Job{ID: "j1", System: "switch", WorkDir: t.TempDir(),
		Metadata: map[string]any{"rom_id": 7}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func write(t *testing.T, p, s string) string {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// fake switch-tool: scan groups the two "Game" files, merge writes one nsp
const fakeTool = `#!/bin/sh
cmd=$1; shift
case $cmd in
scan)
  cat <<'EOF'
scanned 3 files

=== 1 games split across files (merge candidates) ===

base 0100ABCD00010000:
   [1xBASE                ]   0.00G  Game [0100ABCD00010000][v0].nsp
   [1xUPDATE              ]   0.00G  Game [0100ABCD00010800][v65536].nsp

=== 0 loose update/DLC files with no base present ===

=== 1 games already complete in one file ===
EOF
  ;;
merge)
  out=""
  while [ $# -gt 0 ]; do case $1 in --out) out=$2; shift 2;; --keys) shift 2;; *) shift;; esac; done
  echo merged > "$out/Game [0100ABCD00010000] (1base+1update).nsp"
  echo "merging 2 files -> Game [0100ABCD00010000] (1base+1update).nsp"
  echo "  verify: OK - all titles present (base 1, upd 1, dlc 0)"
  ;;
esac
`

func TestSwitchMergeAndDeliver(t *testing.T) {
	bin := write(t, filepath.Join(t.TempDir(), "switch-tool"), fakeTool)
	os.Chmod(bin, 0o755)
	keys := write(t, filepath.Join(t.TempDir(), "prod.keys"), "header_key = 00")
	job := testJob(t)
	dl := filepath.Join(job.WorkDir, "download")
	base := write(t, filepath.Join(dl, "Game [0100ABCD00010000][v0].nsp"), "b")
	upd := write(t, filepath.Join(dl, "Game [0100ABCD00010800][v65536].nsp"), "u")
	single := write(t, filepath.Join(dl, "Solo [0100EEEE00010000][v0].nsp"), "s")
	junk := write(t, filepath.Join(dl, "Ziperto.url"), "ad")

	p, err := Build([]config.Step{
		{Type: "extract"},
		{Type: "filter", Include: []string{`(?i)\.(nsp|xci|nsz|xcz)$`}, RequireMatch: true},
		{Type: "switch_merge", Binary: bin, Keys: keys},
	})
	if err != nil {
		t.Fatal(err)
	}
	files, err := p.Run(context.Background(), job, []string{base, upd, single, junk}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	want := []string{"Game [0100ABCD00010000] (1base+1update).nsp", "Solo [0100EEEE00010000][v0].nsp"}
	if len(names) != 2 || names[0] != want[0] || names[1] != want[1] {
		t.Fatalf("got %v", names)
	}
	// merged parts are gone
	for _, f := range []string{base, upd} {
		if _, err := os.Stat(f); err == nil {
			t.Errorf("%s still exists", f)
		}
	}

	target := t.TempDir()
	write(t, filepath.Join(target, "switch", "Solo [0100EEEE00010000][v0].nsp"), "old")
	sys := config.System{TargetDir: target + "/{{.System}}", OnConflict: "rename"}
	got, err := Deliver(sys, job, files)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("delivered %v", got)
	}
	for _, d := range got {
		if _, err := os.Stat(d.Path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(target, "switch", "Solo [0100EEEE00010000][v0] (2).nsp")); err != nil {
		t.Fatal("conflict was not renamed")
	}
}

func TestExtractNested(t *testing.T) {
	if _, err := exec.LookPath("7z"); err != nil {
		t.Skip("7z not installed")
	}
	job := testJob(t)
	src := t.TempDir()
	rnd := make([]byte, 8000)
	rand.Read(rnd)
	write(t, filepath.Join(src, "Game.nsp"), "PFS0"+string(rnd))
	write(t, filepath.Join(src, "readme.txt"), "hi")
	inner := filepath.Join(t.TempDir(), "inner.zip")
	if out, err := exec.Command("7z", "a", "-tzip", inner, filepath.Join(src, "Game.nsp"), filepath.Join(src, "readme.txt")).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	// multi-volume rar-like set is hard to build without rar; use split 7z volumes
	dl := filepath.Join(job.WorkDir, "download")
	os.MkdirAll(dl, 0o755)
	outer := filepath.Join(dl, "Game.7z")
	if out, err := exec.Command("7z", "a", "-v1k", outer, inner).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	vols, _ := filepath.Glob(outer + ".*")
	if len(vols) < 2 {
		t.Fatalf("expected split volumes, got %v", vols)
	}
	// plus a file that is already a rom (no extraction needed)
	plain := write(t, filepath.Join(dl, "Plain.nsp"), "PFS0")

	p, err := Build([]config.Step{{Type: "extract"}})
	if err != nil {
		t.Fatal(err)
	}
	files, err := p.Run(context.Background(), job, append(vols, plain), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, filepath.Base(f))
	}
	sort.Strings(names)
	if len(names) != 3 || names[0] != "Game.nsp" || names[1] != "Plain.nsp" || names[2] != "readme.txt" {
		t.Fatalf("got %v", names)
	}
	for _, v := range vols {
		if _, err := os.Stat(v); err == nil {
			t.Errorf("volume %s not deleted", v)
		}
	}
}
