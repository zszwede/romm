package downloadsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/jobs"
	"github.com/zszwede/romm-go/internal/downloader/pipeline"
	"github.com/zszwede/romm-go/internal/virtual"
)

// writeConfig writes a minimal valid RommDownloader config.yaml (work/state/out
// under a temp dir, one trivial system). With withHost it adds a catch-all http
// host so links are accepted.
func writeConfig(t *testing.T, withHost bool) string {
	t.Helper()
	d := t.TempDir()
	hosts := ""
	if withHost {
		hosts = "hosts:\n  - name: any\n    match: [\"*\"]\n    type: http\n"
	}
	cfg := fmt.Sprintf(`%s
work_dir: %s/work
state_dir: %s/state
workers: 1
default_system: test
systems:
  test:
    target_dir: %s/out
    steps: []
`, hosts, d, d, d)
	p := filepath.Join(d, "config.yaml")
	if err := os.WriteFile(p, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestService_LoadDispatchCheck(t *testing.T) {
	svc, err := LoadAndStart(writeConfig(t, true), nil, config.Overrides{})
	if err != nil {
		t.Fatalf("load/start: %v", err)
	}
	defer svc.Shutdown()

	// Dispatch returns a job id synchronously (download runs async; not awaited).
	id, err := svc.Dispatch(context.Background(), virtual.Job{
		System: "test", Name: "probe.bin", RomID: 1, PlatformID: 2,
		Links: []virtual.Link{{URL: "http://127.0.0.1:0/x", Category: "base"}},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if id == "" {
		t.Error("empty job id")
	}

	// Check returns one result per URL (unreachable -> not available, no crash).
	res, err := svc.Check(context.Background(), []string{"http://127.0.0.1:0/x"})
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if len(res) != 1 || res[0].URL != "http://127.0.0.1:0/x" {
		t.Errorf("check results = %+v", res)
	}
}

func TestService_RejectsUnmatchedHost(t *testing.T) {
	svc, err := LoadAndStart(writeConfig(t, false), nil, config.Overrides{}) // no hosts configured
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Shutdown()
	if _, err := svc.Dispatch(context.Background(), virtual.Job{
		System: "test", Name: "x", Links: []virtual.Link{{URL: "http://example.com/x", Category: "base"}},
	}); err == nil {
		t.Error("expected error: no host configuration matches")
	}
}

type fakeUpdater struct {
	romID     int64
	dir, name string
	size      int64
	calls     int
}

func (f *fakeUpdater) ApplyDownloaded(_ context.Context, romID int64, dir, name string, size int64) error {
	f.romID, f.dir, f.name, f.size, f.calls = romID, dir, name, size, f.calls+1
	return nil
}

func TestApplyJob(t *testing.T) {
	up := &fakeUpdater{}
	j := &jobs.Job{
		Status:   jobs.Completed,
		Metadata: map[string]any{"rom_id": float64(35950)}, // float64 as after a JSON round-trip
		Files: []pipeline.Delivered{
			{Path: "/mnt/retroEMU/roms/switch/Game [v0].nsp", Name: "Game [v0].nsp", Size: 4242},
		},
	}
	if !applyJob(context.Background(), j, up) {
		t.Fatal("applyJob returned false")
	}
	if up.calls != 1 || up.romID != 35950 || up.dir != "roms/switch" || up.name != "Game [v0].nsp" || up.size != 4242 {
		t.Errorf("updater got romID=%d dir=%q name=%q size=%d calls=%d", up.romID, up.dir, up.name, up.size, up.calls)
	}

	// Not-completed and missing-rom_id are no-ops.
	if applyJob(context.Background(), &jobs.Job{Status: jobs.Failed}, up) {
		t.Error("failed job should not apply")
	}
	if applyJob(context.Background(), &jobs.Job{Status: jobs.Completed}, up) {
		t.Error("job without rom_id should not apply")
	}
}

func TestRelDir(t *testing.T) {
	for in, want := range map[string]string{
		"/mnt/retroEMU/roms/switch/Game.nsp": "roms/switch",
		"/data/x/roms/ps2/disc/Game.iso":     "roms/ps2/disc",
		"/weird/path/Game.bin":               "/weird/path",
	} {
		if got := relDir(in); got != want {
			t.Errorf("relDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMetaInt(t *testing.T) {
	if metaInt(float64(7)) != 7 || metaInt(int64(7)) != 7 || metaInt(int(7)) != 7 || metaInt("x") != 0 {
		t.Error("metaInt coercion wrong")
	}
}
