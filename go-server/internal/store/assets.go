package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
)

// assetKinds whitelists the table names that can be interpolated into asset
// queries (saves and states share an identical shape). Interpolation is only
// ever one of these constants, never user input.
var assetKinds = map[string]bool{"saves": true, "states": true}

// Asset is a save or save-state file row.
type Asset struct {
	ID        int64  `json:"id"`
	RomID     int64  `json:"rom_id"`
	UserID    int64  `json:"user_id"`
	FileName  string `json:"file_name"`
	FilePath  string `json:"file_path"` // dir relative to the assets root
	SizeBytes int64  `json:"file_size_bytes"`
	Emulator  string `json:"emulator,omitempty"`
}

func checkKind(kind string) error {
	if !assetKinds[kind] {
		return fmt.Errorf("invalid asset kind %q", kind)
	}
	return nil
}

// ListAssets returns a user's saves/states for a rom, newest first.
func (s *Store) ListAssets(ctx context.Context, kind string, romID, userID int64) ([]Asset, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx,
		"SELECT id, rom_id, user_id, file_name, file_path, file_size_bytes, COALESCE(emulator,'') "+
			"FROM "+kind+" WHERE rom_id = ? AND user_id = ? ORDER BY updated_at DESC",
		romID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Asset{}
	for rows.Next() {
		var a Asset
		if err := rows.Scan(&a.ID, &a.RomID, &a.UserID, &a.FileName, &a.FilePath, &a.SizeBytes, &a.Emulator); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// InsertAsset records an uploaded save/state and returns its id.
func (s *Store) InsertAsset(ctx context.Context, kind string, a Asset) (int64, error) {
	if err := checkKind(kind); err != nil {
		return 0, err
	}
	ext := strings.TrimPrefix(filepath.Ext(a.FileName), ".")
	noExt := strings.TrimSuffix(a.FileName, filepath.Ext(a.FileName))
	noTags := strings.Join(strings.Fields(storeTagRE.ReplaceAllString(noExt, " ")), " ")
	res, err := s.DB.ExecContext(ctx,
		"INSERT INTO "+kind+" (rom_id, user_id, file_name, file_name_no_tags, file_name_no_ext, "+
			"file_extension, file_path, file_size_bytes, emulator, created_at, updated_at) "+
			"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())",
		a.RomID, a.UserID, a.FileName, noTags, noExt, ext, a.FilePath, a.SizeBytes, nz(a.Emulator))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// AssetLocation returns a save/state's relative dir and file name.
func (s *Store) AssetLocation(ctx context.Context, kind string, id int64) (filePath, fileName string, found bool, err error) {
	if err = checkKind(kind); err != nil {
		return "", "", false, err
	}
	err = s.DB.QueryRowContext(ctx,
		"SELECT COALESCE(file_path,''), COALESCE(file_name,'') FROM "+kind+" WHERE id = ?", id).
		Scan(&filePath, &fileName)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return filePath, fileName, fileName != "", nil
}

// DeleteAsset removes a save/state row.
func (s *Store) DeleteAsset(ctx context.Context, kind string, id int64) error {
	if err := checkKind(kind); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, "DELETE FROM "+kind+" WHERE id = ?", id)
	return err
}

// AssetUpdate carries editable save/state metadata; nil = unchanged.
type AssetUpdate struct {
	IsPublic   *bool
	IsFavorite *bool
	FileName   *string
	Labels     *string // JSON array text
}

// UpdateAsset applies metadata edits to a save/state row.
func (s *Store) UpdateAsset(ctx context.Context, kind string, id int64, up AssetUpdate) error {
	if err := checkKind(kind); err != nil {
		return err
	}
	var sets []string
	var args []any
	if up.IsPublic != nil {
		sets, args = append(sets, "is_public = ?"), append(args, boolToInt(*up.IsPublic))
	}
	if up.IsFavorite != nil {
		sets, args = append(sets, "is_favorite = ?"), append(args, boolToInt(*up.IsFavorite))
	}
	if up.FileName != nil {
		sets, args = append(sets, "file_name = ?"), append(args, *up.FileName)
	}
	if up.Labels != nil {
		sets, args = append(sets, "labels = ?"), append(args, *up.Labels)
	}
	if len(sets) == 0 {
		return nil
	}
	sets = append(sets, "updated_at = NOW()")
	args = append(args, id)
	_, err := s.DB.ExecContext(ctx, "UPDATE "+kind+" SET "+joinComma(sets)+" WHERE id = ?", args...)
	return err
}

// GetAssetByID returns one save/state row.
func (s *Store) GetAssetByID(ctx context.Context, kind string, id int64) (*Asset, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	var a Asset
	err := s.DB.QueryRowContext(ctx,
		"SELECT id, rom_id, user_id, file_name, COALESCE(file_path,''), COALESCE(file_size_bytes,0), COALESCE(emulator,'') "+
			"FROM "+kind+" WHERE id = ?", id).
		Scan(&a.ID, &a.RomID, &a.UserID, &a.FileName, &a.FilePath, &a.SizeBytes, &a.Emulator)
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// BulkDeleteAssets removes save/state rows by id and returns the ids deleted.
func (s *Store) BulkDeleteAssets(ctx context.Context, kind string, ids []int64) ([]int64, error) {
	if err := checkKind(kind); err != nil {
		return nil, err
	}
	deleted := []int64{}
	for _, id := range ids {
		res, err := s.DB.ExecContext(ctx, "DELETE FROM "+kind+" WHERE id = ?", id)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			deleted = append(deleted, id)
		}
	}
	return deleted, nil
}

func joinComma(ss []string) string {
	out := ""
	for i, s := range ss {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
