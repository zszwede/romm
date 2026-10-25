package httpapi

import (
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Manual upload/delete (the detail Manual tab). The manual file is stored under
// the resources root and its relative path recorded on the rom (path_manual).

func (s *Server) uploadManual(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	if s.resourcesRoot == "" {
		writeErr(w, http.StatusServiceUnavailable, "resources root not configured")
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
	file, hdr, err := firstFormFile(r, "manualFile", "file")
	if err != nil {
		writeErr(w, http.StatusBadRequest, "manual file required")
		return
	}
	defer file.Close()

	name := filepath.Base(hdr.Filename)
	relDir := filepath.ToSlash(filepath.Join("roms", fmt.Sprintf("%d", romID), "manual"))
	absDir := filepath.Join(s.resourcesRoot, filepath.FromSlash(relDir))
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "mkdir failed")
		return
	}
	dst, err := os.Create(filepath.Join(absDir, name))
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	_, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		writeErr(w, http.StatusInternalServerError, "write failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.SetRomManual(ctx, romID, relDir+"/"+name); err != nil {
		writeErr(w, http.StatusInternalServerError, "db update failed")
		return
	}
	// Return the refreshed rom (its path_manual/has_manual now set).
	s.getRom(w, r)
}

func (s *Server) deleteManual(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	romID := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.SetRomManual(ctx, romID, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	s.getRom(w, r)
}

func firstFormFile(r *http.Request, fields ...string) (multipart.File, *multipart.FileHeader, error) {
	for _, name := range fields {
		if file, h, e := r.FormFile(name); e == nil {
			return file, h, nil
		}
	}
	return r.FormFile(fields[0]) // return the real error
}
