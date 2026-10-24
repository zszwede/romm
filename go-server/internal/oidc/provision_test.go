package oidc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// memStore is an in-memory UserStore for provisioning tests.
type memStore struct {
	byID    map[int64]*User
	nextID  int64
	added   []NewUser
	updated map[int64]UserUpdates
}

func newMemStore(seed ...*User) *memStore {
	m := &memStore{byID: map[int64]*User{}, nextID: 1, updated: map[int64]UserUpdates{}}
	for _, u := range seed {
		if u.ID == 0 {
			u.ID = m.nextID
		}
		if u.ID >= m.nextID {
			m.nextID = u.ID + 1
		}
		cp := *u
		m.byID[u.ID] = &cp
	}
	return m
}

func (m *memStore) GetUserByOIDCIdentity(_ context.Context, issuer, sub string) (*User, error) {
	for _, u := range m.byID {
		if u.OIDCIssuer == issuer && u.OIDCSub == sub && issuer != "" && sub != "" {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *memStore) GetUserByEmail(_ context.Context, email string) (*User, error) {
	for _, u := range m.byID {
		if strings.EqualFold(u.Email, email) {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *memStore) GetUserByUsername(_ context.Context, username string) (*User, error) {
	for _, u := range m.byID {
		if u.Username == username {
			cp := *u
			return &cp, nil
		}
	}
	return nil, nil
}
func (m *memStore) AddUser(_ context.Context, nu NewUser) (*User, error) {
	m.added = append(m.added, nu)
	id := m.nextID
	m.nextID++
	u := &User{ID: id, Username: nu.Username, Email: nu.Email, Role: nu.Role,
		OIDCIssuer: nu.OIDCIssuer, OIDCSub: nu.OIDCSub, Enabled: nu.Enabled}
	m.byID[id] = u
	cp := *u
	return &cp, nil
}
func (m *memStore) UpdateUser(_ context.Context, id int64, up UserUpdates) (*User, error) {
	m.updated[id] = up
	u := m.byID[id]
	if up.Email != nil {
		u.Email = *up.Email
	}
	if up.Role != nil {
		u.Role = *up.Role
	}
	if up.OIDCIssuer != nil {
		u.OIDCIssuer = *up.OIDCIssuer
	}
	if up.OIDCSub != nil {
		u.OIDCSub = *up.OIDCSub
	}
	cp := *u
	return &cp, nil
}

func provider(cfg Config) *Provider {
	cfg.Enabled = true
	if cfg.UsernameAttribute == "" {
		cfg.UsernameAttribute = "preferred_username"
	}
	return NewProvider(cfg)
}

var noClaimsSupported = &Metadata{} // email_verified not advertised

func claims(kv map[string]any) Claims { return Claims(kv) }

func TestMapUser_NewRegistration(t *testing.T) {
	st := newMemStore()
	p := provider(Config{AllowRegistration: true})
	u, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "jane.doe@example.com", "sub": "s1", "iss": "https://idp",
		"preferred_username": "jane.doe",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.added) != 1 {
		t.Fatalf("expected 1 user created, got %d", len(st.added))
	}
	// Username sanitized ('.' -> '-'); identity recorded.
	if u.Username != "jane-doe" {
		t.Errorf("username = %q, want jane-doe", u.Username)
	}
	if u.OIDCIssuer != "https://idp" || u.OIDCSub != "s1" || u.Role != RoleUser || !u.Enabled {
		t.Errorf("new user wrong: %+v", u)
	}
}

func TestMapUser_RegistrationDisabled(t *testing.T) {
	st := newMemStore()
	p := provider(Config{AllowRegistration: false})
	_, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "new@x.com", "sub": "s", "iss": "i", "preferred_username": "new",
	}))
	if !errors.Is(err, ErrRegistrationDisabled) {
		t.Fatalf("err = %v, want ErrRegistrationDisabled", err)
	}
}

func TestMapUser_MatchByIdentityUpdatesEmailAndRole(t *testing.T) {
	st := newMemStore(&User{ID: 5, Username: "bob", Email: "old@x.com", Role: RoleUser,
		OIDCIssuer: "https://idp", OIDCSub: "sub-bob", Enabled: true})
	p := provider(Config{
		AllowRegistration: false, ClaimRoles: "groups",
		RoleAdmin: []string{"romm-admins"},
	})
	u, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "new@x.com", "sub": "sub-bob", "iss": "https://idp",
		"preferred_username": "bob", "groups": []any{"romm-admins"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if u.Email != "new@x.com" {
		t.Errorf("email not updated: %q", u.Email)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role not promoted: %q", u.Role)
	}
}

func TestMapUser_LinkByEmailSetsIdentity(t *testing.T) {
	st := newMemStore(&User{ID: 9, Username: "carol", Email: "carol@x.com", Role: RoleUser, Enabled: true})
	p := provider(Config{AllowRegistration: false})
	u, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "carol@x.com", "sub": "new-sub", "iss": "https://idp", "preferred_username": "carol",
	}))
	if err != nil {
		t.Fatal(err)
	}
	up, ok := st.updated[9]
	if !ok || up.OIDCIssuer == nil || *up.OIDCIssuer != "https://idp" || up.OIDCSub == nil || *up.OIDCSub != "new-sub" {
		t.Fatalf("identity not linked: %+v", up)
	}
	_ = u
}

