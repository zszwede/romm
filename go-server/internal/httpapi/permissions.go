package httpapi

import "net/http"

// Permission-group admin. This install authorizes by role (admin vs user), not
// permission groups, so the group endpoints return empty/accept and the catalog
// returns the full entity/action matrix the admin dialogs render.

var permEntities = []string{
	"platforms", "roms", "collections", "playlists", "firmware", "assets",
	"devices", "users", "tasks", "logs", "streaming", "emulation",
}
var permActions = []string{"read", "write", "delete"}

func (s *Server) permissionsCatalog(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"entities": permEntities, "actions": permActions})
}

func (s *Server) permissionGroups(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	writeJSON(w, http.StatusOK, []any{})
}

// userPermissions returns a user's permission assignment (none, since roles are
// used), which the admin edit dialog reads.
func (s *Server) userPermissions(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	writeJSON(w, http.StatusOK, map[string]any{
		"user_id":             id,
		"permission_group_id": nil,
		"overrides":           []any{},
		"hidden":              []any{},
		"age_limit":           nil,
		"hide_unrated_roms":   nil,
	})
}

func (s *Server) hiddenEntities(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Add/remove a hidden entity: accepted (no hidden entities in a single-admin
	// setup).
	writeJSON(w, http.StatusOK, map[string]any{"platforms": []any{}, "roms": []any{}})
}

// raRefresh triggers a RetroAchievements progression refresh for a user.
// Accepted; the progression is recomputed lazily when achievements are read.
func (s *Server) raRefresh(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
