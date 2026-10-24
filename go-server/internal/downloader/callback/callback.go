// Package callback notifies the source application when a job finishes.
package callback

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/zszwede/romm-go/internal/downloader/config"
)

// Send delivers payload as JSON with retries (exponential backoff).
func Send(ctx context.Context, cb config.Callback, url string, payload any, log *slog.Logger) error {
	if url == "" {
		return nil
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: cb.Timeout}
	var lastErr error
	for attempt := 1; attempt <= cb.Retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, cb.Method, url, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", "RommDownloader/1.0")
		for k, v := range cb.Headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err == nil {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
			resp.Body.Close()
			if resp.StatusCode < 300 {
				log.Info("callback sent", "url", url, "status", resp.StatusCode)
				return nil
			}
			err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 408 && resp.StatusCode != 429 {
				return err // the receiver rejected it; retrying will not help
			}
		}
		lastErr = err
		log.Warn("callback failed", "url", url, "attempt", attempt, "err", err)
		if attempt < cb.Retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<min(attempt, 6)) * time.Second):
			}
		}
	}
	return lastErr
}
