// Package downloadsvc runs the (absorbed) RommDownloader pipeline IN-PROCESS,
// so the single binary performs virtual downloads itself instead of dispatching
// to a separate service. It satisfies httpapi.VirtualService, so the same
// /api/virtual/{check,dispatch} endpoints drive it. The heavy external tools
// (7z, unar, switch-tool) are still invoked via exec by the pipeline, exactly
// as the standalone service did.
package downloadsvc

import (
	"context"
	"io"
	"log/slog"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/download"
	"github.com/zszwede/romm-go/internal/downloader/jobs"
	"github.com/zszwede/romm-go/internal/virtual"
)

type Service struct {
	cfg *config.Config
	dl  *download.Manager
	mgr *jobs.Manager
}

// New builds and starts the in-process service from a loaded config.
func New(cfg *config.Config) (*Service, error) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
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
	return &Service{cfg: cfg, dl: dl, mgr: mgr}, nil
}

// LoadAndStart loads a RommDownloader config.yaml and starts the service.
func LoadAndStart(path string) (*Service, error) {
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return New(cfg)
}

// Check reports link availability (httpapi.VirtualService).
func (s *Service) Check(ctx context.Context, urls []string) ([]virtual.Availability, error) {
	out := make([]virtual.Availability, 0, len(urls))
	for _, u := range urls {
		a := s.dl.Check(ctx, u)
		out = append(out, virtual.Availability{
			URL:       a.URL,
			Available: a.Available,
			Checked:   a.Checked,
			Reason:    a.Reason,
			Filename:  a.Filename,
			Size:      a.Size,
		})
	}
	return out, nil
}

// Dispatch submits a download+merge job to the in-process pipeline and returns
// the job id (httpapi.VirtualService).
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
		System: j.System,
		Name:   j.Name,
		Links:  links,
		Metadata: map[string]any{
			"rom_id":        j.RomID,
			"platform_id":   j.PlatformID,
			"platform_slug": j.System,
		},
	})
	if err != nil {
		return "", err
	}
	return job.ID, nil
}

func (s *Service) Shutdown() { s.mgr.Shutdown() }
