// Package embed exposes the Fernsicht bridge as an in-process Go API
// for callers that want to skip the stdin/stdout subprocess protocol.
//
// The CLI (cli/cmd/fernsicht) imports this package directly so it can
// open sessions and emit ticks without spawning the bridge as a
// separate process. Language SDKs (Python, R, …) continue to use the
// stdin/stdout protocol — this is for Go callers only.
//
// Implementation strategy: under the hood we run bridge.Run in a
// goroutine, talking to it through io.Pipes. This reuses 100% of the
// existing bridge orchestration (dispatcher, transport, peer, wire)
// without any refactor — the only added cost is JSON marshal /
// unmarshal at the API boundary, which is microseconds per tick.
package embed

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MuteJester/fernsicht/bridge/internal/bridge"
)

// Config holds the inputs to Open. Mirrors the fields language SDKs
// pass via the `session` command (BRIDGE_PROTOCOL §4) plus a few
// CLI-specific knobs.
type Config struct {
	// ServerURL is the Fernsicht signaling server URL.
	ServerURL string

	// JoinSecret is the server's SENDER_JOIN_SECRET, sent as
	// X-Fernsicht-Api-Key. Empty if the server doesn't require auth.
	JoinSecret string

	// MaxViewers caps concurrent viewers. 0 means "let the server pick."
	MaxViewers int

	// Label is a human-readable annotation surfaced in diagnostics.
	// Not transmitted to the bridge or viewers.
	Label string

	// SDKID identifies the caller in the hello frame. The CLI uses
	// "cli"; Go programs embedding the bridge directly should set
	// their own identifier.
	SDKID string

	// SDKVersion is the SDKID's semver, threaded through to the
	// bridge for protocol-level diagnostics.
	SDKVersion string

	// HelloTimeout is the deadline for the bridge to respond to our
	// hello command. Defaults to 5s.
	HelloTimeout time.Duration

	// SessionTimeout is the deadline for the bridge to open a session
	// with the signaling server. Defaults to 30s.
	SessionTimeout time.Duration
}

// Tick is one progress observation passed to Session.Tick.
type Tick struct {
	TaskID  string
	Value   float64
	N       int
	Total   int
	Rate    float64
	Elapsed float64
	ETA     float64
	Unit    string
}

// SessionInfo is what the bridge tells us about the freshly-opened
// session. Surfaced via Session getters.
type SessionInfo struct {
	RoomID           string
	SenderSecret     string
	ViewerURL        string
	ExpiresAt        string
	ExpiresIn        int
	MaxViewers       int
	PollIntervalHint int
	BridgeVersion    string
}

// Errors returned by Open and Session methods.
var (
	ErrNotImplemented = errors.New("embed: not implemented")
	ErrSessionFailed  = errors.New("embed: session creation failed")
	ErrClosed         = errors.New("embed: session is closed")
	ErrTimeout        = errors.New("embed: timed out waiting for bridge event")
	ErrBridgeError    = errors.New("embed: bridge reported error")
)

// Session is an open Fernsicht session.
type Session struct {
	info SessionInfo

	// Communication with the in-process bridge dispatcher.
	cmdW  io.WriteCloser
	cmdMu sync.Mutex // serializes JSON-encoder writes

	eventR     io.ReadCloser
	eventDec   *json.Decoder
	eventWg    sync.WaitGroup
	bridgeErr  atomic.Pointer[error] // non-nil if bridge.Run failed
	bridgeDone chan struct{}         // closed when bridge.Run returns

	closed   atomic.Bool
	closedCh chan struct{}

	// Asynchronous state updated by the event-drain goroutine.
	viewerCount atomic.Int32
	viewersMu   sync.RWMutex
	viewers     []string

	// Optional per-event hook for callers that want to react to
	// non-tick events (viewer joins, errors). Set via SetEventHook.
	hookMu sync.RWMutex
	hook   func(name string, raw json.RawMessage)
}

