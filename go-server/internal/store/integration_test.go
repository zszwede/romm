package store

import (
	"context"
	"os"
	"testing"

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
	var n int
	err = tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM roms WHERE platform_id=? AND fs_name=?", pid, rec.FsName).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("probe row count = %d err=%v, want 1", n, err)
	}
	t.Logf("insert OK against live schema (platform %d), rolling back", pid)
}
