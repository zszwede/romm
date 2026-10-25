package httpapi

import (
	"database/sql"
	"testing"

	"github.com/zszwede/romm-go/internal/store"
)

// TestBuildDownloadLinks_CollapseBaseVariants reproduces the God Eater 3 case:
// the base game is listed BOTH as a standalone .nsz and as a multi-part RAR of
// the NSP (both category "base"), plus an update and a DLC. Only one base
// packaging must survive — the multi-part NSP set (uncompressed, same host) —
// while the update and DLC pass through untouched.
func TestBuildDownloadLinks_CollapseBaseVariants(t *testing.T) {
	vc := `[
	  {"url":"https://1fichier.com/?nsz","name":"God Eater 3.nsz","category":"base","host":"1fichier.com"},
	  {"url":"https://1fichier.com/?p1","name":"God Eater 3 - Base Game NSP - Part1","category":"base","host":"1fichier.com","part":1},
	  {"url":"https://1fichier.com/?p2","name":"God Eater 3 - Base Game NSP - Part2","category":"base","host":"1fichier.com","part":2},
	  {"url":"https://1fichier.com/?upd","name":"God Eater 3 v720896.nsz","category":"update","host":"1fichier.com"},
	  {"url":"https://1fichier.com/?dlc","name":"God Eater 3 DLC.rar","category":"dlc","host":"1fichier.com"}
	]`
	rom := &store.RomFull{
		Name:            sql.NullString{String: "God Eater 3", Valid: true},
		VirtualContents: sql.NullString{String: vc, Valid: true},
	}
	links, mirrors := buildDownloadLinks(rom, []string{"1fichier.com", "archive.org"})
	if mirrors {
		t.Fatalf("mirrors should be false when virtual_contents is present")
	}
	// The standalone .nsz base must be dropped; the two RAR parts kept.
	var gotURLs []string
	for _, l := range links {
		gotURLs = append(gotURLs, l.URL)
	}
	for _, u := range gotURLs {
		if u == "https://1fichier.com/?nsz" {
			t.Fatalf("standalone .nsz base should have been collapsed out, got %v", gotURLs)
		}
	}
	want := []string{
		"https://1fichier.com/?p1",
		"https://1fichier.com/?p2",
		"https://1fichier.com/?upd",
		"https://1fichier.com/?dlc",
	}
	if len(gotURLs) != len(want) {
		t.Fatalf("got %d links %v, want %d %v", len(gotURLs), gotURLs, len(want), want)
	}
	for i := range want {
		if gotURLs[i] != want[i] {
			t.Fatalf("link %d: got %s, want %s (all: %v)", i, gotURLs[i], want[i], gotURLs)
		}
	}
}

// TestBuildDownloadLinks_PrefersEnglishRegion reproduces the Atelier case: one
// rom's virtual_contents lists a US and an Asia base set (multi-part, distinct
// "group"s) plus a US and an Asia update. The download must keep only the US
// (English) base parts and the US update — not the Asian copies.
func TestBuildDownloadLinks_PrefersEnglishRegion(t *testing.T) {
	vc := `[
	  {"url":"https://1fichier.com/?us1","name":"Game (US) NSP part1","category":"base","group":"0:0:0","part":1},
	  {"url":"https://1fichier.com/?us2","name":"Game (US) NSP part2","category":"base","group":"0:0:0","part":2},
	  {"url":"https://1fichier.com/?as1","name":"Game (AS) NSP part1","category":"base","group":"0:0:1","part":1},
	  {"url":"https://1fichier.com/?as2","name":"Game (AS) NSP part2","category":"base","group":"0:0:1","part":2},
	  {"url":"https://1fichier.com/?uupd","name":"Game (US) Update 1.1.0","category":"update","group":"0:2:0","version":"1.1.0"},
	  {"url":"https://1fichier.com/?aupd","name":"Game (AS) Update 1.1.0","category":"update","group":"0:3:0","version":"1.1.0"}
	]`
	rom := &store.RomFull{
		Name:            sql.NullString{String: "Game", Valid: true},
		VirtualContents: sql.NullString{String: vc, Valid: true},
	}
	links, _ := buildDownloadLinks(rom, []string{"1fichier.com"})
	got := map[string]bool{}
	for _, l := range links {
		got[l.URL] = true
	}
	for _, as := range []string{"https://1fichier.com/?as1", "https://1fichier.com/?as2", "https://1fichier.com/?aupd"} {
		if got[as] {
			t.Fatalf("Asian variant %s should have been dropped; got %v", as, keysOf(got))
		}
	}
	for _, us := range []string{"https://1fichier.com/?us1", "https://1fichier.com/?us2", "https://1fichier.com/?uupd"} {
		if !got[us] {
			t.Fatalf("US variant %s should have been kept; got %v", us, keysOf(got))
		}
	}
}

func keysOf(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestBuildDownloadLinks_SingleMultipartKept ensures a base that is ONLY a
// multi-part set (one packaging) keeps every part — collapse must not fire.
func TestBuildDownloadLinks_SingleMultipartKept(t *testing.T) {
	vc := `[
	  {"url":"https://1fichier.com/?a","name":"Go Vacation Part1","category":"base","part":1},
	  {"url":"https://1fichier.com/?b","name":"Go Vacation Part2","category":"base","part":2},
	  {"url":"https://1fichier.com/?c","name":"Go Vacation Part3","category":"base","part":3}
	]`
	rom := &store.RomFull{
		Name:            sql.NullString{String: "Go Vacation", Valid: true},
		VirtualContents: sql.NullString{String: vc, Valid: true},
	}
	links, _ := buildDownloadLinks(rom, []string{"1fichier.com"})
	if len(links) != 3 {
		t.Fatalf("multi-part base must keep all 3 parts, got %d: %v", len(links), links)
	}
}

// TestPackagingKey checks part detection and stem grouping.
func TestPackagingKey(t *testing.T) {
	cases := []struct {
		name     string
		wantKey  string
		wantPart int
	}{
		{"God Eater 3.nsz", "god eater 3", 1},
		{"God Eater 3 - Base Game NSP - Part1", "god eater 3 - base game nsp", 1},
		{"God Eater 3 - Base Game NSP - Part2", "god eater 3 - base game nsp", 2},
		{"Some Game Part 3.rar", "some game", 3},
	}
	for _, c := range cases {
		k, p := packagingKey(c.name)
		if k != c.wantKey || p != c.wantPart {
			t.Errorf("packagingKey(%q) = (%q,%d), want (%q,%d)", c.name, k, p, c.wantKey, c.wantPart)
		}
	}
	// The two RAR parts must share a key; the standalone must differ.
	kp1, _ := packagingKey("God Eater 3 - Base Game NSP - Part1")
	kp2, _ := packagingKey("God Eater 3 - Base Game NSP - Part2")
	kn, _ := packagingKey("God Eater 3.nsz")
	if kp1 != kp2 {
		t.Errorf("RAR parts should share a packaging key: %q vs %q", kp1, kp2)
	}
	if kn == kp1 {
		t.Errorf("standalone and RAR set should differ: %q", kn)
	}
}