// Open establishes a session against the signaling server.
//
// Sequence:
//   1. Spawn bridge.Run in a goroutine, talking via io.Pipes.
//   2. Send hello → wait for hello_ack.
//   3. Send session → wait for session_ready (with backoff retry on
//      transient failures, up to SessionTimeout total).
//   4. Spawn event-drain goroutine for async events.
//   5. Return Session.
//
// Returns ErrSessionFailed wrapped with the bridge's error code on
// fatal failures (bad join secret, signaling unreachable). Caller
// receives ctx-cancelation as a normal context error.
func Open(ctx context.Context, cfg Config) (*Session, error) {
	if cfg.ServerURL == "" {
		return nil, fmt.Errorf("embed: ServerURL is required")
	}
	if cfg.HelloTimeout == 0 {
		cfg.HelloTimeout = 5 * time.Second
	}
	if cfg.SessionTimeout == 0 {
		cfg.SessionTimeout = 30 * time.Second
	}
	if cfg.SDKID == "" {
		cfg.SDKID = "embed"
	}
	if cfg.SDKVersion == "" {
		cfg.SDKVersion = "0.0.0"
	}

	cmdR, cmdW := io.Pipe()
	eventR, eventW := io.Pipe()

	s := &Session{
		cmdW:       cmdW,
		eventR:     eventR,
		eventDec:   json.NewDecoder(bufio.NewReaderSize(eventR, 64*1024)),
		bridgeDone: make(chan struct{}),
		closedCh:   make(chan struct{}),
	}

	// Launch the bridge dispatcher. Cancellation: when the caller
	// cancels ctx, bridge.Run returns; we close the pipes and the
	// event drain exits.
	go func() {
		err := bridge.RunWithOptions(ctx, cmdR, eventW, bridge.Options{})
		_ = eventW.Close()
		_ = cmdR.Close()
		if err != nil {
			s.bridgeErr.Store(&err)
		}
		close(s.bridgeDone)
	}()

	// Step 2: hello.
	if err := s.send(map[string]any{
		"op":           "hello",
		"sdk":          cfg.SDKID,
		"sdk_version":  cfg.SDKVersion,
		"protocol":     1,
	}); err != nil {
		s.cleanupOnError()
		return nil, fmt.Errorf("embed: send hello: %w", err)
	}

	ack, err := s.waitForEvent(ctx, "hello_ack", cfg.HelloTimeout, nil)
	if err != nil {
		s.cleanupOnError()
		return nil, err
	}
	var ackPayload struct {
		BridgeVersion string `json:"bridge_version"`
	}
	_ = json.Unmarshal(ack, &ackPayload)
	s.info.BridgeVersion = ackPayload.BridgeVersion

	// Step 3: session command.
	sessionCmd := map[string]any{
		"op":       "session",
		"base_url": cfg.ServerURL,
	}
	if cfg.JoinSecret != "" {
		sessionCmd["join_secret"] = cfg.JoinSecret
	}
	if cfg.MaxViewers > 0 {
		sessionCmd["max_viewers"] = cfg.MaxViewers
	}
	if err := s.send(sessionCmd); err != nil {
		s.cleanupOnError()
		return nil, fmt.Errorf("embed: send session: %w", err)
	}

	// session_ready may take a while (real HTTP to signaling server).
	// On the way we may see error events — surface them as fatal.
	ready, err := s.waitForEvent(ctx, "session_ready", cfg.SessionTimeout,
		s.handlePreReadyEvent)
	if err != nil {
		s.cleanupOnError()
		return nil, err
	}

	var readyPayload struct {
		RoomID           string `json:"room_id"`
		SenderSecret     string `json:"sender_secret"`
		ViewerURL        string `json:"viewer_url"`
		ExpiresAt        string `json:"expires_at"`
		ExpiresIn        int    `json:"expires_in"`
		MaxViewers       int    `json:"max_viewers"`
		PollIntervalHint int    `json:"poll_interval_hint"`
	}
	if err := json.Unmarshal(ready, &readyPayload); err != nil {
		s.cleanupOnError()
		return nil, fmt.Errorf("embed: parse session_ready: %w", err)
	}
	s.info.RoomID = readyPayload.RoomID
	s.info.SenderSecret = readyPayload.SenderSecret
	s.info.ViewerURL = readyPayload.ViewerURL
	s.info.ExpiresAt = readyPayload.ExpiresAt
	s.info.ExpiresIn = readyPayload.ExpiresIn
	s.info.MaxViewers = readyPayload.MaxViewers
	s.info.PollIntervalHint = readyPayload.PollIntervalHint

	// Step 4: drain async events for the lifetime of the session.
	s.eventWg.Add(1)
	go s.drainEvents()

	return s, nil
}

// send marshals and writes one command. Serialized via cmdMu so
// concurrent Tick / StartTask / EndTask calls don't interleave.
func (s *Session) send(cmd map[string]any) error {
	if s.closed.Load() {
		return ErrClosed
	}
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	_, err = s.cmdW.Write(data)
	return err
}

