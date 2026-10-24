package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/zszwede/romm-go/internal/netplay"
)

// netplayList returns the joinable netplay rooms for a ROM. Ported from
// endpoints/netplay.get_rooms: requires roms.read, the ROM must exist/be
// visible, and only not-full rooms for that ROM are returned.
func (s *Server) netplayList(w http.ResponseWriter, r *http.Request) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope("roms.read") {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	gameID := r.URL.Query().Get("game_id")
	id, err := strconv.ParseInt(gameID, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, "game_id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	rom, err := s.st.GetRom(ctx, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if rom == nil {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}
	writeJSON(w, http.StatusOK, s.netplay.ListRooms(gameID))
}

// Netplay transport/authorization seams. The signaling logic in internal/netplay
// is transport-agnostic; until a Socket.IO transport is wired, the server uses a
// no-op emitter and a deny-all authorizer (no in-process open/join), so the room
// store stays empty in production and the list endpoint returns {}.

type netplayNoopEmitter struct{}

func (netplayNoopEmitter) EnterRoom(string, string)              {}
func (netplayNoopEmitter) LeaveRoom(string, string)              {}
func (netplayNoopEmitter) Emit(string, any, netplay.EmitOpts)    {}

type denyAllAuthz struct{}

func (denyAllAuthz) PlayableROMID(string, string, bool, bool) (string, bool) { return "", false }
