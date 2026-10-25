// Package metadata talks to the external game-metadata providers RomM matches
// against. IGDB and ScreenScraper are implemented (ScreenScraper is this
// install's primary source); further providers slot in behind Provider.
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

// PlatformSearcher is a Provider that can scope a search to a platform's
// provider-specific system id and fetch a game by its provider id. SS needs it
// (SS searches are per-system); the manual-match endpoint uses it when present.
type PlatformSearcher interface {
	Provider
	SearchPlatform(ctx context.Context, term string, systemID int64) ([]Game, error)
	GetByID(ctx context.Context, id int64, systemID int64) (*Game, error)
}
