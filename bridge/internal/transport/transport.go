// Package transport implements the HTTP signaling client for the
// Fernsicht bridge. It is a direct port of the HTTP layer in
// publishers/python/src/fernsicht/_transport.py with the same wire
// shape (paths, bodies, headers, status-code semantics).
//
// The client is intentionally stateless across calls; the orchestrator
// in bridge/internal/bridge composes it with a Backoff and a poll
// ticker to implement the §9 retry/fatal policy.
//
// Wire contracts (must match the existing Fernsicht server):
//
//	POST /session                           — open a publishing session
//	  Body (optional):  {"max_viewers": N}
//	  Header (optional): X-Fernsicht-Api-Key: <SENDER_JOIN_SECRET>
//	  Response: {room_id, sender_secret, viewer_url, signaling_url, ...}
//
//	GET /poll/{room_id}                     — fetch pending viewer tickets
//	  Header: Authorization: Bearer <sender_secret>
//	  Response: {"tickets": [{ticket_id, offer:{type,sdp}}, ...]}
//
//	POST /ticket/{id}/answer                — post the sender's SDP answer
//	  Body: {"answer": {"type","sdp"}, "secret": "<sender_secret>"}
//
//	POST /ticket/{id}/ice/sender            — post the sender's ICE candidates
//	  Body: {"candidates": [...], "secret": "<sender_secret>"}
//
//	GET /ticket/{id}/ice/viewer?since=N     — poll for viewer's ICE candidates
//	  Response: {"candidates": [...], "seq": N}
//
// Status-code semantics:
//
//	200 → success
//	403 → ErrInvalidSecret  (auth failed; fatal upstream)
//	404 → ErrRoomNotFound   (room expired or never existed; fatal upstream)
//	other 4xx/5xx + network → ErrTransient (retry with backoff)
package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DefaultHTTPTimeout is the per-request timeout. Set generously (30s)
// so users on slow connections, congested cellular, or geographically
// distant from the signaling server still complete handshakes.
//
// Originally 10s to mirror the Python SDK, but real-world connections
// (slow TLS handshake, mobile networks, edge VPS regions) regularly
// brushed the ceiling. Polls and ICE exchanges are short by design;
// the long timeout only matters when the network is genuinely slow,
// in which case waiting longer beats failing fast.
const DefaultHTTPTimeout = 30 * time.Second

// --- Public types --------------------------------------------------------

// SessionConfig holds the optional inputs to OpenSession.
//
// APIKey corresponds to the server's SENDER_JOIN_SECRET and is sent as
// X-Fernsicht-Api-Key (NOT as a body field) — matches the Python SDK.
type SessionConfig struct {
	APIKey     string
	MaxViewers int // 0 means "let the server pick its default"
}

// Session is the parsed response from POST /session.
//
// SignalingURL is preserved from the response but the bridge uses
// BaseURL (i.e. the URL the caller passed to New) for subsequent
// requests; SignalingURL is informational and currently identical.
type Session struct {
	RoomID           string
	SenderSecret     string
	ViewerURL        string
	SignalingURL     string
	ExpiresAt        string
	ExpiresIn        int
	MaxViewers       int
	PollIntervalHint int
}

// Ticket is one pending viewer's offer, returned from PollTickets.
type Ticket struct {
	TicketID string             `json:"ticket_id"`
	Offer    SessionDescription `json:"offer"`
}

// SessionDescription mirrors the WebRTC SDP wire shape.
type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

// ICECandidate mirrors the WebRTC ICE-candidate wire shape.
//
// The Pion library uses a slightly different in-process struct; the
// peer package translates between the two when adding/sending. Pointer
// fields here distinguish "field absent" from "explicit zero/empty".
type ICECandidate struct {
	Candidate     string  `json:"candidate"`
	SDPMid        *string `json:"sdpMid,omitempty"`
	SDPMLineIndex *int    `json:"sdpMLineIndex,omitempty"`
}

// ViewerICEResponse is the parsed payload of GET /ticket/{id}/ice/viewer.
//
// Seq is the server-side sequence the client should pass as `since`
// on the next poll to avoid receiving duplicates.
type ViewerICEResponse struct {
	Candidates []ICECandidate
	Seq        int
}

