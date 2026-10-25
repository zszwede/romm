package httpapi

import (
	"database/sql"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// jsonArr parses a JSON-array text column into []any, defaulting to an empty
// array so the frontend's .map/.filter/.length never see undefined.
func jsonArr(raw sql.NullString) any {
	if !raw.Valid || raw.String == "" {
		return []any{}
	}
	var out []any
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil || out == nil {
		return []any{}
	}
	return out
}

func sStr(s sql.NullString) any {
	if s.Valid {
		return s.String
	}
	return nil
}

// jsonRawOrNil parses a JSON text column (object or array) and returns it, or
// nil when the column is NULL/empty — matching the Python schema's `None`
// default for optional JSON like virtual_contents.
func jsonRawOrNil(raw sql.NullString) any {
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var out any
	if err := json.Unmarshal([]byte(raw.String), &out); err != nil {
		return nil
	}
	return out
}
func sInt(n sql.NullInt64) any {
	if n.Valid {
		return n.Int64
	}
	return nil
}
func sFloat(f sql.NullFloat64) any {
	if f.Valid {
		return f.Float64
	}
	return nil
}

// romMetadatum builds the RomMetadataSchema from the generated_* columns.
func romMetadatum(r *store.RomFull) map[string]any {
	pc := ""
	if r.GenPlayerCount.Valid {
		pc = r.GenPlayerCount.String
	}
	return map[string]any{
		"rom_id":             r.ID,
		"genres":             jsonArr(r.GenGenres),
		"franchises":         jsonArr(r.GenFranchises),
		"collections":        jsonArr(r.GenCollections),
		"companies":          jsonArr(r.GenCompanies),
		"publishers":         jsonArr(r.GenPublishers),
		"developers":         jsonArr(r.GenDevelopers),
		"game_modes":         jsonArr(r.GenGameModes),
		"age_ratings":        jsonArr(r.GenAgeRatings),
		"player_count":       pc,
		"first_release_date": sInt(r.GenFirstRelease),
		"average_rating":     sFloat(r.GenAvgRating),
	}
}

// romUserJSON builds the RomUserSchema, using defaults when the user has no row.
func romUserJSON(ru *store.RomUser, userID, romID int64) map[string]any {
	now := time.Now().UTC()
	if ru == nil {
		return map[string]any{
			"id": -1, "user_id": userID, "rom_id": romID,
			"created_at": now, "updated_at": now, "last_played": nil,
			"is_main_sibling": false, "backlogged": false, "now_playing": false,
			"hidden": false, "rating": 0, "difficulty": 0, "completion": 0,
			"status": nil, "pinned_media": nil,
		}
	}
	return map[string]any{
		"id": ru.ID, "user_id": ru.UserID, "rom_id": ru.RomID,
		"created_at": ru.CreatedAt, "updated_at": ru.UpdatedAt, "last_played": ru.LastPlayed,
		"is_main_sibling": ru.IsMainSibling, "backlogged": ru.Backlogged,
		"now_playing": ru.NowPlaying, "hidden": ru.Hidden, "rating": ru.Rating,
		"difficulty": ru.Difficulty, "completion": ru.Completion, "status": ru.Status,
		"pinned_media": nil,
	}
}

// browserURL drops a stored source URL the browser cannot load (file://), which
// would otherwise throw "Not allowed to load local resource".
func browserURL(n sql.NullString) any {
	if !n.Valid || n.String == "" || strings.HasPrefix(n.String, "file://") {
		return nil
	}
	return n.String
}

func platformDisplayName(r *store.RomFull) string {
	if r.PlatformCustomName.Valid && r.PlatformCustomName.String != "" {
		return r.PlatformCustomName.String
	}
	return r.PlatformName
}

// romExtras carries the per-rom detail data loaded by the detail handler
// (files, siblings, provider metadata). Nil for the gallery list, which does
// not need them.
type romExtras struct {
	Files       []store.RomFile
	Siblings    []store.SiblingRom
	Meta        *store.RomMetadata
	Saves       []store.Asset
	States      []store.Asset
	Screenshots []store.Screenshot
	Collections []store.Collection
	// HostOrder is the configured download host preference (config.yml
	// downloads.default_host_order), used to order a virtual rom's sources so
	// the preferred host (e.g. 1fichier.com) is offered first.
	HostOrder []string
}

// romJSON builds the full RomSchema. detail adds the DetailedRomSchema fields;
// extras (when non-nil) fills files, sibling_roms and the provider metadata.
func romJSON(r *store.RomFull, ru *store.RomUser, userID int64, detail bool, extras *romExtras) map[string]any {
	metaID := r.IGDBID.Valid || r.SSID.Valid || r.MobyID.Valid || r.RAID.Valid ||
		r.LaunchboxID.Valid || r.SGDBID.Valid
	m := map[string]any{
		"id":        r.ID,
		"igdb_id":   sInt(r.IGDBID),
		"sgdb_id":   sInt(r.SGDBID),
		"moby_id":   sInt(r.MobyID),
		"ss_id":     sInt(r.SSID),
		"ra_id":     sInt(r.RAID),
		"launchbox_id": sInt(r.LaunchboxID),
		"hasheous_id":  sInt(r.HasheousID),
		"tgdb_id":      sInt(r.TGDBID),
		"flashpoint_id": sStr(r.FlashpointID),
		"hltb_id":    sInt(r.HLTBID),
		"demozoo_id": sInt(r.DemozooID),
		"pouet_id":   sInt(r.PouetID),
		"csdb_id":    sInt(r.CSDBID),
		"steam_id":   sInt(r.SteamID),
		"gamelist_id": sStr(r.GamelistID),
		"libretro_id": sStr(r.LibretroID),

		"platform_id":           r.PlatformID,
		"platform_slug":         r.PlatformSlug,
		"platform_fs_slug":      r.PlatformFsSlug,
		"platform_custom_name":  sStr(r.PlatformCustomName),
		"platform_display_name": platformDisplayName(r),

		"fs_name":         r.FsName,
		"fs_name_no_tags": r.FsNameNoTags,
		"fs_name_no_ext":  r.FsNameNoExt,
		"fs_extension":    r.FsExtension,
		"fs_path":         r.FsPath,
		"fs_size_bytes":   r.FsSizeBytes,

		"name":          sStr(r.Name),
		"name_sort_key": sStr(r.NameSortKey),
		"slug":          sStr(r.Slug),
		"summary":       sStr(r.Summary),
		"alternative_names": []any{},
		"youtube_video_id":  nil,

		"metadatum":          romMetadatum(r),
		"igdb_metadata":      nil,
		"moby_metadata":      nil,
		"ss_metadata":        nil,
		"launchbox_metadata": nil,
		"hasheous_metadata":  nil,
		"flashpoint_metadata": nil,
		"hltb_metadata":      nil,
		"demozoo_metadata":   nil,
		"pouet_metadata":     nil,
		"csdb_metadata":      nil,
		"steam_metadata":     nil,
		"gamelist_metadata":  nil,
		"manual_metadata":    nil,

		"path_cover_small": sStr(r.PathCoverS),
		"path_cover_large": sStr(r.PathCoverL),
		"url_cover":        browserURL(r.URLCover),
		"has_manual":       r.PathManual.Valid && r.PathManual.String != "",
		"has_soundtrack":   false,
		"path_manual":      sStr(r.PathManual),
		"url_manual":       sStr(r.URLManual),
		"path_video":       nil,

		"is_identifying":   false,
		"is_unidentified":  !metaID,
		"is_identified":    metaID,
		"revision":         sStr(r.Revision),
		"regions":          jsonArr(r.Regions),
		"languages":        jsonArr(r.Languages),
		"tags":             jsonArr(r.Tags),
		"crc_hash":         sStr(r.CRCHash),
		"ra_hash":          sStr(r.RAHash),
		"title_id":         sStr(r.TitleID),
		"save_target":      sStr(r.SaveTarget),
		"save_target_layout": sStr(r.SaveTargetLayout),

		"has_simple_single_file": true,
		"has_nested_single_file": false,
		"has_multiple_files":     false,
		"is_easyrpg_game":        false,
		"full_path":              r.FsPath + "/" + r.FsName,

		"created_at": r.CreatedAt,
		"updated_at": r.UpdatedAt,

		"missing_from_fs":  r.MissingFromFs,
		"is_physical":      r.IsPhysical,
		"is_virtual":       r.IsVirtual,
		"has_file_on_disk": !r.IsPhysical && !r.IsVirtual && !r.MissingFromFs,
		"source_url":      sStr(r.SourceURL),
		"source_hosts":    jsonArr(r.SourceHosts),
		"download_status": sStr(r.DownloadStatus),

		"rom_user": romUserJSON(ru, userID, r.ID),

		"sibling_roms":       siblingsJSON(extras),
		"user_notes":         []any{},
		"files":              filesJSON(r, extras),
		"merged_screenshots": mergedScreenshots(extras),
		"merged_ra_metadata": nil,
	}

	// File-shape flags derive from the rom's files (defaults assume a single
	// top-level file when no files are loaded, matching a freshly-scanned rom).
	if extras != nil {
		topLevel, multiFile := fileShape(r, extras.Files)
		m["has_multiple_files"] = topLevel > 1
		m["has_simple_single_file"] = !multiFile && topLevel == 1
		m["has_nested_single_file"] = multiFile && topLevel == 1
		// Provider metadata blobs (Metadata + Media tabs).
		if md := extras.Meta; md != nil {
			m["igdb_metadata"] = jsonRawOrNil(md.IGDB)
			m["moby_metadata"] = jsonRawOrNil(md.Moby)
			m["ss_metadata"] = jsonRawOrNil(md.SS)
			m["launchbox_metadata"] = jsonRawOrNil(md.Launchbox)
			m["hasheous_metadata"] = jsonRawOrNil(md.Hasheous)
			m["flashpoint_metadata"] = jsonRawOrNil(md.Flashpoint)
			m["hltb_metadata"] = jsonRawOrNil(md.HLTB)
			m["demozoo_metadata"] = jsonRawOrNil(md.Demozoo)
			m["pouet_metadata"] = jsonRawOrNil(md.Pouet)
			m["csdb_metadata"] = jsonRawOrNil(md.CSDB)
			m["steam_metadata"] = jsonRawOrNil(md.Steam)
			m["gamelist_metadata"] = jsonRawOrNil(md.Gamelist)
			m["manual_metadata"] = jsonRawOrNil(md.Manual)
			m["merged_ra_metadata"] = jsonRawOrNil(md.RA)
			if alt := alternativeNames(md, sStr(r.Name)); len(alt) > 0 {
				m["alternative_names"] = alt
			}
		}
	}

	if detail {
		saves := assetList(extrasSaves(extras), "saves")
		states := assetList(extrasStates(extras), "states")
		shots := screenshotList(extras)
		cols := collectionList(extras)
		m["user_saves"] = saves
		m["user_states"] = states
		m["all_user_saves"] = saves
		m["all_user_states"] = states
		m["user_screenshots"] = shots
		m["all_user_screenshots"] = shots
		m["user_collections"] = cols
		m["all_user_notes"] = []any{}
		m["download_formats"] = []any{}
		// source_urls: every place a virtual rom can be downloaded — its primary
		// URL plus any captured mirrors, de-duplicated in order. The download
		// dialog reads this; an empty list shows "no download details".
		urls := []string{}
		seen := map[string]bool{}
		for _, u := range append([]string{r.SourceURL.String}, r.Mirrors...) {
			if u != "" && !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
		// Order sources by the configured host preference so the preferred host
		// (e.g. 1fichier.com) is first regardless of which was the primary URL.
		if extras != nil && len(extras.HostOrder) > 0 {
			sortByHostOrder(urls, extras.HostOrder)
			if hosts, ok := m["source_hosts"].([]any); ok {
				hs := make([]string, 0, len(hosts))
				for _, h := range hosts {
					if s, ok := h.(string); ok {
						hs = append(hs, s)
					}
				}
				sortByHostOrder(hs, extras.HostOrder)
				m["source_hosts"] = hs
			}
		}
		m["source_urls"] = urls
		// virtual_contents: the captured base/update/DLC link set (Contents card).
		m["virtual_contents"] = jsonRawOrNil(r.VirtualContents)
	} else {
		m["screenshot_path"] = nil
	}
	return m
}

const frontendResourcesPath = "/assets/romm/resources"

// filesJSON builds the RomFileSchema array from the loaded rom_files.
func filesJSON(r *store.RomFull, extras *romExtras) []map[string]any {
	out := []map[string]any{}
	if extras == nil {
		return out
	}
	for _, f := range extras.Files {
		full := f.FileName
		if f.FilePath != "" {
			full = f.FilePath + "/" + f.FileName
		}
		out = append(out, map[string]any{
			"id":              f.ID,
			"rom_id":          f.RomID,
			"file_name":       f.FileName,
			"file_path":       f.FilePath,
			"file_size_bytes": f.FileSizeBytes,
			"full_path":       full,
			"is_top_level":    isTopLevel(r, f),
			"last_modified":   nfloat(f.LastModified),
			"crc_hash":        sStr(f.CRCHash),
			"md5_hash":        sStr(f.MD5Hash),
			"sha1_hash":       sStr(f.SHA1Hash),
			"ra_hash":         sStr(f.RAHash),
			"chd_sha1_hash":   sStr(f.CHDSHA1Hash),
			"archive_members": jsonRawOrNil(f.ArchiveMembers),
			"category":        sStr(f.Category),
			"created_at":      f.CreatedAt,
			"updated_at":      f.UpdatedAt,
		})
	}
	return out
}

// isTopLevel reports whether a file is the rom's own file or sits directly in
// the rom's folder (mirrors RomFile.is_top_level).
func isTopLevel(r *store.RomFull, f store.RomFile) bool {
	romFull := r.FsPath + "/" + r.FsName
	full := f.FileName
	if f.FilePath != "" {
		full = f.FilePath + "/" + f.FileName
	}
	return full == romFull || f.FilePath == romFull || f.FilePath == r.FsPath
}

// fileShape returns (top-level file count, whether any file is nested) for the
// has_* flags, matching Rom.top_level_file_count / Rom.multi_file.
func fileShape(r *store.RomFull, files []store.RomFile) (topLevel int, multiFile bool) {
	for _, f := range files {
		if f.FilePath != "" && f.FilePath != r.FsPath {
			multiFile = true
		}
		if isTopLevel(r, f) {
			topLevel++
		}
	}
	return
}

// siblingsJSON builds the SiblingRomSchema array.
func siblingsJSON(extras *romExtras) []map[string]any {
	out := []map[string]any{}
	if extras == nil {
		return out
	}
	for _, s := range extras.Siblings {
		name := ""
		if s.Name.Valid {
			name = s.Name.String
		}
		out = append(out, map[string]any{
			"id":              s.ID,
			"name":            sStr(s.Name),
			"fs_name_no_tags": s.FsNameNoTags,
			"fs_name_no_ext":  s.FsNameNoExt,
			"is_main_sibling": s.IsMainSibling,
			"sort_comparator": sortComparator(name, s.FsNameNoTags),
		})
	}
	return out
}

var sortCompareRE = regexp.MustCompile(`[^a-z0-9]+`)

func sortComparator(name, fallback string) string {
	s := name
	if s == "" {
		s = fallback
	}
	return sortCompareRE.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "")
}

// mergedScreenshots turns the stored path_screenshots into resource URLs the
// Media tab loads (mirrors Rom.merged_screenshots).
func mergedScreenshots(extras *romExtras) []string {
	out := []string{}
	if extras == nil || extras.Meta == nil || !extras.Meta.PathScreenshots.Valid {
		return out
	}
	var paths []string
	if err := json.Unmarshal([]byte(extras.Meta.PathScreenshots.String), &paths); err != nil {
		return out
	}
	for _, p := range paths {
		if p != "" {
			out = append(out, frontendResourcesPath+"/"+p)
		}
	}
	return out
}

func nfloat(n sql.NullFloat64) any {
	if n.Valid {
		return n.Float64
	}
	return nil
}

func extrasSaves(e *romExtras) []store.Asset {
	if e == nil {
		return nil
	}
	return e.Saves
}
func extrasStates(e *romExtras) []store.Asset {
	if e == nil {
		return nil
	}
	return e.States
}

// assetList serializes saves/states into the SaveSchema/StateSchema array.
func assetList(items []store.Asset, kind string) []map[string]any {
	out := []map[string]any{}
	for i := range items {
		out = append(out, assetJSON(&items[i], kind))
	}
	return out
}

func screenshotList(e *romExtras) []map[string]any {
	out := []map[string]any{}
	if e == nil {
		return out
	}
	for i := range e.Screenshots {
		out = append(out, screenshotJSON(&e.Screenshots[i]))
	}
	return out
}

func collectionList(e *romExtras) []map[string]any {
	out := []map[string]any{}
	if e == nil {
		return out
	}
	for i := range e.Collections {
		out = append(out, collectionJSON(&e.Collections[i]))
	}
	return out
}

// alternativeNames collects the alternate titles a rom's providers recorded
// (igdb_metadata.alternative_names, moby_metadata.alternate_titles,
// ss_metadata.alternative_names), dropping the displayed name and duplicates.
func alternativeNames(md *store.RomMetadata, displayName any) []string {
	seen := map[string]bool{}
	if name, ok := displayName.(string); ok && name != "" {
		seen[strings.ToLower(strings.TrimSpace(name))] = true
	}
	var out []string
	add := func(raw sql.NullString, key string) {
		if !raw.Valid || raw.String == "" {
			return
		}
		var obj map[string]any
		if json.Unmarshal([]byte(raw.String), &obj) != nil {
			return
		}
		arr, ok := obj[key].([]any)
		if !ok {
			return
		}
		for _, v := range arr {
			s, ok := v.(string)
			if !ok {
				s2, ok2 := v.(map[string]any)
				if ok2 {
					if nm, ok3 := s2["name"].(string); ok3 {
						s = nm
					} else if nm, ok3 := s2["title"].(string); ok3 {
						s = nm
					}
				}
				if s == "" {
					continue
				}
			}
			key := strings.ToLower(strings.TrimSpace(s))
			if key != "" && !seen[key] {
				seen[key] = true
				out = append(out, s)
			}
		}
	}
	add(md.IGDB, "alternative_names")
	add(md.Moby, "alternate_titles")
	add(md.SS, "alternative_names")
	return out
}

// sortByHostOrder stably orders URLs/hosts by the configured host preference:
// an item whose host matches an earlier entry in order sorts first; unlisted
// items keep their relative order at the end.
func sortByHostOrder(items []string, order []string) {
	rank := func(s string) int {
		low := strings.ToLower(s)
		for i, h := range order {
			if h != "" && strings.Contains(low, strings.ToLower(h)) {
				return i
			}
		}
		return len(order)
	}
	sort.SliceStable(items, func(i, j int) bool {
		return rank(items[i]) < rank(items[j])
	})
}
