package scan

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type fakeSink struct {
	recs     []Record
	enriched []string
}

func (f *fakeSink) Upsert(_ context.Context, r Record) error {
	f.recs = append(f.recs, r)
	return nil
}
func (f *fakeSink) EnrichFromGamelist(_ context.Context, _ int64, fsName, name, summary string) error {
	f.enriched = append(f.enriched, fsName+"|"+name+"|"+summary)
	return nil
}

func TestStripTags(t *testing.T) {
	for in, want := range map[string]string{
		"Bayonetta (USA) (En,Fr)": "Bayonetta",
		"Game [0100ABC][US][v0]":  "Game",
		"Plain Name":              "Plain Name",
	} {
		if got := stripTags(in); got != want {
			t.Errorf("stripTags(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPlatforms(t *testing.T) {
	root := t.TempDir()
	// platform "switch" with two roms + noise; platform "nes" with one.
	mustDir(t, filepath.Join(root, "switch"))
	mustDir(t, filepath.Join(root, "switch", "media")) // skipped (dir)
	mustFile(t, filepath.Join(root, "switch", "Zelda (USA).nsp"), "hello")
	mustFile(t, filepath.Join(root, "switch", "Mario.nsp"), "xyz")
	mustFile(t, filepath.Join(root, "switch", "gamelist.xml"), "<x/>") // skipped
	mustFile(t, filepath.Join(root, "switch", ".DS_Store"), "junk")    // skipped
	mustDir(t, filepath.Join(root, "nes"))
	mustFile(t, filepath.Join(root, "nes", "Metroid.nes"), "data")

	plats := []PlatformDir{{ID: 28, FsSlug: "switch"}, {ID: 5, FsSlug: "nes"}, {ID: 9, FsSlug: "absent"}}
	sink := &fakeSink{}
	res, err := Platforms(context.Background(), root, plats, sink, true)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found != 3 || res.Upserted != 3 {
		t.Fatalf("result = %+v, want 3 found/upserted", res)
	}
	byName := map[string]Record{}
	for _, r := range sink.recs {
		byName[r.FsName] = r
	}
	z := byName["Zelda (USA).nsp"]
	if z.PlatformID != 28 || z.FsExtension != "nsp" || z.FsNameNoExt != "Zelda (USA)" ||
		z.FsNameNoTags != "Zelda" || z.FsPath != filepath.Join("roms", "switch") {
		t.Errorf("Zelda record wrong: %+v", z)
	}
	// "hello" is 5 bytes with a known CRC (see hash package vectors).
	if z.Digests.CRC32 != "3610a686" || z.SizeBytes != 5 {
		t.Errorf("Zelda digest/size wrong: %+v size=%d", z.Digests, z.SizeBytes)
	}
	if byName["Metroid.nes"].PlatformID != 5 {
		t.Errorf("Metroid platform wrong")
	}
	if _, ok := byName["gamelist.xml"]; ok {
		t.Error("gamelist.xml should be skipped")
	}
}

func TestGamelistEnrich(t *testing.T) {
	root := t.TempDir()
	mustDir(t, filepath.Join(root, "n64"))
	mustFile(t, filepath.Join(root, "n64", "GoldenEye (USA).n64"), "rom")
	mustFile(t, filepath.Join(root, "n64", "gamelist.xml"),
		`<?xml version="1.0"?><gameList>`+
			`<game><path>./GoldenEye (USA).n64</path><name>GoldenEye 007</name><desc>Bond.</desc></game>`+
			`<game><path>./Absent.n64</path><name>Ignored Name</name></game>`+
			`</gameList>`)

	sink := &fakeSink{}
	res, err := Platforms(context.Background(), root, []PlatformDir{{ID: 9, FsSlug: "n64"}}, sink, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Found != 1 {
		t.Errorf("found = %d, want 1 (gamelist.xml not counted as a rom)", res.Found)
	}
	if res.Enriched != 2 || len(sink.enriched) != 2 {
		t.Fatalf("enriched = %d / %d, want 2", res.Enriched, len(sink.enriched))
	}
	if sink.enriched[0] != "GoldenEye (USA).n64|GoldenEye 007|Bond." {
		t.Errorf("enrich[0] = %q", sink.enriched[0])
	}
}

func mustDir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}
func mustFile(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
