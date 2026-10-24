// Package deviceauth implements the RFC 8628-style device authorization flow
// (a client polls while the user approves the pairing out-of-band in the web
// UI), ported from the Python backend's utils/device_auth.py.
//
// The Python version kept all flow state in Redis with per-key TTLs. The Go
// rewrite drops Redis (see the roadmap), so the same state lives in an
// in-process, mutex-guarded store with lazy TTL expiry. The clock is injectable
// so the whole state machine — pending→approved/denied, one-shot consume, TTL
// expiry, and poll pacing — is deterministically unit-testable without a live
// IdP, a real device, or a running Redis.
package deviceauth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"math/big"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Constants mirror backend/utils/device_auth.py and client_tokens.py exactly so
// the two backends behave identically during the migration.
const (
	DeviceCodeBytes = 32 // -> 64 hex chars
	UserCodeLength  = 8

	PendingTTL          = 600 * time.Second // 10-minute hard ceiling
	DeniedTTL           = 60 * time.Second   // shrink window after explicit deny
	PollDefaultInterval = 5                  // seconds

	// PairAlphabet omits I/L/O/0/1 to avoid ambiguous glyphs in typed codes.
	PairAlphabet       = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
	ClientTokenPrefix  = "rmm_"
	clientTokenRawLen  = 32 // hex-encoded -> 64 chars after the prefix
)

// Status is a flow's lifecycle state.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
)

// Pending is the metadata captured when a device starts a flow.
type Pending struct {
	ClientDeviceIdentifier string
	Name                   string
	Client                 string
	Platform               string
	ClientVersion          string
	RequestedScopes        []string
	Interval               int
	UserCode               string
}

// Approved is the payload handed back to the device on its next poll after the
// user approves. It is read exactly once (ConsumeApproved).
type Approved struct {
	RawToken  string
	DeviceID  string
	Scopes    []string
	ExpiresAt *time.Time
}

// flow is one in-flight (or just-resolved) authorization request.
type flow struct {
	status   Status
	pending  Pending  // populated while status == pending
	approved Approved // populated while status == approved
	expiry   time.Time
}

// Store holds all flow state. Safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	byDevice map[string]*flow
	byUser   map[string]string // user_code -> device_code
	pollLast map[string]time.Time
	now      func() time.Time
}

// NewStore returns an empty store using the wall clock.
func NewStore() *Store { return newStoreWithClock(time.Now) }

func newStoreWithClock(now func() time.Time) *Store {
	return &Store{
		byDevice: map[string]*flow{},
		byUser:   map[string]string{},
		pollLast: map[string]time.Time{},
		now:      now,
	}
}

// NormalizeUserCode strips separators and uppercases a user-typed pairing code.
func NormalizeUserCode(code string) string {
	r := strings.NewReplacer("-", "", " ", "")
	return strings.ToUpper(r.Replace(code))
}

