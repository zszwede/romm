package download

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
)

func TestMatchHost(t *testing.T) {
	cases := []struct {
		pat  []string
		host string
		want bool
	}{
		{[]string{"archive.org"}, "archive.org", true},
		{[]string{"archive.org"}, "ia800.us.archive.org", true},
		{[]string{"archive.org"}, "notarchive.org", false},
		{[]string{"*.1fichier.com"}, "1fichier.com", true},
		{[]string{"*.1fichier.com"}, "a-7.1fichier.com", true},
		{[]string{"*"}, "x.y", true},
	}
	for _, c := range cases {
		if got := MatchHost(c.pat, c.host); got != c.want {
			t.Errorf("MatchHost(%v, %q) = %v", c.pat, c.host, got)
		}
	}
}

func TestAPIURL(t *testing.T) {
	if got := apiURL("https://1fichier.com/?abc123&af=99"); got != "https://1fichier.com/?abc123" {
		t.Fatal(got)
	}
	if got := apiURL("https://1fichier.com/?abc123?af=99"); got != "https://1fichier.com/?abc123" {
		t.Fatal(got)
	}
}

func logger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestOneFichier(t *testing.T) {
	payload := strings.Repeat("x", 5000)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api":
			if r.Header.Get("Authorization") != "Bearer KEY" {
				w.WriteHeader(401)
				io.WriteString(w, `{"status":"KO","message":"bad key"}`)
				return
			}
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["url"] != "https://1fichier.com/?abc" {
				t.Errorf("api got url %q", body["url"])
			}
			io.WriteString(w, `{"status":"OK","url":"`+srv.URL+`/c12345"}`)
		case "/c12345":
			if r.Header.Get("Authorization") != "" {
				t.Error("direct link must not carry the API key")
			}
			w.Header().Set("Content-Disposition", `attachment; filename="Game [0100ABC][v0].part1.rar"`)
			http.ServeContent(w, r, "", time.Time{}, strings.NewReader(payload))
		}
	}))
	defer srv.Close()

	m, err := NewManager([]config.Host{{
		Name: "1f", Type: "onefichier", Match: []string{"1fichier.com"}, Token: "Bearer KEY",
		APIURL: srv.URL + "/api", APIPause: time.Millisecond, Retries: 1, RetryDelay: time.Millisecond,
	}}, logger())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// a partial file from an interrupted run must be resumed
	os.WriteFile(filepath.Join(dir, ".dl-000.part"), []byte(payload[:1000]), 0o644)
	p, err := m.Fetch(context.Background(), Link{URL: "https://1fichier.com/?abc&af=1"}, dir, "000", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "Game [0100ABC][v0].part1.rar" {
		t.Fatalf("name %q", filepath.Base(p))
	}
	b, _ := os.ReadFile(p)
	if string(b) != payload {
		t.Fatalf("content mismatch: %d bytes", len(b))
	}
	// second call is served from the done marker
	p2, err := m.Fetch(context.Background(), Link{URL: "https://1fichier.com/?abc"}, dir, "000", nil)
	if err != nil || p2 != p {
		t.Fatalf("re-fetch: %v %v", p2, err)
	}
}

func TestArchiveOrgAuthAndRedirect(t *testing.T) {
	const auth = "LOW AK:SK"
	var leaked bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked = true
		}
		io.WriteString(w, "foreign")
	}))
	defer other.Close()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != auth {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/download/item/Some%20Game.chd", "/download/item/Some Game.chd":
			http.Redirect(w, r, srv.URL+"/node/items/item/Some%20Game.chd", http.StatusFound)
		case "/node/items/item/Some Game.chd":
			io.WriteString(w, "chd-data")
		case "/download/item/away.bin":
			// storage on a host outside the match list: auth must be dropped
			http.Redirect(w, r, strings.Replace(other.URL, "127.0.0.1", "localhost", 1)+"/f.bin", http.StatusFound)
		}
	}))
	defer srv.Close()

	m, err := NewManager([]config.Host{{
		Name: "ia", Type: "archive_org", Match: []string{"127.0.0.1"}, AccessKey: "AK", SecretKey: "SK",
		Retries: 1, RetryDelay: time.Millisecond,
	}}, logger())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	p, err := m.Fetch(context.Background(), Link{URL: srv.URL + "/download/item/Some%20Game.chd"}, dir, "000", nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(p) != "Some Game.chd" {
		t.Fatalf("name %q", filepath.Base(p))
	}
	if _, err := m.Fetch(context.Background(), Link{URL: srv.URL + "/download/item/away.bin"}, dir, "001", nil); err != nil {
		t.Fatal(err)
	}
	if leaked {
		t.Fatal("Authorization header leaked to a non-matching redirect host")
	}
}

func TestPermanentErrorNoRetry(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(404)
	}))
	defer srv.Close()
	m, _ := NewManager([]config.Host{{Name: "h", Type: "http", Match: []string{"*"}, Retries: 3, RetryDelay: time.Millisecond}}, logger())
	if _, err := m.Fetch(context.Background(), Link{URL: srv.URL + "/x.zip"}, t.TempDir(), "000", nil); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("404 retried: %d calls", calls)
	}
}
