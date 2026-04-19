// Package proto defines the JSON command/event types used between the
// language SDK (stdin/stdout) and the bridge.
//
// The wire shape of every message is one JSON object per line. Commands
// flow SDK→bridge on stdin; events flow bridge→SDK on stdout. The
// authoritative protocol spec lives in
// .private/BRIDGE_IMPLEMENTATION_PLAN.md §4.
//
// This package owns:
//   - Typed structs for every command and event.
//   - ParseCommand(line) — strict parser that rejects malformed input,
//     unknown ops, and missing required fields.
//   - WriteEventLine(w, e) — marshals an event and appends '\n'.
//   - OrderingValidator — single-goroutine state machine enforcing
//     §4.2 (hello → session → tasks → close) and §4.5 edge cases.
//
// This package does NOT touch I/O directly (no os.Stdin/Stdout
// references) and is fully unit-testable with bytes.Buffer / io.Pipe.
package proto

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SupportedProtocolVersion is the protocol version this bridge speaks.
// Bumped on any breaking change to the JSON shape (§4.1).
const SupportedProtocolVersion = 1

// MaxLineLength is the per-line limit for stdin (§4.1, §9). Longer
// lines are rejected with code LINE_TOO_LONG. The reader (in main.go)
// is responsible for enforcing this on the bufio.Scanner; this constant
// is exported so the reader and tests share the same number.
const MaxLineLength = 64 * 1024

// --- Error codes (§9) -----------------------------------------------------

const (
	ErrCodeProtocolMismatch     = "PROTOCOL_VERSION_MISMATCH"
	ErrCodeInvalidCommand       = "INVALID_COMMAND"
	ErrCodeLineTooLong          = "LINE_TOO_LONG"
	ErrCodeNoActiveTask         = "NO_ACTIVE_TASK"
	ErrCodeSessionFailed        = "SESSION_FAILED"
	ErrCodeSessionExpired       = "SESSION_EXPIRED"
	ErrCodeSignalingUnreachable = "SIGNALING_UNREACHABLE"
	ErrCodeTicketHandlingFailed = "TICKET_HANDLING_FAILED"
	ErrCodeInternal             = "INTERNAL"
)

// --- Close reasons (§4.4) -------------------------------------------------

const (
	CloseReasonSDKClose   = "sdk_close"
	CloseReasonStdinEOF   = "stdin_eof"
	CloseReasonSignal     = "signal"
	CloseReasonFatalError = "fatal_error"
)

// --- Commands (SDK → bridge) ---------------------------------------------

// Command is the sealed sum type for every SDK→bridge command.
// The unexported method makes it impossible for outside packages to
// add new variants by accident.
type Command interface {
	// Op returns the wire-level "op" identifier for this command.
	Op() string
	isCommand()
}

type HelloCmd struct {
	SDK        string `json:"sdk"`
	SDKVersion string `json:"sdk_version"`
	Protocol   int    `json:"protocol"`
}

func (HelloCmd) Op() string { return "hello" }
func (HelloCmd) isCommand() {}

type SessionCmd struct {
	BaseURL            string `json:"base_url"`
	JoinSecret         string `json:"join_secret,omitempty"`
	MaxViewers         int    `json:"max_viewers,omitempty"`
	SessionTokenTTLSec int    `json:"session_token_ttl_sec,omitempty"`
}

func (SessionCmd) Op() string { return "session" }
func (SessionCmd) isCommand() {}

type StartCmd struct {
	TaskID string `json:"task_id"`
	Label  string `json:"label"`
}

func (StartCmd) Op() string { return "start" }
func (StartCmd) isCommand() {}

