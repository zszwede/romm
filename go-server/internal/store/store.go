// Package store is the data layer. Phase 1 reads the EXISTING MariaDB schema
// (the one Alembic created for the Python app) so all current data — roms,
// platforms, users, collections — is used as-is with no migration. New
// migrations (goose) come in a later phase; for now the Go server only reads.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

type Store struct{ DB *sql.DB }

// Open connects to MariaDB and verifies the connection.
func Open(host, port, user, pass, name string) (*Store, error) {
	dsn := fmt.Sprintf(
		"%s:%s@tcp(%s:%s)/%s?parseTime=true&charset=utf8mb4&loc=UTC",
		user, pass, host, port, name,
	)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping %s:%s/%s: %w", host, port, name, err)
	}
	return &Store{DB: db}, nil
}

func (s *Store) Close() error { return s.DB.Close() }

// Counts is a quick library summary used by the heartbeat / smoke test.
type Counts struct {
	Roms      int `json:"roms"`
	Virtual   int `json:"virtual"`
	Platforms int `json:"platforms"`
	Users     int `json:"users"`
}

func (s *Store) Counts(ctx context.Context) (Counts, error) {
	var c Counts
	row := s.DB.QueryRowContext(ctx, `
		SELECT
			(SELECT COUNT(*) FROM roms),
			(SELECT COUNT(*) FROM roms WHERE is_virtual = 1),
			(SELECT COUNT(*) FROM platforms),
			(SELECT COUNT(*) FROM users)`)
	if err := row.Scan(&c.Roms, &c.Virtual, &c.Platforms, &c.Users); err != nil {
		return c, err
	}
	return c, nil
}

// Rom is the minimal projection the Phase-1 list endpoint returns.
type Rom struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	FsName     string `json:"fs_name"`
	PlatformID int64  `json:"platform_id"`
	IsVirtual  bool   `json:"is_virtual"`
}

// ListRoms returns up to limit roms, optionally filtered by platform.
func (s *Store) ListRoms(ctx context.Context, platformID int64, limit int) ([]Rom, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := `SELECT id, COALESCE(name, ''), COALESCE(fs_name, ''), platform_id, is_virtual FROM roms`
	args := []any{}
	if platformID > 0 {
		q += ` WHERE platform_id = ?`
		args = append(args, platformID)
	}
	q += ` ORDER BY name LIMIT ?`
	args = append(args, limit)

	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Rom, 0, limit)
	for rows.Next() {
		var r Rom
		if err := rows.Scan(&r.ID, &r.Name, &r.FsName, &r.PlatformID, &r.IsVirtual); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Platform is the list projection with a rom count.
type Platform struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	FsSlug   string `json:"fs_slug"`
	RomCount int    `json:"rom_count"`
}

// ListPlatforms returns every platform with how many roms it holds.
func (s *Store) ListPlatforms(ctx context.Context) ([]Platform, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT p.id,
		       COALESCE(NULLIF(p.custom_name,''), p.name, p.slug),
		       p.slug,
		       COALESCE(p.fs_slug, ''),
		       COUNT(r.id)
		FROM platforms p
		LEFT JOIN roms r ON r.platform_id = p.id
		GROUP BY p.id, p.custom_name, p.name, p.slug, p.fs_slug
		ORDER BY 2`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Platform{}
	for rows.Next() {
		var p Platform
		if err := rows.Scan(&p.ID, &p.Name, &p.Slug, &p.FsSlug, &p.RomCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// RomDetail is the single-rom projection.
type RomDetail struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FsName        string `json:"fs_name"`
	FsNameNoExt   string `json:"fs_name_no_ext"`
	PlatformID    int64  `json:"platform_id"`
	IsVirtual     bool   `json:"is_virtual"`
	MissingFromFs bool   `json:"missing_from_fs"`
	Summary       string `json:"summary"`
	URLCover      string `json:"url_cover"`
	SizeBytes     int64  `json:"fs_size_bytes"`
	SourceURL     string `json:"source_url"`
	IgdbID        *int64 `json:"igdb_id"`
	SsID          *int64 `json:"ss_id"`
	MobyID        *int64 `json:"moby_id"`
}

func nullToPtr(n sql.NullInt64) *int64 {
	if !n.Valid {
		return nil
	}
	v := n.Int64
	return &v
}

// GetRom returns a rom by id, or (nil, nil) when it does not exist.
func (s *Store) GetRom(ctx context.Context, id int64) (*RomDetail, error) {
	var d RomDetail
	var igdb, ss, moby sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(fs_name,''), COALESCE(fs_name_no_ext,''),
		       platform_id, is_virtual, missing_from_fs,
		       COALESCE(summary,''), COALESCE(url_cover,''), COALESCE(fs_size_bytes,0),
		       COALESCE(source_url,''), igdb_id, ss_id, moby_id
		FROM roms WHERE id = ?`, id).
		Scan(&d.ID, &d.Name, &d.FsName, &d.FsNameNoExt, &d.PlatformID, &d.IsVirtual,
			&d.MissingFromFs, &d.Summary, &d.URLCover, &d.SizeBytes, &d.SourceURL,
			&igdb, &ss, &moby)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	d.IgdbID, d.SsID, d.MobyID = nullToPtr(igdb), nullToPtr(ss), nullToPtr(moby)
	return &d, nil
}

// UserIDByUsername resolves a username (JWT sub) to its users.id.
func (s *Store) UserIDByUsername(ctx context.Context, username string) (int64, error) {
	var id int64
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE username = ?`, username).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// RomUserProps carries the per-user rom state fields the write endpoint can set.
// A nil pointer means "leave unchanged".
type RomUserProps struct {
	Status     *string `json:"status"`
	Backlogged *bool   `json:"backlogged"`
	NowPlaying *bool   `json:"now_playing"`
	Hidden     *bool   `json:"hidden"`
	Rating     *int    `json:"rating"`
}

// SetRomUserProps upserts the per-user state for (userID, romID). Returns the
// number of fields written (0 when nothing was supplied).
func (s *Store) SetRomUserProps(ctx context.Context, userID, romID int64, p RomUserProps) (int, error) {
	cols := []string{}
	vals := []any{}
	add := func(c string, v any) { cols = append(cols, c); vals = append(vals, v) }
	if p.Status != nil {
		add("status", *p.Status)
	}
	if p.Backlogged != nil {
		add("backlogged", *p.Backlogged)
	}
	if p.NowPlaying != nil {
		add("now_playing", *p.NowPlaying)
	}
	if p.Hidden != nil {
		add("hidden", *p.Hidden)
	}
	if p.Rating != nil {
		add("rating", *p.Rating)
	}
	if len(cols) == 0 {
		return 0, nil
	}

	insCols := append([]string{"rom_id", "user_id"}, cols...)
	ph := strings.TrimSuffix(strings.Repeat("?,", len(insCols)), ",")
	upd := make([]string, 0, len(cols)+1)
	for _, c := range cols {
		upd = append(upd, fmt.Sprintf("%s=VALUES(%s)", c, c))
	}
	upd = append(upd, "updated_at=NOW()")
	q := fmt.Sprintf(
		"INSERT INTO rom_user (%s, created_at, updated_at) VALUES (%s, NOW(), NOW()) "+
			"ON DUPLICATE KEY UPDATE %s",
		strings.Join(insCols, ", "), ph, strings.Join(upd, ", "),
	)
	args := append([]any{romID, userID}, vals...)
	if _, err := s.DB.ExecContext(ctx, q, args...); err != nil {
		return 0, err
	}
	return len(cols), nil
}
