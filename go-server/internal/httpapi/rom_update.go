package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/auth"
	"github.com/zszwede/romm-go/internal/store"
)

// queryBool reports whether query parameter k is a truthy value (axios sends
// booleans as "true"/"false").
func queryBool(r *http.Request, k string) bool {
	switch strings.ToLower(r.URL.Query().Get(k)) {
	case "true", "1", "on", "yes":
		return true
	}
	return false
}

// updateRom handles PUT /api/roms/{id}: either unmatch all metadata
// (?unmatch_metadata=true) or apply an edit / manual match from the multipart
// form (name, summary, provider ids + raw provider metadata, cover). It returns
// the updated DetailedRom, as the frontend reloads the page from the response.
//
// Not yet handled: physical file rename (fs_name changes are ignored, so DB and
// disk never diverge) and custom artwork file upload (url_cover is handled).
func (s *Server) updateRom(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	if id <= 0 {
		writeErr(w, http.StatusBadRequest, "bad id")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	rom, err := s.st.GetRomFull(ctx, id)
	if err != nil {
		log.Printf("updateRom getRomFull(%d): %v", id, err)
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if rom == nil {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}

	if queryBool(r, "unmatch_metadata") {
		if err := s.st.UnmatchRom(ctx, id, rom.FsName); err != nil {
			log.Printf("updateRom unmatch(%d): %v", id, err)
			writeErr(w, http.StatusInternalServerError, "update failed")
			return
		}
	} else {
		fields, err := romEditFields(r)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "bad form")
			return
		}
		if queryBool(r, "remove_cover") {
			fields["url_cover"] = ""
			fields["path_cover_s"] = ""
			fields["path_cover_l"] = ""
		}
		if err := s.st.UpdateRomFields(ctx, id, fields); err != nil {
			log.Printf("updateRom update(%d): %v", id, err)
			writeErr(w, http.StatusInternalServerError, "update failed")
			return
		}
	}

	s.writeRomDetail(w, ctx, claims, id)
}

