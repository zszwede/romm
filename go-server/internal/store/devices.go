package store

import (
	"context"
	"database/sql"
	"time"
)

// DeviceTokenInput is one approved device pairing: the device to create or
// update, plus the client token to bind to it. Mirrors the single unit of work
// in the Python approve endpoint.
type DeviceTokenInput struct {
	UserID                 int64
	DeviceID               string // uuid to use when inserting a new device
	ClientDeviceIdentifier string
	Name                   string
	Client                 string
	Platform               string
	ClientVersion          string
	HashedToken            string
	Scopes                 string // space-joined
	ExpiresAt              *time.Time
}

// txer is the subset of *sql.DB / *sql.Tx the device write-back needs, so it
// can run either on its own transaction (production) or inside a caller's
// rolled-back transaction (integration test).
type txer interface {
	execer
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// RegisterDeviceToken creates or updates the Device and inserts its bound
// ClientToken in one transaction, returning the device id that was used.
func (s *Store) RegisterDeviceToken(ctx context.Context, in DeviceTokenInput) (string, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	deviceID, err := registerDeviceToken(ctx, tx, in)
	if err != nil {
		_ = tx.Rollback()
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return deviceID, nil
}

func registerDeviceToken(ctx context.Context, tx txer, in DeviceTokenInput) (string, error) {
	deviceID := in.DeviceID

	// Dedupe by (user_id, client_device_identifier) exactly as the Python
	// endpoint does: a re-pairing of the same install updates in place rather
	// than piling up device rows. A blank identifier can't dedupe (its unique
	// index treats NULLs as distinct), so it always inserts.
	var existing string
	if in.ClientDeviceIdentifier != "" {
		err := tx.QueryRowContext(ctx,
			`SELECT id FROM devices WHERE user_id = ? AND client_device_identifier = ?`,
			in.UserID, in.ClientDeviceIdentifier).Scan(&existing)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}

	if existing != "" {
		deviceID = existing
		if _, err := tx.ExecContext(ctx, `
			UPDATE devices SET
				name = ?,
				client_version = COALESCE(NULLIF(?, ''), client_version),
				last_seen = NOW(),
				updated_at = NOW()
			WHERE id = ?`,
			in.Name, in.ClientVersion, deviceID); err != nil {
			return "", err
		}
	} else {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO devices
				(id, user_id, name, platform, client, client_version,
				 client_device_identifier, sync_mode, sync_enabled,
				 last_seen, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, 'api', 1, NOW(), NOW(), NOW())`,
			deviceID, in.UserID, in.Name,
			nullIfEmpty(in.Platform), nullIfEmpty(in.Client), nullIfEmpty(in.ClientVersion),
			nullIfEmpty(in.ClientDeviceIdentifier)); err != nil {
			return "", err
		}
	}

	var expires any
	if in.ExpiresAt != nil {
		expires = in.ExpiresAt.UTC()
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO client_tokens
			(user_id, name, hashed_token, scopes, expires_at, device_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		in.UserID, in.Name, in.HashedToken, in.Scopes, expires, deviceID); err != nil {
		return "", err
	}

	return deviceID, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
