package httpapi

import (
	"database/sql"
	"testing"

	"github.com/zszwede/romm-go/internal/store"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestRomJSON_DetailExtras(t *testing.T) {
	r := &store.RomFull{
		ID: 7, FsName: "Game.nsp", FsPath: "roms/switch",
		IGDBID:    sql.NullInt64{Int64: 100, Valid: true},
		SourceURL: ns("https://1fichier.com/?abc"),
	}
	r.Mirrors = []string{"https://archive.org/x", "https://1fichier.com/?abc"} // dup dropped
	extras := &romExtras{
		Files: []store.RomFile{
			{ID: 1, RomID: 7, FileName: "Game.nsp", FilePath: "roms/switch", FileSizeBytes: 500},
		},
		Siblings: []store.SiblingRom{
			{ID: 8, Name: ns("Game (EU)"), FsNameNoTags: "Game", FsNameNoExt: "Game (EU)", IsMainSibling: true},
		},
		Meta: &store.RomMetadata{
			IGDB:            ns(`{"total_rating":88}`),
			SS:              ns(`{"id":5}`),
			PathScreenshots: ns(`["roms/28/7/screenshots/0.jpg","roms/28/7/screenshots/1.jpg"]`),
		},
	}
	m := romJSON(r, nil, 1, true, extras)

	// files
	files := m["files"].([]map[string]any)
	if len(files) != 1 || files[0]["file_name"] != "Game.nsp" {
		t.Errorf("files = %v", files)
	}
	if files[0]["full_path"] != "roms/switch/Game.nsp" {
		t.Errorf("full_path = %v", files[0]["full_path"])
	}
	if m["has_simple_single_file"] != true || m["has_multiple_files"] != false {
		t.Errorf("file flags wrong: %v / %v", m["has_simple_single_file"], m["has_multiple_files"])
	}
	// siblings
	sibs := m["sibling_roms"].([]map[string]any)
	if len(sibs) != 1 || sibs[0]["is_main_sibling"] != true {
		t.Errorf("siblings = %v", sibs)
	}
	// provider metadata passed through
	if m["igdb_metadata"] == nil || m["ss_metadata"] == nil {
		t.Errorf("metadata not populated: igdb=%v ss=%v", m["igdb_metadata"], m["ss_metadata"])
	}
	// merged screenshots -> resource URLs
	shots := m["merged_screenshots"].([]string)
	if len(shots) != 2 || shots[0] != "/assets/romm/resources/roms/28/7/screenshots/0.jpg" {
		t.Errorf("merged_screenshots = %v", shots)
	}
	// source_urls: primary + mirror, dup removed
	urls := m["source_urls"].([]string)
	if len(urls) != 2 || urls[0] != "https://1fichier.com/?abc" || urls[1] != "https://archive.org/x" {
		t.Errorf("source_urls = %v", urls)
	}
}

func TestRomJSON_NoExtras(t *testing.T) {
	// The gallery path passes nil extras; files/siblings must be empty, not nil,
	// and the response must not panic.
	r := &store.RomFull{ID: 1, FsName: "x.nes", FsPath: "roms/nes"}
	m := romJSON(r, nil, 1, false, nil)
	if _, ok := m["files"].([]map[string]any); !ok {
		t.Errorf("files should be an empty slice, got %T", m["files"])
	}
	if _, ok := m["sibling_roms"].([]map[string]any); !ok {
		t.Errorf("sibling_roms should be an empty slice, got %T", m["sibling_roms"])
	}
}
