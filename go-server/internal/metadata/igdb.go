package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// IGDB queries the IGDB v4 API, authenticating via Twitch client-credentials
// (the token is cached until it nears expiry). Endpoint URLs are fields so
// tests can point them at an httptest server.
type IGDB struct {
	clientID     string
	clientSecret string
	tokenURL     string
	apiURL       string
	hc           *http.Client

	mu    sync.Mutex
	token string
	exp   time.Time
}

func NewIGDB(clientID, clientSecret string) *IGDB {
	return &IGDB{
		clientID:     clientID,
		clientSecret: clientSecret,
		tokenURL:     "https://id.twitch.tv/oauth2/token",
		apiURL:       "https://api.igdb.com/v4",
		hc:           &http.Client{Timeout: 20 * time.Second},
	}
}

func (g *IGDB) Name() string { return "igdb" }

func (g *IGDB) accessToken(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.token != "" && time.Now().Before(g.exp) {
		return g.token, nil
	}
	form := url.Values{
		"client_id":     {g.clientID},
		"client_secret": {g.clientSecret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenURL+"?"+form.Encode(), nil)
	if err != nil {
		return "", err
	}
	resp, err := g.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("igdb token: %s: %s", resp.Status, b)
	}
	var tr struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}
	if tr.AccessToken == "" {
		return "", fmt.Errorf("igdb token: empty access_token")
	}
	g.token = tr.AccessToken
	ttl := time.Duration(tr.ExpiresIn) * time.Second
	if ttl <= time.Minute {
		ttl = time.Hour
	}
	g.exp = time.Now().Add(ttl - time.Minute)
	return g.token, nil
}

func (g *IGDB) Search(ctx context.Context, query string) ([]Game, error) {
	tok, err := g.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body := fmt.Sprintf("search %q; fields name,summary,cover.url; limit 10;", query)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.apiURL+"/games", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Client-ID", g.clientID)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Accept", "application/json")
	resp, err := g.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("igdb search: %s: %s", resp.Status, b)
	}
	var raw []struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		Summary string `json:"summary"`
		Cover   struct {
			URL string `json:"url"`
		} `json:"cover"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, err
	}
	out := make([]Game, 0, len(raw))
	for _, r := range raw {
		out = append(out, Game{
			Provider:   "igdb",
			ProviderID: r.ID,
			Name:       r.Name,
			Summary:    r.Summary,
			CoverURL:   normalizeCover(r.Cover.URL),
		})
	}
	return out, nil
}

// normalizeCover upgrades IGDB's protocol-relative thumb URL to an https
// 1080p cover, matching what the Python app stores.
func normalizeCover(u string) string {
	if u == "" {
		return ""
	}
	if strings.HasPrefix(u, "//") {
		u = "https:" + u
	}
	return strings.Replace(u, "t_thumb", "t_1080p", 1)
}