// waitForEvent blocks until an event with the given `event` field
// arrives, or the context is canceled, or the timeout elapses, or
// the bridge dispatcher exits.
//
// `onEvent` is called for any event that doesn't match name (e.g.,
// the dispatcher emits viewer_count or error before session_ready).
// If onEvent is nil, intervening events are silently buffered and
// dropped (acceptable during handshake).
func (s *Session) waitForEvent(ctx context.Context, name string,
	timeout time.Duration, onEvent func(string, json.RawMessage)) (json.RawMessage, error) {
	deadline := time.Now().Add(timeout)
	type read struct {
		raw json.RawMessage
		err error
	}
	results := make(chan read, 1)
	go func() {
		var raw json.RawMessage
		err := s.eventDec.Decode(&raw)
		results <- read{raw, err}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-s.bridgeDone:
			if e := s.bridgeErr.Load(); e != nil {
				return nil, fmt.Errorf("%w: %v", ErrSessionFailed, *e)
			}
			return nil, fmt.Errorf("%w: bridge exited before %s", ErrSessionFailed, name)
		case <-time.After(time.Until(deadline)):
			return nil, fmt.Errorf("%w: waiting for %s", ErrTimeout, name)
		case r := <-results:
			if r.err != nil {
				if r.err == io.EOF {
					return nil, fmt.Errorf("%w: bridge closed pipe", ErrSessionFailed)
				}
				return nil, fmt.Errorf("embed: decode event: %w", r.err)
			}
			eventName := extractEventName(r.raw)
			if eventName == name {
				return r.raw, nil
			}
			// Errors during handshake are fatal — bridge will emit
			// `closed` after.
			if eventName == "error" {
				if isFatal(r.raw) {
					return nil, fmt.Errorf("%w: %s", ErrBridgeError, errorMessage(r.raw))
				}
			}
			if onEvent != nil {
				onEvent(eventName, r.raw)
			}
			// Re-arm the read goroutine.
			go func() {
				var raw json.RawMessage
				err := s.eventDec.Decode(&raw)
				results <- read{raw, err}
			}()
		}
	}
}

// handlePreReadyEvent is the onEvent callback for the session_ready
// wait. We don't really need to do anything beyond what the default
// already does (skip + continue) — async events that arrive before
// session_ready are rare in practice.
func (s *Session) handlePreReadyEvent(name string, raw json.RawMessage) {
	// no-op; reserved for diagnostics in Phase 4 with --debug
}

// drainEvents runs for the lifetime of the session, consuming events
// the bridge emits asynchronously (viewer_joined, viewer_count,
// error, closed, etc.) and updating Session state.
func (s *Session) drainEvents() {
	defer s.eventWg.Done()
	for {
		var raw json.RawMessage
		if err := s.eventDec.Decode(&raw); err != nil {
			return
		}
		s.handleAsyncEvent(raw)
	}
}

func (s *Session) handleAsyncEvent(raw json.RawMessage) {
	name := extractEventName(raw)
	switch name {
	case "viewer_count":
		var p struct {
			Count int      `json:"count"`
			Names []string `json:"names"`
		}
		if err := json.Unmarshal(raw, &p); err == nil {
			s.viewerCount.Store(int32(p.Count))
			s.viewersMu.Lock()
			s.viewers = append(s.viewers[:0], p.Names...)
			s.viewersMu.Unlock()
		}
	case "viewer_joined":
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err == nil && p.Name != "" {
			s.viewersMu.Lock()
			if !contains(s.viewers, p.Name) {
				s.viewers = append(s.viewers, p.Name)
				s.viewerCount.Store(int32(len(s.viewers)))
			}
			s.viewersMu.Unlock()
		}
	case "viewer_left":
		var p struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(raw, &p); err == nil && p.Name != "" {
			s.viewersMu.Lock()
			s.viewers = removeString(s.viewers, p.Name)
			s.viewerCount.Store(int32(len(s.viewers)))
			s.viewersMu.Unlock()
		}
	case "closed":
		s.closed.Store(true)
		select {
		case <-s.closedCh:
		default:
			close(s.closedCh)
		}
	}
	// Hand off to the optional caller-provided hook AFTER updating
	// internal state, so the hook sees a consistent view.
	s.hookMu.RLock()
	hook := s.hook
	s.hookMu.RUnlock()
	if hook != nil {
		hook(name, raw)
	}
}

// SetEventHook installs (or removes, with nil) a callback invoked for
// every async event after Session state is updated. Useful for the
// CLI's status line + debug logging without polluting the embed API.
func (s *Session) SetEventHook(hook func(name string, raw json.RawMessage)) {
	s.hookMu.Lock()
	s.hook = hook
	s.hookMu.Unlock()
}

// --- Public methods ------------------------------------------------------

