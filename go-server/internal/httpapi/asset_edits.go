package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// Save/state metadata edits + bulk delete (the player's Save data tab).

func (s *Server) editAsset(w http.ResponseWriter, r *http.Request, kind, field string) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	var body struct {
		IsPublic   *bool    `json:"is_public"`
		IsFavorite *bool    `json:"is_favorite"`
		FileName   *string  `json:"file_name"`
		Labels     []string `json:"labels"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	var up store.AssetUpdate
	switch field {
	case "visibility":
		up.IsPublic = body.IsPublic
	case "favorite":
		up.IsFavorite = body.IsFavorite
	case "file-name":
		up.FileName = body.FileName
	case "labels":
		if b, err := json.Marshal(body.Labels); err == nil {
			s := string(b)
			up.Labels = &s
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.UpdateAsset(ctx, kind, id, up); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	a, err := s.st.GetAssetByID(ctx, kind, id)
	if err != nil || a == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, assetJSON(a, kind))
}

func (s *Server) bulkDeleteAssets(w http.ResponseWriter, r *http.Request, kind string) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body map[string][]int64
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ids := body[kind] // {"saves":[...]} / {"states":[...]}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	// Remove files best-effort, then the rows.
	for _, id := range ids {
		if a, err := s.st.GetAssetByID(ctx, kind, id); err == nil && a != nil && s.assetsRoot != "" {
			_ = removeAssetFile(s.assetsRoot, a)
		}
	}
	deleted, err := s.st.BulkDeleteAssets(ctx, kind, ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	writeJSON(w, http.StatusOK, deleted)
}

func removeAssetFile(root string, a *store.Asset) error {
	return os.Remove(filepath.Join(root, filepath.FromSlash(a.FilePath), a.FileName))
}

// activityEntries returns recent activity (empty; not recorded) as a bare array.
func (s *Server) activityEntries(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// activityHeartbeat is the presence ping the app sends; it needs no persistence
// in a single-user setup.
func (s *Server) activityHeartbeat(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if r.Method == http.MethodDelete {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
