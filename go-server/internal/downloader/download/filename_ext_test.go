package download

import (
	"net/http"
	"net/url"
	"testing"
)

// A source (archive.org) names the link after the game title with no extension,
// but the URL is a .nsz. fileName must append the extension so the Switch
// filter keeps the file instead of dropping "Hello Engineer".
func TestFileName_BorrowsExtensionFromURL(t *testing.T) {
	u, _ := url.Parse("https://archive.org/download/backup_nsz_base/Hello%20Engineer%20%5B0100160017A80000%5D%5BUS%5D%5Bv0%5D.nsz")
	resp := &http.Response{Header: http.Header{}, Request: &http.Request{URL: u}}
	link := Link{Name: "Hello Engineer", URL: u.String()}
	if got := fileName(link, resp, "000"); got != "Hello Engineer.nsz" {
		t.Errorf("fileName = %q, want %q", got, "Hello Engineer.nsz")
	}
}

// A preferred name that already has the right extension is left untouched.
func TestFileName_KeepsExistingExtension(t *testing.T) {
	u, _ := url.Parse("https://1fichier.com/?abc")
	resp := &http.Response{Header: http.Header{}, Request: &http.Request{URL: u}}
	link := Link{Name: "Game.nsz", URL: u.String()}
	if got := fileName(link, resp, "000"); got != "Game.nsz" {
		t.Errorf("fileName = %q, want Game.nsz", got)
	}
}
