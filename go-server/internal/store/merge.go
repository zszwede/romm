package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
)

// MergeResult reports a completed merge.
type MergeResult struct {
	KeeperID    int64   `json:"keeper_id"`
	AbsorbedIDs []int64 `json:"absorbed_ids"`
}

var (
	ErrMergeTooFew   = errors.New("pick at least two different roms to merge")
	ErrMergeMultiLocal = errors.New("can't merge more than one local rom; the rest must be virtual")
)

type mergeRom struct {
	id           int64
	isVirtual    bool
	isIdentified bool
}

// MergeRoms folds several same-game rom entries into one keeper and deletes the
// rest, reassigning their collection memberships. A physical (local-file) rom
// always wins as the keeper, and at most one physical rom may take part. Runs in
// a transaction. Mirrors backend virtual merge_roms (minus the virtual_sources
// mirror bookkeeping, which the frontend recomputes).
func (s *Store) MergeRoms(ctx context.Context, romIDs []int64) (*MergeResult, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	res, err := mergeRoms(ctx, tx, romIDs)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func mergeRoms(ctx context.Context, tx txer, romIDs []int64) (*MergeResult, error) {
	// De-dupe while keeping order.
	seen := map[int64]bool{}
	var ids []int64
	for _, id := range romIDs {
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) < 2 {
		return nil, ErrMergeTooFew
	}

	roms := make([]mergeRom, 0, len(ids))
	physical := 0
	for _, id := range ids {
		var isVirtual int
		var igdb, ss sql.NullInt64
		err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(is_virtual,0), igdb_id, ss_id FROM roms WHERE id = ?`, id).
			Scan(&isVirtual, &igdb, &ss)
		if err != nil {
			return nil, err
		}
		mr := mergeRom{id: id, isVirtual: isVirtual != 0, isIdentified: igdb.Valid || ss.Valid}
		if !mr.isVirtual {
			physical++
		}
		roms = append(roms, mr)
	}
	if physical > 1 {
		return nil, ErrMergeMultiLocal
	}

	// Deterministic keeper: physical first, then identified, then lowest id.
	sort.SliceStable(roms, func(a, b int) bool {
		if roms[a].isVirtual != roms[b].isVirtual {
			return !roms[a].isVirtual // physical (false) sorts first
		}
		if roms[a].isIdentified != roms[b].isIdentified {
			return roms[a].isIdentified // identified sorts first
		}
		return roms[a].id < roms[b].id
	})
	keeper := roms[0].id

	var absorbed []int64
	for _, mr := range roms[1:] {
		absorbed = append(absorbed, mr.id)
		// Move collection memberships to the keeper (skip ones it already has),
		// then drop the absorbed row's memberships.
		if _, err := tx.ExecContext(ctx,
			`UPDATE IGNORE collections_roms SET rom_id = ? WHERE rom_id = ?`, keeper, mr.id); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM collections_roms WHERE rom_id = ?`, mr.id); err != nil {
			return nil, err
		}
		// Delete the absorbed rom; ON DELETE CASCADE clears its children.
		if _, err := tx.ExecContext(ctx, `DELETE FROM roms WHERE id = ?`, mr.id); err != nil {
			return nil, err
		}
	}
	return &MergeResult{KeeperID: keeper, AbsorbedIDs: absorbed}, nil
}
