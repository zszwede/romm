package pipeline

import (
	"regexp"
	"os"
	"path/filepath"
	"testing"
)

var rarMagic = []byte("Rar!\x1a\x07\x01\x00")

func writeRar(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, append(rarMagic, make([]byte, 16)...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestClassify_PartNNoExt(t *testing.T) {
	dir := t.TempDir()
	e := &extract{never: compileNever(t)}
	p1 := filepath.Join(dir, "Go Vacation - Base Game NSP - Part1")
	p2 := filepath.Join(dir, "Go Vacation - Base Game NSP - Part2")
	writeRar(t, p1)
	writeRar(t, p2)
	if got := e.classify(p1); got != "first" {
		t.Errorf("Part1 classify = %q, want first", got)
	}
	if got := e.classify(p2); got != "volume" {
		t.Errorf("Part2 classify = %q, want volume", got)
	}
}

func TestRarVolumeSet(t *testing.T) {
	dir := t.TempDir()
	p1 := filepath.Join(dir, "Game - Part1")
	p2 := filepath.Join(dir, "Game - Part2")
	p3 := filepath.Join(dir, "Game - Part3")
	for _, p := range []string{p1, p3, p2} { // out of order on disk
		writeRar(t, p)
	}
	vols, multi := rarVolumeSet(p1)
	if !multi || len(vols) != 3 {
		t.Fatalf("got multi=%v vols=%v", multi, vols)
	}
	if filepath.Base(vols[0]) != "Game - Part1" || filepath.Base(vols[2]) != "Game - Part3" {
		t.Errorf("volumes not ordered: %v", vols)
	}
	// A single part -> not a multi-volume set.
	solo := filepath.Join(t.TempDir(), "Solo - Part1")
	writeRar(t, solo)
	if _, multi := rarVolumeSet(solo); multi {
		t.Error("single Part1 should not be multi")
	}
	// Standard .partN.rar -> rardecode handles it, not us.
	std := filepath.Join(dir, "x.part1.rar")
	writeRar(t, std)
	if _, multi := rarVolumeSet(std); multi {
		t.Error("standard .part1.rar should be left to rardecode")
	}
}

func compileNever(t *testing.T) []*regexp.Regexp {
	r, err := compileAll(defaultNeverExtract)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
