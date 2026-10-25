package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zszwede/romm-go/internal/metadata"
)

// searchRomsResult mirrors the frontend's SearchRomSchema: one card per matched
// title, carrying each source's id and cover so the manual-match dialog can
// offer the user a choice. Only the fields this server populates are filled;
// the rest stay at their zero value, which the schema allows.
type searchRomsResult struct {
	IgdbID        *int64 `json:"igdb_id,omitempty"`
	SsID          *int64 `json:"ss_id,omitempty"`
	MobyID        *int64 `json:"moby_id,omitempty"`
	PlatformID    int64  `json:"platform_id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Summary       string `json:"summary"`
	LibretroID       string `json:"libretro_id,omitempty"`
	IgdbURLCover     string `json:"igdb_url_cover"`
	SsURLCover       string `json:"ss_url_cover"`
	MobyURLCover     string `json:"moby_url_cover"`
	LibretroURLCover string `json:"libretro_url_cover"`
	IsUnidentified   bool   `json:"is_unidentified"`
	IsIdentified     bool   `json:"is_identified"`
}

// searchRoms serves GET /api/search/roms?rom_id=&search_term=&search_by=name|id,
// the manual-match endpoint. It queries every configured metadata provider (SS
// scoped to the platform's ss_id, IGDB by name) and merges hits by normalized
// title so one card shows each source's cover.
func (s *Server) searchRoms(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.authUser(r); !ok {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	q := r.URL.Query()
	romID := int64(atoiDefault(q.Get("rom_id"), 0))
	if romID == 0 {
		writeErr(w, http.StatusUnprocessableEntity, "rom_id required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	rom, err := s.st.GetRom(ctx, romID)
	if err != nil || rom == nil {
		writeErr(w, http.StatusNotFound, "rom not found")
		return
	}
	term := strings.TrimSpace(q.Get("search_term"))
	if term == "" {
		term = rom.Name
		if term == "" {
			term = rom.FsNameNoExt
		}
	}
	searchBy := strings.ToLower(q.Get("search_by"))
	if searchBy == "" {
		searchBy = "name"
	}

	srcIDs, _ := s.st.GetPlatformSourceIDs(ctx, rom.PlatformID)
	var ssSystemID int64
	if srcIDs != nil && srcIDs.SsID != nil {
		ssSystemID = *srcIDs.SsID
	}

	// Merge hits by a normalized title so a game found on several sources
	// becomes one card. Insertion order is preserved for a stable list.
	merged := map[string]*searchRomsResult{}
	var order []string
	add := func(provider string, g metadata.Game) {
		key := normalizeTitle(g.Name)
		if key == "" {
			return
		}
		e, ok := merged[key]
		if !ok {
			e = &searchRomsResult{PlatformID: rom.PlatformID, Name: g.Name, IsIdentified: true}
			merged[key] = e
			order = append(order, key)
		}
		id := g.ProviderID
		switch provider {
		case "ss":
			e.SsID = &id
			if e.SsURLCover == "" {
				e.SsURLCover = g.CoverURL
			}
		case "igdb":
			e.IgdbID = &id
			if e.IgdbURLCover == "" {
				e.IgdbURLCover = g.CoverURL
			}
		case "moby":
			e.MobyID = &id
			if e.MobyURLCover == "" {
				e.MobyURLCover = g.CoverURL
			}
		}
		if e.Summary == "" && g.Summary != "" {
			e.Summary = g.Summary
		}
	}

	if searchBy == "id" {
		// An id search targets a single provider id; SS is this install's
		// primary, so resolve it there (scoped to the platform).
		if id, perr := strconv.ParseInt(term, 10, 64); perr == nil && s.ss != nil {
			if g, gerr := s.ss.GetByID(ctx, id, ssSystemID); gerr == nil && g != nil {
				add("ss", *g)
			}
		}
	} else {
		// Name search across providers. SS first (primary), scoped to system.
		if s.ss != nil {
			if games, gerr := s.ss.SearchPlatform(ctx, term, ssSystemID); gerr == nil {
				for _, g := range games {
					add("ss", g)
				}
			}
		}
		if s.meta != nil {
			if games, gerr := s.meta.Search(ctx, term); gerr == nil {
				for _, g := range games {
					add(s.meta.Name(), g)
				}
			}
		}
	}

	// Enrich each matched title with a libretro box-art cover (an alternative
	// the user can pick), mirroring the Python search endpoint. Keyed by the
	// card's name, scoped to the platform's libretro system.
	if s.libretro != nil {
		plat, _ := s.st.GetPlatform(ctx, rom.PlatformID)
		if plat != nil && plat.Slug != "" {
			for _, k := range order {
				e := merged[k]
				if cov := s.libretro.CoverFor(ctx, e.Name, plat.Slug); cov != nil {
					e.LibretroID = cov.ID
					e.LibretroURLCover = cov.CoverURL
				}
			}
		}
	}

	out := make([]searchRomsResult, 0, len(order))
	for _, k := range order {
		out = append(out, *merged[k])
	}
	writeJSON(w, http.StatusOK, out)
}

// normalizeTitle lowercases and strips non-alphanumerics so the same game from
// two providers collapses to one card (matches the Python merge key closely
// enough for dedup).
func normalizeTitle(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}
