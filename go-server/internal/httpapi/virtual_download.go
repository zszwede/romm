package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// Virtual-download management (the user's RommDownloader fork feature): the
// wanted/queued flags and the queue view. Dispatch to the merge service and
// source assignment are handled elsewhere (/api/virtual/{check,dispatch}).

func (s *Server) virtualQueued(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	items, err := s.st.DownloadQueue(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) virtualMarkDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.SetDownloadStatus(ctx, id, "wanted"); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"msg": "Marked for download", "status": "wanted"})
}

// virtualMerge folds several same-game roms into one keeper (the duplicate-merge
// feature). Destructive: deletes the absorbed rom rows.
func (s *Server) virtualMerge(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	var body struct {
		RomIDs []int64 `json:"rom_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := s.st.MergeRoms(ctx, body.RomIDs)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMergeTooFew), errors.Is(err, store.ErrMergeMultiLocal):
			writeErr(w, http.StatusBadRequest, err.Error())
		default:
			writeErr(w, http.StatusInternalServerError, "merge failed")
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"msg": "ROMs merged", "keeper_id": res.KeeperID, "absorbed_ids": res.AbsorbedIDs,
	})
}

func (s *Server) virtualUnmarkDownload(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.SetDownloadStatus(ctx, id, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"msg": "Download flag cleared"})
}
