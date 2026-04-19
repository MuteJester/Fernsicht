// Package bridge wires the proto / transport / peer / wire subsystems
// into a single Run loop driven by stdin (commands in) and stdout
// (events out).
//
// Public entry point:
//
//	bridge.Run(ctx, in, out) error
//
// Returns when stdin reaches EOF, the SDK sends `close`, the context
// is canceled, or a fatal error occurs. main.go is a 30-line wrapper
// that calls Run with os.Stdin / os.Stdout (Phase 6).
//
// Tests can use RunWithOptions to swap in mock transport / peer
// subsystems without spinning up real WebRTC or HTTP. See
// bridge_test.go.
//
// Concurrency model (per .private/BRIDGE_IMPLEMENTATION_PLAN.md §7):
//
//	Goroutines:
//	  Run                — the dispatcher (sole owner of session state)
//	  stdinReader        — parses lines into proto.Command, sends on cmds
//	  pollLoop (1)       — when session is open, polls for viewer tickets
//	  peer.Manager.*     — pion callbacks fire on its internal goroutines
//
//	Channels (all bounded; sender blocks if full):
//	  cmds         — stdinReader → dispatcher          (buffer 256)
//	  peerEvents   — peer.Manager → dispatcher          (buffer 64)
//	  tickets      — pollLoop → dispatcher              (buffer 16)
//	  fatalCh      — any goroutine → dispatcher         (buffer 1)
//
// Stdout writes go through eventWriter (a single goroutine) reading
// from a separate writes channel — so command-handling and event
// emission never race on the wire.
package bridge

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/MuteJester/fernsicht/bridge/internal/peer"
	"github.com/MuteJester/fernsicht/bridge/internal/transport"
	"github.com/pion/webrtc/v4"
)

// Version is overridden at build time via -ldflags; tests use
// "bridge-test" through RunWithOptions.
const Version = "0.1.0-dev"

// Error sentinels — main.go maps these to the §4.6 exit codes:
//
//	ErrProtocolMismatch  → exit 4
//	ErrSessionFailed     → exit 3
//	ErrFatal             → exit 1 (catch-all for runtime fatals)
//
// Returned errors wrap these (use errors.Is to match).
var (
	ErrProtocolMismatch = errors.New("bridge: SDK protocol version mismatch")
	ErrSessionFailed    = errors.New("bridge: session creation failed")
	ErrFatal            = errors.New("bridge: fatal runtime error")
)

// Tunables sized to match the Python SDK transport's PUBLISH_INTERVAL
// / HEARTBEAT_INTERVAL, plus the §8 graceful-close budget.
const (
	publishInterval      = 500 * time.Millisecond
	drainInterval        = 100 * time.Millisecond
	cullInterval         = 5 * time.Second
	defaultPollInterval  = 25 * time.Second
	gracefulCloseBudget  = 2 * time.Second
	commandChanBuffer    = 256
	peerEventsChanBuffer = 64
	ticketsChanBuffer    = 16
	eventWritesBuffer    = 64
)

// --- Dependency seams (interfaces for testability) ----------------------

// transportClient is the subset of *transport.Client the dispatcher
// uses. Defined here so tests can substitute a mock without touching
// real HTTP.
type transportClient interface {
	OpenSession(ctx context.Context, cfg transport.SessionConfig) (*transport.Session, error)
	PollTickets(ctx context.Context, roomID string) ([]transport.Ticket, error)
	PostAnswer(ctx context.Context, ticketID string, answer transport.SessionDescription) error
	PostSenderICE(ctx context.Context, ticketID string, candidates []transport.ICECandidate) error
	PollViewerICE(ctx context.Context, ticketID string, since int) (*transport.ViewerICEResponse, error)
	// SetSenderSecret lets the dispatcher propagate the secret learned
	// from OpenSession back into the client (so subsequent calls use
	// it). Implementing types are expected to be the real
	// *transport.Client (which exposes a SenderSecret field) or a
	// mock; both can implement this trivially.
	SetSenderSecret(secret string)
}

