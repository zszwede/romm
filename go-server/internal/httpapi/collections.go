package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/store"
)

// Collection write endpoints (create, add/remove roms, delete) — enough for the
// favorites flow and manual collections.

func (s *Server) collectionUser(w http.ResponseWriter, r *http.Request) (int64, bool) {
	claims, ok := s.authUser(r)
	if !ok || !claims.HasScope("collections.write") {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	uid, err := s.st.UserIDByUsername(ctx, claims.Username)
	if err != nil || uid == 0 {
		writeErr(w, http.StatusUnauthorized, "unknown user")
		return 0, false
	}
	return uid, true
}

// collectionJSON builds the CollectionSchema the frontend expects.
func collectionJSON(c *store.Collection) map[string]any {
	ids := c.RomIDs
	if ids == nil {
		ids = []int64{}
	}
	return map[string]any{
		"id":             c.ID,
		"name":           c.Name,
		"description":    c.Description,
		"rom_ids":        ids,
		"rom_count":      len(ids),
		"is_public":      c.IsPublic,
		"is_favorite":    c.IsFavorite,
		"is_smart":       false,
		"is_virtual":     false,
		"user_id":        c.UserID,
		"path_cover_small": nil,
		"path_cover_large": nil,
		"url_cover":        nil,
	}
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.collectionUser(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		// Also accept a JSON body.
		if err2 := r.ParseForm(); err2 != nil {
			writeErr(w, http.StatusBadRequest, "bad form")
			return
		}
	}
	name := r.FormValue("name")
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name required")
		return
	}
	isFav := truthy(r.FormValue("is_favorite"))
	isPub := truthy(r.FormValue("is_public"))
	romIDs := parseJSONIDList(r.FormValue("rom_ids"))

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	c, err := s.st.CreateCollection(ctx, uid, name, r.FormValue("description"), isPub, isFav, romIDs)
	if err != nil || c == nil {
		writeErr(w, http.StatusInternalServerError, "create failed")
		return
	}
	writeJSON(w, http.StatusCreated, collectionJSON(c))
}

func (s *Server) addRomsToCollection(w http.ResponseWriter, r *http.Request) {
	s.mutateCollectionRoms(w, r, true)
}

func (s *Server) removeRomsFromCollection(w http.ResponseWriter, r *http.Request) {
	s.mutateCollectionRoms(w, r, false)
}

func (s *Server) mutateCollectionRoms(w http.ResponseWriter, r *http.Request, add bool) {
	uid, ok := s.collectionUser(w, r)
	if !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	var body struct {
		RomIDs []int64 `json:"rom_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var c *store.Collection
	var err error
	if add {
		c, err = s.st.AddRomsToCollection(ctx, uid, id, body.RomIDs)
	} else {
		c, err = s.st.RemoveRomsFromCollection(ctx, uid, id, body.RomIDs)
	}
	if err != nil || c == nil {
		writeErr(w, http.StatusInternalServerError, "update failed")
		return
	}
	writeJSON(w, http.StatusOK, collectionJSON(c))
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.collectionUser(w, r)
	if !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	c, err := s.st.GetCollection(ctx, uid, id)
	if err != nil || c == nil {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, collectionJSON(c))
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) {
	uid, ok := s.collectionUser(w, r)
	if !ok {
		return
	}
	id := int64(atoiDefault(r.PathValue("id"), 0))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	gone, err := s.st.DeleteCollection(ctx, uid, id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "delete failed")
		return
	}
	if !gone {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func truthy(s string) bool {
	switch s {
	case "true", "1", "on", "yes", "True":
		return true
	}
	return false
}

// parseJSONIDList parses a JSON array string ("[1,2,3]") into []int64.
func parseJSONIDList(s string) []int64 {
	if s == "" {
		return nil
	}
	var out []int64
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	return out
}
