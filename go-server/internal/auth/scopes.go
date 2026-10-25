package auth

// Canonical OAuth scope sets, mirroring backend/handler/auth/constants.py. The
// order matches FULL_SCOPES there so a minted token's scope string is stable.

var (
	ReadScopes = []string{
		"me.read", "roms.read", "platforms.read", "assets.read", "devices.read",
		"firmware.read", "roms.user.read", "collections.read", "playlists.read",
	}
	writeExtra = []string{
		"me.write", "assets.write", "devices.write", "roms.user.write",
		"collections.write", "playlists.write",
	}
	editExtra = []string{"roms.write", "platforms.write", "firmware.write"}
	fullExtra = []string{"users.read", "users.write", "tasks.run", "logs.read"}
)

// WriteScopes = ReadScopes + write grants.
func WriteScopes() []string { return concat(ReadScopes, writeExtra) }

// EditScopes = WriteScopes + content-edit grants.
func EditScopes() []string { return concat(WriteScopes(), editExtra) }

// FullScopes = EditScopes + admin grants. This is what an admin holds.
func FullScopes() []string { return concat(EditScopes(), fullExtra) }

// ScopesForRole returns the scopes a user of the given role holds. Admins get
// the full set; everyone else gets the standard write set.
//
// NOTE: the Python backend derives a non-admin user's scopes from their
// permission group's grants. Until that permission-group system is ported, a
// non-admin defaults to WriteScopes (read + own-asset/collection/playlist
// writes), which matches the common "standard user" grant. Admin — the only
// role in this install — is exact.
func ScopesForRole(role string) []string {
	if role == "admin" {
		return FullScopes()
	}
	return WriteScopes()
}

func concat(parts ...[]string) []string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]string, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
