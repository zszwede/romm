package httpapi

import (
	"testing"

	"github.com/zszwede/romm-go/internal/netplay"
)

// allowAuthz authorizes any sid and maps the game to itself.
type allowAuthz struct{}

func (allowAuthz) PlayableROMID(_ , gameID string, _, _ bool) (string, bool) { return gameID, true }

func TestNetplayList_Endpoint(t *testing.T) {
	s, _ := newTestServer(t) // fakeStore has rom id 7
	tok := token(t, testSecret, "access", "romm:oauth")

	// Seed a hub with one open room for game "7" and swap it in.
	hub := netplay.NewHub(netplay.NewRoomStore(), netplayNoopEmitter{}, allowAuthz{})
	if msg := hub.OpenRoom("ownerSid", netplay.RoomData{Extra: netplay.RoomDataExtra{
		SessionID: "sessX", UserID: "u1", GameID: "7", PlayerName: "Alice", RoomName: "Lobby",
	}}); msg != "" {
		t.Fatalf("seed open failed: %q", msg)
	}
	s.netplay = hub

	// Unauthorized without a token.
	if w := do(s, "GET", "/api/netplay/list?game_id=7", "", ""); w.Code != 401 {
		t.Fatalf("no token = %d, want 401", w.Code)
	}
	// Missing game_id.
	if w := do(s, "GET", "/api/netplay/list", tok, ""); w.Code != 400 {
		t.Fatalf("no game_id = %d, want 400", w.Code)
	}
	// Unknown rom (fakeStore returns nil for ids other than 7).
	if w := do(s, "GET", "/api/netplay/list?game_id=999", tok, ""); w.Code != 404 {
		t.Fatalf("unknown rom = %d, want 404", w.Code)
	}
	// Happy path.
	w := do(s, "GET", "/api/netplay/list?game_id=7", tok, "")
	if w.Code != 200 {
		t.Fatalf("list = %d %s", w.Code, w.Body.String())
	}
	m := decodeBody(t, w.Body.Bytes())
	room, ok := m["sessX"].(map[string]any)
	if !ok {
		t.Fatalf("room not listed: %v", m)
	}
	if room["room_name"] != "Lobby" || room["player_name"] != "Alice" || room["hasPassword"] != false {
		t.Errorf("summary wrong: %v", room)
	}
}
