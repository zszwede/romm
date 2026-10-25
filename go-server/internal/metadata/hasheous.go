package metadata

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Hasheous identifies a ROM from its file hashes (md5/sha1/crc) against
// hasheous.org, returning the provider ids it maps to (IGDB, TheGamesDB) plus a
// logo/cover. It is a hash-lookup source, so it is used during a hashed scan,
// not the name-search dialog. The public app key is the same constant the
// Python client ships (Hasheous requires it but it is not a secret).
type Hasheous struct {
	baseURL string
	origin  string // site root, for relative cover links
	apiKey  string
	hc      *http.Client
}

func NewHasheous() *Hasheous {
	return &Hasheous{
		baseURL: "https://hasheous.org/api/v1",
		origin:  "https://hasheous.org",
		apiKey:  "JNoFBA-jEh4HbxuxEHM6MVzydKoAXs9eCcp2dvcg5LRCnpp312voiWmjuaIssSzS",
		hc:      &http.Client{Timeout: 20 * time.Second},
	}
}

func (h *Hasheous) Name() string { return "hasheous" }

// HashMatch is what a successful Hasheous identification yields.
type HashMatch struct {
	HasheousID *int64
	IgdbID     *int64
	TgdbID     *int64
	Name       string
	CoverURL   string
}

type hasheousGame struct {
	ID       json.Number `json:"id"`
	Name     string      `json:"name"`
	Metadata []struct {
		Source string `json:"source"`
		// immutableId is a number or a slug string, depending on how far
		// Hasheous has migrated that entry; capture it raw and parse leniently.
		ImmutableID json.RawMessage `json:"immutableId"`
	} `json:"metadata"`
	Attributes []struct {
		AttributeName string `json:"attributeName"`
		Link          string `json:"link"`
	} `json:"attributes"`
}

// LookupByHash identifies a ROM from any of its md5/sha1/crc hashes. A nil
// result with nil error means Hasheous answered but knows nothing of the hash.
func (h *Hasheous) LookupByHash(ctx context.Context, md5, sha1, crc string) (*HashMatch, error) {
	entry := map[string]string{}
	if md5 != "" {
		entry["mD5"] = md5
	}
	if sha1 != "" {
		entry["shA1"] = sha1
	}
	if crc != "" {
		entry["crc"] = crc
	}
	if len(entry) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal([]map[string]string{entry})
	u := h.baseURL + "/Lookup/ByHash?returnAllSources=true&returnFields=Signatures,Metadata,Attributes"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Client-API-Key", h.apiKey)
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // known-unknown
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hasheous lookup: %s: %s", resp.Status, trimErr(raw))
	}
	// An empty / null body means no match.
	if t := strings.TrimSpace(string(raw)); t == "" || t == "null" || t == "{}" {
		return nil, nil
	}
	var g hasheousGame
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, fmt.Errorf("hasheous lookup: decode: %w", err)
	}
	return h.toMatch(&g), nil
}

func (h *Hasheous) toMatch(g *hasheousGame) *HashMatch {
	m := &HashMatch{Name: g.Name}
	if id, err := strconv.ParseInt(strings.TrimSpace(g.ID.String()), 10, 64); err == nil && id != 0 {
		m.HasheousID = &id
	}
	for _, meta := range g.Metadata {
		switch meta.Source {
		case "IGDB":
			if id := numericID(meta.ImmutableID); id != nil {
				m.IgdbID = id
			}
		case "TheGamesDb":
			if id := numericID(meta.ImmutableID); id != nil {
				m.TgdbID = id
			}
		}
	}
	for _, attr := range g.Attributes {
		if attr.AttributeName == "Logo" && attr.Link != "" {
			m.CoverURL = h.origin + attr.Link
			break
		}
	}
	// Nothing useful identified -> treat as no match.
	if m.HasheousID == nil && m.IgdbID == nil && m.TgdbID == nil {
		return nil
	}
	return m
}

// numericID parses a provider id from a raw JSON value that may be a number or
// a quoted string. Hasheous sometimes returns a slug instead of an id, which is
// not usable and yields nil.
func numericID(raw json.RawMessage) *int64 {
	s := strings.TrimSpace(string(raw))
	s = strings.Trim(s, `"`)
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v == 0 {
		return nil
	}
	return &v
}
