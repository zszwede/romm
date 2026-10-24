// Package virtual orchestrates the virtual-download workflow by speaking to the
// external merge service (RommDownloader): it checks link availability and
// dispatches download/merge jobs. The merge service itself (archive extraction,
// switch-tool merging, delivery to the library) stays a separate worker — this
// is the same split the Python backend uses; the Go binary is the dispatcher.
package virtual

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	token   string
	hc      *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		hc:      &http.Client{Timeout: 120 * time.Second},
	}
}

// Availability mirrors the merge service's /api/v1/check result entries.
type Availability struct {
	URL       string `json:"url"`
	Available bool   `json:"available"`
	Checked   bool   `json:"checked"`
	Reason    string `json:"reason,omitempty"`
	Filename  string `json:"filename,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// Link is one download source for a job.
type Link struct {
	URL      string `json:"url"`
	Name     string `json:"name,omitempty"`
	Category string `json:"category"` // base | update | dlc
	Version  string `json:"version,omitempty"`
}

// Job is a download+merge request. Metadata (rom_id/platform_id/platform_slug)
// is echoed back by the service on its callback.
type Job struct {
	System     string
	Name       string
	Links      []Link
	RomID      int64
	PlatformID int64
}

func (c *Client) do(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("merge service %s: %s: %s", path, resp.Status, b)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Check asks the merge service which URLs still exist.
func (c *Client) Check(ctx context.Context, urls []string) ([]Availability, error) {
	var resp struct {
		Results []Availability `json:"results"`
	}
	if err := c.do(ctx, "/api/v1/check", map[string]any{"urls": urls}, &resp); err != nil {
		return nil, err
	}
	return resp.Results, nil
}

// Dispatch submits a download+merge job. Returns the service-assigned job id.
func (c *Client) Dispatch(ctx context.Context, j Job) (string, error) {
	links := make([]map[string]any, 0, len(j.Links))
	for _, l := range j.Links {
		cat := l.Category
		if cat == "" {
			cat = "base"
		}
		links = append(links, map[string]any{
			"url": l.URL, "name": l.Name, "category": cat, "version": l.Version,
		})
	}
	payload := map[string]any{
		"system": j.System,
		"name":   j.Name,
		"links":  links,
		"metadata": map[string]any{
			"rom_id":        j.RomID,
			"platform_id":   j.PlatformID,
			"platform_slug": j.System,
		},
	}
	var resp struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, "/api/v1/jobs", payload, &resp); err != nil {
		return "", err
	}
	return resp.ID, nil
}
