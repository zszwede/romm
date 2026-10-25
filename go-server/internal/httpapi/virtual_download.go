package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	links, mirrors := buildDownloadLinks(rom, hostOrder)
	if len(links) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{"msg": "No downloadable URL", "status": "wanted"})
		return
	}
	// Availability pre-check: drop links the downloader confirms are gone (a
	// dead 1fichier link, say), so a live mirror is used instead of failing on a
	// dead primary. Unknown/unchecked links are kept. Best-effort — if the check
	// itself fails, dispatch the full set unchanged.
	links = pruneDeadLinks(ctx, s.virt, links)
	if len(links) == 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"msg": "All sources are unavailable", "status": "wanted"})
		return
	}
	// Mirror/variant fallback: the links are alternative copies of one base
	// game, so download just the first (the preferred-host, still-available one)
	// rather than every copy.
	if mirrors && len(links) > 1 {
		links = links[:1]
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
	// Group identifies a variant packaging within a category (e.g. a US NSP set
	// vs an Asia NSP set): all files of one packaging share it ("0:0:0"), and
	// different regional/format variants get different groups ("0:0:1").
	Group string `json:"group"`
}

// buildDownloadLinks produces the ordered link set to dispatch for a rom: base
// parts + updates + DLC from virtual_contents, de-duplicated and ordered
// base→update→DLC with the preferred host first within each group.
//
// mirrors is true when the rom has no virtual_contents and the source URLs are
// used as a fallback: those URLs are ALTERNATIVE copies of one base game (host
// mirrors and/or dump variants), not separate parts, so the caller must keep
// only one — otherwise every copy is downloaded (e.g. a game offered on two
// hosts × two variants would fetch ~4× the data).
func buildDownloadLinks(rom *store.RomFull, hostOrder []string) (links []virtual.Link, mirrors bool) {
	var items []downloadItem
	if rom.VirtualContents.Valid && rom.VirtualContents.String != "" {
		_ = json.Unmarshal([]byte(rom.VirtualContents.String), &items)
	}
	if len(items) == 0 {
		mirrors = true
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
	// A source lists a game in several variants — regional (US / Asia / EU) and
	// format (NSP / XCI / NSZ), plus multi-part sets — all under one rom, every
	// base tagged "base". Downloading them all wastes bandwidth and pulls in the
	// non-English (Japanese/Asian) copies. Keep one variant per category,
	// preferring the English/Western release.
	uniq = collapseVariants(uniq, hostRank)
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
	return out, mirrors
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

var rePartToken = regexp.MustCompile(`(?i)[\s._-]*\bpart\s*\.?\s*(\d+)\b`)

// packagingKey groups base links that are the same packaging. The parts of a
// multi-part set share a key (the name with the "Part N" token and extension
// removed); a standalone file is its own key. part is the 1-based part number
// (1 for a standalone or an unnumbered file).
func packagingKey(name string) (key string, part int) {
	n, part := name, 1
	if m := rePartToken.FindStringSubmatchIndex(n); m != nil {
		part = atoiDefault(n[m[2]:m[3]], 1)
		n = n[:m[0]] + n[m[1]:] // drop the " Part N" token
	}
	n = strings.TrimSuffix(n, filepath.Ext(n))
	return strings.ToLower(strings.Join(strings.Fields(n), " ")), part
}

// nameIsUncompressed reports whether a base file's name indicates an
// uncompressed Switch image (.nsp/.xci) rather than a compressed one
// (.nsz/.xcz). Uncompressed is preferred: it needs no decompress step and tends
// to be a straight dump, so it is the more robust choice when a game is offered
// both ways.
func nameIsUncompressed(name string) bool {
	l := strings.ToLower(name)
	if strings.Contains(l, "nsz") || strings.Contains(l, "xcz") {
		return false
	}
	return strings.Contains(l, "nsp") || strings.Contains(l, "xci")
}

// variantKey identifies a packaging within a category: the source "group" when
// present (all files of one packaging share it), else the name stem (multi-part
// files of one set share a stem). So US and Asia sets, or NSP and XCI, each get
// a distinct key while their own parts stay together.
func variantKey(it downloadItem) string {
	if g := strings.TrimSpace(it.Group); g != "" {
		return "g:" + g
	}
	k, _ := packagingKey(it.Name)
	return "n:" + k
}

// regionRank ranks a name's region for an English-preferring library: 0 for
// English/Western (US/USA/World/EU/EN/UK), 2 for Asian (AS/ASIA/JP/JPN/KR/CN/
// TW/HK), 1 for unknown. Lower is preferred.
func regionRank(name string) int {
	u := strings.ToUpper(name)
	for _, t := range []string{"(AS)", "[AS]", "(ASIA)", "[ASIA]", "(JP)", "[JP]", "(JPN)", "[JPN]", "JAPAN", "(KR)", "[KR]", "KOREA", "(CN)", "[CN]", "CHINA", "(TW)", "[TW]", "(HK)", "[HK]"} {
		if strings.Contains(u, t) {
			return 2
		}
	}
	for _, t := range []string{"(US)", "[US]", "(USA)", "[USA]", "(WORLD)", "[WORLD]", "(EU)", "[EU]", "(EUR)", "[EUR]", "EUROPE", "(EN)", "[EN]", "(UK)", "[UK]"} {
		if strings.Contains(u, t) {
			return 0
		}
	}
	return 1
}

// versionKey turns "1.7.0" into comparable ints (higher = newer).
func versionKey(v string) []int {
	var out []int
	for _, p := range strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' }) {
		n, _ := strconv.Atoi(p)
		out = append(out, n)
	}
	return out
}

// versionNewer reports whether version a is newer than b.
func versionNewer(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return len(a) > len(b)
}

// collapseVariants keeps a single packaging per category (base / update / DLC)
// when a rom's virtual_contents offers several — regional (US / Asia / EU) and
// format (NSP / XCI / NSZ) variants, plus multi-part sets. Downloading every
// variant wastes bandwidth and pulls in the non-English copies the user does
// not want. For each category one variant group is chosen, preferring: English/
// Western region, then an uncompressed image (NSP/XCI over NSZ), then the latest
// version (updates), then the preferred host, then fewer files. A chosen
// multi-part set is kept whole; unknown-category items pass through untouched.
func collapseVariants(items []downloadItem, hostRank func(string) int) []downloadItem {
	byCat := map[int][]downloadItem{}
	var catOrder []int
	for _, it := range items {
		c := catRank(it.Category)
		if _, ok := byCat[c]; !ok {
			catOrder = append(catOrder, c)
		}
		byCat[c] = append(byCat[c], it)
	}
	out := make([]downloadItem, 0, len(items))
	for _, c := range catOrder {
		its := byCat[c]
		if c == 3 { // unknown category — never collapse
			out = append(out, its...)
			continue
		}
		groups := map[string][]downloadItem{}
		var gorder []string
		for _, it := range its {
			k := variantKey(it)
			if _, ok := groups[k]; !ok {
				gorder = append(gorder, k)
			}
			groups[k] = append(groups[k], it)
		}
		if len(groups) <= 1 {
			out = append(out, its...)
			continue
		}
		best := gorder[0]
		for _, k := range gorder[1:] {
			if betterVariant(groups[k], groups[best], c, hostRank) {
				best = k
			}
		}
		out = append(out, groups[best]...)
	}
	return out
}

// group aggregates used to compare two variant packagings.
func groupRegion(g []downloadItem) int {
	r := 9
	for _, it := range g {
		if rr := regionRank(it.Name); rr < r {
			r = rr
		}
	}
	return r
}
func groupUncompressed(g []downloadItem) bool {
	for _, it := range g {
		if nameIsUncompressed(it.Name) {
			return true
		}
	}
	return false
}
func groupHostRank(g []downloadItem, hostRank func(string) int) int {
	r := 1 << 30
	for _, it := range g {
		if hr := hostRank(it.Host); hr < r {
			r = hr
		}
	}
	return r
}
func groupVersion(g []downloadItem) []int {
	var best []int
	for _, it := range g {
		if v := versionKey(it.Version); versionNewer(v, best) {
			best = v
		}
	}
	return best
}

// betterVariant reports whether packaging a should be preferred over b for a
// category (0 base, 1 update, 2 dlc).
func betterVariant(a, b []downloadItem, cat int, hostRank func(string) int) bool {
	if ra, rb := groupRegion(a), groupRegion(b); ra != rb {
		return ra < rb
	}
	if ua, ub := groupUncompressed(a), groupUncompressed(b); ua != ub {
		return ua
	}
	if cat == 1 { // updates: newest version wins
		if va, vb := groupVersion(a), groupVersion(b); versionNewer(va, vb) || versionNewer(vb, va) {
			return versionNewer(va, vb)
		}
	}
	if ha, hb := groupHostRank(a, hostRank), groupHostRank(b, hostRank); ha != hb {
		return ha < hb
	}
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return variantKey(a[0]) < variantKey(b[0])
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

// pruneDeadLinks removes links the downloader's availability check confirms are
// gone (available=false AND checked=true). Links it couldn't check are kept, so
// a flaky check never drops a good link. Returns the input unchanged on error.
func pruneDeadLinks(ctx context.Context, virt VirtualService, links []virtual.Link) []virtual.Link {
	if len(links) == 0 {
		return links
	}
	urls := make([]string, len(links))
	for i, l := range links {
		urls[i] = l.URL
	}
	avail, err := virt.Check(ctx, urls)
	if err != nil {
		return links
	}
	dead := map[string]bool{}
	for _, a := range avail {
		if a.Checked && !a.Available {
			dead[a.URL] = true
		}
	}
	if len(dead) == 0 {
		return links
	}
	out := links[:0]
	for _, l := range links {
		if !dead[l.URL] {
			out = append(out, l)
		}
	}
	return out
}
