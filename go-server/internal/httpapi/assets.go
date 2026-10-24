package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// Save/state sync. Files live under <assetsRoot>/<kind>/user-<uid>/rom-<id>/.
// kind is always "saves" or "states" (route-literal), never user input.

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request, kind string) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	romID := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return
	}
	items, err := s.st.ListAssets(ctx, kind, romID, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) uploadAsset(w http.ResponseWriter, r *http.Request, kind string) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.assetsRoot == "" {
		writeErr(w, http.StatusServiceUnavailable, "assets root not configured")
		return
	}
	romID := int64(atoiDefault(r.PathValue("id"), 0))
	if romID <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	file, hdr, err := r.FormFile("file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "file field required")
		return
	}
	defer file.Close()

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return
	}

	name := filepath.Base(hdr.Filename)
	relDir := filepath.Join(kind, fmt.Sprintf("user-%d", uid), fmt.Sprintf("rom-%d", romID))
	absDir := filepath.Join(s.assetsRoot, relDir)
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "mkdir failed")
		return
	}
	dst, err := os.Create(filepath.Join(absDir, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	n, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	id, err := s.st.InsertAsset(ctx, kind, store.Asset{
		RomID: romID, UserID: uid, FileName: name, FilePath: relDir,
		SizeBytes: n, Emulator: r.FormValue("emulator"),
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db insert failed")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id": id, "file_name": name, "file_size_bytes": n, "rom_id": romID,
	})
}

func (s *Server) assetContent(w http.ResponseWriter, r *http.Request, kind string) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	abs, name, ok := s.assetAbsPath(r, kind)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	f, err := os.Open(abs)
	if err != nil {
		writeErr(w, http.StatusNotFound, "file missing")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeErr(w, http.StatusNotFound, "file missing")
		return
	}
	w.Header().Set("Content-Disposition", "attachment; filename*=UTF-8''"+url.PathEscape(name))
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (s *Server) deleteAsset(w http.ResponseWriter, r *http.Request, kind string) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	abs, _, ok := s.assetAbsPath(r, kind)
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.DeleteAsset(ctx, kind, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	_ = os.Remove(abs) // best-effort; the row is already gone
	w.WriteHeader(http.StatusNoContent)
}

// assetAbsPath resolves and confines a save/state file path to the assets root.
func (s *Server) assetAbsPath(r *http.Request, kind string) (abs, name string, ok bool) {
	if s.assetsRoot == "" {
		return "", "", false
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		return "", "", false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	relDir, fileName, found, err := s.st.AssetLocation(ctx, kind, id)
	if err != nil || !found {
		return "", "", false
	}
	full := filepath.Clean(filepath.Join(s.assetsRoot, filepath.FromSlash(relDir), fileName))
	if full != s.assetsRoot && !strings.HasPrefix(full, s.assetsRoot+string(os.PathSeparator)) {
		return "", "", false
	}
	return full, fileName, true
}
