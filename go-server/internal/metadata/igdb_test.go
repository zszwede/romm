package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

func TestIGDB_Search(t *testing.T) {
	var tokenHits atomic.Int64
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenHits.Add(1)
		if r.URL.Query().Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.URL.Query().Get("grant_type"))
		}
		w.Write([]byte(`{"access_token":"tok123","expires_in":3600}`))
	}))
	defer tokenSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Client-ID") != "cid" {
			t.Errorf("Client-ID = %q", r.Header.Get("Client-ID"))
		}
		if r.Header.Get("Authorization") != "Bearer tok123" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`[{"id":1022,"name":"The Legend of Zelda","summary":"A link.","cover":{"url":"//images.igdb.com/igdb/image/upload/t_thumb/abc.jpg"}}]`))
	}))
	defer apiSrv.Close()

	g := NewIGDB("cid", "secret")
	g.tokenURL = tokenSrv.URL
	g.apiURL = apiSrv.URL

	games, err := g.Search(context.Background(), "zelda")
	if err != nil {
		t.Fatal(err)
	}
	if len(games) != 1 {
		t.Fatalf("got %d games", len(games))
	}
	got := games[0]
	if got.ProviderID != 1022 || got.Name != "The Legend of Zelda" || got.Provider != "igdb" {
		t.Errorf("game = %+v", got)
	}
	if got.CoverURL != "https://images.igdb.com/igdb/image/upload/t_1080p/abc.jpg" {
		t.Errorf("cover = %q", got.CoverURL)
	}

	// Second search reuses the cached token (token endpoint hit once).
	if _, err := g.Search(context.Background(), "mario"); err != nil {
		t.Fatal(err)
	}
	if n := tokenHits.Load(); n != 1 {
		t.Errorf("token endpoint hit %d times, want 1 (cached)", n)
	}
}

func TestIGDB_APIError(t *testing.T) {
	tokenSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"access_token":"t","expires_in":3600}`))
	}))
	defer tokenSrv.Close()
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusTooManyRequests)
	}))
	defer apiSrv.Close()

	g := NewIGDB("cid", "secret")
	g.tokenURL, g.apiURL = tokenSrv.URL, apiSrv.URL
	if _, err := g.Search(context.Background(), "x"); err == nil {
		t.Error("expected error on API 429")
	}
}

// TestIGDB_Live hits the real IGDB API; skipped unless credentials are set.
func TestIGDB_Live(t *testing.T) {
	id, sec := os.Getenv("IGDB_CLIENT_ID"), os.Getenv("IGDB_CLIENT_SECRET")
	if id == "" || sec == "" {
		t.Skip("IGDB_CLIENT_ID/SECRET not set")
	}
	games, err := NewIGDB(id, sec).Search(context.Background(), "the legend of zelda")
	if err != nil {
		t.Fatalf("live search: %v", err)
	}
	if len(games) == 0 {
		t.Fatal("live search returned no games")
	}
	t.Logf("live IGDB returned %d games; first: %q (cover %v)", len(games), games[0].Name, games[0].CoverURL != "")
}
