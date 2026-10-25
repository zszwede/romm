package store

import (
	"context"
	"strings"
	"time"
)

// ClientToken is a personal API token row (client_tokens).
type ClientToken struct {
	ID         int64      `json:"id"`
	UserID     int64      `json:"user_id"`
	Name       string     `json:"name"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	DeviceID   *string    `json:"device_id"`
	CreatedAt  time.Time  `json:"created_at"`
}

// ListClientTokens returns a user's personal API tokens.
func (s *Store) ListClientTokens(ctx context.Context, userID int64) ([]ClientToken, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, user_id, name, COALESCE(scopes,''), expires_at, last_used_at, device_id, created_at
		FROM client_tokens WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClientToken{}
	for rows.Next() {
		var t ClientToken
		var scopes string
		if err := rows.Scan(&t.ID, &t.UserID, &t.Name, &scopes, &t.ExpiresAt, &t.LastUsedAt, &t.DeviceID, &t.CreatedAt); err != nil {
			return nil, err
		}
		if scopes != "" {
			t.Scopes = strings.Fields(scopes)
		} else {
			t.Scopes = []string{}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// CreateClientToken inserts a token (caller supplies the sha256 hash) and
// returns the stored row.
func (s *Store) CreateClientToken(ctx context.Context, userID int64, name, hashedToken, scopes string, expiresAt *time.Time) (*ClientToken, error) {
	var exp any
	if expiresAt != nil {
		exp = expiresAt.UTC()
	}
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO client_tokens (user_id, name, hashed_token, scopes, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())`, userID, name, hashedToken, scopes, exp)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	t := &ClientToken{ID: id, UserID: userID, Name: name, ExpiresAt: expiresAt, CreatedAt: time.Now().UTC()}
	if scopes != "" {
		t.Scopes = strings.Fields(scopes)
	} else {
		t.Scopes = []string{}
	}
	return t, nil
}

// DeleteClientToken removes a token the user owns.
func (s *Store) DeleteClientToken(ctx context.Context, userID, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM client_tokens WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
