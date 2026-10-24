// Package oidc implements OpenID Connect login (authorization-code flow),
// ported from the Python backend's OpenIDHandler + the Authlib wiring in
// endpoints/auth.py.
//
// Authlib did discovery, JWKS verification and the code exchange for the Python
// app; this package does them directly against net/http so the Go binary keeps
// its "MariaDB + filesystem only" runtime. The protocol logic — discovery,
// RS256 ID-token verification against the provider JWKS, the token exchange,
// and the claim->user provisioning/role-mapping — is all unit-testable with an
// httptest fake issuer; only the real browser redirect to a live IdP is
// integration, and that is the one seam left to the deployment.
package oidc

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const defaultUsernameAttribute = "preferred_username"
const textFieldLength = 255 // matches backend TEXT_FIELD_LENGTH

// Config mirrors the OIDC_* environment variables.
type Config struct {
	Enabled           bool
	AllowRegistration bool
	ClientID          string
	ClientSecret      string
	RedirectURI       string
	// MetadataURL is OIDC_SERVER_METADATA_URL; if empty it is derived from
	// ProviderURL (OIDC_SERVER_APPLICATION_URL) + /.well-known/openid-configuration.
	MetadataURL       string
	ProviderURL       string
	UsernameAttribute string
	ClaimRoles        string
	RoleAdmin         []string
	RoleEditor        []string
	RoleViewer        []string
	HTTPClient        *http.Client
}

func (c Config) usernameAttr() string {
	if c.UsernameAttribute == "" {
		return defaultUsernameAttribute
	}
	return c.UsernameAttribute
}

func (c Config) metadataURL() string {
	if c.MetadataURL != "" {
		return c.MetadataURL
	}
	if c.ProviderURL == "" {
		return ""
	}
	return strings.TrimRight(c.ProviderURL, "/") + "/.well-known/openid-configuration"
}

// Metadata is the subset of the discovery document we use.
type Metadata struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	UserinfoEndpoint      string   `json:"userinfo_endpoint"`
	EndSessionEndpoint    string   `json:"end_session_endpoint"`
	ClaimsSupported       []string `json:"claims_supported"`
}

// TokenSet is the token endpoint's response.
type TokenSet struct {
	AccessToken string `json:"access_token"`
	IDToken     string `json:"id_token"`
	TokenType   string `json:"token_type"`
}

// Provider performs the OIDC protocol against one configured issuer.
type Provider struct {
	cfg Config
	hc  *http.Client

	mu   sync.Mutex
	meta *Metadata
	keys map[string]*rsa.PublicKey // kid -> key (empty kid allowed)
}

func NewProvider(cfg Config) *Provider {
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	return &Provider{cfg: cfg, hc: hc}
}

// Discover fetches and caches the provider metadata.
func (p *Provider) Discover(ctx context.Context) (*Metadata, error) {
	p.mu.Lock()
	if p.meta != nil {
		m := p.meta
		p.mu.Unlock()
		return m, nil
	}
	p.mu.Unlock()

	u := p.cfg.metadataURL()
	if u == "" {
		return nil, errors.New("oidc: no metadata or provider URL configured")
	}
	var m Metadata
	if err := p.getJSON(ctx, u, "", &m); err != nil {
		return nil, fmt.Errorf("oidc discovery: %w", err)
	}
	if m.Issuer == "" || m.TokenEndpoint == "" || m.JWKSURI == "" {
		return nil, errors.New("oidc: discovery document missing required fields")
	}
	p.mu.Lock()
	p.meta = &m
	p.mu.Unlock()
	return &m, nil
}

// AuthCodeURL builds the authorization redirect URL (scope openid email
// profile), carrying state and nonce.
func (p *Provider) AuthCodeURL(ctx context.Context, state, nonce string) (string, error) {
	m, err := p.Discover(ctx)
	if err != nil {
		return "", err
	}
	q := url.Values{
		"response_type": {"code"},
		"client_id":     {p.cfg.ClientID},
		"redirect_uri":  {p.cfg.RedirectURI},
		"scope":         {"openid email profile"},
		"state":         {state},
		"nonce":         {nonce},
	}
	sep := "?"
	if strings.Contains(m.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return m.AuthorizationEndpoint + sep + q.Encode(), nil
}

// Exchange trades an authorization code for tokens.
func (p *Provider) Exchange(ctx context.Context, code string) (*TokenSet, error) {
	m, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {p.cfg.RedirectURI},
		"client_id":     {p.cfg.ClientID},
		"client_secret": {p.cfg.ClientSecret},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := p.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oidc token endpoint: status %d", resp.StatusCode)
	}
	var ts TokenSet
	if err := json.NewDecoder(resp.Body).Decode(&ts); err != nil {
		return nil, err
	}
	if ts.IDToken == "" {
		return nil, errors.New("oidc: token response has no id_token")
	}
	return &ts, nil
}

