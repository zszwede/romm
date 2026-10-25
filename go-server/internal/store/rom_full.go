package store

import (
	"context"
	"database/sql"
	"time"
)

// RomFull is the full rom row the frontend's RomSchema needs (gallery cards and
// the detail view). JSON-array columns (tags/regions/languages/generated_*) are
// carried as raw JSON and passed through; null/absent become empty arrays in the
// response builder.
type RomFull struct {
	ID         int64
	IGDBID     sql.NullInt64
	SGDBID     sql.NullInt64
	MobyID     sql.NullInt64
	SSID       sql.NullInt64
	RAID       sql.NullInt64
	LaunchboxID sql.NullInt64
	HasheousID sql.NullInt64
	TGDBID     sql.NullInt64
	FlashpointID sql.NullString
	HLTBID     sql.NullInt64
	DemozooID  sql.NullInt64
	PouetID    sql.NullInt64
	CSDBID     sql.NullInt64
	SteamID    sql.NullInt64
	GamelistID sql.NullString
	LibretroID sql.NullString

	PlatformID         int64
	PlatformSlug       string
	PlatformFsSlug     string
	PlatformName       string
	PlatformCustomName sql.NullString

	FsName      string
	FsNameNoTags string
	FsNameNoExt string
	FsExtension string
	FsPath      string
	FsSizeBytes int64

	Name        sql.NullString
	NameSortKey sql.NullString
	Slug        sql.NullString
	Summary     sql.NullString
	Revision    sql.NullString

	Tags      sql.NullString
	Regions   sql.NullString
	Languages sql.NullString

	PathCoverS sql.NullString
	PathCoverL sql.NullString
	URLCover   sql.NullString
	URLManual  sql.NullString
	PathManual sql.NullString

	CRCHash  sql.NullString
	MD5Hash  sql.NullString
	SHA1Hash sql.NullString
	RAHash   sql.NullString
	TitleID  sql.NullString

	SaveTarget       sql.NullString
	SaveTargetLayout sql.NullString

	MissingFromFs bool
	IsPhysical    bool
	IsVirtual     bool
	SourceURL     sql.NullString
	SourceHosts   sql.NullString

	// metadatum (generated_* columns)
	GenGenres      sql.NullString
	GenFranchises  sql.NullString
	GenCollections sql.NullString
	GenCompanies   sql.NullString
	GenPublishers  sql.NullString
	GenDevelopers  sql.NullString
	GenGameModes   sql.NullString
	GenAgeRatings  sql.NullString
	GenPlayerCount sql.NullString
	GenFirstRelease sql.NullInt64
	GenAvgRating    sql.NullFloat64

	CreatedAt time.Time
	UpdatedAt time.Time
}

