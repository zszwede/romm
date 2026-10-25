// Package downloadsvc runs the (absorbed) RommDownloader pipeline IN-PROCESS,
// so the single binary performs virtual downloads itself instead of dispatching
// to a separate service. It satisfies httpapi.VirtualService, and — given a
// RomUpdater — applies each finished job back to the DB (the role the Python
// app's HTTP callback played), turning the virtual rom physical.
package downloadsvc

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/download"
	"github.com/zszwede/romm-go/internal/downloader/jobs"
	"github.com/zszwede/romm-go/internal/virtual"
)

// RomUpdater applies a completed download to the rom row (implemented by the
// store). dir is library-relative (e.g. "roms/switch").
type RomUpdater interface {
	ApplyDownloaded(ctx context.Context, romID int64, dir, name string, size int64) error
}

type Service struct {
	cfg     *config.Config
	dl      *download.Manager
	mgr     *jobs.Manager
	updater RomUpdater

	ctx     context.Context
	cancel  context.CancelFunc
	pollWG  sync.WaitGroup
	mu      sync.Mutex
	applied map[string]bool
}

// New builds and starts the in-process service. If updater is non-nil, a poller
// applies finished jobs back to the DB.
func New(cfg *config.Config, updater RomUpdater) (*Service, error) {
	// Download pipeline log (extract/decompress/merge steps, completions,
	// failures). Goes to stderr by default (so it lands in the server log), or
	// to ROMM_DOWNLOADER_LOG when set, for a dedicated tail-able file.
	var logW io.Writer = os.Stderr
	if path := os.Getenv("ROMM_DOWNLOADER_LOG"); path != "" {
		if f, ferr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
			logW = f
		}
	}
	log := slog.New(slog.NewTextHandler(logW, &slog.HandlerOptions{Level: slog.LevelInfo}))
	dl, err := download.NewManager(cfg.Hosts, log)
	if err != nil {
		return nil, err
	}
	mgr, err := jobs.NewManager(cfg, dl, log)
	if err != nil {
		return nil, err
	}
	if err := mgr.Start(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Service{cfg: cfg, dl: dl, mgr: mgr, updater: updater,
		ctx: ctx, cancel: cancel, applied: map[string]bool{}}
	if updater != nil {
		s.pollWG.Add(1)
		go s.poll()
	}
	return s, nil
}

// LoadAndStart loads a RommDownloader config.yaml, applies the runtime path
// overrides (so the Switch steps use RomM's bundled switch-tool + keys, not the
// container paths in the file), and starts the service.
func LoadAndStart(path string, updater RomUpdater, ov config.Overrides) (*Service, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	cfg.ApplyOverrides(ov)
	return New(cfg, updater)
}

func (s *Service) Check(ctx context.Context, urls []string) ([]virtual.Availability, error) {
	out := make([]virtual.Availability, 0, len(urls))
	for _, u := range urls {
		a := s.dl.Check(ctx, u)
		out = append(out, virtual.Availability{
			URL: a.URL, Available: a.Available, Checked: a.Checked,
			Reason: a.Reason, Filename: a.Filename, Size: a.Size,
		})
	}
	return out, nil
}

func (s *Service) Dispatch(_ context.Context, j virtual.Job) (string, error) {
	links := make([]*jobs.Link, 0, len(j.Links))
	for _, l := range j.Links {
		meta := map[string]any{"category": l.Category}
		if l.Version != "" {
			meta["version"] = l.Version
		}
		links = append(links, &jobs.Link{URL: l.URL, Name: l.Name, Meta: meta})
	}
	job, err := s.mgr.Submit(jobs.Request{
		System: j.System, Name: j.Name, Links: links,
		Metadata: map[string]any{
			"rom_id": j.RomID, "platform_id": j.PlatformID, "platform_slug": j.System,
		},
	})
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

func (s *Service) Shutdown() {
	s.cancel()
	s.pollWG.Wait()
	s.mgr.Shutdown()
}

// poll periodically applies newly-completed jobs to the DB (once each).
func (s *Service) poll() {
	defer s.pollWG.Done()
	tk := time.NewTicker(3 * time.Second)
	defer tk.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tk.C:
			for _, j := range s.mgr.List(string(jobs.Completed)) {
				s.mu.Lock()
				done := s.applied[j.ID]
				s.mu.Unlock()
				if done {
					continue
				}
				applyJob(s.ctx, j, s.updater)
				s.mu.Lock()
				s.applied[j.ID] = true
				s.mu.Unlock()
			}
		}
	}
}

// applyJob writes a completed job's primary delivered file back to its rom.
// Returns true if an update was attempted. Pure enough to unit-test.
func applyJob(ctx context.Context, j *jobs.Job, updater RomUpdater) bool {
	if updater == nil || j.Status != jobs.Completed {
		return false
	}
	romID := metaInt(j.Metadata["rom_id"])
	if romID <= 0 {
		return false
	}
	for _, f := range j.Files {
		if f.Skipped {
			continue
		}
		_ = updater.ApplyDownloaded(ctx, romID, relDir(f.Path), f.Name, f.Size)
		return true
	}
	return false
}

// relDir makes a delivered absolute path library-relative, preferring the
// "/roms/" boundary (robust to the merge service's mount prefix).
func relDir(abs string) string {
	p := strings.ReplaceAll(abs, "\\", "/")
	if i := strings.Index(p, "/roms/"); i >= 0 {
		return path.Dir(p[i+1:]) // "roms/<slug>"
	}
	return path.Dir(p)
}

// metaInt coerces a JSON metadata value (float64 after a round-trip, or a
// native int) to int64.
func metaInt(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	default:
		return 0
	}
}
