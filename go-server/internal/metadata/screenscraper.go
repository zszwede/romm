package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// SS queries the ScreenScraper v2 API (api.screenscraper.fr/api2), authenticating
// with the dev credentials plus the user account, exactly as the Python app does
// (devid/devpassword/softname + ssid/sspassword on every request). apiURL is a
// field so tests can point it at an httptest server.
type SS struct {
	devID       string
	devPassword string
	user        string
	password    string
	apiURL      string
	hc          *http.Client
}

// NewSS builds a ScreenScraper client. The dev credentials identify the "romm"
// software to SS; the user account raises the per-day scrape quota.
func NewSS(devID, devPassword, user, password string) *SS {
	return &SS{
		devID:       devID,
		devPassword: devPassword,
		user:        user,
		password:    password,
		apiURL:      "https://api.screenscraper.fr/api2",
		hc:          &http.Client{Timeout: 25 * time.Second},
	}
}

func (s *SS) Name() string { return "ss" }

// HasDevCredentials reports whether the dev (software) credentials are set — the
// SS_DEV_CREDENTIALS_SET flag the frontend reads.
func (s *SS) HasDevCredentials() bool { return s.devID != "" && s.devPassword != "" }

// auth returns the query parameters SS requires on every call.
func (s *SS) auth() url.Values {
	v := url.Values{}
	v.Set("output", "json")
	v.Set("softname", "romm")
	if s.devID != "" {
		v.Set("devid", s.devID)
	}
	if s.devPassword != "" {
		v.Set("devpassword", s.devPassword)
	}
	if s.user != "" {
		v.Set("ssid", s.user)
	}
	if s.password != "" {
		v.Set("sspassword", s.password)
	}
	return v
}

// ssRegionalText is a {region|langue, text} entry (names, synopsis).
type ssRegionalText struct {
	Region string `json:"region"`
	Langue string `json:"langue"`
	Text   string `json:"text"`
}

type ssMedia struct {
	Type   string `json:"type"`
	Region string `json:"region"`
	URL    string `json:"url"`
}

// ssGame is the subset of SS's jeu object this server reads.
type ssGame struct {
	ID       json.Number      `json:"id"`
	NotGame  string           `json:"notgame"`
	Noms     []ssRegionalText `json:"noms"`
	Synopsis []ssRegionalText `json:"synopsis"`
	Medias   []ssMedia        `json:"medias"`
}

type ssEnvelope struct {
	Response struct {
		Jeux []ssGame `json:"jeux"` // jeuRecherche
		Jeu  *ssGame  `json:"jeu"`  // jeuInfos
	} `json:"response"`
}

// Search satisfies Provider with an unscoped name search (no platform).
func (s *SS) Search(ctx context.Context, query string) ([]Game, error) {
	return s.SearchPlatform(ctx, query, 0)
}

// SearchPlatform searches games by name, scoped to a ScreenScraper system id
// (the platform's ss_id) when non-zero.
func (s *SS) SearchPlatform(ctx context.Context, term string, systemID int64) ([]Game, error) {
	q := s.auth()
	q.Set("recherche", term)
	if systemID > 0 {
		q.Set("systemeid", strconv.FormatInt(systemID, 10))
	}
	env, err := s.get(ctx, "jeuRecherche.php", q)
	if err != nil {
		return nil, err
	}
	out := make([]Game, 0, len(env.Response.Jeux))
	for i := range env.Response.Jeux {
		g := &env.Response.Jeux[i]
		if g.NotGame == "true" {
			continue
		}
		if game, ok := s.toGame(g); ok {
			out = append(out, game)
		}
	}
	return out, nil
}

// GetByID fetches one game by its ScreenScraper id, optionally scoped to a
// system id.
func (s *SS) GetByID(ctx context.Context, gameID int64, systemID int64) (*Game, error) {
	q := s.auth()
	q.Set("gameid", strconv.FormatInt(gameID, 10))
	if systemID > 0 {
		q.Set("systemeid", strconv.FormatInt(systemID, 10))
	}
	env, err := s.get(ctx, "jeuInfos.php", q)
	if err != nil {
		return nil, err
	}
	if env.Response.Jeu == nil {
		return nil, nil
	}
	if game, ok := s.toGame(env.Response.Jeu); ok {
		return &game, nil
	}
	return nil, nil
}