// ProgressCmd carries a P| frame's data. Optional stats are pointers
// so the bridge can distinguish "field omitted" from "field is zero",
// since the wire format uses "-" for omitted vs the formatted number
// for explicit zeros.
//
// Value uses *float64 too so the parser can reject a missing `value`
// (it's required) without confusing it with an explicit 0.0.
type ProgressCmd struct {
	TaskID  string   `json:"task_id"`
	Value   *float64 `json:"value"`
	N       *int     `json:"n,omitempty"`
	Total   *int     `json:"total,omitempty"`
	Rate    *float64 `json:"rate,omitempty"`
	Elapsed *float64 `json:"elapsed,omitempty"`
	ETA     *float64 `json:"eta,omitempty"`
	Unit    string   `json:"unit,omitempty"`
}

func (ProgressCmd) Op() string { return "progress" }
func (ProgressCmd) isCommand() {}

type EndCmd struct {
	TaskID string `json:"task_id"`
}

func (EndCmd) Op() string { return "end" }
func (EndCmd) isCommand() {}

type CloseCmd struct{}

func (CloseCmd) Op() string { return "close" }
func (CloseCmd) isCommand() {}

type PingCmd struct {
	ID string `json:"id"`
}

func (PingCmd) Op() string { return "ping" }
func (PingCmd) isCommand() {}

// --- ParseCommand --------------------------------------------------------

// ParseCommand parses one JSON line and returns the typed Command.
//
// Returns a descriptive error on:
//   - empty / whitespace-only input
//   - malformed JSON
//   - missing or empty `op` field
//   - unknown `op` value
//   - missing required fields for the chosen op
//
// All errors are intended to be surfaced to the SDK as
// {"event":"error","code":"INVALID_COMMAND",...} (§4.4 / §9).
func ParseCommand(line []byte) (Command, error) {
	line = trimLine(line)
	if len(line) == 0 {
		return nil, errors.New("empty command line")
	}

	var env struct {
		Op string `json:"op"`
	}
	if err := json.Unmarshal(line, &env); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if env.Op == "" {
		return nil, errors.New("missing 'op' field")
	}

	switch env.Op {
	case "hello":
		var c HelloCmd
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid hello: %w", err)
		}
		if c.SDK == "" {
			return nil, errors.New("hello: 'sdk' is required")
		}
		if c.SDKVersion == "" {
			return nil, errors.New("hello: 'sdk_version' is required")
		}
		if c.Protocol < 1 {
			return nil, errors.New("hello: 'protocol' must be >= 1")
		}
		return c, nil

	case "session":
		var c SessionCmd
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid session: %w", err)
		}
		if c.BaseURL == "" {
			return nil, errors.New("session: 'base_url' is required")
		}
		if c.MaxViewers < 0 {
			return nil, errors.New("session: 'max_viewers' must be >= 0")
		}
		if c.SessionTokenTTLSec < 0 {
			return nil, errors.New("session: 'session_token_ttl_sec' must be >= 0")
		}
		return c, nil

	case "start":
		var c StartCmd
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid start: %w", err)
		}
		if c.TaskID == "" {
			return nil, errors.New("start: 'task_id' is required")
		}
		if c.Label == "" {
			return nil, errors.New("start: 'label' is required")
		}
		return c, nil

	case "progress":
		var c ProgressCmd
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid progress: %w", err)
		}
		if c.TaskID == "" {
			return nil, errors.New("progress: 'task_id' is required")
		}
		if c.Value == nil {
			return nil, errors.New("progress: 'value' is required")
		}
		return c, nil

	case "end":
		var c EndCmd
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid end: %w", err)
		}
		if c.TaskID == "" {
			return nil, errors.New("end: 'task_id' is required")
		}
		return c, nil

	case "close":
		var c CloseCmd
		// CloseCmd has no fields; we still unmarshal to catch malformed
		// JSON whose envelope happened to contain a valid op.
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid close: %w", err)
		}
		return c, nil

	case "ping":
		var c PingCmd
		if err := json.Unmarshal(line, &c); err != nil {
			return nil, fmt.Errorf("invalid ping: %w", err)
		}
		// id is optional; an empty ping is valid (caller may not care
		// about correlation).
		return c, nil
	}

	return nil, fmt.Errorf("unknown op: %q", env.Op)
}

