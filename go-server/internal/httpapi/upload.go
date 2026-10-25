package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zszwede/romm-go/internal/deviceauth"
	"github.com/zszwede/romm-go/internal/scan"
)

var errScanRoot = errors.New("scan root not configured")

// scopedUser authenticates and requires a scope, returning the user id.
func (s *Server) collectionUserScope(w http.ResponseWriter, r *http.Request, scope string) (int64, bool) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope(scope) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return 0, false
	}
	return uid, true
}

// Chunked ROM upload, ported from backend/endpoints/roms/upload.py:
//   POST /api/roms/upload/start  -> {upload_id}   (headers: x-upload-filename,
//                                    x-upload-platform-id, x-upload-total-chunks)
//   PUT  /api/roms/upload/{id}    (header x-upload-chunk-index, body = chunk)
//   POST /api/roms/upload/{id}/cancel
// The Python version staged chunks in Redis+tmp; this stages them on disk and
// assembles into the library platform dir on the last chunk, then rescans that
// platform to create the rom row.

type uploadSession struct {
	platformID  int64
	fsSlug      string
	filename    string
	totalChunks int
	tmpDir      string
	received    map[int]bool
	mu          sync.Mutex
	created     time.Time
}

type uploadManager struct {
	mu       sync.Mutex
	sessions map[string]*uploadSession
	tmpBase  string
}

func newUploadManager(tmpBase string) *uploadManager {
	return &uploadManager{sessions: map[string]*uploadSession{}, tmpBase: tmpBase}
}

func (s *Server) uploadStart(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	filename := r.Header.Get("x-upload-filename")
	platformID := int64(atoiDefault(r.Header.Get("x-upload-platform-id"), 0))
	totalChunks := atoiDefault(r.Header.Get("x-upload-total-chunks"), 0)
	if filename == "" || platformID <= 0 {
		writeErr(w, http.StatusBadRequest, "filename and platform_id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	fsSlug, ok := s.platformFsSlug(ctx, platformID)
	if !ok {
		writeErr(w, http.StatusBadRequest, "unknown platform")
		return
	}
	id := deviceauth.NewUUID4()
	tmpDir := filepath.Join(s.uploads.tmpBase, id)
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		writeErr(w, http.StatusInternalServerError, "mkdir failed")
		return
	}
	sess := &uploadSession{
		platformID: platformID, fsSlug: fsSlug, filename: filepath.Base(filename),
		totalChunks: totalChunks, tmpDir: tmpDir, received: map[int]bool{}, created: time.Now(),
	}
	s.uploads.mu.Lock()
	s.uploads.sessions[id] = sess
	s.uploads.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"upload_id": id})
}

func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := r.PathValue("id")
	s.uploads.mu.Lock()
	sess := s.uploads.sessions[id]
	s.uploads.mu.Unlock()
	if sess == nil {
		writeErr(w, http.StatusNotFound, "unknown upload")
		return
	}
	idx := atoiDefault(r.Header.Get("x-upload-chunk-index"), 0)
	if idx < 0 || (sess.totalChunks > 0 && idx >= sess.totalChunks) {
		writeErr(w, http.StatusBadRequest, "chunk index out of range")
		return
	}
	chunkPath := filepath.Join(sess.tmpDir, chunkName(idx))
	dst, err := os.Create(chunkPath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "chunk create failed")
		return
	}
	_, copyErr := io.Copy(dst, http.MaxBytesReader(w, r.Body, 512<<20))
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		writeErr(w, http.StatusInternalServerError, "chunk write failed")
		return
	}
	sess.mu.Lock()
	sess.received[idx] = true
	sess.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"received": idx})
}

// uploadComplete finalizes a chunked upload: assembles the chunks into the
// library and rescans the platform. The frontend calls this after the last PUT.
func (s *Server) uploadComplete(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := r.PathValue("id")
	s.uploads.mu.Lock()
	sess := s.uploads.sessions[id]
	s.uploads.mu.Unlock()
	if sess == nil {
		writeErr(w, http.StatusNotFound, "unknown upload")
		return
	}
	if err := s.assembleUpload(r.Context(), sess); err != nil {
		writeErr(w, http.StatusInternalServerError, "assemble failed: "+err.Error())
		return
	}
	s.uploads.mu.Lock()
	delete(s.uploads.sessions, id)
	s.uploads.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"uploaded": sess.filename, "platform_id": sess.platformID})
}

func (s *Server) uploadCancel(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := r.PathValue("id")
	s.uploads.mu.Lock()
	sess := s.uploads.sessions[id]
	delete(s.uploads.sessions, id)
	s.uploads.mu.Unlock()
	if sess != nil {
		_ = os.RemoveAll(sess.tmpDir)
	}
	w.WriteHeader(http.StatusNoContent)
}

// assembleUpload concatenates the chunks into the library platform dir and
// rescans that one platform so the rom row is created.
func (s *Server) assembleUpload(ctx context.Context, sess *uploadSession) error {
	if s.scanRoot == "" {
		return errScanRoot
	}
	destDir := filepath.Join(s.scanRoot, sess.fsSlug)
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}
	destPath := filepath.Join(destDir, sess.filename)
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	for i := 0; i < sess.totalChunks; i++ {
		cp := filepath.Join(sess.tmpDir, chunkName(i))
		in, err := os.Open(cp)
		if err != nil {
			out.Close()
			return err
		}
		_, err = io.Copy(out, in)
		in.Close()
		if err != nil {
			out.Close()
			return err
		}
	}
	if err := out.Close(); err != nil {
		return err
	}
	_ = os.RemoveAll(sess.tmpDir)

	// Rescan just this platform to ingest the new file (best-effort, async).
	if s.queue != nil {
		slug := sess.fsSlug
		root := s.scanRoot
		s.queue.Enqueue("upload-scan", func(ctx context.Context) error {
			dirs, err := s.st.ListPlatformDirs(ctx)
			if err != nil {
				return err
			}
			var only []scan.PlatformDir
			for _, d := range dirs {
				if d.FsSlug == slug {
					only = append(only, d)
				}
			}
			_, err = scan.Platforms(ctx, root, only, s.st, true)
			return err
		})
	}
	return nil
}

func (s *Server) platformFsSlug(ctx context.Context, platformID int64) (string, bool) {
	dirs, err := s.st.ListPlatformDirs(ctx)
	if err != nil {
		return "", false
	}
	for _, d := range dirs {
		if d.ID == platformID {
			return d.FsSlug, true
		}
	}
	return "", false
}

func chunkName(i int) string {
	// zero-padded so lexical order == numeric, matching the Python staging.
	const digits = "0123456789"
	b := []byte("00000")
	for p := 4; p >= 0 && i > 0; p-- {
		b[p] = digits[i%10]
		i /= 10
	}
	return string(b)
}
