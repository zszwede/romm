package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/zszwede/romm-go/internal/deviceauth"
	"github.com/zszwede/romm-go/internal/oidc"
)

// This file makes *Store satisfy oidc.UserStore so OIDC login can provision and
// link users against the real users table.

const oidcUserCols = `id, username, COALESCE(email,''), role, COALESCE(oidc_issuer,''), COALESCE(oidc_sub,''), enabled`

func scanOIDCUser(row interface{ Scan(...any) error }) (*oidc.User, error) {
	var u oidc.User
	var enabled int
	if err := row.Scan(&u.ID, &u.Username, &u.Email, &u.Role, &u.OIDCIssuer, &u.OIDCSub, &enabled); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	u.Enabled = enabled != 0
	return &u, nil
}

func (s *Store) GetUserByOIDCIdentity(ctx context.Context, issuer, sub string) (*oidc.User, error) {
	if issuer == "" || sub == "" {
		return nil, nil
	}
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+oidcUserCols+` FROM users WHERE oidc_issuer = ? AND oidc_sub = ?`, issuer, sub)
	return scanOIDCUser(row)
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*oidc.User, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+oidcUserCols+` FROM users WHERE LOWER(email) = LOWER(?)`, email)
	return scanOIDCUser(row)
}

func (s *Store) GetUserByUsername(ctx context.Context, username string) (*oidc.User, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT `+oidcUserCols+` FROM users WHERE username = ?`, username)
	return scanOIDCUser(row)
}

func (s *Store) AddUser(ctx context.Context, u oidc.NewUser) (*oidc.User, error) {
	return addOIDCUser(ctx, s.DB, u)
}

func addOIDCUser(ctx context.Context, e txer, u oidc.NewUser) (*oidc.User, error) {
	// hashed_password gets a random value so password login is impossible for an
	// OIDC-provisioned account (mirrors the Python handler's uuid password).
	// avatar_path is NOT NULL with no default, so set it explicitly.
	res, err := e.ExecContext(ctx, `
		INSERT INTO users
			(username, hashed_password, enabled, role, avatar_path, email,
			 oidc_issuer, oidc_sub, created_at, updated_at)
		VALUES (?, ?, ?, ?, '', ?, ?, ?, NOW(), NOW())`,
		u.Username, deviceauth.GenerateClientToken(), boolToInt(u.Enabled), u.Role,
		nullIfEmpty(u.Email), nullIfEmpty(u.OIDCIssuer), nullIfEmpty(u.OIDCSub))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	row := e.QueryRowContext(ctx, `SELECT `+oidcUserCols+` FROM users WHERE id = ?`, id)
	return scanOIDCUser(row)
}

func (s *Store) UpdateUser(ctx context.Context, id int64, up oidc.UserUpdates) (*oidc.User, error) {
	return updateOIDCUser(ctx, s.DB, id, up)
}

func updateOIDCUser(ctx context.Context, e txer, id int64, up oidc.UserUpdates) (*oidc.User, error) {
	var sets []string
	var args []any
	if up.Email != nil {
		sets = append(sets, "email = ?")
		args = append(args, *up.Email)
	}
	if up.Role != nil {
		sets = append(sets, "role = ?")
		args = append(args, *up.Role)
	}
	if up.OIDCIssuer != nil {
		sets = append(sets, "oidc_issuer = ?")
		args = append(args, *up.OIDCIssuer)
	}
	if up.OIDCSub != nil {
		sets = append(sets, "oidc_sub = ?")
		args = append(args, *up.OIDCSub)
	}
	if len(sets) > 0 {
		sets = append(sets, "updated_at = NOW()")
		args = append(args, id)
		if _, err := e.ExecContext(ctx,
			`UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...); err != nil {
			return nil, err
		}
	}
	row := e.QueryRowContext(ctx, `SELECT `+oidcUserCols+` FROM users WHERE id = ?`, id)
	return scanOIDCUser(row)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