func trimLine(line []byte) []byte {
	// Strip ASCII whitespace from both ends so trailing \r\n or stray
	// padding doesn't trip the JSON decoder.
	for len(line) > 0 && (line[0] == ' ' || line[0] == '\t' || line[0] == '\r' || line[0] == '\n') {
		line = line[1:]
	}
	for len(line) > 0 && (line[len(line)-1] == ' ' || line[len(line)-1] == '\t' || line[len(line)-1] == '\r' || line[len(line)-1] == '\n') {
		line = line[:len(line)-1]
	}
	return line
}

// --- Events (bridge → SDK) -----------------------------------------------
//
// Each event is a struct with an "event" discriminator field set to a
// constant by its constructor. The bridge calls WriteEventLine to
// serialize and emit. There is no parsing path for events on the
// bridge side — only writing.

type HelloAckEvent struct {
	Event         string `json:"event"`
	BridgeVersion string `json:"bridge_version"`
	Protocol      int    `json:"protocol"`
}

func NewHelloAck(bridgeVersion string) HelloAckEvent {
	return HelloAckEvent{
		Event:         "hello_ack",
		BridgeVersion: bridgeVersion,
		Protocol:      SupportedProtocolVersion,
	}
}

type SessionReadyEvent struct {
	Event            string `json:"event"`
	RoomID           string `json:"room_id"`
	SenderSecret     string `json:"sender_secret"`
	ViewerURL        string `json:"viewer_url"`
	ExpiresAt        string `json:"expires_at"`
	ExpiresIn        int    `json:"expires_in"`
	MaxViewers       int    `json:"max_viewers"`
	PollIntervalHint int    `json:"poll_interval_hint"`
}

func NewSessionReady(roomID, senderSecret, viewerURL, expiresAt string, expiresIn, maxViewers, pollIntervalHint int) SessionReadyEvent {
	return SessionReadyEvent{
		Event:            "session_ready",
		RoomID:           roomID,
		SenderSecret:     senderSecret,
		ViewerURL:        viewerURL,
		ExpiresAt:        expiresAt,
		ExpiresIn:        expiresIn,
		MaxViewers:       maxViewers,
		PollIntervalHint: pollIntervalHint,
	}
}

type ViewerJoinedEvent struct {
	Event string `json:"event"`
	Name  string `json:"name"`
}

func NewViewerJoined(name string) ViewerJoinedEvent {
	return ViewerJoinedEvent{Event: "viewer_joined", Name: name}
}

type ViewerLeftEvent struct {
	Event string `json:"event"`
	Name  string `json:"name"`
}

func NewViewerLeft(name string) ViewerLeftEvent {
	return ViewerLeftEvent{Event: "viewer_left", Name: name}
}

type ViewerCountEvent struct {
	Event string   `json:"event"`
	Count int      `json:"count"`
	Names []string `json:"names"`
}

// NewViewerCount normalizes nil/empty Names to [] (not null) so SDK
// consumers can rely on a JSON array always being present.
func NewViewerCount(names []string) ViewerCountEvent {
	if names == nil {
		names = []string{}
	}
	return ViewerCountEvent{
		Event: "viewer_count",
		Count: len(names),
		Names: names,
	}
}

type PongEvent struct {
	Event string `json:"event"`
	ID    string `json:"id"`
}

func NewPong(id string) PongEvent {
	return PongEvent{Event: "pong", ID: id}
}

