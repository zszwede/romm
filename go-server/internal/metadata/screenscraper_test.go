package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSS_SearchPlatform(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/jeuRecherche.php") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"response":{"jeux":[
			{"id":"123","noms":[{"region":"jp","text":"Zeruda"},{"region":"us","text":"The Legend of Zelda"}],
			 "synopsis":[{"langue":"fr","text":"Une aventure"},{"langue":"en","text":"An adventure"}],
			 "medias":[{"type":"ss","region":"wor","url":"x"},{"type":"box-2D","region":"us","url":"https://api.screenscraper.fr/api2/media?ssid=secret&sspassword=pw&devid=d&media=box"}]},
			{"id":"0","noms":[{"region":"us","text":"bad"}]},
			{"id":"456","notgame":"true","noms":[{"region":"us","text":"BIOS"}]},
			{}
		]}}`))
	}))
	defer srv.Close()

	ss := NewSS("dev", "devpw", "user", "userpw")
	ss.apiURL = srv.URL
	games, err := ss.SearchPlatform(context.Background(), "zelda", 4)
	if err != nil {
		t.Fatal(err)
	}
	// id 0 dropped, notgame dropped, empty dropped -> 1 game.
	if len(games) != 1 {
		t.Fatalf("got %d games, want 1: %+v", len(games), games)
	}
	g := games[0]
	if g.Provider != "ss" || g.ProviderID != 123 {
		t.Errorf("provider/id = %s/%d", g.Provider, g.ProviderID)
	}
	// us ranks ahead of jp in the region priority.
	if g.Name != "The Legend of Zelda" {
		t.Errorf("name = %q", g.Name)
	}
	// en synopsis preferred over fr.
	if g.Summary != "An adventure" {
		t.Errorf("summary = %q", g.Summary)
	}
	// cover is the box-2D url with user creds stripped, dev creds kept.
	if strings.Contains(g.CoverURL, "ssid") || strings.Contains(g.CoverURL, "sspassword") {
		t.Errorf("cover leaks user creds: %s", g.CoverURL)
	}
	if !strings.Contains(g.CoverURL, "devid=d") {
		t.Errorf("cover lost dev creds: %s", g.CoverURL)
	}
	// request carried auth + systemeid.
	for _, want := range []string{"devid=dev", "softname=romm", "ssid=user", "systemeid=4", "recherche=zelda", "output=json"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query missing %q: %s", want, gotQuery)
		}
	}
}

func TestSS_GetByID_Error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte("Erreur : Faux identifiants"))
	}))
	defer srv.Close()
	ss := NewSS("dev", "devpw", "", "")
	ss.apiURL = srv.URL
	if _, err := ss.GetByID(context.Background(), 1, 0); err == nil {
		t.Fatal("expected error on 403 credentials body")
	}
	if !ss.HasDevCredentials() {
		t.Error("HasDevCredentials should be true")
	}
}
