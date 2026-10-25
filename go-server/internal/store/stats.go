package store

import (
	"context"
	"database/sql"
)

// Stats is the library-wide count summary the frontend's /api/stats widget reads.
type Stats struct {
	Platforms          int64 `json:"PLATFORMS"`
	Roms               int64 `json:"ROMS"`
	Saves              int64 `json:"SAVES"`
	States             int64 `json:"STATES"`
	Screenshots        int64 `json:"SCREENSHOTS"`
	TotalFilesizeBytes int64 `json:"TOTAL_FILESIZE_BYTES"`
}

// GetStats returns the library totals.
func (s *Store) GetStats(ctx context.Context) (Stats, error) {
	var st Stats
	row := s.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM platforms),
			(SELECT COUNT(*) FROM roms),
			(SELECT COUNT(*) FROM saves),
			(SELECT COUNT(*) FROM states),
			(SELECT COUNT(*) FROM screenshots),
			(SELECT COALESCE(SUM(fs_size_bytes),0) FROM roms)`)
	err := row.Scan(&st.Platforms, &st.Roms, &st.Saves, &st.States, &st.Screenshots, &st.TotalFilesizeBytes)
	return st, err
}

// RandomRomID returns a random rom id (0 if the library is empty).
func (s *Store) RandomRomID(ctx context.Context) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM roms ORDER BY RAND() LIMIT 1`).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return id, nil
}
