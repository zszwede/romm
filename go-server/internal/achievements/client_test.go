package achievements

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestUserGameProgress(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("g") != "14402" || q.Get("u") != "xiam" || q.Get("y") != "key" {
			t.Errorf("query = %v", q)
		}
		// NumAchievements as a string, NumAwardedToUser as a number — RA mixes both.
		w.Write([]byte(`{"Title":"Super Mario World","NumAchievements":"56","NumAwardedToUser":12}`))
	}))
	defer srv.Close()

	c := NewClient("key")
	c.baseURL = srv.URL
	p, err := c.UserGameProgress(context.Background(), 14402, "xiam")
	if err != nil {
		t.Fatal(err)
	}
	if p.Title != "Super Mario World" || p.Total != 56 || p.Earned != 12 || p.GameID != 14402 {
		t.Errorf("progress = %+v", p)
	}
}

func TestUserGameProgress_NoKey(t *testing.T) {
	if _, err := NewClient("").UserGameProgress(context.Background(), 1, "u"); err == nil {
		t.Error("expected error without api key")
	}
}

func TestUserGameProgress_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusUnauthorized)
	}))
	defer srv.Close()
	c := NewClient("key")
	c.baseURL = srv.URL
	if _, err := c.UserGameProgress(context.Background(), 1, "u"); err == nil {
		t.Error("expected error on 401")
	}
}
