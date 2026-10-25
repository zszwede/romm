package store

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/hash"
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

// CreateVirtualSource inserts an archive.org source for a platform.
func (s *Store) CreateVirtualSource(ctx context.Context, platformID int64, url string) (*VirtualSource, error) {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO virtual_rom_sources (platform_id, url, kind, enabled, created_at, updated_at)
		VALUES (?, ?, 'archive', 1, NOW(), NOW())`, platformID, url)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return scanVirtualSource(s.DB.QueryRowContext(ctx, `SELECT `+vsCols+` FROM virtual_rom_sources WHERE id = ?`, id))
}

// VirtualGameInput is one discovered game to create as a virtual rom.
type VirtualGameInput struct {
	Name      string
	SourceURL string
	Size      int64
}

// CreateVirtualRom inserts a virtual rom for a discovered game, skipping ones
// excluded for the platform or already present. Returns whether a row was made.
func (s *Store) CreateVirtualRom(ctx context.Context, platformID int64, fsSlug string, g VirtualGameInput) (bool, error) {
	return createVirtualRom(ctx, s.DB, platformID, fsSlug, g)
}

func createVirtualRom(ctx context.Context, e txer, platformID int64, fsSlug string, g VirtualGameInput) (bool, error) {
	noExt := g.Name
	if i := strings.LastIndex(g.Name, "."); i > 0 {
		noExt = g.Name[:i]
	}
	// Excluded for this platform? (matched on fs_name_no_ext)
	var excluded int
	if err := e.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM virtual_exclusions WHERE platform_id = ? AND name = ?`, platformID, noExt).
		Scan(&excluded); err != nil {
		return false, err
	}
	if excluded > 0 {
		return false, nil
	}
	fsPath := "roms/" + fsSlug
	ext := ""
	if i := strings.LastIndex(g.Name, "."); i > 0 {
		ext = g.Name[i+1:]
	}
	noTags := strings.Join(strings.Fields(storeTagRE.ReplaceAllString(noExt, " ")), " ")
	fph := virtualFullPathHash(fsPath, g.Name)
	res, err := e.ExecContext(ctx, `
		INSERT INTO roms
			(platform_id, fs_name, fs_name_no_tags, fs_extension, fs_path, fs_name_no_ext,
			 fs_size_bytes, full_path_hash, name, is_virtual, missing_from_fs, source_url,
			 download_status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 1, 1, ?, NULL, NOW(), NOW())
		ON DUPLICATE KEY UPDATE updated_at = updated_at`,
		platformID, g.Name, noTags, ext, fsPath, noExt, g.Size, fph, noExt, g.SourceURL)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil // 0 when the ON DUPLICATE no-op fired (already present)
}

// ListEnabledSources returns a platform's enabled source URLs.
func (s *Store) ListEnabledSources(ctx context.Context, platformID int64) ([]string, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT url FROM virtual_rom_sources WHERE platform_id = ? AND COALESCE(enabled,1) = 1`, platformID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func virtualFullPathHash(fsPath, fsName string) string { return hash.FullPathHash(fsPath, fsName) }
