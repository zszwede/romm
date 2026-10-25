package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// Save/state sync. Files live under <assetsRoot>/<kind>/user-<uid>/rom-<id>/.
// kind is always "saves" or "states" (route-literal), never user input.

// assetRomID resolves the rom id from the path ({id}) or, for the /api/saves
// and /api/states routes, the rom_id query param.
func assetRomID(r *http.Request) int64 {
	if id := int64(atoiDefault(r.PathValue("id"), 0)); id > 0 {
		return id
	}
	return int64(atoiDefault(r.URL.Query().Get("rom_id"), 0))
}

func (s *Server) listAssets(w http.ResponseWriter, r *http.Request, kind string) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	romID := assetRomID(r)
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
	// The frontend's save/state list reads a bare array of SaveSchema/StateSchema.
	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, assetJSON(&items[i], kind))
	}
	writeJSON(w, http.StatusOK, out)
}

// assetJSON builds a SaveSchema/StateSchema the frontend accepts.
func assetJSON(a *store.Asset, kind string) map[string]any {
	ext := ""
	if dot := strings.LastIndex(a.FileName, "."); dot >= 0 {
		ext = a.FileName[dot+1:]
	}
	m := map[string]any{
		"id":               a.ID,
		"rom_id":           a.RomID,
		"user_id":          a.UserID,
		"file_name":        a.FileName,
		"file_name_no_ext": strings.TrimSuffix(a.FileName, "."+ext),
		"file_name_no_tags": strings.TrimSuffix(a.FileName, "."+ext),
		"file_extension":   ext,
		"file_path":        a.FilePath,
		"file_size_bytes":  a.SizeBytes,
		"download_path":    "/api/" + kind + "/" + strconv.FormatInt(a.ID, 10) + "/content/" + url.PathEscape(a.FileName),
		"emulator":         emptyToNil(a.Emulator),
		"missing_from_fs":  false,
		"is_public":        false,
		"is_favorite":      false,
		"content_hash":     nil,
		"created_at":       time.Now().UTC(),
		"updated_at":       time.Now().UTC(),
	}
	if kind == "saves" {
		m["slot"] = nil
		m["screenshot"] = nil
		m["origin_device_id"] = nil
		m["device_syncs"] = []any{}
	} else {
		m["core"] = nil
		m["disc_file_id"] = nil
	}
	return m
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
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
	romID := assetRomID(r)
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
	emulator := r.FormValue("emulator")
	if emulator == "" {
		emulator = r.URL.Query().Get("emulator")
	}
	asset := store.Asset{
		RomID: romID, UserID: uid, FileName: name, FilePath: relDir,
		SizeBytes: n, Emulator: emulator,
	}
	id, err := s.st.InsertAsset(ctx, kind, asset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db insert failed")
		return
	}
	asset.ID = id
	writeJSON(w, http.StatusCreated, assetJSON(&asset, kind))
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
