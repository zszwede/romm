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

// RomDownloadStatus returns a rom's current download_status ("" if null/none).
func (s *Store) RomDownloadStatus(ctx context.Context, romID int64) (string, error) {
	var st sql.NullString
	err := s.DB.QueryRowContext(ctx, `SELECT download_status FROM roms WHERE id = ?`, romID).Scan(&st)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return st.String, err
}
