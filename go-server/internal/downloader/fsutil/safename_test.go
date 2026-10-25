package fsutil

import (
	"strings"
	"testing"
)

func TestSafeName_ExfatReservedChars(t *testing.T) {
	cases := map[string]string{
		"Hitman: Absolution - Base Game NSP": "Hitman_ Absolution - Base Game NSP",
		"Hyrule Warriors: Age Of Calamity":   "Hyrule Warriors_ Age Of Calamity",
		"What/If\\It*Has?All\"<>|":            "What_If_It_Has_All____",
		"Normal Game [0100ABC][v0].nsp":      "Normal Game [0100ABC][v0].nsp",
		".hack G.U. Last Recode":             ".hack G.U. Last Recode", // leading dot kept
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	// No reserved char may survive (would break rename on exFAT).
	for _, bad := range []string{":", "*", "?", "\"", "<", ">", "|", "/", "\\"} {
		if strings.Contains(SafeName("a"+bad+"b"), bad) {
			t.Errorf("SafeName left reserved char %q", bad)
		}
	}
	// Trailing dots/spaces (illegal on FAT/NTFS) are trimmed.
	if got := SafeName("Trailing Dot."); got != "Trailing Dot" {
		t.Errorf("trailing dot not trimmed: %q", got)
	}
	// Degenerate names collapse to empty so the caller falls back.
	for _, in := range []string{"", "   ", ".", "..", "::"} {
		if got := SafeName(in); got != "" && got != "__" {
			t.Errorf("SafeName(%q) = %q, want empty-ish", in, got)
		}
	}
}
