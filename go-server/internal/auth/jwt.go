// Package auth verifies the access tokens the Python RomM backend issues, so
// the Go server accepts the exact same bearer tokens. They are HS256 JWTs
// signed with ROMM_AUTH_SECRET_KEY, carrying: sub (username), iss="romm:oauth",
// type="access", scopes (space-joined string), exp.
package auth

import (
	"errors"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalid = errors.New("invalid or expired token")

type Claims struct {
	Username string
	Scopes   []string
}

// HasScope reports whether the token carries the given OAuth scope.
func (c *Claims) HasScope(scope string) bool {
	for _, s := range c.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Verify parses and validates an access token against the shared secret.
// Signature, algorithm (HS256 only), and expiry are all checked; issuer and
// token type must match the Python backend's access tokens.
func Verify(tokenString, secret string) (*Claims, error) {
	if secret == "" || tokenString == "" {
		return nil, ErrInvalid
	}
	tok, err := jwt.Parse(
		tokenString,
		func(t *jwt.Token) (any, error) { return []byte(secret), nil },
		jwt.WithValidMethods([]string{"HS256"}),
	)
	if err != nil || !tok.Valid {
		return nil, ErrInvalid
	}
	mc, ok := tok.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrInvalid
	}
	if iss, _ := mc["iss"].(string); iss != "romm:oauth" {
		return nil, ErrInvalid
	}
	if typ, _ := mc["type"].(string); typ != "access" {
		return nil, ErrInvalid
	}
	sub, _ := mc["sub"].(string)
	if sub == "" {
		return nil, ErrInvalid
	}
	var scopes []string
	if sc, _ := mc["scopes"].(string); sc != "" {
		scopes = strings.Fields(sc)
	}
	return &Claims{Username: sub, Scopes: scopes}, nil
}
