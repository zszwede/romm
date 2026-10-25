package store

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// VirtualSource is an archive.org source definition (virtual_rom_sources).
type VirtualSource struct {
	ID           int64      `json:"id"`
	PlatformID   int64      `json:"platform_id"`
	URL          string     `json:"url"`
	Kind         string     `json:"kind"`
	RegionFilter *string    `json:"region_filter"`
	Enabled      bool       `json:"enabled"`
	LastSyncedAt *time.Time `json:"last_synced_at"`
	LastResult   *string    `json:"last_result"`
}

const vsCols = `id, platform_id, COALESCE(url,''), COALESCE(kind,''), region_filter, COALESCE(enabled,1), last_synced_at, last_result`

func scanVirtualSource(row interface{ Scan(...any) error }) (*VirtualSource, error) {
	var v VirtualSource
	var enabled int
	if err := row.Scan(&v.ID, &v.PlatformID, &v.URL, &v.Kind, &v.RegionFilter, &enabled, &v.LastSyncedAt, &v.LastResult); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	v.Enabled = enabled != 0
	return &v, nil
}

// ListVirtualSources returns sources, optionally filtered by platform.
func (s *Store) ListVirtualSources(ctx context.Context, platformID int64) ([]VirtualSource, error) {
	q := `SELECT ` + vsCols + ` FROM virtual_rom_sources`
	var args []any
	if platformID > 0 {
		q += ` WHERE platform_id = ?`
		args = append(args, platformID)
	}
	q += ` ORDER BY id`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VirtualSource{}
	for rows.Next() {
		v, err := scanVirtualSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *v)
	}
	return out, rows.Err()
}

// VirtualSourceUpdate carries editable source fields; nil = unchanged.
type VirtualSourceUpdate struct {
	URL          *string
	RegionFilter *string
	Enabled      *bool
}

// UpdateVirtualSource edits a source and returns it.
func (s *Store) UpdateVirtualSource(ctx context.Context, id int64, up VirtualSourceUpdate) (*VirtualSource, error) {
	var sets []string
	var args []any
	if up.URL != nil {
		sets, args = append(sets, "url = ?"), append(args, *up.URL)
	}
	if up.RegionFilter != nil {
		sets, args = append(sets, "region_filter = ?"), append(args, nullIfEmpty(*up.RegionFilter))
	}
	if up.Enabled != nil {
		sets, args = append(sets, "enabled = ?"), append(args, boolToInt(*up.Enabled))
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = NOW()")
		args = append(args, id)
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE virtual_rom_sources SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return nil, err
		}
	}
	return scanVirtualSource(s.DB.QueryRowContext(ctx, `SELECT `+vsCols+` FROM virtual_rom_sources WHERE id = ?`, id))
}

// DeleteVirtualSource removes a source definition.
func (s *Store) DeleteVirtualSource(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM virtual_rom_sources WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
