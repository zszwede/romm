package store

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// romUpdatableColumns whitelists the roms columns that PUT /api/roms/{id} may
// set. Anything not listed is silently dropped, so a handler can never write
// platform_id, fs_path, is_virtual, hashes, etc. by accident.
var romUpdatableColumns = map[string]bool{
	"name": true, "name_sort_key": true, "summary": true, "slug": true, "revision": true,
	"url_cover": true, "path_cover_s": true, "path_cover_l": true,
	"url_manual": true, "path_manual": true,
	"url_screenshots": true, "path_screenshots": true,
	// metadata provider ids
	"igdb_id": true, "sgdb_id": true, "moby_id": true, "ss_id": true, "launchbox_id": true,
	"ra_id": true, "hasheous_id": true, "tgdb_id": true, "flashpoint_id": true, "hltb_id": true,
	"demozoo_id": true, "pouet_id": true, "csdb_id": true, "steam_id": true,
	"gamelist_id": true, "libretro_id": true,
	// provider metadata blobs
	"igdb_metadata": true, "moby_metadata": true, "ss_metadata": true, "ra_metadata": true,
	"launchbox_metadata": true, "hasheous_metadata": true, "flashpoint_metadata": true,
	"hltb_metadata": true, "demozoo_metadata": true, "pouet_metadata": true, "csdb_metadata": true,
	"steam_metadata": true, "gamelist_metadata": true, "manual_metadata": true,
}

// metadataIDColumns are the provider-id columns cleared by an unmatch (the live
// twin of the Python UNMATCH_ID_FIELDS: sgdb_id + METADATA_SOURCE_COLUMNS).
var metadataIDColumns = []string{
	"sgdb_id", "igdb_id", "ss_id", "moby_id", "launchbox_id", "ra_id", "hasheous_id",
	"tgdb_id", "flashpoint_id", "hltb_id", "demozoo_id", "pouet_id", "csdb_id",
	"steam_id", "gamelist_id", "libretro_id",
}

// metadataBlobColumns are the provider metadata JSON columns cleared by an
// unmatch. manual_metadata is intentionally excluded (an uploaded manual is not
// a provider match).
var metadataBlobColumns = []string{
	"igdb_metadata", "moby_metadata", "ss_metadata", "ra_metadata", "launchbox_metadata",
	"hasheous_metadata", "flashpoint_metadata", "hltb_metadata", "demozoo_metadata",
	"pouet_metadata", "csdb_metadata", "steam_metadata", "gamelist_metadata",
}

// UpdateRomFields applies a whitelisted set of column updates to one rom.
// Unknown keys are ignored. A nil value writes SQL NULL. No-op when nothing
// updatable is supplied.
func (s *Store) UpdateRomFields(ctx context.Context, id int64, fields map[string]any) error {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if romUpdatableColumns[k] {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	sort.Strings(keys) // deterministic SET order
	sets := make([]string, len(keys))
	args := make([]any, 0, len(keys)+1)
	for i, k := range keys {
		sets[i] = k + " = ?"
		args = append(args, fields[k])
	}
	args = append(args, id)
	q := fmt.Sprintf("UPDATE roms SET %s, updated_at = NOW() WHERE id = ?", strings.Join(sets, ", "))
	_, err := s.DB.ExecContext(ctx, q, args...)
	return err
}

// DeleteRom removes a rom row. Related rows (rom_files, metadata, user rows,
// siblings) cascade via their foreign keys. found is false when no row matched.
func (s *Store) DeleteRom(ctx context.Context, id int64) (found bool, err error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM roms WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnmatchRom clears every metadata provider match from a rom: all provider ids
// and metadata blobs, the provider-derived name/cover/screenshots/summary. The
// name reverts to the filesystem name and name_sort_key to NULL (ordering then
// falls back to the name). Mirrors the Python update_rom(unmatch_metadata=true).
func (s *Store) UnmatchRom(ctx context.Context, id int64, fsName string) error {
	fields := map[string]any{
		"name":             fsName,
		"name_sort_key":    nil,
		"summary":          "",
		"slug":             "",
		"revision":         "",
		"url_cover":        "",
		"path_cover_s":     "",
		"path_cover_l":     "",
		"url_manual":       "",
		"url_screenshots":  "[]",
		"path_screenshots": "[]",
	}
	for _, c := range metadataIDColumns {
		fields[c] = nil
	}
	for _, c := range metadataBlobColumns {
		fields[c] = "{}"
	}
	return s.UpdateRomFields(ctx, id, fields)
}
