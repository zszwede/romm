package oidc

import (
	"context"
	"errors"
	"strings"
)

// Role values match backend models.user.Role.
const (
	RoleUser  = "user"
	RoleAdmin = "admin"
)

// Typed provisioning errors; the HTTP layer maps them to status codes
// identically to the Python endpoint.
var (
	ErrEmailMissing         = errors.New("email is missing from token")
	ErrEmailUnverified      = errors.New("email is not verified")
	ErrUsernameMissing      = errors.New("username attribute is missing from token")
	ErrNoRoles              = errors.New("user has not been granted any roles for this application")
	ErrRegistrationDisabled = errors.New("user registration is disabled")
	ErrIdentityMismatch     = errors.New("account is linked to a different identity at the provider")
	ErrUsernameTaken        = errors.New("username is already taken")
	ErrUserDisabled         = errors.New("user is disabled")
)

// User is the slice of a RomM user the provisioning logic reads and writes.
type User struct {
	ID         int64
	Username   string
	Email      string
	Role       string
	OIDCIssuer string
	OIDCSub    string
	Enabled    bool
}

// NewUser is a user to create.
type NewUser struct {
	Username   string
	Email      string
	Role       string
	OIDCIssuer string
	OIDCSub    string
	Enabled    bool
}

// UserUpdates are the fields provisioning may change on an existing user.
// A nil pointer means "leave unchanged".
type UserUpdates struct {
	Email      *string
	Role       *string
	OIDCIssuer *string
	OIDCSub    *string
}

// UserStore is the persistence the provisioning logic needs. Implemented by
// internal/store against MariaDB, and by a fake in tests.
type UserStore interface {
	GetUserByOIDCIdentity(ctx context.Context, issuer, sub string) (*User, error)
	GetUserByEmail(ctx context.Context, email string) (*User, error)
	GetUserByUsername(ctx context.Context, username string) (*User, error)
	AddUser(ctx context.Context, u NewUser) (*User, error)
	UpdateUser(ctx context.Context, id int64, up UserUpdates) (*User, error)
}

// MapUser turns verified OIDC claims into a RomM user, provisioning or linking
// as configured. Ported 1:1 from OpenIDHandler.get_current_active_user_from_
// openid_token. meta provides claims_supported for the email_verified gate.
func (p *Provider) MapUser(ctx context.Context, store UserStore, meta *Metadata, claims Claims) (*User, error) {
	email := claims.Email()
	if email == "" {
		return nil, ErrEmailMissing
	}

	// Fail if email is explicitly unverified, or email_verified is an advertised
	// claim and not explicitly true.
	ev := claims.EmailVerified()
	claimsSupportedHasEV := containsString(meta.ClaimsSupported, "email_verified")
	if (ev != nil && !*ev) || (claimsSupportedHasEV && (ev == nil || !*ev)) {
		return nil, ErrEmailUnverified
	}

	preferredUsername := claims.str(p.cfg.usernameAttr())
	if preferredUsername == "" {
		return nil, ErrUsernameMissing
	}

	role := RoleUser
	claimsProvided := p.cfg.ClaimRoles != ""
	if claimsProvided {
		roles, ok := claims.roleSet(p.cfg.ClaimRoles)
		if !ok {
			claimsProvided = false // claim absent -> default role, like Python's `in userinfo` guard
		} else {
			switch {
			case intersects(p.cfg.RoleAdmin, roles):
				role = RoleAdmin
			case intersects(p.cfg.RoleEditor, roles) || intersects(p.cfg.RoleViewer, roles) || containsString(p.cfg.RoleViewer, "*"):
				role = RoleUser
			default:
				return nil, ErrNoRoles
			}
		}
	}

	issuer := claims.Issuer()
	sub := claims.Subject()
	haveIdentity := issuer != "" && sub != ""

	var user *User
	var err error
	if haveIdentity {
		if user, err = store.GetUserByOIDCIdentity(ctx, issuer, sub); err != nil {
			return nil, err
		}
	}
	matchedByIdentity := user != nil

	if user == nil {
		if user, err = store.GetUserByEmail(ctx, email); err != nil {
			return nil, err
		}
		// The provider now vouches for a different subject under this email:
		// linking would hand the account to someone else.
		if user != nil && haveIdentity && user.OIDCIssuer == issuer && user.OIDCSub != "" && user.OIDCSub != sub {
			return nil, ErrIdentityMismatch
		}
	}

	if user == nil {
		if !p.cfg.AllowRegistration {
			return nil, ErrRegistrationDisabled
		}
		username, err := p.rommUsername(ctx, store, preferredUsername, emailStem(email))
		if err != nil {
			return nil, err
		}
		nu := NewUser{Username: username, Email: email, Role: role, Enabled: true}
		if haveIdentity {
			nu.OIDCIssuer, nu.OIDCSub = issuer, sub
		}
		return store.AddUser(ctx, nu)
	}

	if !user.Enabled {
		return nil, ErrUserDisabled
	}

	up := UserUpdates{}
	if haveIdentity && (user.OIDCIssuer != issuer || user.OIDCSub != sub) {
		iss, s := issuer, sub
		up.OIDCIssuer, up.OIDCSub = &iss, &s
	}
	if matchedByIdentity && !strings.EqualFold(user.Email, email) {
		if other, err := store.GetUserByEmail(ctx, email); err != nil {
			return nil, err
		} else if other == nil {
			e := email
			up.Email = &e
		}
	}
	if claimsProvided && user.Role != role {
		r := role
		up.Role = &r
	}

	if up.Email != nil || up.Role != nil || up.OIDCIssuer != nil || up.OIDCSub != nil {
		return store.UpdateUser(ctx, user.ID, up)
	}
	return user, nil
}

// rommUsername returns a valid, unused RomM username for a provider name.
func (p *Provider) rommUsername(ctx context.Context, store UserStore, provided, fallback string) (string, error) {
	username := sanitizeUsername(provided, fallback)
	existing, err := store.GetUserByUsername(ctx, username)
	if err != nil {
		return "", err
	}
	if existing != nil {
		return "", ErrUsernameTaken
	}
	return username, nil
}

func emailStem(email string) string {
	if i := strings.IndexByte(email, '@'); i > 0 {
		return email[:i]
	}
	return email
}

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func intersects(list []string, set map[string]struct{}) bool {
	for _, s := range list {
		if _, ok := set[s]; ok {
			return true
		}
	}
	return false
}
