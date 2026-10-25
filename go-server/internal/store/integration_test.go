package store

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zszwede/romm-go/internal/oidc"
	"github.com/zszwede/romm-go/internal/scan"
)

// TestUpsert_Integration validates the scanned-rom INSERT against the real
// schema (confirming no NOT-NULL column is missed) WITHOUT persisting: it runs
// inside a transaction that is always rolled back. Skipped unless DB_HOST and
// DB_PASSWD are set in the environment.
func TestUpsert_Integration(t *testing.T) {
	host, pass := os.Getenv("DB_HOST"), os.Getenv("DB_PASSWD")
	if host == "" || pass == "" {
		t.Skip("DB_HOST/DB_PASSWD not set; skipping live schema test")
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	st, err := Open(host, envOr("DB_PORT", "3306"), envOr("DB_USER", "romm-user"), pass, envOr("DB_NAME", "romm"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	var pid int64
	if err := st.DB.QueryRowContext(ctx, "SELECT id FROM platforms LIMIT 1").Scan(&pid); err != nil {
		t.Fatalf("pick platform: %v", err)
	}

	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() // never persist

	rec := scan.Record{
		PlatformID:   pid,
		FsPath:       "roms/__gotest__",
		FsName:       "__integration_probe__.bin",
		FsNameNoExt:  "__integration_probe__",
		FsNameNoTags: "integration probe",
		FsExtension:  "bin",
		SizeBytes:    5,
	}
	rec.Digests.CRC32 = "3610a686"

	if err := upsertRom(ctx, tx, rec); err != nil {
		t.Fatalf("upsert against real schema failed: %v", err)
	}
	// Confirm the row is visible inside the tx before rollback.
	var id int
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM roms WHERE platform_id=? AND fs_name=?", pid, rec.FsName).Scan(&id)
	if err != nil {
		t.Fatalf("probe row lookup: %v", err)
	}

	// ApplyDownloaded must also run cleanly against the real schema.
	if err := applyDownloaded(ctx, tx, int64(id), "roms/__gotest__", "merged (USA).nsp", 999); err != nil {
		t.Fatalf("applyDownloaded against live schema failed: %v", err)
	}
	var fsName string
	var virt int
	if err := tx.QueryRowContext(ctx, "SELECT fs_name, is_virtual FROM roms WHERE id=?", id).Scan(&fsName, &virt); err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if fsName != "merged (USA).nsp" || virt != 0 {
		t.Fatalf("after apply: fs_name=%q is_virtual=%d", fsName, virt)
	}
	t.Logf("insert + applyDownloaded OK against live schema (platform %d), rolling back", pid)
}

// TestRegisterDeviceToken_Integration validates the device + client_token
// write-back against the real schema inside an always-rolled-back transaction.
// It also exercises the dedupe-on-second-pair path. Skipped unless DB creds set.
func TestRegisterDeviceToken_Integration(t *testing.T) {
	host, pass := os.Getenv("DB_HOST"), os.Getenv("DB_PASSWD")
	if host == "" || pass == "" {
		t.Skip("DB_HOST/DB_PASSWD not set; skipping live schema test")
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	st, err := Open(host, envOr("DB_PORT", "3306"), envOr("DB_USER", "romm-user"), pass, envOr("DB_NAME", "romm"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	var uid int64
	if err := st.DB.QueryRowContext(ctx, "SELECT id FROM users LIMIT 1").Scan(&uid); err != nil {
		t.Fatalf("pick user: %v", err)
	}

	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() // never persist

	exp := time.Now().Add(24 * time.Hour)
	in := DeviceTokenInput{
		UserID:                 uid,
		DeviceID:               "gotest-device-uuid-0001",
		ClientDeviceIdentifier: "__gotest_install__",
		Name:                   "Go Test Device",
		Client:                 "grout",
		Platform:               "muOS",
		ClientVersion:          "1.0.0",
		HashedToken:            "gotest" + strings.Repeat("0", 58), // 64-char sha256-shaped
		Scopes:                 "me.read roms.read",
		ExpiresAt:              &exp,
	}

	id1, err := registerDeviceToken(ctx, tx, in)
	if err != nil {
		t.Fatalf("first register against live schema failed: %v", err)
	}
	if id1 != in.DeviceID {
		t.Fatalf("first register device id = %q, want %q", id1, in.DeviceID)
	}

	// Second pairing of the same install must dedupe onto the same device row,
	// not insert a new one, and must add a second token.
	in2 := in
	in2.DeviceID = "gotest-device-uuid-0002" // ignored; dedupe wins
	in2.Name = "Go Test Device (renamed)"
	in2.HashedToken = "gotest" + strings.Repeat("1", 58)
	id2, err := registerDeviceToken(ctx, tx, in2)
	if err != nil {
		t.Fatalf("second register failed: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("re-pair created a new device (%q) instead of reusing %q", id2, id1)
	}

	var deviceCount, tokenCount int
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM devices WHERE user_id=? AND client_device_identifier=?",
		uid, in.ClientDeviceIdentifier).Scan(&deviceCount); err != nil {
		t.Fatalf("count devices: %v", err)
	}
	if deviceCount != 1 {
		t.Fatalf("device rows = %d, want 1 (dedupe)", deviceCount)
	}
	if err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM client_tokens WHERE device_id=?", id1).Scan(&tokenCount); err != nil {
		t.Fatalf("count tokens: %v", err)
	}
	if tokenCount != 2 {
		t.Fatalf("token rows = %d, want 2", tokenCount)
	}
	t.Logf("device+token write-back OK against live schema (user %d), rolling back", uid)
}

// TestOIDCUserProvision_Integration validates the OIDC user INSERT/UPDATE
// against the real users schema inside an always-rolled-back transaction
// (confirming avatar_path/role NOT-NULL columns and the oidc_* columns are
// handled). Skipped unless DB creds set.
func TestOIDCUserProvision_Integration(t *testing.T) {
	host, pass := os.Getenv("DB_HOST"), os.Getenv("DB_PASSWD")
	if host == "" || pass == "" {
		t.Skip("DB_HOST/DB_PASSWD not set; skipping live schema test")
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	st, err := Open(host, envOr("DB_PORT", "3306"), envOr("DB_USER", "romm-user"), pass, envOr("DB_NAME", "romm"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() // never persist

	created, err := addOIDCUser(ctx, tx, oidc.NewUser{
		Username:   "__gotest_oidc__",
		Email:      "gotest-oidc@example.com",
		Role:       oidc.RoleUser,
		OIDCIssuer: "https://idp.example",
		OIDCSub:    "gotest-sub-1",
		Enabled:    true,
	})
	if err != nil {
		t.Fatalf("addOIDCUser against live schema failed: %v", err)
	}
	if created == nil || created.ID == 0 || created.Role != oidc.RoleUser || !created.Enabled {
		t.Fatalf("created user wrong: %+v", created)
	}

	newEmail := "gotest-oidc-new@example.com"
	newRole := oidc.RoleAdmin
	updated, err := updateOIDCUser(ctx, tx, created.ID, oidc.UserUpdates{Email: &newEmail, Role: &newRole})
	if err != nil {
		t.Fatalf("updateOIDCUser failed: %v", err)
	}
	if updated.Email != newEmail || updated.Role != oidc.RoleAdmin {
		t.Fatalf("after update: %+v", updated)
	}
	t.Logf("OIDC user provision+update OK against live schema (id %d), rolling back", created.ID)
}

// TestMergeRoms_Integration validates the duplicate-merge against the real
// schema inside an always-rolled-back transaction: two throwaway roms (one
// physical keeper, one virtual) are inserted, merged, and the keeper must
// survive with the absorbed row gone. Skipped unless DB creds set.
func TestMergeRoms_Integration(t *testing.T) {
	host, pass := os.Getenv("DB_HOST"), os.Getenv("DB_PASSWD")
	if host == "" || pass == "" {
		t.Skip("DB_HOST/DB_PASSWD not set; skipping live schema test")
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	st, err := Open(host, envOr("DB_PORT", "3306"), envOr("DB_USER", "romm-user"), pass, envOr("DB_NAME", "romm"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	var pid int64
	if err := st.DB.QueryRowContext(ctx, "SELECT id FROM platforms LIMIT 1").Scan(&pid); err != nil {
		t.Fatalf("pick platform: %v", err)
	}
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback() // never persist

	mk := func(name string, virtual int) int64 {
		rec := scan.Record{PlatformID: pid, FsPath: "roms/__gotest__", FsName: name,
			FsNameNoExt: name, FsNameNoTags: name, FsExtension: "bin", SizeBytes: 1}
		rec.Digests.CRC32 = "00000000"
		if err := upsertRom(ctx, tx, rec); err != nil {
			t.Fatalf("insert %s: %v", name, err)
		}
		var id int64
		if err := tx.QueryRowContext(ctx, "SELECT id FROM roms WHERE platform_id=? AND fs_name=?", pid, name).Scan(&id); err != nil {
			t.Fatalf("lookup %s: %v", name, err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE roms SET is_virtual=? WHERE id=?", virtual, id); err != nil {
			t.Fatalf("set virtual %s: %v", name, err)
		}
		return id
	}
	physical := mk("__merge_keeper__.bin", 0)
	virtual := mk("__merge_absorbed__.bin", 1)

	// Pass the virtual one first to prove the physical keeper still wins.
	res, err := mergeRoms(ctx, tx, []int64{virtual, physical})
	if err != nil {
		t.Fatalf("mergeRoms against live schema: %v", err)
	}
	if res.KeeperID != physical {
		t.Fatalf("keeper = %d, want physical %d", res.KeeperID, physical)
	}
	if len(res.AbsorbedIDs) != 1 || res.AbsorbedIDs[0] != virtual {
		t.Fatalf("absorbed = %v, want [%d]", res.AbsorbedIDs, virtual)
	}
	var keeperCount, absorbedCount int
	tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM roms WHERE id=?", physical).Scan(&keeperCount)
	tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM roms WHERE id=?", virtual).Scan(&absorbedCount)
	if keeperCount != 1 || absorbedCount != 0 {
		t.Fatalf("after merge: keeper=%d absorbed=%d (want 1,0)", keeperCount, absorbedCount)
	}
	t.Logf("merge OK against live schema (keeper %d absorbed %d), rolling back", physical, virtual)
}

// TestExcludeRom_Integration validates exclude (record exclusion + delete rom)
// against the real schema in a rolled-back tx. Skipped unless DB creds set.
func TestExcludeRom_Integration(t *testing.T) {
	host, pass := os.Getenv("DB_HOST"), os.Getenv("DB_PASSWD")
	if host == "" || pass == "" {
		t.Skip("DB_HOST/DB_PASSWD not set; skipping live schema test")
	}
	envOr := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	st, err := Open(host, envOr("DB_PORT", "3306"), envOr("DB_USER", "romm-user"), pass, envOr("DB_NAME", "romm"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	ctx := context.Background()
	var pid int64
	if err := st.DB.QueryRowContext(ctx, "SELECT id FROM platforms LIMIT 1").Scan(&pid); err != nil {
		t.Fatalf("pick platform: %v", err)
	}
	tx, err := st.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer tx.Rollback()

	rec := scan.Record{PlatformID: pid, FsPath: "roms/__gotest__", FsName: "__excl__.bin",
		FsNameNoExt: "__excl__", FsNameNoTags: "excl", FsExtension: "bin", SizeBytes: 1}
	rec.Digests.CRC32 = "00000000"
	if err := upsertRom(ctx, tx, rec); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var id int64
	tx.QueryRowContext(ctx, "SELECT id FROM roms WHERE platform_id=? AND fs_name=?", pid, "__excl__.bin").Scan(&id)

	if err := excludeRom(ctx, tx, id); err != nil {
		t.Fatalf("excludeRom against live schema: %v", err)
	}
	var romCount, exclCount int
	tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM roms WHERE id=?", id).Scan(&romCount)
	tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM virtual_exclusions WHERE platform_id=? AND name=?", pid, "__excl__").Scan(&exclCount)
	if romCount != 0 || exclCount != 1 {
		t.Fatalf("after exclude: rom=%d exclusion=%d (want 0,1)", romCount, exclCount)
	}
	t.Logf("exclude OK against live schema (rom %d), rolling back", id)
}
