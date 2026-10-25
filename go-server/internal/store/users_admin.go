package store

import (
	"context"
	"strings"
)

// CreateLocalUser inserts a username/password user (admin "Add user"). The
// password is pre-hashed by the caller (bcrypt).
func (s *Store) CreateLocalUser(ctx context.Context, username, hashedPassword, email, role string, enabled bool) (*UserRecord, error) {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO users (username, hashed_password, enabled, role, avatar_path, email, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, NOW(), NOW())`,
		username, hashedPassword, boolToInt(enabled), role, nullIfEmpty(email))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return s.GetUserByID(ctx, id)
}

// AdminUserUpdate carries the admin-editable user fields; nil = leave unchanged.
type AdminUserUpdate struct {
	Email          *string
	Role           *string
	Enabled        *bool
	HashedPassword *string
}

// AdminUpdateUser applies an admin edit and returns the updated user.
func (s *Store) AdminUpdateUser(ctx context.Context, id int64, up AdminUserUpdate) (*UserRecord, error) {
	var sets []string
	var args []any
	if up.Email != nil {
		sets, args = append(sets, "email = ?"), append(args, *up.Email)
	}
	if up.Role != nil {
		sets, args = append(sets, "role = ?"), append(args, *up.Role)
	}
	if up.Enabled != nil {
		sets, args = append(sets, "enabled = ?"), append(args, boolToInt(*up.Enabled))
	}
	if up.HashedPassword != nil {
		sets, args = append(sets, "hashed_password = ?"), append(args, *up.HashedPassword)
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = NOW()")
		args = append(args, id)
		if _, err := s.DB.ExecContext(ctx,
			`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return nil, err
		}
	}
	return s.GetUserByID(ctx, id)
}

// DeleteUser removes a user by id.
func (s *Store) DeleteUser(ctx context.Context, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
