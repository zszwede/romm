// Package achievements is a thin RetroAchievements web-API client: given a
// game's RA id and a user, it reports how many achievements the game has and
// how many the user has earned. Base URL is a field so tests can point it at an
// httptest server.
package achievements

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type Client struct {
	apiKey  string
	baseURL string
	hc      *http.Client
}

func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		baseURL: "https://retroachievements.org/API",
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

// Progress is a user's achievement standing for one game.
type Progress struct {
	GameID int    `json:"game_id"`
	Title  string `json:"title"`
	Total  int    `json:"total"`  // achievements in the game
	Earned int    `json:"earned"` // earned by the user
}

// UserGameProgress calls API_GetGameInfoAndUserProgress.
func (c *Client) UserGameProgress(ctx context.Context, gameID int, user string) (Progress, error) {
	if c.apiKey == "" {
		return Progress{}, fmt.Errorf("retroachievements: no api key")
	}
	q := url.Values{
		"g": {strconv.Itoa(gameID)},
		"u": {user},
		"y": {c.apiKey},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/API_GetGameInfoAndUserProgress.php?"+q.Encode(), nil)
	if err != nil {
		return Progress{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Progress{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return Progress{}, fmt.Errorf("retroachievements: %s: %s", resp.Status, b)
	}
	// RA returns some numeric fields as JSON strings, so decode loosely.
	var raw struct {
		Title            string `json:"Title"`
		NumAchievements  any    `json:"NumAchievements"`
		NumAwardedToUser any    `json:"NumAwardedToUser"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return Progress{}, err
	}
	return Progress{
		GameID: gameID,
		Title:  raw.Title,
		Total:  toInt(raw.NumAchievements),
		Earned: toInt(raw.NumAwardedToUser),
	}, nil
}

func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	default:
		return 0
	}
}
