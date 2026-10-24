package virtual

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/check" || r.Header.Get("Authorization") != "Bearer tkn" {
			t.Errorf("path=%s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		var in struct{ URLs []string }
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.URLs) != 2 {
			t.Errorf("urls = %v", in.URLs)
		}
		w.Write([]byte(`{"results":[
			{"url":"u1","available":true,"checked":true,"filename":"a.rar","size":10},
			{"url":"u2","available":false,"checked":true,"reason":"gone"}]}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tkn")
	res, err := c.Check(context.Background(), []string{"u1", "u2"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || !res[0].Available || res[0].Filename != "a.rar" || res[1].Available || res[1].Reason != "gone" {
		t.Errorf("results = %+v", res)
	}
}

func TestDispatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/jobs" {
			t.Errorf("path = %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		var p map[string]any
		json.Unmarshal(body, &p)
		if p["system"] != "switch" || p["name"] != "Game.nsp" {
			t.Errorf("payload system/name wrong: %v", p)
		}
		md := p["metadata"].(map[string]any)
		if md["rom_id"].(float64) != 35950 || md["platform_slug"] != "switch" {
			t.Errorf("metadata wrong: %v", md)
		}
		links := p["links"].([]any)
		if len(links) != 1 || links[0].(map[string]any)["category"] != "base" {
			t.Errorf("links wrong: %v", links)
		}
		w.Write([]byte(`{"id":"job-abc"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "")
	id, err := c.Dispatch(context.Background(), Job{
		System: "switch", Name: "Game.nsp", RomID: 35950, PlatformID: 28,
		Links: []Link{{URL: "https://1fichier.com/?x", Category: "base"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if id != "job-abc" {
		t.Errorf("id = %q, want job-abc", id)
	}
}

func TestDispatch_ServiceError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	if _, err := NewClient(srv.URL, "").Dispatch(context.Background(), Job{System: "x", Name: "y"}); err == nil {
		t.Error("expected error on 500")
	}
}
