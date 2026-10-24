package deviceauth

import (
	"reflect"
	"regexp"
	"testing"
	"time"
)

// fakeClock is a manually-advanced clock so TTL and poll-pacing logic is
// deterministic.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time   { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestStore() (*Store, *fakeClock) {
	c := &fakeClock{t: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	return newStoreWithClock(c.now), c
}

func TestNormalizeUserCode(t *testing.T) {
	for in, want := range map[string]string{
		"ab-cd ef":  "ABCDEF",
		"ABCD1234":  "ABCD1234",
		" a b c ":   "ABC",
		"zz-99-xx":  "ZZ99XX",
	} {
		if got := NormalizeUserCode(in); got != want {
			t.Errorf("NormalizeUserCode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneratedCodesShape(t *testing.T) {
	dc := GenerateDeviceCode()
	if len(dc) != DeviceCodeBytes*2 || !regexp.MustCompile(`^[0-9a-f]+$`).MatchString(dc) {
		t.Errorf("device code %q wrong shape", dc)
	}
	uc := GenerateUserCode()
	if len(uc) != UserCodeLength {
		t.Errorf("user code %q len=%d, want %d", uc, len(uc), UserCodeLength)
	}
	for _, r := range uc {
		if !regexp.MustCompile(`[` + PairAlphabet + `]`).MatchString(string(r)) {
			t.Errorf("user code %q contains char outside alphabet", uc)
		}
	}
	// Uniqueness (probabilistic, but collisions here would be a real bug).
	if GenerateDeviceCode() == dc {
		t.Error("device codes collided")
	}
}

func TestClientTokenAndHash(t *testing.T) {
	raw := GenerateClientToken()
	if raw[:len(ClientTokenPrefix)] != ClientTokenPrefix || len(raw) != len(ClientTokenPrefix)+clientTokenRawLen*2 {
		t.Errorf("client token %q wrong shape", raw)
	}
	h := HashClientToken("rmm_known")
	// sha256 hex is 64 chars and stable.
	if len(h) != 64 || h != HashClientToken("rmm_known") {
		t.Errorf("hash unstable or wrong length: %q", h)
	}
	if HashClientToken("a") == HashClientToken("b") {
		t.Error("distinct tokens hashed to the same value")
	}
}

func TestBuildVerificationPaths(t *testing.T) {
	p, pc := BuildVerificationPaths("ABCD1234")
	if p != "/pair/device" {
		t.Errorf("path = %q", p)
	}
	if pc != "/pair/device?user_code=ABCD1234" {
		t.Errorf("path_complete = %q", pc)
	}
}

func TestFlow_ApproveHappyPath(t *testing.T) {
	st, _ := newTestStore()
	dc, uc := "devcode1", "USERCODE"
	st.StorePending(dc, uc, Pending{
		ClientDeviceIdentifier: "install-123",
		Name:                   "My Phone",
		Client:                 "grout",
		RequestedScopes:        []string{"roms.read", "me.read"},
	})

	// Resolvable by user code; metadata intact; status pending.
	gotDC, ok := st.ResolveDeviceCodeFromUserCode(uc)
	if !ok || gotDC != dc {
		t.Fatalf("resolve = %q,%v", gotDC, ok)
	}
	p, status, ok := st.LoadPending(dc)
	if !ok || status != StatusPending || p.Name != "My Phone" {
		t.Fatalf("load = %+v %q %v", p, status, ok)
	}
	if p.Interval != PollDefaultInterval {
		t.Errorf("default interval not applied: %d", p.Interval)
	}

	// A poll while pending gets authorization_pending (not approved yet).
	if _, consumable := st.ConsumeApproved(dc); consumable {
		t.Fatal("consumed before approval")
	}

	exp := time.Now().Add(time.Hour)
	st.MarkApproved(dc, Approved{RawToken: "rmm_tok", DeviceID: "dev-uuid", Scopes: []string{"roms.read"}, ExpiresAt: &exp})

	// user_code is retired on approval.
	if _, ok := st.ResolveDeviceCodeFromUserCode(uc); ok {
		t.Error("user_code still resolvable after approval")
	}

	// One-shot consume.
	a, ok := st.ConsumeApproved(dc)
	if !ok || a.RawToken != "rmm_tok" || a.DeviceID != "dev-uuid" {
		t.Fatalf("consume = %+v %v", a, ok)
	}
	if _, ok := st.ConsumeApproved(dc); ok {
		t.Error("second consume succeeded; token not one-shot")
	}
	// After consume the flow is gone entirely.
	if _, _, ok := st.LoadPending(dc); ok {
		t.Error("flow still present after consume")
	}
}

func TestFlow_Deny(t *testing.T) {
	st, _ := newTestStore()
	dc, uc := "d2", "UC2"
	st.StorePending(dc, uc, Pending{Name: "x"})
	st.MarkDenied(dc)
	_, status, ok := st.LoadPending(dc)
	if !ok || status != StatusDenied {
		t.Fatalf("after deny: status=%q ok=%v", status, ok)
	}
	// A denied flow is never consumable.
	if _, ok := st.ConsumeApproved(dc); ok {
		t.Error("consumed a denied flow")
	}
	if _, ok := st.ResolveDeviceCodeFromUserCode(uc); ok {
		t.Error("user_code resolvable after deny")
	}
}

func TestFlow_PendingTTLExpiry(t *testing.T) {
	st, clk := newTestStore()
	dc, uc := "d3", "UC3"
	st.StorePending(dc, uc, Pending{Name: "x"})

	clk.add(PendingTTL - time.Second)
	if _, _, ok := st.LoadPending(dc); !ok {
		t.Fatal("expired one second early")
	}
	clk.add(2 * time.Second) // now past the ceiling
	if _, _, ok := st.LoadPending(dc); ok {
		t.Error("flow survived past PendingTTL")
	}
	if _, ok := st.ResolveDeviceCodeFromUserCode(uc); ok {
		t.Error("user_code index survived expiry")
	}
}

func TestFlow_DeniedShortTTL(t *testing.T) {
	st, clk := newTestStore()
	dc := "d4"
	st.StorePending(dc, "UC4", Pending{Name: "x"})
	st.MarkDenied(dc)
	clk.add(DeniedTTL + time.Second)
	if _, _, ok := st.LoadPending(dc); ok {
		t.Error("denied flow outlived DeniedTTL")
	}
}

func TestPolledTooFast(t *testing.T) {
	st, clk := newTestStore()
	dc := "d5"
	if st.PolledTooFast(dc, 5) {
		t.Error("first poll flagged as too fast")
	}
	clk.add(2 * time.Second)
	if !st.PolledTooFast(dc, 5) {
		t.Error("2s gap with 5s interval should be too fast")
	}
	clk.add(6 * time.Second)
	if st.PolledTooFast(dc, 5) {
		t.Error("6s gap with 5s interval should be allowed")
	}
}

func TestMarkApproved_NoopOnExpired(t *testing.T) {
	st, clk := newTestStore()
	dc := "d6"
	st.StorePending(dc, "UC6", Pending{Name: "x"})
	clk.add(PendingTTL + time.Second)
	st.MarkApproved(dc, Approved{RawToken: "rmm_x"}) // flow already gone
	if _, ok := st.ConsumeApproved(dc); ok {
		t.Error("approving an expired flow resurrected it")
	}
}

func TestIntersectAndSubset(t *testing.T) {
	got := Intersect([]string{"roms.read", "me.write", "me.read", "roms.read"}, []string{"me.read", "roms.read"})
	want := []string{"me.read", "roms.read"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Intersect = %v, want %v (sorted, deduped, clamped)", got, want)
	}
	if !Subset([]string{"me.read"}, []string{"me.read", "roms.read"}) {
		t.Error("me.read should be a subset")
	}
	if Subset([]string{"me.write"}, []string{"me.read"}) {
		t.Error("me.write is not a subset of {me.read}")
	}
	if !Subset(nil, []string{"x"}) {
		t.Error("empty set is a subset of anything")
	}
}

func TestNewUUID4Shape(t *testing.T) {
	re := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	u := NewUUID4()
	if !re.MatchString(u) {
		t.Errorf("uuid %q not a valid v4", u)
	}
	if NewUUID4() == u {
		t.Error("uuids collided")
	}
}
