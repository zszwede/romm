// Package netplay implements the EmulatorJS netplay signaling server, ported
// from backend/endpoints/sockets/netplay.py + handler/netplay_handler.py.
//
// It is a room state machine plus a WebRTC-signal relay: peers in a room
// exchange SDP offers/answers and ICE candidates through the server, but the
// actual audio/video/input stream is peer-to-peer in the browser and never
// touches the server. That makes the whole thing — room lifecycle, the auth
// gates, and the relay rules — unit-testable with a fake emitter and a fake
// authorizer, exactly as the Python backend tests it with socket stubs.
//
// Two seams are intentionally injected rather than built here: the Socket.IO
// wire transport (Emitter) and the permission/ROM-visibility check
// (Authorizer). The signaling logic, where the bugs live, is fully covered.
package netplay

import (
	"crypto/subtle"
	"sync"
)

// DefaultMaxPlayers matches endpoints/netplay.DEFAULT_MAX_PLAYERS.
const DefaultMaxPlayers = 4

// PlayerInfo is one participant in a room.
type PlayerInfo struct {
	SocketID   string `json:"socketId"`
	PlayerName string `json:"player_name"`
	UserID     string `json:"userid,omitempty"`
	PlayerID   string `json:"playerId,omitempty"`
}

// Room is a netplay room keyed by its session id.
type Room struct {
	Owner      string                `json:"owner"`
	Players    map[string]PlayerInfo `json:"players"` // keyed by player id
	Peers      []string              `json:"peers"`
	RoomName   string                `json:"room_name"`
	GameID     string                `json:"game_id"`
	Domain     string                `json:"domain,omitempty"`
	Password   string                `json:"password,omitempty"`
	MaxPlayers int                   `json:"max_players"`
}

// RoomStore holds rooms in-process (the Python handler used a Redis hash).
type RoomStore struct {
	mu    sync.Mutex
	rooms map[string]*Room
}

func NewRoomStore() *RoomStore { return &RoomStore{rooms: map[string]*Room{}} }

func (s *RoomStore) Get(sessionID string) *Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rooms[sessionID]
	if !ok {
		return nil
	}
	cp := cloneRoom(r)
	return &cp
}

func (s *RoomStore) Set(sessionID string, r *Room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := cloneRoom(r)
	s.rooms[sessionID] = &cp
}

func (s *RoomStore) Delete(sessionIDs ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range sessionIDs {
		delete(s.rooms, id)
	}
}

func (s *RoomStore) GetAll() map[string]Room {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]Room, len(s.rooms))
	for id, r := range s.rooms {
		out[id] = cloneRoom(r)
	}
	return out
}

func cloneRoom(r *Room) Room {
	players := make(map[string]PlayerInfo, len(r.Players))
	for k, v := range r.Players {
		players[k] = v
	}
	peers := append([]string(nil), r.Peers...)
	return Room{
		Owner: r.Owner, Players: players, Peers: peers, RoomName: r.RoomName,
		GameID: r.GameID, Domain: r.Domain, Password: r.Password, MaxPlayers: r.MaxPlayers,
	}
}

// EmitOpts selects the recipients of an Emit.
type EmitOpts struct {
	Room    string // emit to everyone in this socket-room
	To      string // emit to one socket id
	SkipSID string // when broadcasting to a room, skip this sender
}

// Emitter is the Socket.IO transport, injected so the logic is transport-
// agnostic and testable.
type Emitter interface {
	EnterRoom(sid, room string)
	LeaveRoom(sid, room string)
	Emit(event string, data any, opts EmitOpts)
}

// Authorizer decides whether a socket's user may play a ROM, gating room
// creation and joins. Mirrors _playable_rom_id (scope + emulation perms + ROM
// visibility), injected so the signaling logic needn't depend on the full
// permission system.
type Authorizer interface {
	// PlayableROMID returns the resolved ROM id and true if the user behind sid
	// may play gameID. needWrite adds the emulation WRITE grant (room owner);
	// owned marks the room as the user's own (where "own items only" counts).
	PlayableROMID(sid, gameID string, needWrite, owned bool) (romID string, ok bool)
}

// RoomData is the client payload for open-room / join-room.
type RoomData struct {
	Extra      RoomDataExtra
	MaxPlayers *int
	Password   string
}

type RoomDataExtra struct {
	SessionID  string
	UserID     string
	PlayerID   string
	RoomName   string
	GameID     string
	Domain     string
	PlayerName string
}

func (e RoomDataExtra) sessionID() string { return e.SessionID }
func (e RoomDataExtra) playerID() string {
	if e.UserID != "" {
		return e.UserID
	}
	return e.PlayerID
}

