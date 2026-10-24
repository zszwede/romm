package download

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
)

// ---------------------------------------------------------------------------
// plain HTTP
// ---------------------------------------------------------------------------

type plainHTTP struct {
	cfg    config.Host
	client *http.Client
}

func newPlainHTTP(h config.Host) *plainHTTP {
	return &plainHTTP{cfg: h, client: &http.Client{}}
}

func (p *plainHTTP) Client() *http.Client { return p.client }

func (p *plainHTTP) Request(ctx context.Context, l Link) (*http.Request, error) {
	return baseRequest(ctx, p.cfg, l.URL)
}

// ---------------------------------------------------------------------------
// archive.org: S3 "LOW <access>:<secret>" authorization. It carries the
// account's download entitlements, so access-restricted items serve too.
// ---------------------------------------------------------------------------

type archiveOrg struct {
	cfg    config.Host
	auth   string
	client *http.Client
}

func newArchiveOrg(h config.Host) *archiveOrg {
	a := &archiveOrg{cfg: h, auth: "LOW " + h.AccessKey + ":" + h.SecretKey}
	a.client = &http.Client{
		// archive.org redirects /download/... to a storage node
		// (ia800xxx.us.archive.org). Keep the auth header on redirects that
		// stay on a matching host, drop it anywhere else.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			if MatchHost(h.Match, req.URL.Hostname()) {
				req.Header.Set("Authorization", a.auth)
			} else {
				req.Header.Del("Authorization")
			}
			return nil
		},
	}
	return a
}

func (a *archiveOrg) Client() *http.Client { return a.client }

func (a *archiveOrg) Request(ctx context.Context, l Link) (*http.Request, error) {
	req, err := baseRequest(ctx, a.cfg, l.URL)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", a.auth)
	return req, nil
}

// ---------------------------------------------------------------------------
// 1fichier: POST the file URL to the get_token API (Bearer API key), then
// download the one-shot direct URL it returns.
// ---------------------------------------------------------------------------

type oneFichier struct {
	cfg    config.Host
	token  string
	client *http.Client // API + downloads

	mu      sync.Mutex // serializes API calls to respect the rate limit
	lastAPI time.Time
}

func newOneFichier(h config.Host) (*oneFichier, error) {
	token := h.Token
	if token == "" {
		b, err := os.ReadFile(h.TokenFile)
		if err != nil {
			return nil, fmt.Errorf("read token_file: %w", err)
		}
		token = string(b)
	}
	token = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(token), "Bearer "))
	if token == "" {
		return nil, fmt.Errorf("empty 1fichier token")
	}
	return &oneFichier{cfg: h, token: token, client: &http.Client{}}, nil
}

func (o *oneFichier) Client() *http.Client { return o.client }

// apiURL strips tracking params ("?id&af=123" or "?id?af=123" -> "?id"): the
// API rejects anything but the bare file id.
func apiURL(raw string) string {
	i := strings.Index(raw, "?")
	if i < 0 {
		return raw
	}
	id := raw[i+1:]
	if j := strings.IndexAny(id, "?&"); j >= 0 {
		id = id[:j]
	}
	return raw[:i+1] + id
}

func (o *oneFichier) Request(ctx context.Context, l Link) (*http.Request, error) {
	direct, err := o.getToken(ctx, l.URL)
	if err != nil {
		return nil, err
	}
	return baseRequest(ctx, o.cfg, direct)
}

func (o *oneFichier) getToken(ctx context.Context, fileURL string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if wait := o.cfg.APIPause - time.Since(o.lastAPI); wait > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(wait):
		}
	}
	defer func() { o.lastAPI = time.Now() }()

	body, _ := json.Marshal(map[string]string{"url": apiURL(fileURL)})
	actx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, o.cfg.APIURL, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	applyHeaders(req, o.cfg)
	req.Header.Set("Authorization", "Bearer "+o.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var tr struct {
		Status  string `json:"status"`
		URL     string `json:"url"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", fmt.Errorf("1fichier: bad token response (HTTP %d): %s", resp.StatusCode, truncate(string(raw), 160))
	}
	if tr.Status != "OK" || tr.URL == "" {
		err := fmt.Errorf("1fichier: token refused (HTTP %d): %s", resp.StatusCode, truncate(tr.Message, 160))
		// missing/deleted files and bad keys will not fix themselves
		if resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 404 {
			return "", permanentError{err}
		}
		return "", err
	}
	return tr.URL, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

// infoURL is the file-info endpoint, derived from the download/get_token URL
// (…/download/get_token.cgi → …/file/info.cgi), with a hard fallback.
func (o *oneFichier) infoURL() string {
	if u := o.cfg.APIURL; strings.Contains(u, "download/get_token.cgi") {
		return strings.Replace(u, "download/get_token.cgi", "file/info.cgi", 1)
	}
	return "https://api.1fichier.com/v1/file/info.cgi"
}

// Check reports whether a 1fichier file still exists, via file/info.cgi. It
// does not consume a download token or quota. A deleted file returns HTTP 404
// with {"status":"KO"}; a live file returns its filename/size. Network or
// other API errors (rate limit, auth) are reported as Checked=false so callers
// treat them as "unknown", never as a definite "gone".
func (o *oneFichier) Check(ctx context.Context, rawURL string) Availability {
	o.mu.Lock()
	defer o.mu.Unlock()
	if wait := o.cfg.APIPause - time.Since(o.lastAPI); wait > 0 {
		select {
		case <-ctx.Done():
			return Availability{Checked: false, Reason: ctx.Err().Error()}
		case <-time.After(wait):
		}
	}
	defer func() { o.lastAPI = time.Now() }()

	body, _ := json.Marshal(map[string]string{"url": apiURL(rawURL)})
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(actx, http.MethodPost, o.infoURL(), strings.NewReader(string(body)))
	if err != nil {
		return Availability{Checked: false, Reason: err.Error()}
	}
	applyHeaders(req, o.cfg)
	req.Header.Set("Authorization", "Bearer "+o.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := o.client.Do(req)
	if err != nil {
		return Availability{Checked: false, Reason: err.Error()}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	var ir struct {
		Status   string `json:"status"`
		Message  string `json:"message"`
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
	}
	_ = json.Unmarshal(raw, &ir)

	if resp.StatusCode == http.StatusNotFound || strings.EqualFold(ir.Status, "KO") {
		msg := ir.Message
		if msg == "" {
			msg = "file not found"
		}
		return Availability{Available: false, Checked: true, Reason: msg}
	}
	if resp.StatusCode != http.StatusOK {
		return Availability{Checked: false, Reason: fmt.Sprintf("info HTTP %d: %s", resp.StatusCode, truncate(ir.Message, 120))}
	}
	return Availability{Available: true, Checked: true, Filename: ir.Filename, Size: ir.Size}
}
