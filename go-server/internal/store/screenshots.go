package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
)

// Screenshot is a screenshots-table row (no emulator col; has is_gallery/is_public).
type Screenshot struct {
	ID        int64  `json:"id"`
	RomID     int64  `json:"rom_id"`
	UserID    int64  `json:"user_id"`
	FileName  string `json:"file_name"`
	FilePath  string `json:"file_path"`
	SizeBytes int64  `json:"file_size_bytes"`
}

// InsertScreenshot records an uploaded screenshot and returns its id.
func (s *Store) InsertScreenshot(ctx context.Context, sc Screenshot) (int64, error) {
	ext := strings.TrimPrefix(filepath.Ext(sc.FileName), ".")
	noExt := strings.TrimSuffix(sc.FileName, filepath.Ext(sc.FileName))
	noTags := strings.Join(strings.Fields(storeTagRE.ReplaceAllString(noExt, " ")), " ")
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO screenshots
			(rom_id, user_id, file_name, file_name_no_tags, file_name_no_ext, file_extension,
			 file_path, file_size_bytes, is_gallery, is_public, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, NOW(), NOW())`,
		sc.RomID, sc.UserID, sc.FileName, noTags, noExt, ext, sc.FilePath, sc.SizeBytes)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListScreenshots returns a rom's screenshots for a user.
func (s *Store) ListScreenshots(ctx context.Context, romID, userID int64) ([]Screenshot, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, rom_id, user_id, file_name, COALESCE(file_path,''), COALESCE(file_size_bytes,0)
		 FROM screenshots WHERE rom_id = ? AND user_id = ? ORDER BY updated_at DESC`, romID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Screenshot{}
	for rows.Next() {
		var sc Screenshot
		if err := rows.Scan(&sc.ID, &sc.RomID, &sc.UserID, &sc.FileName, &sc.FilePath, &sc.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, sc)
	}
	return out, rows.Err()
}

// ScreenshotLocation returns a screenshot's relative dir and file name.
func (s *Store) ScreenshotLocation(ctx context.Context, id int64) (filePath, fileName string, found bool, err error) {
	err = s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(file_path,''), file_name FROM screenshots WHERE id = ?`, id).Scan(&filePath, &fileName)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return filePath, fileName, true, nil
}

// DeleteScreenshot removes a screenshot row.
func (s *Store) DeleteScreenshot(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM screenshots WHERE id = ?`, id)
	return err
}
