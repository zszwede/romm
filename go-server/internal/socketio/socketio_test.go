package socketio

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestOpenPacketShape(t *testing.T) {
	p := openPacket("abc")
	if p[0] != '0' {
		t.Fatalf("open packet must start with '0', got %q", p[:1])
	}
	var h map[string]any
	if err := json.Unmarshal([]byte(p[1:]), &h); err != nil {
		t.Fatalf("open payload not JSON: %v", err)
	}
	if h["sid"] != "abc" {
		t.Errorf("sid = %v", h["sid"])
	}
	if _, ok := h["pingInterval"]; !ok {
		t.Error("missing pingInterval")
	}
	if _, ok := h["upgrades"]; !ok {
		t.Error("missing upgrades")
	}
}

// dialTest spins up the hub handler and returns a connected ws client.
func dialTest(t *testing.T) (*Hub, *websocket.Conn, *httptest.Server) {
	t.Helper()
	hub := NewHub()
	srv := httptest.NewServer(http.HandlerFunc(hub.Handler))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws/socket.io/?EIO=4&transport=websocket"
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return hub, conn, srv
}

func readText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(msg)
}

func TestHandshake_OpenThenConnectAck(t *testing.T) {
	_, conn, _ := dialTest(t)

	// First frame is the Engine.IO OPEN.
	open := readText(t, conn)
	if open[0] != '0' || !strings.Contains(open, `"sid"`) {
		t.Fatalf("expected OPEN packet, got %q", open)
	}

	// Client sends Socket.IO CONNECT; server must ack with "40{...sid...}".
	if err := conn.WriteMessage(websocket.TextMessage, []byte("40")); err != nil {
		t.Fatal(err)
	}
	ack := readText(t, conn)
	if !strings.HasPrefix(ack, "40") || !strings.Contains(ack, `"sid"`) {
		t.Fatalf("expected CONNECT ack, got %q", ack)
	}
}

func TestEmit_ReachesClient(t *testing.T) {
	hub, conn, _ := dialTest(t)
	_ = readText(t, conn) // OPEN
	if err := conn.WriteMessage(websocket.TextMessage, []byte("40")); err != nil {
		t.Fatal(err)
	}
	_ = readText(t, conn) // CONNECT ack

	// Give the server a moment to register the client, then broadcast.
	deadline := time.Now().Add(2 * time.Second)
	for {
		hub.mu.Lock()
		n := len(hub.clients)
		hub.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.Emit("scan:done", map[string]any{"platforms": 1})
	got := readText(t, conn)
	if !strings.HasPrefix(got, "42") || !strings.Contains(got, "scan:done") {
		t.Fatalf("expected EVENT frame, got %q", got)
	}
	// The payload must be a valid Socket.IO EVENT array ["scan:done", {...}].
	var arr []any
	if err := json.Unmarshal([]byte(got[2:]), &arr); err != nil || len(arr) < 1 || arr[0] != "scan:done" {
		t.Fatalf("bad event payload %q (%v)", got, err)
	}
}

func TestClientEvent_Dispatched(t *testing.T) {
	hub, conn, _ := dialTest(t)
	got := make(chan []json.RawMessage, 1)
	hub.On("scan", func(args []json.RawMessage) { got <- args })

	_ = readText(t, conn) // OPEN
	if err := conn.WriteMessage(websocket.TextMessage, []byte("40")); err != nil {
		t.Fatal(err)
	}
	_ = readText(t, conn) // CONNECT ack

	// Client emits ["scan", {"type":"quick"}] as a Socket.IO EVENT ("42" + array).
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`42["scan",{"type":"quick"}]`)); err != nil {
		t.Fatal(err)
	}
	select {
	case args := <-got:
		if len(args) != 1 || string(args[0]) != `{"type":"quick"}` {
			t.Fatalf("handler got wrong args: %v", args)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("scan handler was not dispatched")
	}
}

func TestClientPingGetsPong(t *testing.T) {
	_, conn, _ := dialTest(t)
	_ = readText(t, conn) // OPEN
	if err := conn.WriteMessage(websocket.TextMessage, []byte("2")); err != nil {
		t.Fatal(err)
	}
	if got := readText(t, conn); got != "3" {
		t.Fatalf("expected pong '3', got %q", got)
	}
}
