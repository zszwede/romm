package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// fakeIssuer is an httptest OIDC provider: discovery, JWKS, and a token
// endpoint that returns a signed ID token for a preset claim set.
type fakeIssuer struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	kid    string
	claims jwt.MapClaims // claims the token endpoint will sign
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	fi := &fakeIssuer{key: key, kid: "test-kid-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 base,
			"authorization_endpoint": base + "/authorize",
			"token_endpoint":         base + "/token",
			"jwks_uri":               base + "/jwks",
			"userinfo_endpoint":      base + "/userinfo",
			"end_session_endpoint":   base + "/logout",
			"claims_supported":       []string{"sub", "email", "email_verified"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		pub := fi.key.PublicKey
		eb := big2bytes(pub.E)
		json.NewEncoder(w).Encode(map[string]any{
			"keys": []map[string]any{{
				"kty": "RSA", "use": "sig", "kid": fi.kid,
				"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
				"e": base64.RawURLEncoding.EncodeToString(eb),
			}},
		})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "authorization_code" || r.FormValue("code") == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		idtok := fi.signID(t, fi.claims)
		json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fake-access", "token_type": "Bearer", "id_token": idtok,
		})
	})
	fi.srv = httptest.NewServer(mux)
	t.Cleanup(fi.srv.Close)
	return fi
}

func (fi *fakeIssuer) signID(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()
	c := jwt.MapClaims{}
	for k, v := range claims {
		c[k] = v
	}
	if c["iss"] == nil {
		c["iss"] = fi.srv.URL
	}
	if c["exp"] == nil {
		c["exp"] = time.Now().Add(time.Hour).Unix()
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	tok.Header["kid"] = fi.kid
	s, err := tok.SignedString(fi.key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func big2bytes(e int) []byte {
	var b []byte
	for e > 0 {
		b = append([]byte{byte(e & 0xff)}, b...)
		e >>= 8
	}
	return b
}

func testProvider(fi *fakeIssuer) *Provider {
	return NewProvider(Config{
		Enabled:           true,
		AllowRegistration: true,
		ClientID:          "romm-client",
		ClientSecret:      "shh",
		RedirectURI:       "https://romm.local/api/auth/oauth/openid",
		MetadataURL:       fi.srv.URL + "/.well-known/openid-configuration",
		UsernameAttribute: "preferred_username",
	})
}

func TestDiscoverAndAuthCodeURL(t *testing.T) {
	fi := newFakeIssuer(t)
	p := testProvider(fi)
	m, err := p.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if m.TokenEndpoint == "" || m.JWKSURI == "" || m.Issuer != fi.srv.URL {
		t.Fatalf("bad metadata: %+v", m)
	}
	u, err := p.AuthCodeURL(context.Background(), "state123", "nonce456")
	if err != nil {
		t.Fatal(err)
	}
	if !contains(u, "client_id=romm-client") || !contains(u, "state=state123") ||
		!contains(u, "nonce=nonce456") || !contains(u, "response_type=code") {
		t.Fatalf("auth url missing params: %s", u)
	}
}

func TestExchangeAndVerify(t *testing.T) {
	fi := newFakeIssuer(t)
	fi.claims = jwt.MapClaims{
		"sub": "abc", "email": "a@b.com", "email_verified": true,
		"preferred_username": "alice", "aud": "romm-client", "nonce": "N1",
	}
	p := testProvider(fi)

	ts, err := p.Exchange(context.Background(), "the-code")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	claims, err := p.VerifyIDToken(context.Background(), ts.IDToken, "N1")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Email() != "a@b.com" || claims.Subject() != "abc" {
		t.Fatalf("claims: %+v", claims)
	}
}

func TestVerify_RejectsBadAudExpNonceSignature(t *testing.T) {
	fi := newFakeIssuer(t)
	p := testProvider(fi)

	// wrong audience
	bad := fi.signID(t, jwt.MapClaims{"sub": "x", "aud": "someone-else"})
	if _, err := p.VerifyIDToken(context.Background(), bad, ""); err == nil {
		t.Error("accepted wrong audience")
	}
	// expired
	exp := fi.signID(t, jwt.MapClaims{"sub": "x", "aud": "romm-client", "exp": time.Now().Add(-time.Minute).Unix()})
	if _, err := p.VerifyIDToken(context.Background(), exp, ""); err == nil {
		t.Error("accepted expired token")
	}
	// nonce mismatch
	good := fi.signID(t, jwt.MapClaims{"sub": "x", "aud": "romm-client", "nonce": "A"})
	if _, err := p.VerifyIDToken(context.Background(), good, "B"); err == nil {
		t.Error("accepted nonce mismatch")
	}
	// signed by a different key
	other, _ := rsa.GenerateKey(rand.Reader, 2048)
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"sub": "x", "aud": "romm-client", "iss": fi.srv.URL, "exp": time.Now().Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = fi.kid
	forged, _ := tok.SignedString(other)
	if _, err := p.VerifyIDToken(context.Background(), forged, ""); err == nil {
		t.Error("accepted token signed by a foreign key")
	}
}

func TestVerify_RejectsNoneAlg(t *testing.T) {
	fi := newFakeIssuer(t)
	p := testProvider(fi)
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"sub": "x", "aud": "romm-client", "iss": fi.srv.URL, "exp": time.Now().Add(time.Hour).Unix(),
	})
	s, _ := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := p.VerifyIDToken(context.Background(), s, ""); err == nil {
		t.Error("accepted alg=none id token")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
