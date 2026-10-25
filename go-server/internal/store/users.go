package store

import (
	"context"
	"database/sql"
	"time"
)

// UserRecord is a user row for authentication and the /users/me response.
type UserRecord struct {
	ID                int64      `json:"id"`
	Username          string     `json:"username"`
	Email             *string    `json:"email"`
	Enabled           bool       `json:"enabled"`
	Role              string     `json:"role"`
	PermissionGroupID *int64     `json:"permission_group_id"`
	AvatarPath        string     `json:"avatar_path"`
	RAUsername        *string    `json:"ra_username"`
	LastLogin         *time.Time `json:"last_login"`
	LastActive        *time.Time `json:"last_active"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`

	// HashedPassword is used only for password verification and never serialized.
	HashedPassword string `json:"-"`
}

const userRecordCols = `id, username, email, enabled, role, permission_group_id,
	COALESCE(avatar_path,''), ra_username, last_login, last_active, created_at, updated_at,
	COALESCE(hashed_password,'')`

func scanUserRecord(row interface{ Scan(...any) error }) (*UserRecord, error) {
	var u UserRecord
	var enabled int
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &enabled, &u.Role, &u.PermissionGroupID,
		&u.AvatarPath, &u.RAUsername, &u.LastLogin, &u.LastActive, &u.CreatedAt, &u.UpdatedAt,
		&u.HashedPassword); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.Enabled = enabled != 0
	return &u, nil
}

// GetUserAuth returns a user by username (including the password hash) for login.
func (s *Store) GetUserAuth(ctx context.Context, username string) (*UserRecord, error) {
	return scanUserRecord(s.DB.QueryRowContext(ctx,
		`SELECT `+userRecordCols+` FROM users WHERE username = ?`, username))
}

// GetUserByID returns a user by id (for session resolution).
func (s *Store) GetUserByID(ctx context.Context, id int64) (*UserRecord, error) {
	return scanUserRecord(s.DB.QueryRowContext(ctx,
		`SELECT `+userRecordCols+` FROM users WHERE id = ?`, id))
}

// ListUsers returns all users (admin view).
func (s *Store) ListUsers(ctx context.Context) ([]UserRecord, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+userRecordCols+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []UserRecord{}
	for rows.Next() {
		u, err := scanUserRecord(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

// TouchLogin stamps last_login and last_active to now.
func (s *Store) TouchLogin(ctx context.Context, id int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE users SET last_login = NOW(), last_active = NOW(), updated_at = NOW() WHERE id = ?`, id)
	return err
}
