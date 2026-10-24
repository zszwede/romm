package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/callback"
	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/download"
	"github.com/zszwede/romm-go/internal/downloader/pipeline"
)

var (
	ErrNotFound   = errors.New("job not found")
	ErrBadRequest = errors.New("bad request")
	ErrConflict   = errors.New("conflict")
)

// Request is the body of POST /api/v1/jobs.
type Request struct {
	System      string         `json:"system"` // or metadata.system
	Name        string         `json:"name,omitempty"`
	Links       []*Link        `json:"links"`
	Metadata    map[string]any `json:"metadata,omitempty"`
	CallbackURL string         `json:"callback_url,omitempty"`
}

type Manager struct {
	cfg       *config.Config
	dl        *download.Manager
	pipelines map[string]*pipeline.Pipeline
	log       *slog.Logger

	mu      sync.Mutex
	jobs    map[string]*Job
	cancels map[string]context.CancelFunc
	queue   chan string

	ctx  context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup
	bgWG sync.WaitGroup // callbacks
}

func NewManager(cfg *config.Config, dl *download.Manager, log *slog.Logger) (*Manager, error) {
	m := &Manager{
		cfg:       cfg,
		dl:        dl,
		log:       log,
		pipelines: map[string]*pipeline.Pipeline{},
		jobs:      map[string]*Job{},
		cancels:   map[string]context.CancelFunc{},
		queue:     make(chan string, 100000),
	}
	for name, s := range cfg.Systems {
		p, err := pipeline.Build(s.Steps)
		if err != nil {
			return nil, fmt.Errorf("systems.%s: %w", name, err)
		}
		m.pipelines[name] = p
	}
	for _, d := range []string{cfg.WorkDir, cfg.StateDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	return m, nil
}

// Start loads persisted jobs, re-queues unfinished ones and starts workers.
func (m *Manager) Start() error {
	m.ctx, m.stop = context.WithCancel(context.Background())
	entries, err := os.ReadDir(m.cfg.StateDir)
	if err != nil {
		return err
	}
	var resume []*Job
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(m.cfg.StateDir, e.Name()))
		if err != nil {
			return err
		}
		var j Job
		if err := json.Unmarshal(b, &j); err != nil {
			m.log.Error("skipping unreadable job state", "file", e.Name(), "err", err)
			continue
		}
		m.jobs[j.ID] = &j
		if !j.Status.Finished() {
			resume = append(resume, &j)
		} else if j.Callback.Status == "pending" {
			m.sendCallback(&j) // finished, but the callback never got through
		}
	}
	sort.Slice(resume, func(a, b int) bool { return resume[a].CreatedAt.Before(resume[b].CreatedAt) })
	for _, j := range resume {
		m.log.Info("resuming job", "job", j.ID, "was", j.Status)
		j.Status = Queued
		j.Step = ""
		m.persist(j)
		m.queue <- j.ID
	}
	for i := 0; i < m.cfg.Workers; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	return nil
}

