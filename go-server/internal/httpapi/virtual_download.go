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

func (s *Server) virtualSources(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	platformID := int64(atoiDefault(r.URL.Query().Get("platform_id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	sources, err := s.st.ListVirtualSources(ctx, platformID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, sources)
}

func (s *Server) virtualUpdateSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	var body struct {
		URL          *string `json:"url"`
		RegionFilter *string `json:"region_filter"`
		Enabled      *bool   `json:"enabled"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	v, err := s.st.UpdateVirtualSource(ctx, id, store.VirtualSourceUpdate{URL: body.URL, RegionFilter: body.RegionFilter, Enabled: body.Enabled})
	if err != nil || v == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) virtualDeleteSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	gone, err := s.st.DeleteVirtualSource(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"msg": "Source deleted"})
}

// virtualExclude deletes a virtual rom and records the exclusion so a later list
// re-import won't re-add it. Destructive.
func (s *Server) virtualExclude(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := s.st.ExcludeRom(ctx, id); err != nil {
		writeErr(w, http.StatusInternalServerError, "exclude failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"msg": "ROM deleted and excluded"})
}
