package httpapi

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// scopedToken mints an access token with an explicit scope set.
func scopedToken(t *testing.T, scopes string) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": "xiam", "iss": "romm:oauth", "type": "access",
		"scopes": scopes, "exp": time.Now().Add(time.Hour).Unix(),
	}).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func decodeBody(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return m
}

// startFlow runs /init and returns the device_code and user_code.
func startFlow(t *testing.T, s *Server) (string, string) {
	t.Helper()
	w := do(s, "POST", "/api/auth/device/init", "",
		`{"client_device_identifier":"install-1","name":"Phone","client":"grout","requested_scopes":["me.read","roms.read","users.write"]}`)
	if w.Code != 201 {
		t.Fatalf("init code = %d, body %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	dc, _ := m["device_code"].(string)
	uc, _ := m["user_code"].(string)
	if dc == "" || uc == "" {
		t.Fatalf("init missing codes: %v", m)
	}
	if got := m["verification_path"]; got != "/pair/device" {
		t.Errorf("verification_path = %v", got)
	}
	return dc, uc
}

func TestDeviceFlow_ApproveEndToEnd(t *testing.T) {
	s, fs := newTestServer(t)
	userTok := scopedToken(t, "me.read me.write roms.read")

	dc, uc := startFlow(t, s)

	// Poll while pending -> authorization_pending.
	w := do(s, "POST", "/api/auth/device/token", "", `{"device_code":"`+dc+`"}`)
	if w.Code != 400 || decodeBody(t, w.Body.Bytes())["detail"] != "authorization_pending" {
		t.Fatalf("pending poll = %d %s", w.Code, w.Body.String())
	}
	// Immediate re-poll -> slow_down.
	w = do(s, "POST", "/api/auth/device/token", "", `{"device_code":"`+dc+`"}`)
	if w.Code != 400 || decodeBody(t, w.Body.Bytes())["detail"] != "slow_down" {
		t.Fatalf("fast re-poll = %d %s", w.Code, w.Body.String())
	}

	// Approval screen: allowed_scopes is request ∩ caller, so users.write drops.
	w = do(s, "GET", "/api/auth/device/pending/"+uc, userTok, "")
	if w.Code != 200 {
		t.Fatalf("pending GET = %d %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	allowed := toStrings(m["allowed_scopes"])
	if len(allowed) != 2 || allowed[0] != "me.read" || allowed[1] != "roms.read" {
		t.Fatalf("allowed_scopes = %v, want [me.read roms.read]", allowed)
	}
	if m["name"] != "Phone" {
		t.Errorf("name = %v", m["name"])
	}

	// Approve a subset.
	w = do(s, "POST", "/api/auth/device/approve", userTok,
		`{"user_code":"`+uc+`","approved_scopes":["roms.read"],"expires_in":"30d","device_name":"My muOS"}`)
	if w.Code != 200 {
		t.Fatalf("approve = %d %s", w.Code, w.Body.String())
	}
	if fs.regCalls != 1 {
		t.Fatalf("RegisterDeviceToken calls = %d, want 1", fs.regCalls)
	}
	if fs.lastReg.Scopes != "roms.read" || fs.lastReg.Name != "My muOS" || fs.lastReg.ClientDeviceIdentifier != "install-1" {
		t.Fatalf("write-back input wrong: %+v", fs.lastReg)
	}
	if fs.lastReg.HashedToken == "" || len(fs.lastReg.HashedToken) != 64 {
		t.Errorf("hashed token not stored: %q", fs.lastReg.HashedToken)
	}

	// Device's next poll gets the token exactly once.
	w = do(s, "POST", "/api/auth/device/token", "", `{"device_code":"`+dc+`"}`)
	if w.Code != 200 {
		t.Fatalf("approved poll = %d %s", w.Code, w.Body.String())
	}
	m = decodeBody(t, w.Body.Bytes())
	tok, _ := m["access_token"].(string)
	if len(tok) < 4 || tok[:4] != "rmm_" {
		t.Fatalf("access_token = %q", tok)
	}
	if m["device_id"] != fs.regDevID {
		t.Errorf("device_id = %v, want %v", m["device_id"], fs.regDevID)
	}
	// Second poll: token already consumed.
	w = do(s, "POST", "/api/auth/device/token", "", `{"device_code":"`+dc+`"}`)
	if w.Code != 400 || decodeBody(t, w.Body.Bytes())["detail"] != "expired_token" {
		t.Fatalf("second poll = %d %s (token not one-shot)", w.Code, w.Body.String())
	}
}

func TestDeviceFlow_Deny(t *testing.T) {
	s, _ := newTestServer(t)
	userTok := scopedToken(t, "me.read me.write")
	dc, uc := startFlow(t, s)

	w := do(s, "POST", "/api/auth/device/deny", userTok, `{"user_code":"`+uc+`"}`)
	if w.Code != 204 {
		t.Fatalf("deny = %d %s", w.Code, w.Body.String())
	}
	w = do(s, "POST", "/api/auth/device/token", "", `{"device_code":"`+dc+`"}`)
	if w.Code != 400 || decodeBody(t, w.Body.Bytes())["detail"] != "access_denied" {
		t.Fatalf("poll after deny = %d %s", w.Code, w.Body.String())
	}
}

func TestDeviceFlow_ApproveScopeExceedsAllowed(t *testing.T) {
	s, fs := newTestServer(t)
	userTok := scopedToken(t, "me.read me.write roms.read")
	_, uc := startFlow(t, s)

	// users.write was requested but the caller doesn't hold it -> not allowed.
	w := do(s, "POST", "/api/auth/device/approve", userTok,
		`{"user_code":"`+uc+`","approved_scopes":["users.write"]}`)
	if w.Code != 403 {
		t.Fatalf("over-scope approve = %d %s, want 403", w.Code, w.Body.String())
	}
	if fs.regCalls != 0 {
		t.Errorf("token written despite rejected scopes (calls=%d)", fs.regCalls)
	}
}

func TestDeviceFlow_ApproveRequiresMeWrite(t *testing.T) {
	s, _ := newTestServer(t)
	_, uc := startFlow(t, s)

	// Authenticated but only me.read -> 401 (lacks me.write).
	readOnly := scopedToken(t, "me.read roms.read")
	w := do(s, "POST", "/api/auth/device/approve", readOnly,
		`{"user_code":"`+uc+`","approved_scopes":["roms.read"]}`)
	if w.Code != 401 {
		t.Fatalf("approve without me.write = %d, want 401", w.Code)
	}
	// No bearer at all -> 401.
	w = do(s, "POST", "/api/auth/device/approve", "", `{"user_code":"`+uc+`","approved_scopes":["roms.read"]}`)
	if w.Code != 401 {
		t.Fatalf("approve without token = %d, want 401", w.Code)
	}
}

func TestDeviceFlow_BadExpiry(t *testing.T) {
	s, _ := newTestServer(t)
	userTok := scopedToken(t, "me.read me.write roms.read")
	_, uc := startFlow(t, s)
	w := do(s, "POST", "/api/auth/device/approve", userTok,
		`{"user_code":"`+uc+`","approved_scopes":["roms.read"],"expires_in":"5m"}`)
	if w.Code != 422 {
		t.Fatalf("bad expires_in = %d, want 422", w.Code)
	}
}

func TestDeviceFlow_UnknownCode(t *testing.T) {
	s, _ := newTestServer(t)
	userTok := scopedToken(t, "me.read me.write")
	if w := do(s, "GET", "/api/auth/device/pending/NOPE", userTok, ""); w.Code != 404 {
		t.Errorf("pending unknown = %d, want 404", w.Code)
	}
	if w := do(s, "POST", "/api/auth/device/token", "", `{"device_code":"deadbeef"}`); w.Code != 400 {
		t.Errorf("token unknown = %d, want 400", w.Code)
	}
}

func toStrings(v any) []string {
	arr, _ := v.([]any)
	out := make([]string, 0, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