// session is the per-socket server-side state (auth user + current room).
type session struct {
	authUserID string
	roomID     string
	playerID   string
}

// Hub ties the store, sessions, emitter and authorizer together.
type Hub struct {
	store *RoomStore
	em    Emitter
	authz Authorizer

	mu       sync.Mutex
	sessions map[string]*session
}

func NewHub(store *RoomStore, em Emitter, authz Authorizer) *Hub {
	return &Hub{store: store, em: em, authz: authz, sessions: map[string]*session{}}
}

func (h *Hub) sess(sid string) *session {
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sessions[sid]
	if !ok {
		s = &session{}
		h.sessions[sid] = s
	}
	return s
}

func socketRoom(sessionID string) string { return "netplay-room:" + sessionID }

func playerName(extra RoomDataExtra, playerID string) string {
	if extra.PlayerName != "" {
		return extra.PlayerName
	}
	return "Player " + playerID
}

func playerInfo(sid string, extra RoomDataExtra, playerID string) PlayerInfo {
	return PlayerInfo{
		SocketID:   sid,
		PlayerName: playerName(extra, playerID),
		UserID:     extra.UserID,
		PlayerID:   extra.PlayerID,
	}
}

// Connect records the connect-time authenticated user (empty for a guest).
func (h *Hub) Connect(sid, authUserID string) {
	h.sess(sid).authUserID = authUserID
}

func (h *Hub) enterRoom(sid, sessionID, playerID string, room *Room) {
	h.em.EnterRoom(sid, socketRoom(sessionID))
	s := h.sess(sid)
	s.roomID, s.playerID = sessionID, playerID
	h.em.Emit("users-updated", room.Players, EmitOpts{Room: socketRoom(sessionID)})
}

// OpenRoom creates a room owned by sid. Returns an error message (non-empty) on
// rejection, matching the Python ack contract.
func (h *Hub) OpenRoom(sid string, data RoomData) string {
	extra := data.Extra
	sessionID := extra.sessionID()
	playerID := extra.playerID()
	if sessionID == "" || playerID == "" {
		return "Invalid data: sessionId and playerId required"
	}

	romID, ok := h.authz.PlayableROMID(sid, extra.GameID, true, true)
	if !ok {
		return "Not authorized to open a room for this game"
	}

	h.LeaveRoom(sid)

	if h.store.Get(sessionID) != nil {
		return "Room already exists"
	}

	maxPlayers := DefaultMaxPlayers
	if data.MaxPlayers != nil && *data.MaxPlayers >= 1 {
		maxPlayers = *data.MaxPlayers
	}

	roomName := extra.RoomName
	if roomName == "" {
		roomName = "Room " + sessionID
	}
	room := &Room{
		Owner:      sid,
		Players:    map[string]PlayerInfo{playerID: playerInfo(sid, extra, playerID)},
		Peers:      []string{},
		RoomName:   roomName,
		GameID:     romID,
		Domain:     extra.Domain,
		Password:   data.Password,
		MaxPlayers: maxPlayers,
	}
	h.store.Set(sessionID, room)
	h.enterRoom(sid, sessionID, playerID, room)
	return ""
}

// JoinRoom adds sid to an existing room. On success errMsg is empty and players
// is the room roster; on rejection errMsg is set.
func (h *Hub) JoinRoom(sid string, data RoomData) (errMsg string, players map[string]PlayerInfo) {
	extra := data.Extra
	sessionID := extra.sessionID()
	playerID := extra.playerID()
	if sessionID == "" || playerID == "" {
		return "Invalid data: sessionId and playerId required", nil
	}

	h.LeaveRoom(sid)

	room := h.store.Get(sessionID)
	if room == nil {
		return "Room not found", nil
	}

	if room.Password != "" {
		if !passwordMatches(room.Password, data.Password) {
			return "Incorrect password", nil
		}
	} else if _, ok := h.authz.PlayableROMID(sid, room.GameID, false, false); !ok {
		return "Not authorized to join this room", nil
	}

	if _, exists := room.Players[playerID]; exists {
		return "Player already in room", nil
	}
	if len(room.Players) >= room.MaxPlayers {
		return "Room is full", nil
	}

	room.Players[playerID] = playerInfo(sid, extra, playerID)
	h.store.Set(sessionID, room)
	h.enterRoom(sid, sessionID, playerID, room)
	return "", room.Players
}