// --- Sentinel errors -----------------------------------------------------
//
// Callers use errors.Is(err, ErrXxx) to dispatch on the cause. ErrTransient
// is returned for any condition the orchestrator should retry (network,
// 5xx, decoding failure on success-status, etc.). ErrInvalidSecret and
// ErrRoomNotFound are fatal upstream and should NOT be retried.

var (
	ErrInvalidSecret = errors.New("transport: invalid sender secret (HTTP 403)")
	ErrRoomNotFound  = errors.New("transport: room not found (HTTP 404)")
	ErrTransient     = errors.New("transport: transient error")
)

// --- Client --------------------------------------------------------------

// Client makes HTTP requests to the Fernsicht signaling server.
//
// SenderSecret is empty until OpenSession populates it; afterwards
// it's used in Authorization headers (poll) and request bodies
// (answer, ICE).
type Client struct {
	BaseURL      string
	SenderSecret string

	// HTTP is the underlying HTTP client. Exposed so tests can swap in
	// httptest transports or constrain timeouts. Never nil after New.
	HTTP *http.Client
}

// New returns a Client targeting baseURL with a default 10s HTTP timeout.
// Trailing slashes on baseURL are stripped.
func New(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: DefaultHTTPTimeout},
	}
}

// OpenSession POSTs to /session and parses the response.
//
// On success, the returned Session.SenderSecret is also stored on
// c.SenderSecret for subsequent authenticated calls.
func (c *Client) OpenSession(ctx context.Context, cfg SessionConfig) (*Session, error) {
	url := c.BaseURL + "/session"

	var body io.Reader
	headers := http.Header{}
	headers.Set("Accept", "application/json")
	if cfg.APIKey != "" {
		headers.Set("X-Fernsicht-Api-Key", cfg.APIKey)
	}
	if cfg.MaxViewers > 0 {
		payload, err := json.Marshal(map[string]int{"max_viewers": cfg.MaxViewers})
		if err != nil {
			return nil, fmt.Errorf("marshal session body: %w", err)
		}
		body = bytes.NewReader(payload)
		headers.Set("Content-Type", "application/json")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("build session request: %w", err)
	}
	req.Header = headers

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, wrapTransient(err, "POST /session")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// 4xx from /session is informative — surface the body so SDK
		// authors can see what the server complained about.
		snippet := readSnippet(resp.Body)
		return nil, fmt.Errorf("POST /session: HTTP %d: %s", resp.StatusCode, snippet)
	}

	var raw struct {
		RoomID           string `json:"room_id"`
		SenderSecret     string `json:"sender_secret"`
		ViewerURL        string `json:"viewer_url"`
		SignalingURL     string `json:"signaling_url"`
		ExpiresAt        string `json:"expires_at"`
		ExpiresIn        int    `json:"expires_in"`
		MaxViewers       int    `json:"max_viewers"`
		PollIntervalHint int    `json:"poll_interval_hint"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse /session response: %w", err)
	}
	if raw.RoomID == "" {
		return nil, errors.New("session response missing room_id")
	}
	if raw.SenderSecret == "" {
		return nil, errors.New("session response missing sender_secret")
	}
	if raw.ViewerURL == "" {
		return nil, errors.New("session response missing viewer_url")
	}

	c.SenderSecret = raw.SenderSecret

	return &Session{
		RoomID:           raw.RoomID,
		SenderSecret:     raw.SenderSecret,
		ViewerURL:        raw.ViewerURL,
		SignalingURL:     raw.SignalingURL,
		ExpiresAt:        raw.ExpiresAt,
		ExpiresIn:        raw.ExpiresIn,
		MaxViewers:       raw.MaxViewers,
		PollIntervalHint: raw.PollIntervalHint,
	}, nil
}

// PollTickets calls GET /poll/{roomID} and returns any pending tickets.
//
// Returns an empty slice (not an error) when the server has no
// tickets. Returns ErrInvalidSecret on 403, ErrRoomNotFound on 404,
// ErrTransient on any other non-200 or network error.
func (c *Client) PollTickets(ctx context.Context, roomID string) ([]Ticket, error) {
	url := c.BaseURL + "/poll/" + roomID

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build poll request: %w", err)
	}
	// Bearer auth so the secret never ends up in URLs / proxy logs.
	req.Header.Set("Authorization", "Bearer "+c.SenderSecret)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, wrapTransient(err, "GET /poll")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusForbidden:
		return nil, ErrInvalidSecret
	case http.StatusNotFound:
		return nil, ErrRoomNotFound
	default:
		return nil, fmt.Errorf("%w: GET /poll: HTTP %d", ErrTransient, resp.StatusCode)
	}

	var raw struct {
		Tickets []Ticket `json:"tickets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: parse /poll response: %v", ErrTransient, err)
	}
	if raw.Tickets == nil {
		return []Ticket{}, nil
	}
	return raw.Tickets, nil
}

