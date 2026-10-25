package httpapi

import (
	"context"
	"net/http"
	"time"
)

// Small frontend-bootstrap endpoints. Several return empty collections or
// disabled-feature config for the home-lab single-user setup; they exist so the
// Vue app's stores receive the array/object shapes they expect instead of the
// SPA HTML fallback.

func (s *Server) getStats(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	st, err := s.st.GetStats(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// getNotifications returns the caller's notifications (none in this build).
func (s *Server) getNotifications(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getRecommendations returns recommended roms (none in this build).
func (s *Server) getRecommendations(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getSimilarRoms returns library games similar to one (none in this build; the
// similarity index isn't computed). A bare array — the detail view iterates it.
func (s *Server) getSimilarRoms(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getSmartCollections returns smart collections (none in this build).
func (s *Server) getSmartCollections(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getAuditEvents returns the audit log (empty; not recorded in this build) as a
// paged response the EventLog viewer reads.
func (s *Server) getAuditEvents(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "total": 0})
}

// getLogs returns backend log entries (empty; the Go server logs to stdout/the
// service journal, not an in-app viewer) as a bare array (list[LogEntrySchema]).
func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getFirmware returns platform BIOS/firmware (none in this build) as a bare
// array (list[FirmwareSchema]).
func (s *Server) getFirmware(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getVirtualCollections returns virtual (auto-grouped) collections (none in
// this build).
func (s *Server) getVirtualCollections(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getTasksStatus reports running tasks as a bare array (none running). The
// frontend's scan watcher does data.find(t => t.task_type==='scan'), so this
// must be an array, not an object.
func (s *Server) getTasksStatus(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// getStreamingConfig reports remote-session streaming as disabled.
func (s *Server) getStreamingConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":         false,
		"containers":      []any{},
		"emulator_labels": map[string]any{},
	})
}

// getRandomRom returns one random rom (the home "surprise me" button).
func (s *Server) getRandomRom(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	id, err := s.st.RandomRomID(ctx)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "query failed")
		return
	}
	if id == 0 {
		writeErr(w, http.StatusNotFound, "no roms")
		return
	}
	rom, err := s.st.GetRom(ctx, id)
	if err != nil || rom == nil {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}
	writeJSON(w, http.StatusOK, rom)
}
