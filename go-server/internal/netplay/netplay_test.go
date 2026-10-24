package netplay

import (
	"testing"
)

// recEmitter records every emit, enter and leave for assertions.
type recEmitter struct {
	emits   []emitRec
	entered []string // "sid|room"
	left    []string
}

type emitRec struct {
	event string
	data  any
	opts  EmitOpts
}

func (e *recEmitter) EnterRoom(sid, room string) { e.entered = append(e.entered, sid+"|"+room) }
func (e *recEmitter) LeaveRoom(sid, room string)  { e.left = append(e.left, sid+"|"+room) }
func (e *recEmitter) Emit(event string, data any, opts EmitOpts) {
	e.emits = append(e.emits, emitRec{event, data, opts})
}
func (e *recEmitter) lastEmit(event string) (emitRec, bool) {
	for i := len(e.emits) - 1; i >= 0; i-- {
		if e.emits[i].event == event {
			return e.emits[i], true
		}
	}
	return emitRec{}, false
}
func (e *recEmitter) countEmit(event string) int {
	n := 0
	for _, r := range e.emits {
		if r.event == event {
			n++
		}
	}
	return n
}

// fakeAuthz authorizes a fixed set of sids, mapping any game_id to romID.
type fakeAuthz struct {
	allowed   map[string]bool // sid -> allowed
	romID     string
	denyWrite map[string]bool // sids that may read but not write (can't own)
}

func (a *fakeAuthz) PlayableROMID(sid, gameID string, needWrite, owned bool) (string, bool) {
	if !a.allowed[sid] {
		return "", false
	}
	if needWrite && a.denyWrite[sid] {
		return "", false
	}
	return a.romID, true
}

func newHub(authz Authorizer) (*Hub, *recEmitter) {
	em := &recEmitter{}
	return NewHub(NewRoomStore(), em, authz), em
}

func extra(session, user string) RoomDataExtra {
	return RoomDataExtra{SessionID: session, UserID: user, GameID: "100", PlayerName: user}
}

func TestOpenRoom_HappyPath(t *testing.T) {
	h, em := newHub(&fakeAuthz{allowed: map[string]bool{"sidA": true}, romID: "777"})
	if msg := h.OpenRoom("sidA", RoomData{Extra: extra("sess1", "u1")}); msg != "" {
		t.Fatalf("open rejected: %q", msg)
	}
	room := h.store.Get("sess1")
	if room == nil || room.Owner != "sidA" || room.GameID != "777" || room.MaxPlayers != DefaultMaxPlayers {
		t.Fatalf("room wrong: %+v", room)
	}
	if _, ok := room.Players["u1"]; !ok {
		t.Fatal("owner not in players")
	}
	if len(em.entered) != 1 || em.entered[0] != "sidA|netplay-room:sess1" {
		t.Errorf("enter-room not emitted: %v", em.entered)
	}
	if _, ok := em.lastEmit("users-updated"); !ok {
		t.Error("users-updated not emitted on open")
	}
}

func TestOpenRoom_Validation(t *testing.T) {
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"s": true}, romID: "1"})
	if msg := h.OpenRoom("s", RoomData{Extra: RoomDataExtra{UserID: "u"}}); msg == "" {
		t.Error("missing sessionId accepted")
	}
	// Unauthorized user.
	h2, _ := newHub(&fakeAuthz{allowed: map[string]bool{}, romID: "1"})
	if msg := h2.OpenRoom("s", RoomData{Extra: extra("x", "u")}); msg != "Not authorized to open a room for this game" {
		t.Errorf("unauth open = %q", msg)
	}
	// Duplicate room: a *different authorized* owner cannot reuse a live session
	// id (authz is checked before the exists gate, so the opener must be allowed).
	h3, _ := newHub(&fakeAuthz{allowed: map[string]bool{"s": true, "s2": true}, romID: "1"})
	h3.OpenRoom("s", RoomData{Extra: extra("dup", "u")})
	if msg := h3.OpenRoom("s2", RoomData{Extra: extra("dup", "u2")}); msg != "Room already exists" {
		t.Errorf("duplicate room = %q", msg)
	}
}

func TestJoinRoom_HappyPathAndRoster(t *testing.T) {
	h, em := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true, "guest": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1")})
	msg, players := h.JoinRoom("guest", RoomData{Extra: extra("sess", "u2")})
	if msg != "" {
		t.Fatalf("join rejected: %q", msg)
	}
	if len(players) != 2 {
		t.Fatalf("roster = %v", players)
	}
	// users-updated emitted on both open and join.
	if em.countEmit("users-updated") < 2 {
		t.Errorf("users-updated count = %d", em.countEmit("users-updated"))
	}
}

func TestJoinRoom_PasswordGate(t *testing.T) {
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1"), Password: "hunter2"})

	// Guest with no ROM authorization still gets in with the right password.
	if msg, _ := h.JoinRoom("guest", RoomData{Extra: extra("sess", "u2"), Password: "hunter2"}); msg != "" {
		t.Errorf("correct password rejected: %q", msg)
	}
	if msg, _ := h.JoinRoom("guest2", RoomData{Extra: extra("sess", "u3"), Password: "wrong"}); msg != "Incorrect password" {
		t.Errorf("wrong password = %q", msg)
	}
	if msg, _ := h.JoinRoom("guest3", RoomData{Extra: extra("sess", "u4")}); msg != "Incorrect password" {
		t.Errorf("missing password = %q", msg)
	}
}

func TestJoinRoom_AuthGateWhenNoPassword(t *testing.T) {
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1")})
	// Unauthorized guest, no password on the room -> rejected.
	if msg, _ := h.JoinRoom("stranger", RoomData{Extra: extra("sess", "u2")}); msg != "Not authorized to join this room" {
		t.Errorf("unauth join = %q", msg)
	}
}