func (s *SS) get(ctx context.Context, endpoint string, q url.Values) (*ssEnvelope, error) {
	u := s.apiURL + "/" + endpoint + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "RomM/go")
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	// SS answers credential/quota problems with a plain-text body and often a
	// non-200 (403 for a bad login, 429/430 for an exhausted quota).
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ss %s: %s: %s", endpoint, resp.Status, trimErr(body))
	}
	if looksLikeSSError(body) {
		return nil, fmt.Errorf("ss %s: %s", endpoint, trimErr(body))
	}
	var env ssEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("ss %s: decode: %w", endpoint, err)
	}
	return &env, nil
}

// toGame maps an SS game to the common Game, choosing a region-preferred name
// and box-art. Games with no usable id or name are dropped.
func (s *SS) toGame(g *ssGame) (Game, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(g.ID.String()), 10, 64)
	if err != nil || id == 0 {
		return Game{}, false
	}
	name := pickRegional(g.Noms, ssRegionPriority)
	if name == "" {
		return Game{}, false
	}
	return Game{
		Provider:   "ss",
		ProviderID: id,
		Name:       name,
		Summary:    pickRegional(g.Synopsis, ssLanguagePriority),
		CoverURL:   s.pickCover(g.Medias),
	}, true
}

// ssRegionPriority orders the region codes a name/cover is chosen by; anything
// else falls through to the first available entry.
var ssRegionPriority = []string{"wor", "us", "eu", "jp", "ss", "world"}

var ssLanguagePriority = []string{"en", "wor", "us"}

// pickRegional returns the text whose region/langue ranks first in priority,
// else the first non-empty text.
func pickRegional(items []ssRegionalText, priority []string) string {
	key := func(t ssRegionalText) string {
		if t.Region != "" {
			return strings.ToLower(t.Region)
		}
		return strings.ToLower(t.Langue)
	}
	for _, want := range priority {
		for _, it := range items {
			if key(it) == want && strings.TrimSpace(it.Text) != "" {
				return strings.TrimSpace(it.Text)
			}
		}
	}
	for _, it := range items {
		if strings.TrimSpace(it.Text) != "" {
			return strings.TrimSpace(it.Text)
		}
	}
	return ""
}

// pickCover returns the box-2D media URL (region-preferred) with the user
// credentials stripped — the dev credentials left on it are enough to fetch the
// image, and ssid/sspassword must never reach the browser.
func (s *SS) pickCover(medias []ssMedia) string {
	var fallback string
	best := ""
	bestRank := len(ssRegionPriority) + 1
	for _, m := range medias {
		if m.Type != "box-2D" || m.URL == "" {
			continue
		}
		if fallback == "" {
			fallback = m.URL
		}
		rank := len(ssRegionPriority)
		for i, r := range ssRegionPriority {
			if strings.EqualFold(m.Region, r) {
				rank = i
				break
			}
		}
		if rank < bestRank {
			bestRank = rank
			best = m.URL
		}
	}
	if best == "" {
		best = fallback
	}
	return stripSSUserCreds(best)
}

// stripSSUserCreds removes ssid/sspassword from an SS media URL.
func stripSSUserCreds(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	q := u.Query()
	q.Del("ssid")
	q.Del("sspassword")
	u.RawQuery = q.Encode()
	return u.String()
}

func looksLikeSSError(body []byte) bool {
	// A JSON reply starts with '{'; SS error pages are plain text, often
	// beginning with "Erreur" or naming the bad credential.
	for _, b := range body {
		if b == ' ' || b == '\n' || b == '\r' || b == '\t' {
			continue
		}
		return b != '{'
	}
	return true // empty body
}

func trimErr(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
