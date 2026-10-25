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

// Screenshot upload/list/download/delete. Files live under
// <assetsRoot>/screenshots/user-<uid>/rom-<id>/. The upload field is
// "screenshotFile" (not "file") and the rom id is the rom_id query param.

func screenshotJSON(sc *store.Screenshot) map[string]any {
	return map[string]any{
		"id":              sc.ID,
		"rom_id":          sc.RomID,
		"user_id":         sc.UserID,
		"file_name":       sc.FileName,
		"file_path":       sc.FilePath,
		"file_size_bytes": sc.SizeBytes,
		"download_path":   "/api/screenshots/" + strconv.FormatInt(sc.ID, 10) + "/content/" + url.PathEscape(sc.FileName),
		"is_gallery":      false,
		"is_public":       false,
		"created_at":      time.Now().UTC(),
		"updated_at":      time.Now().UTC(),
	}
}

func (s *Server) listScreenshots(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	romID := int64(atoiDefault(r.URL.Query().Get("rom_id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, err := s.st.ListScreenshots(ctx, romID, uid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	out := make([]map[string]any, 0, len(items))
	for i := range items {
		out = append(out, screenshotJSON(&items[i]))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) uploadScreenshot(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.noteUser(w, r)
	if !ok {
		return
	}
	if s.assetsRoot == "" {
		writeErr(w, http.StatusServiceUnavailable, "assets root not configured")
		return
	}
	romID := int64(atoiDefault(r.URL.Query().Get("rom_id"), 0))
	if romID <= 0 {
		writeErr(w, http.StatusBadRequest, "rom_id required")
		return
	}
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeErr(w, http.StatusBadRequest, "bad multipart form")
		return
	}
	file, hdr, err := r.FormFile("screenshotFile")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "screenshotFile field required")
		return
	}
	defer file.Close()

	name := filepath.Base(hdr.Filename)
	relDir := filepath.Join("screenshots", fmt.Sprintf("user-%d", uid), fmt.Sprintf("rom-%d", romID))
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
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	sc := store.Screenshot{RomID: romID, UserID: uid, FileName: name, FilePath: relDir, SizeBytes: n}
	id, err := s.st.InsertScreenshot(ctx, sc)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "db insert failed")
		return
	}
	sc.ID = id
	writeJSON(w, http.StatusCreated, screenshotJSON(&sc))
}

func (s *Server) screenshotContent(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if s.assetsRoot == "" {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	relDir, name, found, err := s.st.ScreenshotLocation(ctx, id)
	if err != nil || !found {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	full := filepath.Clean(filepath.Join(s.assetsRoot, filepath.FromSlash(relDir), name))
	if full != s.assetsRoot && !strings.HasPrefix(full, s.assetsRoot+string(os.PathSeparator)) {
		writeErr(w, http.StatusBadRequest, "invalid path")
		return
	}
	f, err := os.Open(full)
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
	http.ServeContent(w, r, name, st.ModTime(), f)
}

func (s *Server) deleteScreenshot(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "assets.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if relDir, name, found, err := s.st.ScreenshotLocation(ctx, id); err == nil && found && s.assetsRoot != "" {
		_ = os.Remove(filepath.Join(s.assetsRoot, filepath.FromSlash(relDir), name))
	}
	if err := s.st.DeleteScreenshot(ctx, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
