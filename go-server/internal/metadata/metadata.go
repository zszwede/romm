// Package metadata talks to the external game-metadata providers RomM matches
// against. Phase 4 implements IGDB (the primary one in use); ScreenScraper and
// Hasheous slot in behind the same Provider interface next.
package metadata

import "context"

// Game is a provider-agnostic search hit.
type Game struct {
	Provider   string `json:"provider"`
	ProviderID int64  `json:"provider_id"`
	Name       string `json:"name"`
	Summary    string `json:"summary,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
}

type Provider interface {
	Name() string
	Search(ctx context.Context, query string) ([]Game, error)
}
