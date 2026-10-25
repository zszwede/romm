package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/archiveorg"
	"github.com/zszwede/romm-go/internal/store"
)

// virtualAssign creates archive.org sources for a platform and kicks off a sync
// that discovers and creates virtual roms from them.
func (s *Server) virtualAssign(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	var body struct {
		PlatformID int64    `json:"platform_id"`
		URLs       []string `json:"urls"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<18)).Decode(&body); err != nil || body.PlatformID <= 0 {
		writeErr(w, http.StatusBadRequest, "platform_id and urls required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	created := []store.VirtualSource{}
	for _, u := range body.URLs {
		src, err := s.st.CreateVirtualSource(ctx, body.PlatformID, u)
		if err != nil || src == nil {
			writeErr(w, http.StatusInternalServerError, "create source failed")
			return
		}
		created = append(created, *src)
	}
	s.enqueueVirtualSync(body.PlatformID)
	writeJSON(w, http.StatusOK, created)
}

func (s *Server) virtualSyncPlatform(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.collectionUserScope(w, r, "roms.write"); !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	s.enqueueVirtualSync(id)
	writeJSON(w, http.StatusOK, map[string]any{"msg": "Sync started"})
}

// enqueueVirtualSync discovers and creates virtual roms for a platform's enabled
// sources, on the worker queue (archive.org fetches can be slow).
func (s *Server) enqueueVirtualSync(platformID int64) {
	if s.queue == nil {
		return
	}
	s.queue.Enqueue("virtual-sync", func(ctx context.Context) error {
		fsSlug, ok := s.platformFsSlug(ctx, platformID)
		if !ok {
			return nil
		}
		urls, err := s.st.ListEnabledSources(ctx, platformID)
		if err != nil {
			return err
		}
		client := archiveorg.NewClient()
		for _, u := range urls {
			games, _, err := client.ListGames(ctx, u, nil) // nil exts = keep all ROM-ish files
			if err != nil {
				log.Printf("virtual-sync: %s: %v", u, err)
				continue
			}
			for _, g := range games {
				var size int64
				if g.Size != nil {
					size = *g.Size
				}
				if _, err := s.st.CreateVirtualRom(ctx, platformID, fsSlug, store.VirtualGameInput{
					Name: g.Name, SourceURL: g.SourceURL, Size: size,
				}); err != nil {
					log.Printf("virtual-sync: create %q: %v", g.Name, err)
				}
			}
		}
		s.sockets.Emit("scan:done", map[string]any{})
		return nil
	})
}

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
