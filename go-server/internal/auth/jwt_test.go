package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret-key-0123456789"

func mint(t *testing.T, secret string, claims jwt.MapClaims, method jwt.SigningMethod) string {
	t.Helper()
	tok := jwt.NewWithClaims(method, claims)
	s, err := tok.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return s
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub":    "xiam",
		"iss":    "romm:oauth",
		"type":   "access",
		"scopes": "roms.read roms.write",
		"exp":    time.Now().Add(time.Hour).Unix(),
	}
}

func TestVerify_Valid(t *testing.T) {
	tok := mint(t, testSecret, validClaims(), jwt.SigningMethodHS256)
	c, err := Verify(tok, testSecret)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Username != "xiam" {
		t.Errorf("username = %q, want xiam", c.Username)
	}
	if !c.HasScope("roms.write") || !c.HasScope("roms.read") {
		t.Errorf("scopes = %v, want roms.read+roms.write", c.Scopes)
	}
	if c.HasScope("roms.delete") {
		t.Errorf("unexpected scope roms.delete")
	}
}

func TestVerify_Rejects(t *testing.T) {
	cases := map[string]func() string{
		"wrong secret": func() string {
			return mint(t, "other-secret", validClaims(), jwt.SigningMethodHS256)
		},
		"expired": func() string {
			c := validClaims()
			c["exp"] = time.Now().Add(-time.Minute).Unix()
			return mint(t, testSecret, c, jwt.SigningMethodHS256)
		},
		"wrong issuer": func() string {
			c := validClaims()
			c["iss"] = "someone-else"
			return mint(t, testSecret, c, jwt.SigningMethodHS256)
		},
		"wrong type (refresh)": func() string {
			c := validClaims()
			c["type"] = "refresh"
			return mint(t, testSecret, c, jwt.SigningMethodHS256)
		},
		"missing sub": func() string {
			c := validClaims()
			delete(c, "sub")
			return mint(t, testSecret, c, jwt.SigningMethodHS256)
		},
		"garbage": func() string { return "not.a.jwt" },
	}
	for name, make := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Verify(make(), testSecret); err == nil {
				t.Errorf("%s: expected rejection, got nil error", name)
			}
		})
	}
}

func TestVerify_RejectsNoneAlg(t *testing.T) {
	// A token signed with "none" must never be accepted.
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
	s, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign none: %v", err)
	}
	if _, err := Verify(s, testSecret); err == nil {
		t.Error("expected rejection of alg=none token")
	}
}

func TestVerify_EmptyInputs(t *testing.T) {
	if _, err := Verify("", testSecret); err == nil {
		t.Error("empty token should fail")
	}
	if _, err := Verify(mint(t, testSecret, validClaims(), jwt.SigningMethodHS256), ""); err == nil {
		t.Error("empty secret should fail")
	}
}
