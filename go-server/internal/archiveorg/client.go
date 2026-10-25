package archiveorg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client fetches and enumerates archive.org sources. Base defaults to the real
// archive.org; tests point it at an httptest server.
type Client struct {
	HTTP *http.Client
	Base string // e.g. "https://archive.org"; metadata at Base/metadata/<id>
}

// NewClient returns a client against the real archive.org.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 30 * time.Second}, Base: "https://archive.org"}
}

func (c *Client) base() string {
	if c.Base != "" {
		return c.Base
	}
	return "https://archive.org"
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

// ListGames fetches a source URL and returns the games it exposes, plus the
// kind ("item" or "archive"). Ported from archive_org.list_games.
func (c *Client) ListGames(ctx context.Context, sourceURL string, romExts map[string]bool) ([]VirtualGame, string, error) {
	src, err := ParseSourceURL(sourceURL)
	if err != nil {
		return nil, "", err
	}

	// A single named file: one game, no fetch.
	if src.FilePath != "" {
		name := src.FilePath
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		return []VirtualGame{{Name: unquote(name), SourceURL: c.downloadURL(src.Identifier, src.FilePath)}}, "item", nil
	}

	archivePath := src.ArchivePath
	if archivePath == "" {
		meta, err := c.getJSON(ctx, c.base()+"/metadata/"+src.Identifier)
		if err != nil {
			return nil, "", err
		}
		games, toEnumerate := GamesFromMetadata(meta, src.Identifier, romExts)
		if toEnumerate == "" {
			return games, "item", nil
		}
		archivePath = toEnumerate
	}

	// Enumerate a zip's members via the download URL with a trailing slash,
	// which archive.org serves as the view_archive HTML listing.
	listing, err := c.getText(ctx, c.downloadURL(src.Identifier, encodePath(archivePath))+"/")
	if err != nil {
		return nil, "", err
	}
	return GamesFromArchiveListing(listing, romExts), "archive", nil
}

// downloadURL honours a custom Base so tests stay self-contained.
func (c *Client) downloadURL(identifier, path string) string {
	if c.Base != "" && c.Base != "https://archive.org" {
		return c.base() + "/download/" + identifier + "/" + path
	}
	return downloadURL(identifier, path)
}

func (c *Client) getJSON(ctx context.Context, u string) (map[string]any, error) {
	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("archiveorg: bad metadata JSON: %w", err)
	}
	return out, nil
}

func (c *Client) getText(ctx context.Context, u string) (string, error) {
	body, err := c.get(ctx, u)
	return string(body), err
}

func (c *Client) get(ctx context.Context, u string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archiveorg GET %s: status %d", u, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 32<<20))
}
