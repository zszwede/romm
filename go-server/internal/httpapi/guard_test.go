package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGuardSensitiveFiles(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := guardSensitiveFiles(ok)
	cases := map[string]int{
		"/assets/romm/resources/switch/prod.keys":      http.StatusNotFound,
		"/assets/romm/resources/switch/title.keys":     http.StatusNotFound,
		"/assets/romm/resources/switch/anything.png":   http.StatusNotFound, // switch dir blocked
		"/assets/romm/resources/roms/1/cover/small.png": http.StatusOK,
		"/assets/romm/resources/roms/1/manual.pdf":      http.StatusOK,
	}
	for path, want := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != want {
			t.Errorf("%s: got %d, want %d", path, rec.Code, want)
		}
	}
}
