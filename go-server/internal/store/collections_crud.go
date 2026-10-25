package store

import "context"

// CreateCollection inserts a collection (optionally the favorite one) with an
// initial rom set, and returns it with rom_ids populated.
func (s *Store) CreateCollection(ctx context.Context, userID int64, name, description string, isPublic, isFavorite bool, romIDs []int64) (*Collection, error) {
	res, err := s.DB.ExecContext(ctx, `
		INSERT INTO collections (name, description, is_public, is_favorite, user_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		name, nullIfEmpty(description), boolToInt(isPublic), boolToInt(isFavorite), userID)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	if len(romIDs) > 0 {
		if err := s.addCollectionRoms(ctx, id, romIDs); err != nil {
			return nil, err
		}
	}
	return s.GetCollection(ctx, userID, id)
}

// GetCollection returns a collection the user can see (owned or public), with
// rom_ids.
func (s *Store) GetCollection(ctx context.Context, userID, id int64) (*Collection, error) {
	var c Collection
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, COALESCE(name,''), COALESCE(description,''), is_public, is_favorite, COALESCE(user_id,0)
		FROM collections WHERE id = ? AND (user_id = ? OR is_public = 1)`, id, userID).
		Scan(&c.ID, &c.Name, &c.Description, &c.IsPublic, &c.IsFavorite, &c.UserID)
	if err != nil {
		return nil, err
	}
	cols := []Collection{c}
	if err := s.fillCollectionRomIDs(ctx, cols); err != nil {
		return nil, err
	}
	return &cols[0], nil
}

// AddRomsToCollection adds roms (idempotently) and returns the updated collection.
func (s *Store) AddRomsToCollection(ctx context.Context, userID, id int64, romIDs []int64) (*Collection, error) {
	if err := s.addCollectionRoms(ctx, id, romIDs); err != nil {
		return nil, err
	}
	s.touchCollection(ctx, id)
	return s.GetCollection(ctx, userID, id)
}

// RemoveRomsFromCollection removes roms and returns the updated collection.
func (s *Store) RemoveRomsFromCollection(ctx context.Context, userID, id int64, romIDs []int64) (*Collection, error) {
	for _, rid := range romIDs {
		if _, err := s.DB.ExecContext(ctx,
			`DELETE FROM collections_roms WHERE collection_id = ? AND rom_id = ?`, id, rid); err != nil {
			return nil, err
		}
	}
	s.touchCollection(ctx, id)
	return s.GetCollection(ctx, userID, id)
}

// DeleteCollection removes a collection the user owns.
func (s *Store) DeleteCollection(ctx context.Context, userID, id int64) (bool, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM collections WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) addCollectionRoms(ctx context.Context, id int64, romIDs []int64) error {
	for _, rid := range romIDs {
		if _, err := s.DB.ExecContext(ctx,
			`INSERT IGNORE INTO collections_roms (collection_id, rom_id) VALUES (?, ?)`, id, rid); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) touchCollection(ctx context.Context, id int64) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE collections SET updated_at = NOW() WHERE id = ?`, id)
}
