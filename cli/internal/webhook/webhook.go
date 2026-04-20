// Package webhook posts a JSON payload to the user's --webhook URL
// when the wrapped command exits. Useful for "send me a Slack ping
// when my 6-hour training finishes" workflows.
package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SessionInfo describes the just-ended session.
type SessionInfo struct {
	RoomID        string    `json:"room_id"`
	ViewerURL     string    `json:"viewer_url"`
	Label         string    `json:"label,omitempty"`
	StartedAt     time.Time `json:"started_at"`
	EndedAt       time.Time `json:"ended_at"`
	DurationSec   float64   `json:"duration_sec"`
	MaxViewers    int       `json:"max_viewers"`
}

// WrappedInfo describes the just-ended wrapped command.
type WrappedInfo struct {
	Command         string `json:"command"`
	ExitCode        int    `json:"exit_code"`
	KilledBySignal  *int   `json:"killed_by_signal"` // nil if not killed
}

// Payload is the JSON shape POSTed to the webhook URL. Stable across
// CLI versions per semver — any added fields will be additive.
type Payload struct {
	Event   string      `json:"event"`
	Session SessionInfo `json:"session"`
	Wrapped WrappedInfo `json:"wrapped"`
}

// Default timeout for the POST. Fire-and-forget — we don't want a
// slow webhook to block the CLI's exit.
const DefaultTimeout = 5 * time.Second

// Client is a small wrapper around http.Client with sensible defaults.
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
}

// New returns a Client with a sane HTTP client + 5s timeout.
func New() *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: DefaultTimeout},
		Timeout: DefaultTimeout,
	}
}

// Send POSTs the payload to url with the configured timeout. Returns
// any HTTP / network error; caller decides whether to log or ignore.
//
// Idempotent: webhook receivers may see this once or zero times
// (we never retry — a webhook is best-effort by design).
func (c *Client) Send(ctx context.Context, url string, p Payload) error {
	if c == nil {
		c = New()
	}
	body, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("webhook: marshal: %w", err)
	}

	timeout := c.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "fernsicht-cli")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("webhook: POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("webhook: POST %s returned HTTP %d", url, resp.StatusCode)
	}
	return nil
}
