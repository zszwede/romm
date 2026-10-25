package store

import (
	"context"
	"database/sql"
	"time"
)

// Note is a per-user rom note (UserNoteSchema). Stored on rom_user.
type Note struct {
	ID          int64     `json:"id"`
	RomID       int64     `json:"rom_id"`
	UserID      int64     `json:"user_id"`
	Username    string    `json:"username"`
	RawMarkdown string    `json:"raw_markdown"`
	IsPublic    bool      `json:"is_public"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ListRomNotes returns the caller's own note plus other users' public notes.
func (s *Store) ListRomNotes(ctx context.Context, romID, userID int64) ([]Note, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT ru.id, ru.rom_id, ru.user_id, u.username,
		       COALESCE(ru.note_raw_markdown,''), COALESCE(ru.note_is_public,0),
		       ru.created_at, ru.updated_at
		FROM rom_user ru JOIN users u ON u.id = ru.user_id
		WHERE ru.rom_id = ? AND ru.note_raw_markdown IS NOT NULL AND ru.note_raw_markdown <> ''
		  AND (ru.user_id = ? OR ru.note_is_public = 1)
		ORDER BY ru.updated_at DESC`, romID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		var n Note
		var pub int
		if err := rows.Scan(&n.ID, &n.RomID, &n.UserID, &n.Username, &n.RawMarkdown, &pub, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.IsPublic = pub != 0
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetRomNote upserts the caller's note (one per user/rom) and returns it. Seeds
// the rom_user NOT-NULL columns on a first insert.
func (s *Store) SetRomNote(ctx context.Context, userID, romID int64, markdown string, isPublic bool) (*Note, error) {
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO rom_user
			(rom_id, user_id, note_raw_markdown, note_is_public,
			 is_main_sibling, backlogged, now_playing, hidden, rating, difficulty, completion,
			 created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, 0, 0, 0, 0, 0, 0, NOW(), NOW())
		ON DUPLICATE KEY UPDATE note_raw_markdown = VALUES(note_raw_markdown),
		                        note_is_public = VALUES(note_is_public), updated_at = NOW()`,
		romID, userID, markdown, boolToInt(isPublic))
	if err != nil {
		return nil, err
	}
	return s.getRomNote(ctx, userID, romID)
}

// ClearRomNote blanks the caller's note.
func (s *Store) ClearRomNote(ctx context.Context, userID, romID int64) error {
	_, err := s.DB.ExecContext(ctx,
		`UPDATE rom_user SET note_raw_markdown = NULL, updated_at = NOW() WHERE user_id = ? AND rom_id = ?`,
		userID, romID)
	return err
}

func (s *Store) getRomNote(ctx context.Context, userID, romID int64) (*Note, error) {
	var n Note
	var pub int
	err := s.DB.QueryRowContext(ctx, `
		SELECT ru.id, ru.rom_id, ru.user_id, u.username,
		       COALESCE(ru.note_raw_markdown,''), COALESCE(ru.note_is_public,0), ru.created_at, ru.updated_at
		FROM rom_user ru JOIN users u ON u.id = ru.user_id
		WHERE ru.user_id = ? AND ru.rom_id = ?`, userID, romID).
		Scan(&n.ID, &n.RomID, &n.UserID, &n.Username, &n.RawMarkdown, &pub, &n.CreatedAt, &n.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.IsPublic = pub != 0
	return &n, nil
}
