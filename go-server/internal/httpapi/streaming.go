package httpapi

import "net/http"

// Remote-session streaming is a session-HOST feature: its session operations
// (launch, heartbeat, save/load-state, volume/mute, join) command real emulator
// processes streamed over WebRTC from a host machine. That host isn't part of
// this deployment (play is in-browser via EmulatorJS), so streaming is reported
// disabled in /api/config + /api/heartbeat and the orchestration endpoints are
// intentionally NOT ported — they'd be unverifiable code driving hardware that
// isn't here.
//
// The READ endpoints below are the ones the UI may poll regardless; they return
// safe empties so nothing can error while streaming is off.

func (s *Server) streamingJoinable(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": []any{}})
}

func (s *Server) streamingStatePicks(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// No shared states to offer when streaming is off.
	writeJSON(w, http.StatusOK, map[string]any{"saves": []any{}, "states": []any{}})
}

func (s *Server) streamingSessionStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// No active session.
	writeJSON(w, http.StatusOK, map[string]any{"active": false, "status": "idle"})
}