func TestMapUser_IdentityReassignmentRejected(t *testing.T) {
	// User already linked to (idp, sub-A); provider now presents sub-B for the
	// same email under the same issuer -> refuse to hand the account over.
	st := newMemStore(&User{ID: 1, Username: "d", Email: "d@x.com", Role: RoleUser,
		OIDCIssuer: "https://idp", OIDCSub: "sub-A", Enabled: true})
	p := provider(Config{AllowRegistration: true})
	_, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "d@x.com", "sub": "sub-B", "iss": "https://idp", "preferred_username": "d",
	}))
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("err = %v, want ErrIdentityMismatch", err)
	}
}

func TestMapUser_EmailVerifiedGate(t *testing.T) {
	p := provider(Config{AllowRegistration: true})
	meta := &Metadata{ClaimsSupported: []string{"email", "email_verified"}}

	// explicitly false -> reject
	if _, err := p.MapUser(context.Background(), newMemStore(), meta, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i", "preferred_username": "x", "email_verified": false,
	})); !errors.Is(err, ErrEmailUnverified) {
		t.Errorf("explicit false: err = %v", err)
	}
	// advertised but absent -> reject
	if _, err := p.MapUser(context.Background(), newMemStore(), meta, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i", "preferred_username": "x",
	})); !errors.Is(err, ErrEmailUnverified) {
		t.Errorf("advertised+absent: err = %v", err)
	}
	// advertised and true -> ok
	if _, err := p.MapUser(context.Background(), newMemStore(), meta, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i", "preferred_username": "x", "email_verified": true,
	})); err != nil {
		t.Errorf("advertised+true: err = %v", err)
	}
	// not advertised and absent -> ok (provider doesn't support the claim)
	if _, err := p.MapUser(context.Background(), newMemStore(), noClaimsSupported, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i", "preferred_username": "x",
	})); err != nil {
		t.Errorf("not advertised+absent: err = %v", err)
	}
}

func TestMapUser_NoMatchingRoleRejected(t *testing.T) {
	p := provider(Config{
		AllowRegistration: true, ClaimRoles: "groups",
		RoleAdmin: []string{"admins"}, RoleViewer: []string{"viewers"},
	})
	_, err := p.MapUser(context.Background(), newMemStore(), noClaimsSupported, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i", "preferred_username": "x",
		"groups": []any{"some-other-group"},
	}))
	if !errors.Is(err, ErrNoRoles) {
		t.Fatalf("err = %v, want ErrNoRoles", err)
	}
}

func TestMapUser_WildcardViewerGrants(t *testing.T) {
	p := provider(Config{
		AllowRegistration: true, ClaimRoles: "groups", RoleViewer: []string{"*"},
	})
	u, err := p.MapUser(context.Background(), newMemStore(), noClaimsSupported, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i", "preferred_username": "x",
		"groups": []any{"anything"},
	}))
	if err != nil || u.Role != RoleUser {
		t.Fatalf("wildcard viewer: u=%+v err=%v", u, err)
	}
}

func TestMapUser_MissingEmailAndUsername(t *testing.T) {
	p := provider(Config{AllowRegistration: true})
	if _, err := p.MapUser(context.Background(), newMemStore(), noClaimsSupported, claims(map[string]any{
		"sub": "s", "iss": "i", "preferred_username": "x",
	})); !errors.Is(err, ErrEmailMissing) {
		t.Errorf("missing email: %v", err)
	}
	if _, err := p.MapUser(context.Background(), newMemStore(), noClaimsSupported, claims(map[string]any{
		"email": "x@y.com", "sub": "s", "iss": "i",
	})); !errors.Is(err, ErrUsernameMissing) {
		t.Errorf("missing username: %v", err)
	}
}

func TestMapUser_UsernameTaken(t *testing.T) {
	st := newMemStore(&User{ID: 1, Username: "taken", Email: "other@x.com", Enabled: true})
	p := provider(Config{AllowRegistration: true})
	_, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "fresh@x.com", "sub": "s", "iss": "i", "preferred_username": "taken",
	}))
	if !errors.Is(err, ErrUsernameTaken) {
		t.Fatalf("err = %v, want ErrUsernameTaken", err)
	}
}

func TestMapUser_DisabledUserRejected(t *testing.T) {
	st := newMemStore(&User{ID: 1, Username: "z", Email: "z@x.com", OIDCIssuer: "i", OIDCSub: "s", Enabled: false})
	p := provider(Config{AllowRegistration: false})
	_, err := p.MapUser(context.Background(), st, noClaimsSupported, claims(map[string]any{
		"email": "z@x.com", "sub": "s", "iss": "i", "preferred_username": "z",
	}))
	if !errors.Is(err, ErrUserDisabled) {
		t.Fatalf("err = %v, want ErrUserDisabled", err)
	}
}

func TestSanitizeUsername(t *testing.T) {
	cases := map[string]string{
		"jane.doe":      "jane-doe",
		"a@b.com":       "a-b-com",
		"ok_name-1":     "ok_name-1",
		"аб":            "fallback", // non-ASCII collapses below the 3-char floor -> fallback
	}
	for in, want := range cases {
		if got := sanitizeUsername(in, "fallback"); got != want {
			t.Errorf("sanitizeUsername(%q) = %q, want %q", in, got, want)
		}
	}
	if got := sanitizeUsername("!!", "stem"); got != "stem" {
		t.Errorf("fallback not used: %q", got)
	}
}
