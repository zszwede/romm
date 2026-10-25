package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/zszwede/romm-go/internal/scan"
	"github.com/zszwede/romm-go/internal/worker"
)

func TestChunkedUpload_Assembles(t *testing.T) {
	root := t.TempDir() // acts as the scan root (library/roms)
	fs := &fakeStore{userID: 42, dirs: []scan.PlatformDir{{ID: 28, FsSlug: "switch"}}}
	q := worker.New(context.Background(), 1, 4)
	s := New(Deps{Store: fs, AuthSecret: testSecret, ScanRoot: filepath.Join(root, "roms"), Queue: q})
	tok := token(t, testSecret, "access", "romm:oauth")

	// start
	r := httptest.NewRequest("POST", "/api/roms/upload/start", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	r.Header.Set("x-upload-filename", "Test (USA).nsp")
	r.Header.Set("x-upload-platform-id", "28") // fakeStore dir fsSlug=switch
	r.Header.Set("x-upload-total-chunks", "2")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	id := decodeBody(t, w.Body.Bytes())["upload_id"].(string)

	// two chunks
	putChunk := func(idx, data string) *httptest.ResponseRecorder {
		rr := httptest.NewRequest("PUT", "/api/roms/upload/"+id, bytes.NewReader([]byte(data)))
		rr.Header.Set("Authorization", "Bearer "+tok)
		rr.Header.Set("x-upload-chunk-index", idx)
		ww := httptest.NewRecorder()
		s.Handler().ServeHTTP(ww, rr)
		return ww
	}
	if ww := putChunk("0", "HELLO"); ww.Code != 200 {
		t.Fatalf("chunk 0: %d %s", ww.Code, ww.Body.String())
	}
	if ww := putChunk("1", "WORLD"); ww.Code != 201 {
		t.Fatalf("final chunk: %d %s", ww.Code, ww.Body.String())
	}

	// assembled file lands in <scanRoot>/switch/Test (USA).nsp
	got, err := os.ReadFile(filepath.Join(root, "roms", "switch", "Test (USA).nsp"))
	if err != nil || string(got) != "HELLOWORLD" {
		t.Fatalf("assembled = %q err=%v", got, err)
	}

	// auth required
	if ww := httptest.NewRecorder(); true {
		s.Handler().ServeHTTP(ww, httptest.NewRequest("POST", "/api/roms/upload/start", nil))
		if ww.Code != 401 {
			t.Errorf("no-auth start = %d, want 401", ww.Code)
		}
	}
	_ = http.StatusOK
}
