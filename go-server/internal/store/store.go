// Package store is the data layer. Phase 1 reads the EXISTING MariaDB schema
// (the one Alembic created for the Python app) so all current data — roms,
// platforms, users, collections — is used as-is with no migration. New
// migrations (goose) come in a later phase; for now the Go server only reads.
package store

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/zszwede/romm-go/internal/hash"
	"github.com/zszwede/romm-go/internal/scan"
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

// --- scan support (Phase 3) ---

// ListPlatformDirs returns the (id, fs_slug) of every platform that has a
// folder name, for the scanner to walk.
func (s *Store) ListPlatformDirs(ctx context.Context) ([]scan.PlatformDir, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, fs_slug FROM platforms WHERE fs_slug IS NOT NULL AND fs_slug <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []scan.PlatformDir{}
	for rows.Next() {
		var d scan.PlatformDir
		if err := rows.Scan(&d.ID, &d.FsSlug); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Upsert inserts a scanned rom or refreshes an existing one (matched on the
// unique (platform_id, full_path_hash)). On conflict it updates size/hashes and
// clears missing_from_fs, but never clobbers the name/metadata of a rom that
// was already identified.
func (s *Store) Upsert(ctx context.Context, r scan.Record) error {
	return upsertRom(ctx, s.DB, r)
}

// execer is satisfied by both *sql.DB and *sql.Tx, so the upsert can run inside
// a rolled-back transaction in integration tests.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func upsertRom(ctx context.Context, e execer, r scan.Record) error {
	fph := hash.FullPathHash(r.FsPath, r.FsName)
	_, err := e.ExecContext(ctx, `
		INSERT INTO roms
			(platform_id, fs_name, fs_name_no_tags, fs_extension, fs_path,
			 fs_name_no_ext, fs_size_bytes, full_path_hash, name,
			 crc_hash, md5_hash, sha1_hash, missing_from_fs, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, NOW(), NOW())
		ON DUPLICATE KEY UPDATE
			fs_size_bytes = VALUES(fs_size_bytes),
			crc_hash = VALUES(crc_hash),
			md5_hash = VALUES(md5_hash),
			sha1_hash = VALUES(sha1_hash),
			missing_from_fs = 0,
			updated_at = NOW()`,
		r.PlatformID, r.FsName, r.FsNameNoTags, r.FsExtension, r.FsPath,
		r.FsNameNoExt, r.SizeBytes, fph, r.FsNameNoExt,
		nz(r.Digests.CRC32), nz(r.Digests.MD5), nz(r.Digests.SHA1))
	return err
}

// nz turns an empty hash into NULL so an un-hashed inventory pass doesn't
// store empty strings in the hash columns.
func nz(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- collections (Phase 6, read) ---

type Collection struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPublic    bool   `json:"is_public"`
	IsFavorite  bool   `json:"is_favorite"`
	UserID      int64  `json:"user_id"`
}

// ListCollections returns all collections (ordered by name).
func (s *Store) ListCollections(ctx context.Context) ([]Collection, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(description,''),
		       is_public, is_favorite, COALESCE(user_id,0)
		FROM collections ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Collection{}
	for rows.Next() {
		var c Collection
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.IsPublic, &c.IsFavorite, &c.UserID); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// EnrichFromGamelist fills a rom's empty name/summary from ES-DE gamelist.xml,
// matched on (platform_id, fs_name). Existing (identified) values are kept.
func (s *Store) EnrichFromGamelist(ctx context.Context, platformID int64, fsName, name, summary string) error {
	_, err := s.DB.ExecContext(ctx, `
		UPDATE roms
		SET name = IF(name IS NULL OR name = '', NULLIF(?, ''), name),
		    summary = IF(summary IS NULL OR summary = '', NULLIF(?, ''), summary),
		    updated_at = NOW()
		WHERE platform_id = ? AND fs_name = ?`,
		name, summary, platformID, fsName)
	return err
}

// --- virtual download completion (apply a finished job to the rom) ---

var storeTagRE = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)

// ApplyDownloaded turns a virtual rom into a physical one after its download+
// merge completed: records the delivered file and clears the virtual/download
// state. dir is library-relative (e.g. "roms/switch"); name is the file.
func (s *Store) ApplyDownloaded(ctx context.Context, romID int64, dir, name string, size int64) error {
	return applyDownloaded(ctx, s.DB, romID, dir, name, size)
}

func applyDownloaded(ctx context.Context, e execer, romID int64, dir, name string, size int64) error {
	ext := strings.TrimPrefix(filepath.Ext(name), ".")
	noExt := strings.TrimSuffix(name, filepath.Ext(name))
	noTags := strings.Join(strings.Fields(storeTagRE.ReplaceAllString(noExt, " ")), " ")
	fph := hash.FullPathHash(dir, name)
	_, err := e.ExecContext(ctx, `
		UPDATE roms SET
			is_virtual = 0,
			missing_from_fs = 0,
			fs_path = ?, fs_name = ?, fs_name_no_ext = ?, fs_name_no_tags = ?,
			fs_extension = ?, fs_size_bytes = ?, full_path_hash = ?,
			download_status = NULL,
			updated_at = NOW()
		WHERE id = ?`,
		dir, name, noExt, noTags, ext, size, fph, romID)
	return err
}
