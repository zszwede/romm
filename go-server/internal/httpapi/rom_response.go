package httpapi

import (
	"database/sql"
	"encoding/json"
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

// romJSON builds the full RomSchema. detail adds the DetailedRomSchema fields.
func romJSON(r *store.RomFull, ru *store.RomUser, userID int64, detail bool) map[string]any {
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

		"missing_from_fs": r.MissingFromFs,
		"is_physical":     r.IsPhysical,
		"is_virtual":      r.IsVirtual,
		"source_url":      sStr(r.SourceURL),
		"source_hosts":    jsonArr(r.SourceHosts),

		"rom_user": romUserJSON(ru, userID, r.ID),

		// sibling/file collections — populated once those endpoints land.
		"siblings":     []any{},
		"user_notes":   []any{},
		"files":        []any{},
	}

	if detail {
		m["user_saves"] = []any{}
		m["user_states"] = []any{}
		m["all_user_saves"] = []any{}
		m["all_user_states"] = []any{}
		m["user_screenshots"] = []any{}
		m["all_user_screenshots"] = []any{}
		m["user_collections"] = []any{}
		m["all_user_notes"] = []any{}
		m["download_formats"] = []any{}
		m["source_urls"] = []any{}
		m["virtual_contents"] = nil
	} else {
		m["screenshot_path"] = nil
	}
	return m
}