// VerifyIDToken validates the ID token's RS256 signature against the provider
// JWKS and checks issuer, audience, expiry, and (when supplied) nonce. It
// returns the token's claims.
func (p *Provider) VerifyIDToken(ctx context.Context, rawIDToken, nonce string) (Claims, error) {
	m, err := p.Discover(ctx)
	if err != nil {
		return nil, err
	}
	if err := p.ensureKeys(ctx, m.JWKSURI); err != nil {
		return nil, err
	}

	keyfunc := func(t *jwt.Token) (any, error) {
		kid, _ := t.Header["kid"].(string)
		p.mu.Lock()
		defer p.mu.Unlock()
		if k, ok := p.keys[kid]; ok {
			return k, nil
		}
		// Single-key providers often omit kid; fall back to the only key.
		if len(p.keys) == 1 {
			for _, k := range p.keys {
				return k, nil
			}
		}
		return nil, fmt.Errorf("oidc: no JWKS key for kid %q", kid)
	}

	parsed, err := jwt.Parse(rawIDToken, keyfunc,
		jwt.WithValidMethods([]string{"RS256", "RS384", "RS512"}),
		jwt.WithIssuer(m.Issuer),
		jwt.WithAudience(p.cfg.ClientID),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("oidc: id token invalid: %w", err)
	}
	mc, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("oidc: unexpected claims type")
	}
	claims := Claims(mc)
	if nonce != "" {
		if got, _ := mc["nonce"].(string); got != nonce {
			return nil, errors.New("oidc: nonce mismatch")
		}
	}
	return claims, nil
}

// FetchUserinfo pulls the userinfo endpoint and merges its claims over base
// (used when the ID token lacks email). Returns base unchanged if there is no
// userinfo endpoint.
func (p *Provider) FetchUserinfo(ctx context.Context, accessToken string, base Claims) (Claims, error) {
	m, err := p.Discover(ctx)
	if err != nil {
		return base, err
	}
	if m.UserinfoEndpoint == "" || accessToken == "" {
		return base, nil
	}
	var extra map[string]any
	if err := p.getJSON(ctx, m.UserinfoEndpoint, accessToken, &extra); err != nil {
		return base, err
	}
	for k, v := range extra {
		if _, exists := base[k]; !exists {
			base[k] = v
		}
	}
	return base, nil
}

func (p *Provider) ensureKeys(ctx context.Context, jwksURI string) error {
	p.mu.Lock()
	have := p.keys != nil
	p.mu.Unlock()
	if have {
		return nil
	}
	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
			Use string `json:"use"`
		} `json:"keys"`
	}
	if err := p.getJSON(ctx, jwksURI, "", &doc); err != nil {
		return fmt.Errorf("oidc jwks: %w", err)
	}
	keys := map[string]*rsa.PublicKey{}
	for _, k := range doc.Keys {
		if k.Kty != "RSA" {
			continue // RSA is what joserfc signs ID tokens with in practice here
		}
		pub, err := rsaKey(k.N, k.E)
		if err != nil {
			return err
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return errors.New("oidc: JWKS has no usable RSA keys")
	}
	p.mu.Lock()
	p.keys = keys
	p.mu.Unlock()
	return nil
}

func rsaKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, fmt.Errorf("oidc: bad JWKS modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, fmt.Errorf("oidc: bad JWKS exponent: %w", err)
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		e = 65537
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}

func (p *Provider) getJSON(ctx context.Context, u, bearer string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := p.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %d", u, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// --- claims helpers ---

// Claims is a decoded set of OIDC claims.
type Claims map[string]any

func (c Claims) str(key string) string {
	s, _ := c[key].(string)
	return s
}

func (c Claims) Email() string { return c.str("email") }
func (c Claims) Issuer() string { return c.str("iss") }
func (c Claims) Subject() string { return c.str("sub") }

// EmailVerified returns the tri-state email_verified claim: nil when absent.
func (c Claims) EmailVerified() *bool {
	v, ok := c["email_verified"]
	if !ok {
		return nil
	}
	b, ok := v.(bool)
	if !ok {
		return nil
	}
	return &b
}

// roleSet returns the role claim as a set of strings (handles single string or
// list).
func (c Claims) roleSet(claimName string) (map[string]struct{}, bool) {
	v, ok := c[claimName]
	if !ok || v == nil {
		return nil, false
	}
	out := map[string]struct{}{}
	switch t := v.(type) {
	case string:
		out[t] = struct{}{}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				out[s] = struct{}{}
			}
		}
	case []string:
		for _, s := range t {
			out[s] = struct{}{}
		}
	default:
		return nil, false
	}
	return out, true
}

var usernameSanitizeRE = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// sanitizeUsername coerces a provider-supplied username into one RomM accepts.
// Mirrors backend utils.validation.sanitize_username.
func sanitizeUsername(username, fallback string) string {
	clean := func(s string) string {
		s = usernameSanitizeRE.ReplaceAllString(s, "-")
		s = strings.Trim(s, "-")
		if len(s) > textFieldLength {
			s = s[:textFieldLength]
		}
		return s
	}
	out := clean(username)
	if len(out) < 3 {
		out = clean(fallback)
	}
	if len(out) < 3 {
		out = "user"
	}
	return out
}
