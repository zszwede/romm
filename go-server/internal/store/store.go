// Package store is the data layer. Phase 1 reads the EXISTING MariaDB schema
// (the one Alembic created for the Python app) so all current data — roms,
// platforms, users, collections — is used as-is with no migration. New
// migrations (goose) come in a later phase; for now the Go server only reads.
package store

import (
	"context"
	"database/sql"
	"fmt"
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
