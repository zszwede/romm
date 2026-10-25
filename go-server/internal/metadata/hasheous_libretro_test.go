package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHasheous_LookupByHash(t *testing.T) {
	var gotKey, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Client-API-Key")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":42,"name":"Super Mario World",
			"metadata":[{"source":"IGDB","immutableId":"1068"},{"source":"TheGamesDb","immutableId":"slug-not-id"}],
			"attributes":[{"attributeName":"Logo","link":"/images/mario.png"}]}`))
	}))
	defer srv.Close()

	h := NewHasheous()
	h.baseURL = srv.URL
	h.origin = srv.URL
	m, err := h.LookupByHash(context.Background(), "abc", "def", "123")
	if err != nil {
		t.Fatal(err)
	}
	if m == nil {
		t.Fatal("expected a match")
	}
	if m.HasheousID == nil || *m.HasheousID != 42 {
		t.Errorf("hasheous id = %v", m.HasheousID)
	}
	if m.IgdbID == nil || *m.IgdbID != 1068 {
		t.Errorf("igdb id = %v", m.IgdbID)
	}
	if m.TgdbID != nil { // slug, not numeric -> dropped
		t.Errorf("tgdb id should be nil (slug), got %v", *m.TgdbID)
	}
	if !strings.Contains(m.CoverURL, "/images/mario.png") {
		t.Errorf("cover = %s", m.CoverURL)
	}
	if gotKey == "" {
		t.Error("API key header missing")
	}
	for _, want := range []string{`"mD5":"abc"`, `"shA1":"def"`, `"crc":"123"`} {
		if !strings.Contains(gotBody, want) {
			t.Errorf("body missing %s: %s", want, gotBody)
		}
	}
}

func TestHasheous_NoMatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`null`))
	}))
	defer srv.Close()
	h := NewHasheous()
	h.baseURL = srv.URL
	m, err := h.LookupByHash(context.Background(), "x", "", "")
	if err != nil || m != nil {
		t.Errorf("expected (nil,nil), got (%v,%v)", m, err)
	}
}

func TestLibretro_CoverFor(t *testing.T) {
	listing := `<html><body>
	<a href="?C=N;O=D">Name</a>
	<a href="../">Parent</a>
	<a href="Super%20Mario%20World%20%28USA%29.png">x</a>
	<a href="Zelda%20no%20Densetsu%20%28Japan%29.png">y</a>
	</body></html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "Named_Boxarts") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Write([]byte(listing))
	}))
	defer srv.Close()

	l := NewLibretro()
	l.root = srv.URL

	// Exact match (tags included) on snes.
	cov := l.CoverFor(context.Background(), "Super Mario World (USA).sfc", "snes")
	if cov == nil {
		t.Fatal("expected a cover match")
	}
	if !strings.Contains(cov.CoverURL, "Super%20Mario%20World%20%28USA%29.png") {
		t.Errorf("cover url = %s", cov.CoverURL)
	}
	if len(cov.ID) != 40 { // sha1 hex
		t.Errorf("libretro id = %q", cov.ID)
	}

	// Fuzzy fallback: tag-stripped containment.
	cov2 := l.CoverFor(context.Background(), "Super Mario World (Europe).sfc", "snes")
	if cov2 == nil || !strings.Contains(cov2.CoverURL, "Super%20Mario%20World") {
		t.Errorf("fuzzy match failed: %v", cov2)
	}

	// Unmapped platform -> nil.
	if l.CoverFor(context.Background(), "x", "no-such-platform") != nil {
		t.Error("unmapped platform should return nil")
	}
}