// peerManager is the subset of *peer.Manager the dispatcher uses.
// Mockable for tests that don't need real WebRTC handshakes.
type peerManager interface {
	Add(ctx context.Context, ticket transport.Ticket)
	Broadcast(frame string) int
	SessionFrames(frames []string) int
	Drain() int
	Names() []string
	Count() int
	Cull(now time.Time) []string
	Close(ctx context.Context, drainTimeout time.Duration) error
}

// transportFactory builds a transportClient for the given base URL.
// In production this returns a *realTransportClient wrapping
// transport.New; tests pass a custom factory.
type transportFactory func(baseURL string) transportClient

// peerManagerFactory builds a peerManager wired to the given
// transport client and event channel. In production this returns a
// *peer.Manager; tests pass a mock.
type peerManagerFactory func(client peer.TransportClient, iceServers []webrtc.ICEServer, events chan<- peer.Event) peerManager

// --- Public entry points ------------------------------------------------

// Run drives the bridge end-to-end with default subsystems (real HTTP
// transport, real pion peer manager). Returns when:
//
//   - stdin reaches EOF
//   - the SDK sends `{"op":"close"}`
//   - ctx is canceled (e.g. SIGINT/SIGTERM)
//   - a fatal error occurs (session creation failed, signaling
//     unreachable for >5 minutes, internal panic)
//
// All cleanup (peer connections closed, final `closed` event emitted)
// happens before Run returns.
func Run(ctx context.Context, in io.Reader, out io.Writer) error {
	return RunWithOptions(ctx, in, out, Options{})
}

// Options controls the subsystems Run uses. Empty zero-value Options
// produces production defaults.
type Options struct {
	// Version overrides the bridge version reported in hello_ack.
	// Empty string uses the package-level Version constant.
	Version string

	// TransportFactory builds the transport.Client. Empty defaults to
	// the real one wrapping transport.New.
	TransportFactory transportFactory

	// PeerManagerFactory builds the peer.Manager. Empty defaults to
	// the real one wrapping peer.NewManager.
	PeerManagerFactory peerManagerFactory

	// ICEServers is passed to the peer.Manager. Nil defaults to the
	// Google public STUN servers (matching the Python SDK).
	ICEServers []webrtc.ICEServer

	// NowFn returns the current time, used for backoff and cull
	// timing. Nil defaults to time.Now. Tests use a synthetic clock.
	NowFn func() time.Time
}

// RunWithOptions is the testable form of Run. main.go uses Run().
func RunWithOptions(ctx context.Context, in io.Reader, out io.Writer, opts Options) error {
	d := newDispatcher(in, out, opts)
	return d.run(ctx)
}

// --- Real implementations of the dependency seams -----------------------

// realTransportClient adapts *transport.Client to the dispatcher's
// transportClient interface (extra SetSenderSecret method).
type realTransportClient struct {
	*transport.Client
}

func (r *realTransportClient) SetSenderSecret(secret string) {
	r.Client.SenderSecret = secret
}

// defaultTransportFactory is used when Options.TransportFactory is nil.
func defaultTransportFactory(baseURL string) transportClient {
	return &realTransportClient{Client: transport.New(baseURL)}
}

// defaultPeerManagerFactory is used when Options.PeerManagerFactory is nil.
func defaultPeerManagerFactory(client peer.TransportClient, iceServers []webrtc.ICEServer, events chan<- peer.Event) peerManager {
	return peer.NewManager(client, iceServers, events)
}

// defaultICEServers mirrors the Python SDK's STUN_SERVERS list.
func defaultICEServers() []webrtc.ICEServer {
	return []webrtc.ICEServer{
		{URLs: []string{"stun:stun.l.google.com:19302"}},
		{URLs: []string{"stun:stun1.l.google.com:19302"}},
	}
}