func (h *Hub) handleLeave(sid, sessionID, playerID string) {
	room := h.store.Get(sessionID)
	if room == nil {
		return
	}
	delete(room.Players, playerID)

	if len(room.Players) == 0 {
		h.store.Delete(sessionID)
		h.em.Emit("users-updated", map[string]PlayerInfo{}, EmitOpts{Room: socketRoom(sessionID)})
		return
	}

	if sid == room.Owner {
		// Promote some remaining player's socket to owner (iteration order is
		// unspecified, matching the Python next(iter(...))).
		for _, p := range room.Players {
			room.Owner = p.SocketID
			break
		}
	}
	h.store.Set(sessionID, room)
	h.em.Emit("users-updated", room.Players, EmitOpts{Room: socketRoom(sessionID)})
}

// LeaveRoom removes sid from whatever room it is in.
func (h *Hub) LeaveRoom(sid string) {
	s := h.sess(sid)
	h.mu.Lock()
	sessionID, playerID := s.roomID, s.playerID
	s.roomID, s.playerID = "", ""
	h.mu.Unlock()

	if sessionID != "" && playerID != "" {
		h.handleLeave(sid, sessionID, playerID)
		h.em.LeaveRoom(sid, socketRoom(sessionID))
	}
}

// WebRTCSignalData is one relayed signaling message.
type WebRTCSignalData struct {
	Target             string
	Candidate          any
	Offer              any
	Answer             any
	RequestRenegotiate bool
}

func (h *Hub) isRoomPeer(sid, target string) bool {
	s := h.sess(sid)
	h.mu.Lock()
	sessionID := s.roomID
	h.mu.Unlock()
	if sessionID == "" {
		return false
	}
	room := h.store.Get(sessionID)
	if room == nil {
		return false
	}
	peers := map[string]struct{}{}
	for _, p := range room.Players {
		peers[p.SocketID] = struct{}{}
	}
	_, selfIn := peers[sid]
	_, targetIn := peers[target]
	return selfIn && targetIn
}

// WebRTCSignal relays an offer/answer/candidate to one peer in the same room.
// A signal to a non-peer is dropped, so a client cannot inject into a session
// it never joined.
func (h *Hub) WebRTCSignal(sid string, data WebRTCSignalData) {
	if data.Target == "" || !h.isRoomPeer(sid, data.Target) {
		return
	}
	var payload map[string]any
	if data.RequestRenegotiate {
		payload = map[string]any{"sender": sid, "requestRenegotiate": true}
	} else {
		payload = map[string]any{
			"sender":    sid,
			"candidate": data.Candidate,
			"offer":     data.Offer,
			"answer":    data.Answer,
		}
	}
	h.em.Emit("webrtc-signal", payload, EmitOpts{To: data.Target})
}

// Disconnect tears down sid's membership.
func (h *Hub) Disconnect(sid string) {
	h.LeaveRoom(sid)
	h.mu.Lock()
	delete(h.sessions, sid)
	h.mu.Unlock()
}

func (h *Hub) broadcastToRoom(sid, event string, data any) {
	s := h.sess(sid)
	h.mu.Lock()
	sessionID := s.roomID
	h.mu.Unlock()
	if sessionID != "" {
		h.em.Emit(event, data, EmitOpts{Room: socketRoom(sessionID), SkipSID: sid})
	}
}

// DataMessage, Snapshot and Input fan non-signaling events out to the rest of
// the room.
func (h *Hub) DataMessage(sid string, data any) { h.broadcastToRoom(sid, "data-message", data) }
func (h *Hub) Snapshot(sid string, data any)    { h.broadcastToRoom(sid, "snapshot", data) }
func (h *Hub) Input(sid string, data any)       { h.broadcastToRoom(sid, "input", data) }

// RoomSummary is one entry in the joinable-rooms list.
type RoomSummary struct {
	RoomName    string `json:"room_name"`
	Current     int    `json:"current"`
	Max         int    `json:"max"`
	PlayerName  string `json:"player_name"`
	HasPassword bool   `json:"hasPassword"`
}

// ListRooms returns the open rooms for a ROM (not full, same game), keyed by
// session id. Mirrors endpoints/netplay.get_rooms.
func (h *Hub) ListRooms(gameID string) map[string]RoomSummary {
	out := map[string]RoomSummary{}
	for sessionID, room := range h.store.GetAll() {
		if len(room.Players) >= room.MaxPlayers || room.GameID != gameID {
			continue
		}
		out[sessionID] = RoomSummary{
			RoomName:    room.RoomName,
			Current:     len(room.Players),
			Max:         room.MaxPlayers,
			PlayerName:  ownerPlayerName(room),
			HasPassword: room.Password != "",
		}
	}
	return out
}

func ownerPlayerName(room Room) string {
	if room.Owner == "" {
		return "Unknown"
	}
	for _, p := range room.Players {
		if p.SocketID == room.Owner {
			return p.PlayerName
		}
	}
	return "Unknown"
}

func passwordMatches(expected, supplied string) bool {
	if supplied == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(expected), []byte(supplied)) == 1
}