// deleteRoms handles POST /api/roms/delete: {roms:[ids], delete_from_fs:[ids]}.
// Each rom's DB row is removed (related rows cascade); ids also in delete_from_fs
// have their file removed from the library (confined to the library root, empty
// parent dir cleaned up). Returns the BulkOperationResponse the frontend expects.
func (s *Server) deleteRoms(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	var body struct {
		Roms         []int64 `json:"roms"`
		DeleteFromFs []int64 `json:"delete_from_fs"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	fromFs := map[int64]bool{}
	for _, id := range body.DeleteFromFs {
		fromFs[id] = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var deleted, failed []int64
	var errs []string
	for _, id := range body.Roms {
		// Resolve the file location before the row is gone (needed for fs removal).
		fsPath, fsName, found, err := s.st.GetRomLocation(ctx, id)
		if err != nil {
			failed = append(failed, id)
			errs = append(errs, fmt.Sprintf("ROM %d: lookup failed", id))
			continue
		}
		if !found {
			failed = append(failed, id)
			errs = append(errs, fmt.Sprintf("ROM with ID %d not found", id))
			continue
		}
		ok, err := s.st.DeleteRom(ctx, id)
		if err != nil || !ok {
			failed = append(failed, id)
			errs = append(errs, fmt.Sprintf("ROM %d: delete failed", id))
			continue
		}
		if fromFs[id] {
			if msg := s.removeRomFile(fsPath, fsName); msg != "" {
				errs = append(errs, fmt.Sprintf("ROM %d: %s", id, msg))
			}
		}
		deleted = append(deleted, id)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"successful_items": len(deleted),
		"failed_ids":       failed,
		"errors":           errs,
	})
}

// removeRomFile deletes a rom's file (or directory) from the library, confined
// to the library root, and cleans up its parent directory if it becomes empty.
// Returns "" on success or when nothing needed removing, else a short message.
func (s *Server) removeRomFile(fsPath, fsName string) string {
	if s.libraryRoot == "" || fsName == "" {
		return ""
	}
	clean := filepath.Clean(filepath.Join(s.libraryRoot, filepath.FromSlash(fsPath), fsName))
	if clean != s.libraryRoot && !strings.HasPrefix(clean, s.libraryRoot+string(os.PathSeparator)) {
		return "refusing to delete file outside library root"
	}
	if err := os.RemoveAll(clean); err != nil {
		return "file removal failed: " + err.Error()
	}
	// Best-effort: remove the parent dir if now empty (and not the library root).
	parent := filepath.Dir(clean)
	if parent != s.libraryRoot && strings.HasPrefix(parent, s.libraryRoot+string(os.PathSeparator)) {
		if entries, err := os.ReadDir(parent); err == nil && len(entries) == 0 {
			_ = os.Remove(parent)
		}
	}
	return ""
}

// romEditFields reads the multipart edit form into a column→value map for
// UpdateRomFields. Empty id fields become NULL (clearing a match); empty
// name_sort_key becomes NULL so ordering falls back to the name. fs_name is
// deliberately not mapped (no physical rename yet).
func romEditFields(r *http.Request) (map[string]any, error) {
	if err := r.ParseMultipartForm(8 << 20); err != nil && err != http.ErrNotMultipart {
		return nil, err
	}
	get := func(k string) (string, bool) {
		if r.Form == nil {
			return "", false
		}
		v, ok := r.Form[k]
		if !ok || len(v) == 0 {
			return "", false
		}
		return v[0], true
	}
	fields := map[string]any{}

	// Plain text fields.
	if v, ok := get("name"); ok {
		fields["name"] = v
	}
	if v, ok := get("summary"); ok {
		fields["summary"] = v
	}
	if v, ok := get("name_sort_key"); ok {
		if strings.TrimSpace(v) == "" {
			fields["name_sort_key"] = nil
		} else {
			fields["name_sort_key"] = v
		}
	}
	// url_cover only when no artwork file was uploaded (artwork upload TBD).
	if _, hasArt := get("artwork"); !hasArt {
		if v, ok := get("url_cover"); ok {
			fields["url_cover"] = v
		}
	}

	// Provider ids: empty string clears (NULL), otherwise the raw value (MySQL
	// coerces numeric strings into the integer id columns).
	idCols := []string{
		"igdb_id", "sgdb_id", "moby_id", "ss_id", "launchbox_id", "ra_id",
		"flashpoint_id", "hasheous_id", "tgdb_id", "hltb_id", "demozoo_id",
		"pouet_id", "csdb_id", "steam_id", "gamelist_id", "libretro_id",
	}
	for _, c := range idCols {
		if v, ok := get(c); ok {
			if strings.TrimSpace(v) == "" {
				fields[c] = nil
			} else {
				fields[c] = v
			}
		}
	}

	// raw_<provider> -> <provider>_metadata; raw_manual_metadata -> manual_metadata.
	if r.Form != nil {
		for k, vals := range r.Form {
			if !strings.HasPrefix(k, "raw_") || len(vals) == 0 {
				continue
			}
			var col string
			if k == "raw_manual_metadata" {
				col = "manual_metadata"
			} else {
				col = strings.TrimPrefix(k, "raw_") + "_metadata"
			}
			if romUpdatableMetadataCol(col) {
				fields[col] = vals[0]
			}
		}
	}
	return fields, nil
}

// romUpdatableMetadataCol reports whether col is a provider metadata blob column
// the edit form is allowed to set.
func romUpdatableMetadataCol(col string) bool {
	switch col {
	case "igdb_metadata", "moby_metadata", "ss_metadata", "ra_metadata",
		"launchbox_metadata", "hasheous_metadata", "flashpoint_metadata",
		"hltb_metadata", "demozoo_metadata", "pouet_metadata", "csdb_metadata",
		"steam_metadata", "gamelist_metadata", "manual_metadata":
		return true
	}
	return false
}

// writeRomDetail loads and writes the full DetailedRom response for id (the same
// shape GET /api/roms/{id} returns), so an update's response reloads the page.
func (s *Server) writeRomDetail(w http.ResponseWriter, ctx context.Context, claims *auth.Claims, id int64) {
	rom, err := s.st.GetRomFull(ctx, id)
	if err != nil || rom == nil {
		writeErr(w, http.StatusInternalServerError, "reload failed")
		return
	}
	uid, _ := s.st.UserIDByUsername(ctx, claims.Username)
	var ru *store.RomUser
	if uid > 0 {
		ru, _ = s.st.GetRomUser(ctx, uid, id)
	}
	if rom.IsVirtual {
		rom.Mirrors, _ = s.st.RomMirrorURLs(ctx, id)
	}
	extras := &romExtras{}
	if s.configFile != nil {
		extras.HostOrder = s.configFile.Downloads.DefaultHostOrder
	}
	extras.Files, _ = s.st.ListRomFiles(ctx, id)
	extras.Siblings, _ = s.st.ListSiblings(ctx, id, uid)
	extras.Meta, _ = s.st.GetRomMetadata(ctx, id)
	extras.Collections, _ = s.st.RomCollections(ctx, id)
	if uid > 0 {
		extras.Saves, _ = s.st.ListAssets(ctx, "saves", id, uid)
		extras.States, _ = s.st.ListAssets(ctx, "states", id, uid)
		extras.Screenshots, _ = s.st.ListScreenshots(ctx, id, uid)
	}
	writeJSON(w, http.StatusOK, romJSON(rom, ru, uid, true, extras))
}
