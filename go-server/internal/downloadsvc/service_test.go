package downloadsvc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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
	svc, err := LoadAndStart(writeConfig(t, true))
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
	svc, err := LoadAndStart(writeConfig(t, false)) // no hosts configured
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
