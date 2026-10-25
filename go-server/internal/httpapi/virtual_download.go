package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/archiveorg"
	"github.com/zszwede/romm-go/internal/store"
	"github.com/zszwede/romm-go/internal/virtual"
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
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if err := s.st.SetDownloadStatus(ctx, id, "wanted"); err != nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	// The D hotkey closes the loop with one keypress: when a downloader is
	// wired, dispatch immediately (→ queued). Without one it stays "wanted" for
	// a later dispatch or an external poll of /virtual/queued.
	if s.virt == nil {
		writeJSON(w, http.StatusOK, map[string]any{"msg": "Marked for download", "status": "wanted"})
		return
	}
	rom, err := s.st.GetRomFull(ctx, id)
	if err != nil || rom == nil {
		writeJSON(w, http.StatusOK, map[string]any{"msg": "Marked for download", "status": "wanted"})
		return
	}
	rom.Mirrors, _ = s.st.RomMirrorURLs(ctx, id)
	var hostOrder []string
	if s.configFile != nil {
		hostOrder = s.configFile.Downloads.DefaultHostOrder
	}
	links := buildDownloadLinks(rom, hostOrder)
	if len(links) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"msg": "No downloadable URL", "status": "wanted"})
		return
	}
	job := virtual.Job{
		System: rom.PlatformFsSlug, Name: rom.Name.String,
		Links: links, RomID: id, PlatformID: rom.PlatformID,
	}
	if _, err := s.virt.Dispatch(ctx, job); err != nil {
		log.Printf("mark-download dispatch rom %d failed, left wanted: %v", id, err)
		writeJSON(w, http.StatusOK, map[string]any{
			"msg": "Flagged (dispatch failed; will retry)", "status": "wanted", "dispatch_error": err.Error()})
		return
	}
	_ = s.st.SetDownloadStatus(ctx, id, "queued")
	writeJSON(w, http.StatusOK, map[string]any{"msg": "Dispatched", "status": "queued"})
}

// downloadItem mirrors one entry of a rom's virtual_contents.
type downloadItem struct {
	URL      string `json:"url"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Version  string `json:"version"`
	Host     string `json:"host"`
	Part     int    `json:"part"`
}

// buildDownloadLinks produces the ordered link set to dispatch for a rom: base
// parts + updates + DLC from virtual_contents (or the source URLs as a single
// base when none captured), de-duplicated and ordered base→update→DLC with the
// preferred host first within each group. Mirrors select_download_items.
func buildDownloadLinks(rom *store.RomFull, hostOrder []string) []virtual.Link {
	var items []downloadItem
	if rom.VirtualContents.Valid && rom.VirtualContents.String != "" {
		_ = json.Unmarshal([]byte(rom.VirtualContents.String), &items)
	}
	if len(items) == 0 {
		for _, u := range append([]string{rom.SourceURL.String}, rom.Mirrors...) {
			if u != "" {
				items = append(items, downloadItem{URL: u, Name: rom.Name.String, Category: "base", Host: hostOf(u)})
			}
		}
	}
	seen := map[string]bool{}
	uniq := make([]downloadItem, 0, len(items))
	for _, it := range items {
		if it.URL != "" && !seen[it.URL] {
			seen[it.URL] = true
			if it.Host == "" {
				it.Host = hostOf(it.URL)
			}
			uniq = append(uniq, it)
		}
	}
	lowerOrder := make([]string, len(hostOrder))
	for i, h := range hostOrder {
		lowerOrder[i] = strings.ToLower(h)
	}
	hostRank := func(h string) int {
		h = strings.ToLower(h)
		for i, o := range lowerOrder {
			if o != "" && strings.Contains(h, o) {
				return i
			}
		}
		return len(lowerOrder)
	}
	sort.SliceStable(uniq, func(a, b int) bool {
		if ca, cb := catRank(uniq[a].Category), catRank(uniq[b].Category); ca != cb {
			return ca < cb
		}
		if ra, rb := hostRank(uniq[a].Host), hostRank(uniq[b].Host); ra != rb {
			return ra < rb
		}
		return uniq[a].Part < uniq[b].Part
	})
	out := make([]virtual.Link, 0, len(uniq))
	for _, it := range uniq {
		cat := it.Category
		if cat == "" {
			cat = "base"
		}
		out = append(out, virtual.Link{URL: it.URL, Name: it.Name, Category: cat, Version: it.Version})
	}
	return out
}

func catRank(c string) int {
	switch strings.ToLower(c) {
	case "base", "base_game", "game", "":
		return 0
	case "update", "patch":
		return 1
	case "dlc":
		return 2
	default:
		return 3
	}
}

func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Host
	}
	return ""
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