func TestJoinRoom_FullAndDuplicate(t *testing.T) {
	one := 1
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true, "g": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1"), MaxPlayers: &one})
	if msg, _ := h.JoinRoom("g", RoomData{Extra: extra("sess", "u2")}); msg != "Room is full" {
		t.Errorf("full room = %q", msg)
	}
	// Not found.
	if msg, _ := h.JoinRoom("g", RoomData{Extra: extra("nope", "u2")}); msg != "Room not found" {
		t.Errorf("missing room = %q", msg)
	}
}

func TestLeaveRoom_OwnerReassignAndCleanup(t *testing.T) {
	h, em := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true, "guest": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1")})
	h.JoinRoom("guest", RoomData{Extra: extra("sess", "u2")})

	h.LeaveRoom("owner")
	room := h.store.Get("sess")
	if room == nil {
		t.Fatal("room deleted while a player remained")
	}
	if room.Owner != "guest" {
		t.Errorf("owner not reassigned: %q", room.Owner)
	}
	if _, ok := room.Players["u1"]; ok {
		t.Error("left player still in roster")
	}

	// Last player leaves -> room gone + empty users-updated.
	h.LeaveRoom("guest")
	if h.store.Get("sess") != nil {
		t.Error("empty room not deleted")
	}
	if last, ok := em.lastEmit("users-updated"); ok {
		if m, _ := last.data.(map[string]PlayerInfo); len(m) != 0 {
			t.Errorf("final users-updated not empty: %v", last.data)
		}
	}
}

func TestOpenRoom_LeavesPreviousRoomFirst(t *testing.T) {
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"s": true}, romID: "1"})
	h.OpenRoom("s", RoomData{Extra: extra("r1", "u1")})
	h.OpenRoom("s", RoomData{Extra: extra("r2", "u1")})
	// r1 had only this player; opening r2 should have cleaned r1 up.
	if h.store.Get("r1") != nil {
		t.Error("previous room not left on re-open")
	}
	if h.store.Get("r2") == nil {
		t.Error("new room missing")
	}
}

func TestWebRTCSignal_RelayAndIsolation(t *testing.T) {
	h, em := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true, "guest": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1")})
	h.JoinRoom("guest", RoomData{Extra: extra("sess", "u2")})

	h.WebRTCSignal("owner", WebRTCSignalData{Target: "guest", Offer: "sdp-offer"})
	rec, ok := em.lastEmit("webrtc-signal")
	if !ok || rec.opts.To != "guest" {
		t.Fatalf("signal not relayed to peer: %+v", rec)
	}
	payload := rec.data.(map[string]any)
	if payload["sender"] != "owner" || payload["offer"] != "sdp-offer" {
		t.Errorf("payload wrong: %v", payload)
	}

	// Signal to a non-peer is dropped.
	before := em.countEmit("webrtc-signal")
	h.WebRTCSignal("owner", WebRTCSignalData{Target: "stranger", Offer: "x"})
	if em.countEmit("webrtc-signal") != before {
		t.Error("signal to non-peer was relayed")
	}
	// Renegotiate payload shape.
	h.WebRTCSignal("guest", WebRTCSignalData{Target: "owner", RequestRenegotiate: true})
	rec, _ = em.lastEmit("webrtc-signal")
	if rec.data.(map[string]any)["requestRenegotiate"] != true {
		t.Errorf("renegotiate flag missing: %v", rec.data)
	}
}

func TestBroadcast_SkipsSender(t *testing.T) {
	h, em := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1")})
	h.Input("owner", map[string]any{"btn": "A"})
	rec, ok := em.lastEmit("input")
	if !ok || rec.opts.SkipSID != "owner" || rec.opts.Room != "netplay-room:sess" {
		t.Fatalf("broadcast opts wrong: %+v", rec.opts)
	}
}

func TestDisconnect_CleansUp(t *testing.T) {
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"owner": true}, romID: "1"})
	h.OpenRoom("owner", RoomData{Extra: extra("sess", "u1")})
	h.Disconnect("owner")
	if h.store.Get("sess") != nil {
		t.Error("room survived owner disconnect")
	}
}

func TestListRooms(t *testing.T) {
	two := 2
	h, _ := newHub(&fakeAuthz{allowed: map[string]bool{"a": true, "b": true, "c": true}, romID: "100"})
	h.OpenRoom("a", RoomData{Extra: RoomDataExtra{SessionID: "open", UserID: "u1", GameID: "100", PlayerName: "Alice", RoomName: "Lobby"}, MaxPlayers: &two})
	// A full room (max 1) must be excluded.
	one := 1
	h.OpenRoom("b", RoomData{Extra: RoomDataExtra{SessionID: "full", UserID: "u2", GameID: "100"}, MaxPlayers: &one})
	// A room for a different game must be excluded.
	h.OpenRoom("c", RoomData{Extra: RoomDataExtra{SessionID: "other", UserID: "u3", GameID: "100"}})

	// authz maps every game to romID "100", so all rooms have GameID "100";
	// distinguish by making the list query for "100".
	got := h.ListRooms("100")
	if _, ok := got["open"]; !ok {
		t.Error("open room missing from list")
	}
	if _, ok := got["full"]; ok {
		t.Error("full room listed")
	}
	if got["open"].PlayerName != "Alice" || got["open"].RoomName != "Lobby" || got["open"].Max != 2 {
		t.Errorf("summary wrong: %+v", got["open"])
	}
	// Query for a game id no room has -> empty.
	if len(h.ListRooms("999")) != 0 {
		t.Error("rooms listed for a non-matching game id")
	}
}
