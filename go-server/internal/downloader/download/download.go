// Package download fetches links with host-specific authentication.
//
// A Provider turns a link into a ready-to-send HTTP request (resolving
// 1fichier download tokens, adding archive.org S3 auth, ...). The shared
// fetch loop handles retries, resumable .part files and file naming.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
	"github.com/zszwede/romm-go/internal/downloader/fsutil"
)

// Link is one file to download.
type Link struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"` // optional file name override
}

// Availability is the result of an availability pre-check for one link.
// Checked=false means the host has no availability API (or the check could
// not be completed) — callers must then treat the link as "unknown", i.e.
// assume it is fetchable, never prune it.
type Availability struct {
	URL       string `json:"url"`
	Available bool   `json:"available"`
	Checked   bool   `json:"checked"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Checker is an optional Provider capability: report whether a link's file
// still exists without downloading it.
type Checker interface {
	Check(ctx context.Context, rawURL string) Availability
}

// Provider builds the request for a link. It is called again for every
// attempt, so one-shot URLs (1fichier tokens) are fresh each time.
type Provider interface {
	Request(ctx context.Context, link Link) (*http.Request, error)
	// Client returns the HTTP client to use (redirect policy may carry auth).
	Client() *http.Client
}

type hostEntry struct {
	cfg      config.Host
	provider Provider
}

// Manager picks the provider by link host.
type Manager struct {
	hosts []hostEntry
	log   *slog.Logger
}

func NewManager(hosts []config.Host, log *slog.Logger) (*Manager, error) {
	m := &Manager{log: log}
	for _, h := range hosts {
		var p Provider
		var err error
		switch h.Type {
		case "onefichier":
			p, err = newOneFichier(h)
		case "archive_org":
			p = newArchiveOrg(h)
		case "http":
			p = newPlainHTTP(h)
		default:
			err = fmt.Errorf("unknown host type %q", h.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("host %s: %w", h.Name, err)
		}
		m.hosts = append(m.hosts, hostEntry{cfg: h, provider: p})
	}
	return m, nil
}

// HostFor returns the host config handling rawURL.
func (m *Manager) HostFor(rawURL string) (*config.Host, Provider, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	host := strings.ToLower(u.Hostname())
	for i := range m.hosts {
		if MatchHost(m.hosts[i].cfg.Match, host) {
			return &m.hosts[i].cfg, m.hosts[i].provider, nil
		}
	}
	return nil, nil, fmt.Errorf("no host configuration matches %q", host)
}

// Check reports availability for rawURL, routing to the host's provider. A
// host whose provider is not a Checker yields Checked=false, Available=true
// (unknown → assume fetchable). An unroutable URL is reported as a definite
// failure (Checked=true, Available=false) so callers can surface it.
func (m *Manager) Check(ctx context.Context, rawURL string) Availability {
	_, p, err := m.HostFor(rawURL)
	if err != nil {
		return Availability{URL: rawURL, Available: false, Checked: true, Reason: err.Error()}
	}
	if c, ok := p.(Checker); ok {
		a := c.Check(ctx, rawURL)
		a.URL = rawURL
		return a
	}
	return Availability{URL: rawURL, Available: true, Checked: false}
}

// MatchHost: "*" matches all, "*.x.com" matches subdomains and x.com itself,
// anything else matches exactly (and its subdomains, e.g. "archive.org" also
// matches "ia800.us.archive.org").
func MatchHost(patterns []string, host string) bool {
	host = strings.ToLower(host)
	for _, p := range patterns {
		p = strings.ToLower(strings.TrimSpace(p))
		switch {
		case p == "*":
			return true
		case strings.HasPrefix(p, "*."):
			base := p[2:]
			if host == base || strings.HasSuffix(host, "."+base) {
				return true
			}
		default:
			if host == p || strings.HasSuffix(host, "."+p) {
				return true
			}
		}
	}
	return false
}

// Progress is called while downloading (total < 0 when unknown).
type Progress func(written, total int64)

// Fetch downloads link into dir and returns the final file path. slot is a
// stable identifier of the link within its job; it names the .part file so
// an interrupted download resumes after a restart.
func (m *Manager) Fetch(ctx context.Context, link Link, dir, slot string, progress Progress) (string, error) {
	hc, prov, err := m.HostFor(link.URL)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	// finished before (e.g. job restarted after a crash in a later stage)
	doneMarker := filepath.Join(dir, ".dl-"+slot+".done")
	if b, err := os.ReadFile(doneMarker); err == nil {
		p := filepath.Join(dir, strings.TrimSpace(string(b)))
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			return p, nil
		}
	}

	var lastErr error
	for attempt := 1; attempt <= hc.Retries; attempt++ {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		p, err := m.fetchOnce(ctx, hc, prov, link, dir, slot, progress)
		if err == nil {
			if err := os.WriteFile(doneMarker, []byte(filepath.Base(p)), 0o644); err != nil {
				return "", err
			}
			return p, nil
		}
		lastErr = err
		var perm permanentError
		if errors.As(err, &perm) {
			break
		}
		m.log.Warn("download attempt failed", "url", link.URL, "attempt", attempt, "of", hc.Retries, "err", err)
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(time.Duration(attempt) * hc.RetryDelay):
		}
	}
	return "", lastErr
}

// permanentError stops retrying (e.g. 401/404).
type permanentError struct{ error }

func (m *Manager) fetchOnce(ctx context.Context, hc *config.Host, prov Provider, link Link, dir, slot string, progress Progress) (string, error) {
	if hc.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, hc.Timeout)
		defer cancel()
	}
	req, err := prov.Request(ctx, link)
	if err != nil {
		return "", err
	}
	part := filepath.Join(dir, ".dl-"+slot+".part")
	var offset int64
	if st, err := os.Stat(part); err == nil {
		offset = st.Size()
	}
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}
	resp, err := prov.Client().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part) // stale .part: start over on the next attempt
		return "", fmt.Errorf("range not satisfiable, restarting")
	case resp.StatusCode == http.StatusOK:
		offset = 0 // server ignored the range
	case resp.StatusCode == http.StatusPartialContent:
	case resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 || resp.StatusCode == 410:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return "", permanentError{fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))}
	default:
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	total := int64(-1)
	if resp.ContentLength >= 0 {
		total = offset + resp.ContentLength
	}
	flags := os.O_CREATE | os.O_WRONLY
	if offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return "", err
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return "", err
	}
	w := &progressWriter{w: f, n: offset, total: total, fn: progress}
	_, copyErr := io.Copy(w, resp.Body)
	closeErr := f.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if total >= 0 && w.n != total {
		return "", fmt.Errorf("short download: %d of %d bytes", w.n, total)
	}

	name := fileName(link, resp, slot)
	dest := fsutil.UniquePath(filepath.Join(dir, name))
	if err := os.Rename(part, dest); err != nil {
		return "", err
	}
	return dest, nil
}

// fileName: request override > Content-Disposition > URL path > slot.
func fileName(link Link, resp *http.Response, slot string) string {
	if n := fsutil.SafeName(link.Name); n != "" {
		return n
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != "" {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			if n := fsutil.SafeName(params["filename"]); n != "" {
				return n
			}
		}
	}
	for _, u := range []*url.URL{resp.Request.URL, mustParse(link.URL)} {
		if u == nil {
			continue
		}
		if b := path.Base(u.Path); b != "/" && b != "." && b != "" {
			if dec, err := url.PathUnescape(b); err == nil {
				b = dec
			}
			if n := fsutil.SafeName(b); n != "" && strings.Contains(n, ".") {
				return n
			}
		}
	}
	return "download-" + slot
}

func mustParse(s string) *url.URL {
	u, _ := url.Parse(s)
	return u
}

type progressWriter struct {
	w        io.Writer
	n, total int64
	fn       Progress
	last     time.Time
}

func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.n += int64(n)
	if p.fn != nil && time.Since(p.last) > time.Second {
		p.last = time.Now()
		p.fn(p.n, p.total)
	}
	return n, err
}

// baseRequest builds a GET with the host's configured headers.
func baseRequest(ctx context.Context, h config.Host, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	applyHeaders(req, h)
	return req, nil
}

func applyHeaders(req *http.Request, h config.Host) {
	ua := h.UserAgent
	if ua == "" {
		ua = "RommDownloader/1.0"
	}
	req.Header.Set("User-Agent", ua)
	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}
}
