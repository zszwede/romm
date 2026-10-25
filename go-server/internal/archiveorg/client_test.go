package archiveorg

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClient_ListGames_Item(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/MyColl", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"files":[
			{"name":"Mario.nes","source":"original","size":"40960"},
			{"name":"cover.png","source":"original","size":"10"}
		]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Base: srv.URL}

	games, kind, err := c.ListGames(context.Background(), "https://archive.org/download/MyColl", map[string]bool{"nes": true})
	if err != nil {
		t.Fatal(err)
	}
	if kind != "item" || len(games) != 1 || games[0].Name != "Mario.nes" {
		t.Fatalf("games=%+v kind=%s", games, kind)
	}
}

func TestClient_ListGames_SingleFile(t *testing.T) {
	c := &Client{Base: "https://archive.org"} // no fetch for a direct file
	games, kind, err := c.ListGames(context.Background(), "https://archive.org/download/MyColl/Zelda.nsp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if kind != "item" || len(games) != 1 || games[0].Name != "Zelda.nsp" ||
		games[0].SourceURL != "https://archive.org/download/MyColl/Zelda.nsp" {
		t.Fatalf("games=%+v kind=%s", games, kind)
	}
}

func TestClient_ListGames_LargeArchiveEnumerated(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metadata/Big", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"files":[{"name":"pack.zip","source":"original","size":"134217728"}]}`))
	})
	// The download URL with a trailing slash serves the member listing.
	mux.HandleFunc("/download/Big/pack.zip/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<a href="//archive.org/download/Big/pack.zip/A.nes">A.nes</a><td id="size">100</td>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := &Client{HTTP: srv.Client(), Base: srv.URL}

	games, kind, err := c.ListGames(context.Background(), srv.URL+"/download/Big", map[string]bool{"nes": true})
	if err != nil {
		t.Fatal(err)
	}
	if kind != "archive" || len(games) != 1 || !strings.Contains(games[0].Name, "A.nes") {
		t.Fatalf("games=%+v kind=%s", games, kind)
	}
}