// Shutdown stops the workers. Running jobs stay unfinished in the state dir
// and resume on the next start.
func (m *Manager) Shutdown() {
	m.stop()
	m.wg.Wait()
	// give in-flight callbacks a moment; unsent ones stay "pending" and are
	// re-sent on the next start
	done := make(chan struct{})
	go func() { m.bgWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
	}
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// Submit validates and queues a request.
func (m *Manager) Submit(r Request) (*Job, error) {
	if len(r.Links) == 0 {
		return nil, fmt.Errorf("%w: links is empty", ErrBadRequest)
	}
	reqSystem := r.System
	if reqSystem == "" && r.Metadata != nil {
		if s, ok := r.Metadata["system"].(string); ok {
			reqSystem = s
		}
	}
	if reqSystem == "" && m.cfg.DefaultSystem == "" {
		return nil, fmt.Errorf("%w: system is required (top level or metadata.system)", ErrBadRequest)
	}
	system, ok := m.cfg.ResolveSystem(reqSystem)
	if !ok {
		return nil, fmt.Errorf("%w: system %q is not configured", ErrBadRequest, reqSystem)
	}
	for i, l := range r.Links {
		if l == nil || l.URL == "" {
			return nil, fmt.Errorf("%w: links[%d]: url is empty", ErrBadRequest, i)
		}
		h, _, err := m.dl.HostFor(l.URL)
		if err != nil {
			return nil, fmt.Errorf("%w: links[%d]: %v", ErrBadRequest, i, err)
		}
		l.Host = h.Name
		l.File, l.Bytes, l.Total, l.Done = "", 0, 0, false
	}
	cb := m.cfg.CallbackFor(system)
	cbURL := cb.URL
	if r.CallbackURL != "" {
		if !config.Bool(cb.AllowRequestOverride, false) {
			return nil, fmt.Errorf("%w: callback_url override is disabled in the configuration", ErrBadRequest)
		}
		cbURL = r.CallbackURL
	}
	now := time.Now().UTC()
	j := &Job{
		ID:              newID(),
		System:          system,
		RequestedSystem: reqSystem,
		Name:            r.Name,
		Links:           r.Links,
		Metadata:        r.Metadata,
		Status:          Queued,
		Callback:        CallbackState{URL: cbURL, Status: "none"},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.persistLocked(j)
	m.mu.Unlock()
	select {
	case m.queue <- j.ID:
	default:
		return nil, fmt.Errorf("queue is full")
	}
	m.log.Info("job queued", "job", j.ID, "system", system, "links", len(j.Links))
	return m.Get(j.ID)
}

// Get returns a snapshot of a job.
func (m *Manager) Get(id string) (*Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return snapshot(j), nil
}

func snapshot(j *Job) *Job {
	b, _ := json.Marshal(j)
	var c Job
	json.Unmarshal(b, &c)
	return &c
}

// List returns jobs, newest first, optionally filtered by status.
func (m *Manager) List(status string) []*Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*Job
	for _, j := range m.jobs {
		if status == "" || string(j.Status) == status {
			out = append(out, snapshot(j))
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out
}

// Cancel stops a queued/running job.
func (m *Manager) Cancel(id string) (*Job, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	if j.Status.Finished() {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: job already %s", ErrConflict, j.Status)
	}
	j.cancelRequested = true
	if cancel, running := m.cancels[id]; running {
		cancel() // the worker finishes it as canceled
	} else {
		m.finishLocked(j, Canceled, "canceled before start")
	}
	m.mu.Unlock()
	return m.Get(id)
}

// Delete removes a finished job record.
func (m *Manager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return ErrNotFound
	}
	if !j.Status.Finished() {
		return fmt.Errorf("%w: job is %s, cancel it first", ErrConflict, j.Status)
	}
	delete(m.jobs, id)
	os.Remove(m.statePath(id))
	os.RemoveAll(m.workDir(id))
	return nil
}

// Retry re-queues a failed or canceled job (downloads already finished and
// still in the work dir are reused).
func (m *Manager) Retry(id string) (*Job, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	if j.Status != Failed && j.Status != Canceled {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: only failed or canceled jobs can be retried (job is %s)", ErrConflict, j.Status)
	}
	j.Status, j.Step, j.Error, j.FinishedAt, j.Files = Queued, "", "", nil, nil
	j.cancelRequested = false
	j.Callback.Status, j.Callback.Error = "none", ""
	m.persistLocked(j)
	m.mu.Unlock()
	m.queue <- id
	return m.Get(id)
}

// ---------------------------------------------------------------------------
// worker
// ---------------------------------------------------------------------------

func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case id := <-m.queue:
			m.run(id)
		}
	}
}

func (m *Manager) workDir(id string) string   { return filepath.Join(m.cfg.WorkDir, id) }
func (m *Manager) statePath(id string) string { return filepath.Join(m.cfg.StateDir, id+".json") }

func (m *Manager) run(id string) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok || j.Status != Queued {
		m.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(m.ctx)
	defer cancel()
	m.cancels[id] = cancel
	now := time.Now().UTC()
	j.StartedAt = &now
	j.Attempts++
	m.setStatusLocked(j, Downloading, "")
	sys := m.cfg.Systems[j.System]
	pl := m.pipelines[j.System]
	links := j.Links
	pjob := &pipeline.Job{ID: j.ID, System: j.System, Name: j.Name, Metadata: j.Metadata,
		WorkDir: m.workDir(id), Log: m.log.With("job", j.ID)}
	m.mu.Unlock()

	err := m.process(ctx, j, pjob, sys, pl, links)

	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, id)
	switch {
	case err == nil:
		m.finishLocked(j, Completed, "")
	case j.cancelRequested:
		m.finishLocked(j, Canceled, "canceled")
	case m.ctx.Err() != nil:
		// shutting down: leave the job unfinished so it resumes on restart
		pjob.Log.Info("job interrupted by shutdown, will resume")
	default:
		pjob.Log.Error("job failed", "err", err)
		m.finishLocked(j, Failed, err.Error())
	}
}