// RomUser is the per-user rom state (RomUserSchema core fields).
type RomUser struct {
	ID            int64
	UserID        int64
	RomID         int64
	LastPlayed    *time.Time
	IsMainSibling bool
	Backlogged    bool
	NowPlaying    bool
	Hidden        bool
	Rating        int
	Difficulty    int
	Completion    int
	Status        *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

const romFullCols = `
	r.id, r.igdb_id, r.sgdb_id, r.moby_id, r.ss_id, r.ra_id, r.launchbox_id,
	r.hasheous_id, r.tgdb_id, r.flashpoint_id, r.hltb_id, r.demozoo_id, r.pouet_id,
	r.csdb_id, r.steam_id, r.gamelist_id, r.libretro_id,
	r.platform_id, p.slug, COALESCE(p.fs_slug,''), COALESCE(NULLIF(p.name,''),p.slug), p.custom_name,
	r.fs_name, COALESCE(r.fs_name_no_tags,''), COALESCE(r.fs_name_no_ext,''), COALESCE(r.fs_extension,''),
	COALESCE(r.fs_path,''), COALESCE(r.fs_size_bytes,0),
	r.name, r.name_sort_key, r.slug, r.summary, r.revision,
	r.tags, r.regions, r.languages,
	r.path_cover_s, r.path_cover_l, r.url_cover, r.url_manual, r.path_manual,
	r.crc_hash, r.md5_hash, r.sha1_hash, r.ra_hash, r.title_id,
	r.save_target, r.save_target_layout,
	COALESCE(r.missing_from_fs,0), COALESCE(r.is_physical,0), COALESCE(r.is_virtual,0),
	r.source_url, r.source_hosts,
	r.generated_genres, r.generated_franchises, r.generated_collections, r.generated_companies,
	r.generated_publishers, r.generated_developers, r.generated_game_modes, r.generated_age_ratings,
	r.generated_player_count, r.generated_first_release_date, r.generated_average_rating,
	r.created_at, r.updated_at
	FROM roms r JOIN platforms p ON p.id = r.platform_id`

func scanRomFull(row interface{ Scan(...any) error }) (*RomFull, error) {
	var r RomFull
	var missing, physical, virtual int
	err := row.Scan(
		&r.ID, &r.IGDBID, &r.SGDBID, &r.MobyID, &r.SSID, &r.RAID, &r.LaunchboxID,
		&r.HasheousID, &r.TGDBID, &r.FlashpointID, &r.HLTBID, &r.DemozooID, &r.PouetID,
		&r.CSDBID, &r.SteamID, &r.GamelistID, &r.LibretroID,
		&r.PlatformID, &r.PlatformSlug, &r.PlatformFsSlug, &r.PlatformName, &r.PlatformCustomName,
		&r.FsName, &r.FsNameNoTags, &r.FsNameNoExt, &r.FsExtension, &r.FsPath, &r.FsSizeBytes,
		&r.Name, &r.NameSortKey, &r.Slug, &r.Summary, &r.Revision,
		&r.Tags, &r.Regions, &r.Languages,
		&r.PathCoverS, &r.PathCoverL, &r.URLCover, &r.URLManual, &r.PathManual,
		&r.CRCHash, &r.MD5Hash, &r.SHA1Hash, &r.RAHash, &r.TitleID,
		&r.SaveTarget, &r.SaveTargetLayout,
		&missing, &physical, &virtual,
		&r.SourceURL, &r.SourceHosts,
		&r.GenGenres, &r.GenFranchises, &r.GenCollections, &r.GenCompanies,
		&r.GenPublishers, &r.GenDevelopers, &r.GenGameModes, &r.GenAgeRatings,
		&r.GenPlayerCount, &r.GenFirstRelease, &r.GenAvgRating,
		&r.CreatedAt, &r.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	r.MissingFromFs, r.IsPhysical, r.IsVirtual = missing != 0, physical != 0, virtual != 0
	return &r, nil
}

// GetRomFull returns the full rom by id.
func (s *Store) GetRomFull(ctx context.Context, id int64) (*RomFull, error) {
	return scanRomFull(s.DB.QueryRowContext(ctx, `SELECT `+romFullCols+` WHERE r.id = ?`, id))
}

// ListRomsFull returns full roms with ordering/paging. platformID<=0 means all.
func (s *Store) ListRomsFull(ctx context.Context, platformID int64, orderBy, orderDir string, limit, offset int) ([]RomFull, error) {
	order := romOrderColumn(orderBy)
	dir := "ASC"
	if orderDir == "desc" {
		dir = "DESC"
	}
	if limit <= 0 || limit > 250 {
		limit = 72
	}
	q := `SELECT ` + romFullCols
	args := []any{}
	if platformID > 0 {
		q += ` WHERE r.platform_id = ?`
		args = append(args, platformID)
	}
	q += ` ORDER BY ` + order + ` ` + dir + `, r.id ` + dir + ` LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RomFull{}
	for rows.Next() {
		r, err := scanRomFull(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// CountRoms counts roms for a platform (or all when platformID<=0).
func (s *Store) CountRoms(ctx context.Context, platformID int64) (int64, error) {
	var n int64
	var err error
	if platformID > 0 {
		err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM roms WHERE platform_id = ?`, platformID).Scan(&n)
	} else {
		err = s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM roms`).Scan(&n)
	}
	return n, err
}

// romOrderColumn whitelists sortable columns to avoid injection.
func romOrderColumn(orderBy string) string {
	switch orderBy {
	case "name":
		return "COALESCE(r.name_sort_key, r.name, r.fs_name)"
	case "first_release_date":
		return "r.generated_first_release_date"
	case "last_played":
		return "r.updated_at" // best-effort without a per-user join here
	case "fs_size_bytes":
		return "r.fs_size_bytes"
	case "created_at":
		return "r.created_at"
	default:
		return "r.id"
	}
}

// GetRomUser returns the per-user state for (userID, romID), or nil if none.
func (s *Store) GetRomUser(ctx context.Context, userID, romID int64) (*RomUser, error) {
	var ru RomUser
	var lastPlayed sql.NullTime
	var status sql.NullString
	var main, back, now, hidden int
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, user_id, rom_id, last_played, is_main_sibling, backlogged, now_playing,
		       hidden, rating, difficulty, completion, status, created_at, updated_at
		FROM rom_user WHERE user_id = ? AND rom_id = ?`, userID, romID).Scan(
		&ru.ID, &ru.UserID, &ru.RomID, &lastPlayed, &main, &back, &now,
		&hidden, &ru.Rating, &ru.Difficulty, &ru.Completion, &status, &ru.CreatedAt, &ru.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastPlayed.Valid {
		ru.LastPlayed = &lastPlayed.Time
	}
	if status.Valid {
		ru.Status = &status.String
	}
	ru.IsMainSibling, ru.Backlogged, ru.NowPlaying, ru.Hidden = main != 0, back != 0, now != 0, hidden != 0
	return &ru, nil
}