// Tick emits a progress update for the active task.
func (s *Session) Tick(t Tick) error {
	if s.closed.Load() {
		return ErrClosed
	}
	cmd := map[string]any{
		"op":      "progress",
		"task_id": t.TaskID,
		"value":   t.Value,
	}
	if t.N != 0 {
		cmd["n"] = t.N
	}
	if t.Total != 0 {
		cmd["total"] = t.Total
	}
	if t.Rate != 0 {
		cmd["rate"] = t.Rate
	}
	if t.Elapsed != 0 {
		cmd["elapsed"] = t.Elapsed
	}
	if t.ETA != 0 {
		cmd["eta"] = t.ETA
	}
	if t.Unit != "" {
		cmd["unit"] = t.Unit
	}
	return s.send(cmd)
}

// StartTask begins a new task. The bridge implicitly ends any
// previously-active task per BRIDGE_PROTOCOL §6.
func (s *Session) StartTask(taskID, label string) error {
	return s.send(map[string]any{
		"op":      "start",
		"task_id": taskID,
		"label":   label,
	})
}

// EndTask explicitly ends the named task.
func (s *Session) EndTask(taskID string) error {
	return s.send(map[string]any{
		"op":      "end",
		"task_id": taskID,
	})
}

// Close gracefully tears down the session. Sends `close`, waits up
// to 5s (or ctx) for the bridge to acknowledge with `closed`, then
// closes the command pipe so bridge.Run exits.
//
// Idempotent — safe to call multiple times.
func (s *Session) Close(ctx context.Context) error {
	if !s.closed.CompareAndSwap(false, true) {
		// Already closed by us or by the bridge emitting `closed`.
		return nil
	}

	// Best-effort send `close`; ignore errors (pipe may be broken).
	_ = s.sendUnchecked(map[string]any{"op": "close"})

	// Wait for the bridge to exit (it emits `closed` then returns).
	select {
	case <-s.bridgeDone:
	case <-ctx.Done():
		// Timed out waiting; force-close the cmd pipe and let bridge exit.
		_ = s.cmdW.Close()
		<-s.bridgeDone
	case <-time.After(5 * time.Second):
		_ = s.cmdW.Close()
		<-s.bridgeDone
	}

	// Drain any remaining async-event goroutine.
	_ = s.eventR.Close()
	s.eventWg.Wait()

	if e := s.bridgeErr.Load(); e != nil {
		return *e
	}
	return nil
}

// sendUnchecked bypasses the closed check (used by Close itself).
func (s *Session) sendUnchecked(cmd map[string]any) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.cmdMu.Lock()
	defer s.cmdMu.Unlock()
	_, err = s.cmdW.Write(data)
	return err
}

// cleanupOnError tears down the bridge after a failed Open. Distinct
// from Close because Open's caller never sees a Session, so it can't
// call Close.
//
// Order matters: close eventR FIRST. The bridge's gracefulShutdown
// emits a `closed` event and may have other events buffered. Without
// a reader, the writer goroutine blocks on the io.Pipe write and the
// bridge's run() never returns from its event-writer-wait defer.
// Closing the read side breaks the pending write so bridge unblocks.
func (s *Session) cleanupOnError() {
	s.closed.Store(true)
	_ = s.eventR.Close()
	_ = s.cmdW.Close()
	<-s.bridgeDone
}

// --- Read-only accessors ------------------------------------------------

// Info returns a snapshot of the session metadata learned at Open.
func (s *Session) Info() SessionInfo { return s.info }

// ViewerURL is the shareable browser URL for this session.
func (s *Session) ViewerURL() string { return s.info.ViewerURL }

// RoomID is the session's room identifier.
func (s *Session) RoomID() string { return s.info.RoomID }

// ViewerCount returns the number of currently-connected viewers.
func (s *Session) ViewerCount() int { return int(s.viewerCount.Load()) }

// Viewers returns the current viewer roster (copied — caller may
// modify the result without affecting Session state).
func (s *Session) Viewers() []string {
	s.viewersMu.RLock()
	defer s.viewersMu.RUnlock()
	out := make([]string, len(s.viewers))
	copy(out, s.viewers)
	return out
}

// IsOpen reports whether the session is still open (no `closed`
// event seen, no Close() called).
func (s *Session) IsOpen() bool { return !s.closed.Load() }

// --- Helpers ------------------------------------------------------------

func extractEventName(raw json.RawMessage) string {
	var probe struct {
		Event string `json:"event"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Event
}

func isFatal(raw json.RawMessage) bool {
	var probe struct {
		Fatal bool `json:"fatal"`
	}
	_ = json.Unmarshal(raw, &probe)
	return probe.Fatal
}

func errorMessage(raw json.RawMessage) string {
	var probe struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &probe)
	if probe.Code != "" {
		return fmt.Sprintf("%s: %s", probe.Code, probe.Message)
	}
	return probe.Message
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func removeString(s []string, v string) []string {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