func (m *Manager) process(ctx context.Context, j *Job, pjob *pipeline.Job, sys config.System, pl *pipeline.Pipeline, links []*Link) error {
	dlDir := filepath.Join(pjob.WorkDir, "download")
	var files []string
	for i, l := range links {
		slot := fmt.Sprintf("%03d", i)
		pjob.Log.Info("downloading", "url", l.URL, "host", l.Host)
		p, err := m.dl.Fetch(ctx, download.Link{URL: l.URL, Name: l.Name}, dlDir, slot, func(written, total int64) {
			m.mu.Lock()
			l.Bytes, l.Total = written, total
			m.mu.Unlock()
		})
		if err != nil {
			return fmt.Errorf("download %s: %w", l.URL, err)
		}
		st, _ := os.Stat(p)
		m.mu.Lock()
		l.File, l.Done = p, true
		if st != nil {
			l.Bytes, l.Total = st.Size(), st.Size()
		}
		m.persistLocked(j)
		m.mu.Unlock()
		files = append(files, p)
	}

	m.setStatus(j, Processing, "")
	files, err := pl.Run(ctx, pjob, files, func(step string) { m.setStatus(j, Processing, step) })
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("pipeline produced no files")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	m.setStatus(j, Delivering, "")
	delivered, err := pipeline.Deliver(sys, pjob, files)
	m.mu.Lock()
	j.Files = delivered
	m.mu.Unlock()
	return err
}

func (m *Manager) setStatus(j *Job, s Status, step string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setStatusLocked(j, s, step)
}

func (m *Manager) setStatusLocked(j *Job, s Status, step string) {
	j.Status, j.Step = s, step
	m.persistLocked(j)
}

func (m *Manager) finishLocked(j *Job, s Status, errMsg string) {
	now := time.Now().UTC()
	j.Status, j.Step, j.Error, j.FinishedAt = s, "", errMsg, &now
	keep := s == Failed && m.cfg.KeepFailedWorkDirs
	if !keep {
		os.RemoveAll(m.workDir(j.ID))
		for _, l := range j.Links {
			l.File = ""
		}
	}
	cb := m.cfg.CallbackFor(j.System)
	if j.Callback.URL != "" && (s == Completed || config.Bool(cb.OnFailure, true)) {
		j.Callback.Status = "pending"
	}
	m.persistLocked(j)
	m.log.Info("job finished", "job", j.ID, "status", s, "files", len(j.Files), "err", errMsg)
	if j.Callback.Status == "pending" {
		m.sendCallback(j)
	}
}

func (m *Manager) sendCallback(j *Job) {
	cb := m.cfg.CallbackFor(j.System)
	payload := j.payload()
	url := j.Callback.URL
	m.bgWG.Add(1)
	go func() {
		defer m.bgWG.Done()
		// independent of the shutdown context: a finished job deserves its
		// notification; if we die midway it is retried on the next start
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := callback.Send(ctx, cb, url, payload, m.log.With("job", j.ID))
		m.mu.Lock()
		defer m.mu.Unlock()
		if err != nil {
			j.Callback.Status, j.Callback.Error = "failed", err.Error()
		} else {
			j.Callback.Status, j.Callback.Error = "sent", ""
		}
		m.persistLocked(j)
	}()
}

// ResendCallback re-sends the callback of a finished job.
func (m *Manager) ResendCallback(id string) (*Job, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, ErrNotFound
	}
	if !j.Status.Finished() || j.Callback.URL == "" {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: job not finished or no callback url", ErrConflict)
	}
	j.Callback.Status, j.Callback.Error = "pending", ""
	m.persistLocked(j)
	m.sendCallback(j)
	m.mu.Unlock()
	return m.Get(id)
}

func (m *Manager) persist(j *Job) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.persistLocked(j)
}

func (m *Manager) persistLocked(j *Job) {
	j.UpdatedAt = time.Now().UTC()
	b, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		m.log.Error("marshal job", "job", j.ID, "err", err)
		return
	}
	tmp := m.statePath(j.ID) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err == nil {
		err = os.Rename(tmp, m.statePath(j.ID))
	}
	if err != nil {
		m.log.Error("persist job", "job", j.ID, "err", err)
	}
}
