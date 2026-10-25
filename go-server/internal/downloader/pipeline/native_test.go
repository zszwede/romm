package pipeline

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Native extraction must work with no external binary installed, so these
// fixtures are built in-process (zip + tar.gz).

func TestExtractArchive_Zip(t *testing.T) {
	dir := t.TempDir()
	zp := filepath.Join(dir, "game.zip")
	f, err := os.Create(zp)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("Game.nes")
	w.Write([]byte("ROMDATA"))
	w2, _ := zw.Create("sub/readme.txt")
	w2.Write([]byte("hi"))
	zw.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	os.MkdirAll(dest, 0o755)
	if err := extractArchive(context.Background(), zp, dest, ""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "Game.nes")); err != nil || string(b) != "ROMDATA" {
		t.Errorf("Game.nes = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "readme.txt")); err != nil {
		t.Errorf("sub/readme.txt missing: %v", err)
	}
}

func TestExtractArchive_TarGz(t *testing.T) {
	dir := t.TempDir()
	tp := filepath.Join(dir, "game.tar.gz")
	f, _ := os.Create(tp)
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	data := []byte("SNESROM")
	tw.WriteHeader(&tar.Header{Name: "Game.sfc", Mode: 0o644, Size: int64(len(data)), Typeflag: tar.TypeReg})
	tw.Write(data)
	tw.Close()
	gz.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	os.MkdirAll(dest, 0o755)
	if err := extractArchive(context.Background(), tp, dest, ""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "Game.sfc")); err != nil || string(b) != "SNESROM" {
		t.Errorf("Game.sfc = %q, %v", b, err)
	}
}

func TestExtractArchive_GzipSingle(t *testing.T) {
	dir := t.TempDir()
	gp := filepath.Join(dir, "Game.bin.gz")
	f, _ := os.Create(gp)
	gz := gzip.NewWriter(f)
	gz.Write([]byte("GENESIS"))
	gz.Close()
	f.Close()

	dest := filepath.Join(dir, "out")
	os.MkdirAll(dest, 0o755)
	if err := extractArchive(context.Background(), gp, dest, ""); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "Game.bin")); err != nil || string(b) != "GENESIS" {
		t.Errorf("Game.bin = %q, %v", b, err)
	}
}

func TestSanitizeEntryName(t *testing.T) {
	cases := map[string]string{
		"a/b.nes":          filepath.Join("a", "b.nes"),
		"/abs/x.nes":       filepath.Join("abs", "x.nes"),
		`win\path\x.nes`:   filepath.Join("win", "path", "x.nes"),
		"../escape":        "",
		"../../etc/passwd": "",
		"":                 "",
		".":                "",
	}
	for in, want := range cases {
		if got := sanitizeEntryName(in); got != want {
			t.Errorf("sanitizeEntryName(%q) = %q, want %q", in, got, want)
		}
	}
}
