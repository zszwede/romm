package archiveorg

import (
	"encoding/json"
	"testing"
)

func TestParseSourceURL(t *testing.T) {
	cases := []struct {
		url      string
		id       string
		archive  string
		file     string
		wantErr  bool
	}{
		{url: "https://archive.org/download/MyCollection", id: "MyCollection"},
		{url: "https://archive.org/download/My-ID/game.nsp", id: "My-ID", file: "game.nsp"},
		{url: "https://archive.org/download/My-ID/pack.zip/", id: "My-ID", archive: "pack.zip"},
		{url: "https://ia600502.us.archive.org/view_archive.php?archive=/12/items/PSNStuff/games.zip", id: "PSNStuff", archive: "games.zip"},
		{url: "https://example.com/not-archive", wantErr: true},
	}
	for _, c := range cases {
		got, err := ParseSourceURL(c.url)
		if c.wantErr {
			if err == nil {
				t.Errorf("%s: expected error", c.url)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", c.url, err)
			continue
		}
		if got.Identifier != c.id || got.ArchivePath != c.archive || got.FilePath != c.file {
			t.Errorf("%s -> %+v, want id=%q archive=%q file=%q", c.url, got, c.id, c.archive, c.file)
		}
	}
}

func TestGamesFromMetadata(t *testing.T) {
	// A metadata doc with: two ROMs, a derived file, an internal file, and the
	// metadata manifest — only the two originals that pass `keep` survive.
	raw := `{"files":[
		{"name":"Mario (USA).nsp","source":"original","size":"104857600"},
		{"name":"Zelda (USA).nsp","source":"original","size":"52428800"},
		{"name":"derived.nsp","source":"derivative","size":"100"},
		{"name":"__ia_thumb.jpg","source":"original","size":"100"},
		{"name":"Coll_meta.xml","source":"original","size":"100"},
		{"name":"images.zip","source":"original","size":"999999999"}
	]}`
	var meta map[string]any
	if err := json.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatal(err)
	}
	games, archive := GamesFromMetadata(meta, "Coll", map[string]bool{"nsp": true})
	if archive != "" {
		t.Fatalf("unexpected multi-archive: %q", archive)
	}
	if len(games) != 2 {
		t.Fatalf("games = %d (%+v), want 2", len(games), games)
	}
	if games[0].Name != "Mario (USA).nsp" || games[0].SourceURL != "https://archive.org/download/Coll/Mario%20%28USA%29.nsp" {
		t.Errorf("game 0 wrong: %+v", games[0])
	}
	if games[0].Size == nil || *games[0].Size != 104857600 {
		t.Errorf("game 0 size: %v", games[0].Size)
	}
}

func TestGamesFromMetadata_SingleLargeArchive(t *testing.T) {
	raw := `{"files":[{"name":"wholeset.zip","source":"original","size":"134217728"}]}`
	var meta map[string]any
	json.Unmarshal([]byte(raw), &meta)
	games, archive := GamesFromMetadata(meta, "Coll", nil)
	if len(games) != 0 || archive != "wholeset.zip" {
		t.Fatalf("games=%v archive=%q, want 0 games + wholeset.zip", games, archive)
	}
}

func TestGamesFromMetadata_SmallArchiveKept(t *testing.T) {
	// A small zip is kept as a single game, not enumerated.
	raw := `{"files":[{"name":"onegame.zip","source":"original","size":"1048576"}]}`
	var meta map[string]any
	json.Unmarshal([]byte(raw), &meta)
	games, archive := GamesFromMetadata(meta, "Coll", nil)
	if archive != "" || len(games) != 1 || games[0].Name != "onegame.zip" {
		t.Fatalf("games=%v archive=%q, want 1 game onegame.zip", games, archive)
	}
}

func TestGamesFromArchiveListing(t *testing.T) {
	htmlDoc := `<table>
	<tr><td><a href="//archive.org/download/Coll/pack.zip/A%20Game.nes">A Game.nes</a></td><td id="size">40960</td></tr>
	<tr><td><a href="//archive.org/download/Coll/pack.zip/__ia_x.jpg">__ia_x.jpg</a></td><td id="size">10</td></tr>
	<tr><td><a href="//archive.org/download/Coll/pack.zip/readme.txt">readme.txt</a></td><td id="size">5</td></tr>
	</table>`
	games := GamesFromArchiveListing(htmlDoc, map[string]bool{"nes": true})
	if len(games) != 1 {
		t.Fatalf("games = %d (%+v), want 1 (nes only)", len(games), games)
	}
	if games[0].Name != "A Game.nes" || games[0].SourceURL != "https://archive.org/download/Coll/pack.zip/A%20Game.nes" {
		t.Errorf("member wrong: %+v", games[0])
	}
	if games[0].Size == nil || *games[0].Size != 40960 {
		t.Errorf("member size: %v", games[0].Size)
	}
}

func TestKeepRules(t *testing.T) {
	exts := map[string]bool{"nes": true}
	for name, want := range map[string]bool{
		"game.nes":       true,
		"pack.zip":       true, // archive always kept
		"__ia_thumb.jpg": false,
		"images.zip":     false,
		"meta.xml":       false,
		"cover.png":      false,
		"other.sfc":      false, // not in rom exts
		"noext":          false,
	} {
		if got := keep(name, exts); got != want {
			t.Errorf("keep(%q) = %v, want %v", name, got, want)
		}
	}
}