type ErrorEvent struct {
	Event   string `json:"event"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Fatal   bool   `json:"fatal"`
}

func NewError(code, message string, fatal bool) ErrorEvent {
	return ErrorEvent{Event: "error", Code: code, Message: message, Fatal: fatal}
}

type ClosedEvent struct {
	Event  string `json:"event"`
	Reason string `json:"reason"`
}

func NewClosed(reason string) ClosedEvent {
	return ClosedEvent{Event: "closed", Reason: reason}
}

// WriteEventLine marshals e as JSON and writes it followed by a single
// newline. Returns an error if marshaling or writing fails.
//
// e SHOULD be one of the *Event types in this package; the function is
// a thin wrapper around json.Marshal so any JSON-marshalable value
// works, but using an unrecognized type means the SDK won't get the
// event-discriminator field.
func WriteEventLine(w io.Writer, e any) error {
	data, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if _, err := w.Write([]byte{'\n'}); err != nil {
		return fmt.Errorf("write event newline: %w", err)
	}
	return nil
}

// --- OrderingValidator ----------------------------------------------------
//
// Enforces the protocol state machine from §4.2 + the edge cases from
// §4.5. NOT goroutine-safe — the dispatcher owns one instance and is
// the only mutator (per §7 ownership rules).
//
// Usage:
//
//	cmd, err := proto.ParseCommand(line)
//	if err != nil { /* INVALID_COMMAND */ }
//	if err := v.Validate(cmd); err != nil {
//		if errors.Is(err, proto.ErrSilentDrop) {
//			// During close: drop without emitting noise.
//			continue
//		}
//		// Otherwise: emit INVALID_COMMAND error event.
//	}
//	v.Mark(cmd)
//	// ... actually handle cmd ...

// OrderingValidator tracks where in the §4.2 state machine the SDK is.
type OrderingValidator struct {
	helloDone   bool
	sessionOpen bool
	closing     bool
}

// NewOrderingValidator returns a fresh validator in the initial state.
func NewOrderingValidator() *OrderingValidator {
	return &OrderingValidator{}
}

// ErrSilentDrop is returned when a command should be dropped without
// emitting an error event — specifically, post-close commands per
// §4.5 row "progress arrives during graceful close" / "close arrives
// more than once".
var ErrSilentDrop = errors.New("silent drop (bridge closing)")

// Validate returns nil if cmd is allowed in the current state.
// Otherwise it returns a descriptive error that the caller surfaces as
// an INVALID_COMMAND event (or as a silent drop if errors.Is matches
// ErrSilentDrop).
func (v *OrderingValidator) Validate(cmd Command) error {
	// ping is always allowed (even before hello, even after close).
	if _, ok := cmd.(PingCmd); ok {
		return nil
	}

	// During graceful close, ignore everything else silently.
	if v.closing {
		return ErrSilentDrop
	}

	// Pre-hello: only hello allowed (ping handled above).
	if !v.helloDone {
		if _, ok := cmd.(HelloCmd); !ok {
			return fmt.Errorf("command %q sent before hello", cmd.Op())
		}
		return nil
	}

	// Post-hello cases:
	switch cmd.(type) {
	case HelloCmd:
		return errors.New("hello sent more than once")
	case SessionCmd:
		if v.sessionOpen {
			return errors.New("session already open")
		}
		return nil
	case CloseCmd:
		// Close allowed any time after hello (even before session opens).
		return nil
	default:
		// Task ops require an open session.
		if !v.sessionOpen {
			return fmt.Errorf("command %q sent before session", cmd.Op())
		}
		return nil
	}
}

// Mark records that cmd was successfully validated and accepted.
// Caller MUST only call Mark after Validate returned nil and after
// the command was actually processed (not for silent drops).
func (v *OrderingValidator) Mark(cmd Command) {
	switch cmd.(type) {
	case HelloCmd:
		v.helloDone = true
	case SessionCmd:
		v.sessionOpen = true
	case CloseCmd:
		v.closing = true
	}
}

// State returns a human-readable snapshot of the current validator
// state. Used in diagnostic dumps (SIGUSR1) and tests.
func (v *OrderingValidator) State() string {
	parts := []string{}
	if v.helloDone {
		parts = append(parts, "hello_done")
	}
	if v.sessionOpen {
		parts = append(parts, "session_open")
	}
	if v.closing {
		parts = append(parts, "closing")
	}
	if len(parts) == 0 {
		return "initial"
	}
	return strings.Join(parts, "+")
}