// PostAnswer sends the sender's SDP answer for ticketID.
//
// Returns ErrInvalidSecret on 403, ErrRoomNotFound on 404,
// ErrTransient on any other non-200.
func (c *Client) PostAnswer(ctx context.Context, ticketID string, answer SessionDescription) error {
	body := map[string]interface{}{
		"answer": answer,
		"secret": c.SenderSecret,
	}
	return c.postJSON(ctx, "/ticket/"+ticketID+"/answer", body)
}

// PostSenderICE sends a batch of the sender's ICE candidates for ticketID.
//
// Calling with an empty slice is a no-op and returns nil (matches
// the Python SDK's _flush_sender_ice early-return).
func (c *Client) PostSenderICE(ctx context.Context, ticketID string, candidates []ICECandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	body := map[string]interface{}{
		"candidates": candidates,
		"secret":     c.SenderSecret,
	}
	return c.postJSON(ctx, "/ticket/"+ticketID+"/ice/sender", body)
}

// PollViewerICE polls for the viewer's ICE candidates that have arrived
// since `since`. Returns the candidates and the new seq the caller
// should pass on the next poll.
//
// On 404, returns ErrRoomNotFound (the ticket was reaped). On other
// errors returns ErrTransient.
func (c *Client) PollViewerICE(ctx context.Context, ticketID string, since int) (*ViewerICEResponse, error) {
	url := c.BaseURL + "/ticket/" + ticketID + "/ice/viewer?since=" + strconv.Itoa(since)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build viewer-ICE request: %w", err)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, wrapTransient(err, "GET /ticket/.../ice/viewer")
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through
	case http.StatusNotFound:
		return nil, ErrRoomNotFound
	default:
		return nil, fmt.Errorf("%w: GET ice/viewer: HTTP %d", ErrTransient, resp.StatusCode)
	}

	var raw struct {
		Candidates []ICECandidate `json:"candidates"`
		Seq        int            `json:"seq"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("%w: parse ice/viewer response: %v", ErrTransient, err)
	}
	if raw.Candidates == nil {
		raw.Candidates = []ICECandidate{}
	}
	return &ViewerICEResponse{Candidates: raw.Candidates, Seq: raw.Seq}, nil
}

// --- Internal helpers ----------------------------------------------------

// postJSON marshals body as JSON and POSTs it to path (relative to
// BaseURL). Returns the same error sentinels as PollTickets.
func (c *Client) postJSON(ctx context.Context, path string, body interface{}) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal POST %s body: %w", path, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build POST %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return wrapTransient(err, "POST "+path)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusForbidden:
		return ErrInvalidSecret
	case http.StatusNotFound:
		return ErrRoomNotFound
	default:
		return fmt.Errorf("%w: POST %s: HTTP %d", ErrTransient, path, resp.StatusCode)
	}
}

// wrapTransient marks any low-level error (network, timeout, connection
// refused, context.DeadlineExceeded) as transient so callers can retry.
// Context cancellation is NOT marked transient — the caller is shutting
// down and should propagate it as-is.
func wrapTransient(err error, op string) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%w: %s: %v", ErrTransient, op, err)
}

// readSnippet reads at most 256 bytes from r as a string for inclusion
// in error messages. Never returns an error; on read failure returns
// an empty string.
func readSnippet(r io.Reader) string {
	buf := make([]byte, 256)
	n, _ := r.Read(buf)
	return strings.TrimSpace(string(buf[:n]))
}

// --- Backoff -------------------------------------------------------------

// Backoff is the §9 transient-failure tracker for the poll loop.
//
// Per the plan:
//
//   - First failure: retry immediately (delay = 0).
//   - Subsequent failures: exponential backoff capped at MaxDelay.
//   - AlertAfter consecutive failures: fire BackoffAlert (SDK should be
//     told a non-fatal error so they know polling is degraded).
//   - FatalAfter sustained: fire BackoffFatal (the orchestrator should
//     trigger graceful close with SIGNALING_UNREACHABLE).
//
// Backoff is NOT goroutine-safe; one Backoff per poll loop.
type Backoff struct {
	InitialDelay time.Duration // first non-zero retry delay (e.g. 1s)
	MaxDelay     time.Duration // cap on exponential growth (60s per §9)
	AlertAfter   int           // consecutive failures before alert (5 per §9)
	FatalAfter   time.Duration // sustained failure window before fatal (5 min per §9)

	failures     int
	firstFailure time.Time
	alertFired   bool
}

// BackoffEvent describes the outcome of RecordFailure.
type BackoffEvent int

const (
	// BackoffRetry means: sleep `delay` then try again.
	BackoffRetry BackoffEvent = iota
	// BackoffAlert means: same as Retry, AND the orchestrator should
	// emit a non-fatal error event to the SDK. Fired once per failure
	// streak (re-armed on success).
	BackoffAlert
	// BackoffFatal means: the failure window has exceeded FatalAfter.
	// Trigger graceful close with SIGNALING_UNREACHABLE. delay is 0.
	BackoffFatal
)

// DefaultBackoff returns a Backoff configured per plan §9 defaults:
// initial 1s, max 60s, alert at 5 consecutive, fatal after 5 minutes.
func DefaultBackoff() *Backoff {
	return &Backoff{
		InitialDelay: 1 * time.Second,
		MaxDelay:     60 * time.Second,
		AlertAfter:   5,
		FatalAfter:   5 * time.Minute,
	}
}

// RecordSuccess clears all failure state. Call after every successful
// transport call.
func (b *Backoff) RecordSuccess() {
	b.failures = 0
	b.firstFailure = time.Time{}
	b.alertFired = false
}

// RecordFailure records a transient failure occurring at `now` and
// returns the next event + sleep duration the caller should respect.
//
// Caller pattern:
//
//	tickets, err := client.PollTickets(ctx, room)
//	if err == nil {
//		bo.RecordSuccess()
//		// ... process tickets ...
//	} else if errors.Is(err, transport.ErrTransient) {
//		ev, d := bo.RecordFailure(time.Now())
//		switch ev {
//		case transport.BackoffAlert: emitNonFatalError(...)
//		case transport.BackoffFatal: triggerClose(...); return
//		}
//		select { case <-time.After(d): case <-ctx.Done(): return }
//	} else {
//		// fatal upstream (ErrInvalidSecret / ErrRoomNotFound)
//	}
func (b *Backoff) RecordFailure(now time.Time) (BackoffEvent, time.Duration) {
	b.failures++
	if b.failures == 1 {
		b.firstFailure = now
	}

	// Has the sustained-failure window crossed FatalAfter?
	if b.FatalAfter > 0 && !b.firstFailure.IsZero() && now.Sub(b.firstFailure) >= b.FatalAfter {
		return BackoffFatal, 0
	}

	delay := b.computeDelay()

	if b.AlertAfter > 0 && b.failures >= b.AlertAfter && !b.alertFired {
		b.alertFired = true
		return BackoffAlert, delay
	}
	return BackoffRetry, delay
}

// computeDelay returns 0 for the first failure (retry-immediately
// per §9) and exponential growth thereafter, capped at MaxDelay.
func (b *Backoff) computeDelay() time.Duration {
	if b.failures <= 1 {
		return 0
	}
	// failure 2 → InitialDelay
	// failure 3 → InitialDelay * 2
	// failure N → InitialDelay * 2^(N-2), capped
	d := b.InitialDelay
	for i := 0; i < b.failures-2; i++ {
		d *= 2
		if d >= b.MaxDelay {
			return b.MaxDelay
		}
	}
	return d
}

// Failures returns the current consecutive-failure count. Used by
// diagnostic dumps (SIGUSR1).
func (b *Backoff) Failures() int { return b.failures }
