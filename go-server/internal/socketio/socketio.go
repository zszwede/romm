// Package socketio is a minimal Socket.IO v4 (Engine.IO v4) server, enough for
// the RomM frontend's socket.io-client to connect and stay alive and to receive
// server-pushed events (scan progress, etc.). The Python app used python-socketio
// over Redis; this is the in-process Go equivalent.
//
// Protocol (Engine.IO v4 framing, Socket.IO v4 packets):
//   - Engine.IO: "0"<json> open, "2" ping, "3" pong, "4"<data> message.
//   - Socket.IO (inside a "4" message): "0" CONNECT, "1" DISCONNECT, "2" EVENT.
//
// The server sends the Engine.IO ping; the client answers with a pong (v4
// reversed the direction from v3). Only the default namespace "/" is used.
package socketio

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pingInterval = 25 * time.Second
	pingTimeout  = 20 * time.Second
	writeWait    = 10 * time.Second
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	// The SPA is same-origin behind the same Caddy vhost; accept any origin so
	// dev ports and the reverse proxy both work.
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Handler reacts to a client-emitted event. args are the JSON-encoded event
// arguments (everything after the event name in the Socket.IO EVENT array).
type Handler func(args []json.RawMessage)

// Hub tracks connected clients so the server can broadcast events to all of
// them (e.g. scan progress), and dispatches client-emitted events to handlers.
// Safe for concurrent use.
type Hub struct {
	mu       sync.Mutex
	clients  map[*client]struct{}
	handlers map[string]Handler
	seq      int64
}

func NewHub() *Hub {
	return &Hub{clients: map[*client]struct{}{}, handlers: map[string]Handler{}}
}

// On registers a handler for a client-emitted event name (e.g. "scan").
func (h *Hub) On(event string, fn Handler) {
	h.mu.Lock()
	h.handlers[event] = fn
	h.mu.Unlock()
}

func (h *Hub) dispatch(event string, args []json.RawMessage) {
	h.mu.Lock()
	fn := h.handlers[event]
	h.mu.Unlock()
	if fn != nil {
		go fn(args) // don't block the socket read loop
	}
}

type client struct {
	hub  *Hub
	conn *websocket.Conn
	mu   sync.Mutex // serializes writes
	done chan struct{}
}

func (h *Hub) add(c *client) {
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

func (h *Hub) nextSID() string {
	h.mu.Lock()
	h.seq++
	n := h.seq
	h.mu.Unlock()
	return "go-" + time.Now().Format("150405") + "-" + itoa(n)
}

// Emit sends a Socket.IO event (name + args) to every connected client on the
// default namespace. Used for scan progress and other live pushes.
func (h *Hub) Emit(event string, args ...any) {
	payload := append([]any{event}, args...)
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	frame := "42" + string(data) // "4"=message, "2"=EVENT
	h.mu.Lock()
	clients := make([]*client, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.Unlock()
	for _, c := range clients {
		c.write(websocket.TextMessage, []byte(frame))
	}
}

func (c *client) write(mt int, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
	_ = c.conn.WriteMessage(mt, data)
}

// Handler upgrades the request to a WebSocket and runs the Engine.IO/Socket.IO
// handshake and lifecycle.
func (h *Hub) Handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // Upgrade already wrote an error response.
	}
	c := &client{hub: h, conn: conn, done: make(chan struct{})}
	h.add(c)
	defer func() {
		h.remove(c)
		close(c.done)
		_ = conn.Close()
	}()

	// Engine.IO OPEN packet ("0" + handshake JSON).
	open := openPacket(h.nextSID())
	if err := conn.WriteMessage(websocket.TextMessage, []byte(open)); err != nil {
		return
	}

	go c.pingLoop()

	conn.SetReadDeadline(time.Now().Add(pingInterval + pingTimeout))
	for {
		mt, msg, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if mt != websocket.TextMessage || len(msg) == 0 {
			continue
		}
		c.handle(msg)
		// Any traffic (incl. the pong) extends the deadline.
		conn.SetReadDeadline(time.Now().Add(pingInterval + pingTimeout))
	}
}

// handle processes one Engine.IO frame from the client.
func (c *client) handle(msg []byte) {
	switch msg[0] {
	case '2': // client ping (rare in v4) -> pong
		c.write(websocket.TextMessage, []byte("3"))
	case '3': // pong to our ping -> deadline already extended
	case '4': // Socket.IO message
		if len(msg) < 2 {
			return
		}
		switch msg[1] {
		case '0': // CONNECT to a namespace -> ack with the socket's sid
			c.write(websocket.TextMessage, []byte(`40{"sid":"`+c.hub.nextSID()+`"}`))
		case '1': // DISCONNECT
			// client leaving a namespace; nothing to persist.
		case '2': // EVENT from client -> dispatch to a registered handler.
			c.handleEvent(msg[2:])
		}
	case '1': // Engine.IO close
		// loop will end on the next read error.
	}
}

// handleEvent parses a Socket.IO EVENT payload (a JSON array whose first
// element is the event name) and dispatches it. A leading ack id (digits before
// the array) is tolerated and ignored.
func (c *client) handleEvent(payload []byte) {
	// Skip an optional numeric ack id before the '['.
	i := 0
	for i < len(payload) && payload[i] >= '0' && payload[i] <= '9' {
		i++
	}
	payload = payload[i:]
	var arr []json.RawMessage
	if err := json.Unmarshal(payload, &arr); err != nil || len(arr) == 0 {
		return
	}
	var name string
	if err := json.Unmarshal(arr[0], &name); err != nil {
		return
	}
	c.hub.dispatch(name, arr[1:])
}

func (c *client) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
			c.write(websocket.TextMessage, []byte("2")) // Engine.IO ping
		}
	}
}

// openPacket builds the Engine.IO OPEN frame.
func openPacket(sid string) string {
	h := map[string]any{
		"sid":          sid,
		"upgrades":     []string{},
		"pingInterval": int(pingInterval / time.Millisecond),
		"pingTimeout":  int(pingTimeout / time.Millisecond),
		"maxPayload":   1000000,
	}
	b, _ := json.Marshal(h)
	return "0" + string(b)
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
