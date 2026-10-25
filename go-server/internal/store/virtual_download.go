package store

import (
	"context"
	"database/sql"
)

// QueuedRom is one entry in the virtual-download queue.
type QueuedRom struct {
	RomID      int64  `json:"rom_id"`
	PlatformID int64  `json:"platform_id"`
	Name       string `json:"name"`
	Status     string `json:"status"`
}

// SetDownloadStatus sets (or clears, when status=="") a rom's download_status.
func (s *Store) SetDownloadStatus(ctx context.Context, romID int64, status string) error {
	var v any
	if status != "" {
		v = status
	}
	_, err := s.DB.ExecContext(ctx,
		`UPDATE roms SET download_status = ?, updated_at = NOW() WHERE id = ?`, v, romID)
	return err
}

// DownloadQueue returns roms flagged wanted or queued.
func (s *Store) DownloadQueue(ctx context.Context) ([]QueuedRom, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, platform_id, COALESCE(fs_name,''), COALESCE(download_status,'')
		FROM roms WHERE download_status IN ('wanted','queued')
		ORDER BY updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QueuedRom{}
	for rows.Next() {
		var q QueuedRom
		if err := rows.Scan(&q.RomID, &q.PlatformID, &q.Name, &q.Status); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// ExcludeRom records a virtual-game exclusion (so a later list re-import won't
// re-add it) and deletes the rom. Runs in a tx.
func (s *Store) ExcludeRom(ctx context.Context, romID int64) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := excludeRom(ctx, tx, romID); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func excludeRom(ctx context.Context, tx txer, romID int64) error {
	var platformID int64
	var name sql.NullString
	err := tx.QueryRowContext(ctx,
		`SELECT platform_id, COALESCE(NULLIF(fs_name_no_ext,''), fs_name) FROM roms WHERE id = ?`, romID).
		Scan(&platformID, &name)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT IGNORE INTO virtual_exclusions (platform_id, name, created_at, updated_at)
		 VALUES (?, ?, NOW(), NOW())`, platformID, name.String); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM roms WHERE id = ?`, romID)
	return err
}

// RomDownloadStatus returns a rom's current download_status ("" if null/none).
func (s *Store) RomDownloadStatus(ctx context.Context, romID int64) (string, error) {
	var st sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT download_status FROM roms WHERE id = ?`, romID).Scan(&st)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return st.String, err
}