// GenerateDeviceCode returns a 64-char hex secret the device polls with.
func GenerateDeviceCode() string {
	b := make([]byte, DeviceCodeBytes)
	if _, err := rand.Read(b); err != nil {
		panic("deviceauth: crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

// GenerateUserCode returns a short code from the unambiguous alphabet.
func GenerateUserCode() string {
	var sb strings.Builder
	sb.Grow(UserCodeLength)
	max := big.NewInt(int64(len(PairAlphabet)))
	for i := 0; i < UserCodeLength; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic("deviceauth: crypto/rand failed: " + err.Error())
		}
		sb.WriteByte(PairAlphabet[n.Int64()])
	}
	return sb.String()
}

// GenerateClientToken mints a raw bearer token (rmm_ + 64 hex chars). Matches
// AuthHandler.generate_client_token.
func GenerateClientToken() string {
	b := make([]byte, clientTokenRawLen)
	if _, err := rand.Read(b); err != nil {
		panic("deviceauth: crypto/rand failed: " + err.Error())
	}
	return ClientTokenPrefix + hex.EncodeToString(b)
}

// HashClientToken is the at-rest hash of a raw client token (sha256 hex).
// Matches AuthHandler.hash_client_token.
func HashClientToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// BuildVerificationPaths returns the relative web-UI approval paths. The server
// stays origin-agnostic; the client joins these with the origin it reaches.
func BuildVerificationPaths(userCode string) (path, pathComplete string) {
	path = "/pair/device"
	q := url.Values{"user_code": {userCode}}
	return path, path + "?" + q.Encode()
}

// --- flow lifecycle (replaces the Redis-backed helpers) ---

// StorePending records a new pending flow and its user_code index.
func (s *Store) StorePending(deviceCode, userCode string, p Pending) {
	p.UserCode = userCode
	if p.Interval == 0 {
		p.Interval = PollDefaultInterval
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.evictLocked(now)
	s.byDevice[deviceCode] = &flow{status: StatusPending, pending: p, expiry: now.Add(PendingTTL)}
	s.byUser[userCode] = deviceCode
}

// LoadPending returns a flow by device_code (any status), or ok=false if it is
// unknown or expired.
func (s *Store) LoadPending(deviceCode string) (Pending, Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.liveLocked(deviceCode)
	if f == nil {
		return Pending{}, "", false
	}
	return f.pending, f.status, true
}

// ResolveDeviceCodeFromUserCode maps a user_code to its device_code.
func (s *Store) ResolveDeviceCodeFromUserCode(userCode string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evictLocked(s.now())
	dc, ok := s.byUser[userCode]
	if !ok {
		return "", false
	}
	// The device_code entry may have expired even if the index lingered.
	if s.liveLocked(dc) == nil {
		delete(s.byUser, userCode)
		return "", false
	}
	return dc, true
}

// PendingExpiresAt returns the flow's deadline (zero time if gone).
func (s *Store) PendingExpiresAt(deviceCode string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f := s.liveLocked(deviceCode); f != nil {
		return f.expiry
	}
	return time.Time{}
}

// MarkApproved stores the approved payload and retires the user_code. No-op if
// the flow is gone.
func (s *Store) MarkApproved(deviceCode string, a Approved) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.liveLocked(deviceCode)
	if f == nil {
		return
	}
	// Keep whatever remains of the pending TTL (capped), matching Python.
	remaining := f.expiry.Sub(s.now())
	if remaining < 1*time.Second || remaining > PendingTTL {
		remaining = PendingTTL
	}
	delete(s.byUser, f.pending.UserCode)
	s.byDevice[deviceCode] = &flow{status: StatusApproved, approved: a, expiry: s.now().Add(remaining)}
}

// MarkDenied flips a flow to denied with the short deny TTL. No-op if gone.
func (s *Store) MarkDenied(deviceCode string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.liveLocked(deviceCode)
	if f == nil {
		return
	}
	delete(s.byUser, f.pending.UserCode)
	s.byDevice[deviceCode] = &flow{status: StatusDenied, expiry: s.now().Add(DeniedTTL)}
}

// ConsumeApproved returns the approved payload exactly once, deleting it. It
// refuses (and leaves intact) a flow that is not approved.
func (s *Store) ConsumeApproved(deviceCode string) (Approved, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.liveLocked(deviceCode)
	if f == nil || f.status != StatusApproved {
		return Approved{}, false
	}
	delete(s.byDevice, deviceCode)
	return f.approved, true
}

// PolledTooFast records this poll and reports whether it came within interval
// seconds of the previous poll for the same device_code. The first poll is
// never too fast.
func (s *Store) PolledTooFast(deviceCode string, interval int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	prev, had := s.pollLast[deviceCode]
	s.pollLast[deviceCode] = now
	if !had {
		return false
	}
	return now.Sub(prev) < time.Duration(interval)*time.Second
}

// liveLocked returns the flow if present and unexpired, pruning it otherwise.
// Caller holds s.mu.
func (s *Store) liveLocked(deviceCode string) *flow {
	f, ok := s.byDevice[deviceCode]
	if !ok {
		return nil
	}
	if !s.now().Before(f.expiry) {
		delete(s.byDevice, deviceCode)
		delete(s.pollLast, deviceCode)
		return nil
	}
	return f
}

// evictLocked prunes all expired flows. Caller holds s.mu.
func (s *Store) evictLocked(now time.Time) {
	for dc, f := range s.byDevice {
		if !now.Before(f.expiry) {
			delete(s.byDevice, dc)
			delete(s.pollLast, dc)
		}
	}
	for uc, dc := range s.byUser {
		if _, ok := s.byDevice[dc]; !ok {
			delete(s.byUser, uc)
		}
	}
}

// Intersect returns the sorted set intersection of two scope lists. Used to
// clamp a device's requested scopes to what the approving user actually holds.
func Intersect(requested, held []string) []string {
	set := make(map[string]struct{}, len(held))
	for _, s := range held {
		set[s] = struct{}{}
	}
	seen := map[string]struct{}{}
	var out []string
	for _, s := range requested {
		if _, ok := set[s]; !ok {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sortStrings(out)
	return out
}

// Subset reports whether every element of sub is in super.
func Subset(sub, super []string) bool {
	set := make(map[string]struct{}, len(super))
	for _, s := range super {
		set[s] = struct{}{}
	}
	for _, s := range sub {
		if _, ok := set[s]; !ok {
			return false
		}
	}
	return true
}

func sortStrings(ss []string) {
	// insertion sort: scope lists are tiny.
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

// NewUUID4 returns a random RFC 4122 v4 UUID string (for devices.id). Avoids a
// third-party dependency.
func NewUUID4() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("deviceauth: crypto/rand failed: " + err.Error())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return hex.EncodeToString(b[0:4]) + "-" +
		hex.EncodeToString(b[4:6]) + "-" +
		hex.EncodeToString(b[6:8]) + "-" +
		hex.EncodeToString(b[8:10]) + "-" +
		hex.EncodeToString(b[10:16])
}
