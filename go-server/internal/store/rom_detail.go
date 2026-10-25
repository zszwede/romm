package store

import (
	"context"
	"database/sql"
	"time"
)

// RomFile is a row of the rom_files table (the Files tab + download selection).
type RomFile struct {
	ID            int64
	RomID         int64
	FileName      string
	FilePath      string
	FileSizeBytes int64
	LastModified  sql.NullFloat64
	CRCHash       sql.NullString
	MD5Hash       sql.NullString
	SHA1Hash      sql.NullString
	RAHash        sql.NullString
	CHDSHA1Hash   sql.NullString
	ArchiveMembers sql.NullString // JSON
	Category      sql.NullString
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ListRomFiles returns a rom's files ordered by name.
func (s *Store) ListRomFiles(ctx context.Context, romID int64) ([]RomFile, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, rom_id, file_name, COALESCE(file_path,''), COALESCE(file_size_bytes,0),
		       last_modified, crc_hash, md5_hash, sha1_hash, ra_hash, chd_sha1_hash,
		       archive_members, category, created_at, updated_at
		FROM rom_files WHERE rom_id = ? ORDER BY file_name`, romID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RomFile
	for rows.Next() {
		var f RomFile
		if err := rows.Scan(&f.ID, &f.RomID, &f.FileName, &f.FilePath, &f.FileSizeBytes,
			&f.LastModified, &f.CRCHash, &f.MD5Hash, &f.SHA1Hash, &f.RAHash, &f.CHDSHA1Hash,
			&f.ArchiveMembers, &f.Category, &f.CreatedAt, &f.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SiblingRom is a reduced rom projection for the sibling (versions) list.
type SiblingRom struct {
	ID            int64
	Name          sql.NullString
	FsNameNoTags  string
	FsNameNoExt   string
	IsMainSibling bool
}

// ListSiblings returns the roms that share a metadata group with romID (the
// sibling_roms association), with each sibling's is_main_sibling for the viewer.
func (s *Store) ListSiblings(ctx context.Context, romID, userID int64) ([]SiblingRom, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT r.id, r.name, COALESCE(r.fs_name_no_tags,''), COALESCE(r.fs_name_no_ext,''),
		       COALESCE(ru.is_main_sibling, 0)
		FROM sibling_roms sr
		JOIN roms r ON r.id = sr.sibling_rom_id
		LEFT JOIN rom_user ru ON ru.rom_id = r.id AND ru.user_id = ?
		WHERE sr.rom_id = ?
		ORDER BY COALESCE(r.name_sort_key, r.name, r.fs_name)`, userID, romID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SiblingRom
	for rows.Next() {
		var sib SiblingRom
		var main int
		if err := rows.Scan(&sib.ID, &sib.Name, &sib.FsNameNoTags, &sib.FsNameNoExt, &main); err != nil {
			return nil, err
		}
		sib.IsMainSibling = main != 0
		out = append(out, sib)
	}
	return out, rows.Err()
}

// RomMetadata holds a rom's provider metadata blobs and screenshot lists (the
// Metadata and Media tabs). All are raw JSON text passed through to the client.
type RomMetadata struct {
	IGDB       sql.NullString
	Moby       sql.NullString
	SS         sql.NullString
	Launchbox  sql.NullString
	Hasheous   sql.NullString
	Flashpoint sql.NullString
	HLTB       sql.NullString
	Demozoo    sql.NullString
	Pouet      sql.NullString
	CSDB       sql.NullString
	Steam      sql.NullString
	Gamelist   sql.NullString
	Manual     sql.NullString
	RA         sql.NullString
	PathScreenshots sql.NullString
	URLScreenshots  sql.NullString
}

// GetRomMetadata loads the per-provider metadata + screenshots for one rom.
func (s *Store) GetRomMetadata(ctx context.Context, romID int64) (*RomMetadata, error) {
	var m RomMetadata
	err := s.DB.QueryRowContext(ctx, `
		SELECT igdb_metadata, moby_metadata, ss_metadata, launchbox_metadata,
		       hasheous_metadata, flashpoint_metadata, hltb_metadata, demozoo_metadata,
		       pouet_metadata, csdb_metadata, steam_metadata, gamelist_metadata,
		       manual_metadata, ra_metadata, path_screenshots, url_screenshots
		FROM roms WHERE id = ?`, romID).Scan(
		&m.IGDB, &m.Moby, &m.SS, &m.Launchbox, &m.Hasheous, &m.Flashpoint, &m.HLTB,
		&m.Demozoo, &m.Pouet, &m.CSDB, &m.Steam, &m.Gamelist, &m.Manual, &m.RA,
		&m.PathScreenshots, &m.URLScreenshots)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}
