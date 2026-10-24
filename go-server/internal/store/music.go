package store

import "context"

// Playlist is a user's music playlist (game soundtracks).
type Playlist struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsPublic    bool   `json:"is_public"`
	UserID      int64  `json:"user_id"`
}

// Track is one entry in a playlist (a rom_file).
type Track struct {
	ID         int64 `json:"id"`
	PlaylistID int64 `json:"playlist_id"`
	RomFileID  int64 `json:"rom_file_id"`
	Position   int   `json:"position"`
}

// ListPlaylists returns the user's own playlists plus any public ones.
func (s *Store) ListPlaylists(ctx context.Context, userID int64) ([]Playlist, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, name, COALESCE(description,''), is_public, user_id
		 FROM music_playlists WHERE user_id = ? OR is_public = 1 ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.IsPublic, &p.UserID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreatePlaylist(ctx context.Context, userID int64, name, description string, isPublic bool) (int64, error) {
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO music_playlists (name, description, is_public, user_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`, name, description, isPublic, userID)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// DeletePlaylist removes a playlist the user owns; returns whether a row went.
func (s *Store) DeletePlaylist(ctx context.Context, userID, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx,
		`DELETE FROM music_playlists WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) ListPlaylistTracks(ctx context.Context, playlistID int64) ([]Track, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT id, playlist_id, rom_file_id, position
		 FROM music_playlist_tracks WHERE playlist_id = ? ORDER BY position`, playlistID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Track{}
	for rows.Next() {
		var t Track
		if err := rows.Scan(&t.ID, &t.PlaylistID, &t.RomFileID, &t.Position); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddTrack appends a rom_file to a playlist at the next position.
func (s *Store) AddTrack(ctx context.Context, playlistID, romFileID int64) (int64, error) {
	var pos int
	if err := s.DB.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position)+1, 0) FROM music_playlist_tracks WHERE playlist_id = ?`,
		playlistID).Scan(&pos); err != nil {
		return 0, err
	}
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO music_playlist_tracks (playlist_id, rom_file_id, position, created_at, updated_at)
		 VALUES (?, ?, ?, NOW(), NOW())`, playlistID, romFileID, pos)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) RemoveTrack(ctx context.Context, trackID int64) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM music_playlist_tracks WHERE id = ?`, trackID)
	return err
}
