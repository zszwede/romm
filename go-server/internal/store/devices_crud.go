package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/zszwede/romm-go/internal/deviceauth"
)

// Device is a sync device row (DeviceSchema).
type Device struct {
	ID                     string     `json:"id"`
	UserID                 int64      `json:"user_id"`
	Name                   *string    `json:"name"`
	Platform               *string    `json:"platform"`
	Client                 *string    `json:"client"`
	ClientVersion          *string    `json:"client_version"`
	IPAddress              *string    `json:"ip_address"`
	MACAddress             *string    `json:"mac_address"`
	Hostname               *string    `json:"hostname"`
	ClientDeviceIdentifier *string    `json:"client_device_identifier"`
	SyncMode               string     `json:"sync_mode"`
	SyncEnabled            bool       `json:"sync_enabled"`
	LastSeen               *time.Time `json:"last_seen"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// DeviceCreate is the input for registering a device.
type DeviceCreate struct {
	Name          string
	Platform      string
	Client        string
	ClientVersion string
	Hostname      string
	SyncMode      string // "api" default
}

// CreateDevice inserts a sync device and returns it.
func (s *Store) CreateDevice(ctx context.Context, userID int64, in DeviceCreate) (*Device, error) {
	id := deviceauth.NewUUID4()
	mode := in.SyncMode
	if mode == "" {
		mode = "api"
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO devices
			(id, user_id, name, platform, client, client_version, hostname,
			 sync_mode, sync_enabled, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 1, NOW(), NOW(), NOW())`,
		id, userID, nullIfEmpty(in.Name), nullIfEmpty(in.Platform), nullIfEmpty(in.Client),
		nullIfEmpty(in.ClientVersion), nullIfEmpty(in.Hostname), mode)
	if err != nil {
		return nil, err
	}
	return s.GetDevice(ctx, userID, id)
}

const deviceCols = `id, user_id, name, platform, client, client_version, ip_address,
	mac_address, hostname, client_device_identifier, sync_mode, sync_enabled,
	last_seen, created_at, updated_at`

func scanDevice(row interface{ Scan(...any) error }) (*Device, error) {
	var d Device
	var enabled int
	if err := row.Scan(&d.ID, &d.UserID, &d.Name, &d.Platform, &d.Client, &d.ClientVersion,
		&d.IPAddress, &d.MACAddress, &d.Hostname, &d.ClientDeviceIdentifier, &d.SyncMode,
		&enabled, &d.LastSeen, &d.CreatedAt, &d.UpdatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	d.SyncEnabled = enabled != 0
	return &d, nil
}

// GetDevice returns one of the user's devices.
func (s *Store) GetDevice(ctx context.Context, userID int64, id string) (*Device, error) {
	return scanDevice(s.DB.QueryRowContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE id = ? AND user_id = ?`, id, userID))
}

// ListDevices returns the user's devices.
func (s *Store) ListDevices(ctx context.Context, userID int64) ([]Device, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT `+deviceCols+` FROM devices WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// DeleteDevice removes one of the user's devices.
func (s *Store) DeleteDevice(ctx context.Context, userID int64, id string) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM devices WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
