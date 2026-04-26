// Package peer implements WebRTC DataChannel management for the
// Fernsicht bridge. It is a port of the per-viewer state machinery in
// publishers/python/src/fernsicht/_transport.py — same lifecycle, same
// ICE flow, same disconnect grace period — but rebuilt against
// pion/webrtc/v4 with explicit Go concurrency primitives.
//
// Shape:
//
//	Manager — owns N viewerPeers, exposes goroutine-safe methods to the
//	          orchestrator (Add, Broadcast, Drain, Cull, Names, Close).
//	          Emits Events on a caller-provided channel for state changes
//	          (HELLO arrived, viewer left, handshake failed).
//
//	viewerPeer — one entry per ticket. Holds the pion *PeerConnection,
//	             the DataChannel (when it arrives), the per-viewer
//	             outbound queue, presence name, and disconnect timestamp.
//
// The handshake flow per ticket (see Add → handleTicket):
//
//	1. Create *PeerConnection with our STUN config.
//	2. Register OnDataChannel / OnICECandidate / OnConnectionStateChange.
//	3. SetRemoteDescription with the viewer's offer.
//	4. CreateAnswer + SetLocalDescription.
//	5. Wait for ICE gathering complete (≤3s).
//	6. POST answer via transport.
//	7. Flush any sender-side ICE that was buffered during gather.
//	8. Wait briefly (≤1s) for the DataChannel to materialize.
//	9. Wire DataChannel callbacks (OnOpen / OnMessage / OnClose).
//	10. Spawn a background ICE-poll loop that adds the viewer's
//	    candidates as they arrive (capped at ICEPollMaxDuration or
//	    until the connection is established).
//
// Threading note: pion fires its callbacks from internal goroutines.
// All mutation of viewerPeer state happens behind viewerPeer.mu.
// Manager.peers is guarded by Manager.mu. Pion's own send paths are
// internally synchronized.
package peer

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MuteJester/fernsicht/bridge/internal/transport"
	"github.com/pion/webrtc/v4"
)

// --- Tunables ------------------------------------------------------------

const (
	// DataChannelLabel is what the viewer's DataChannel must be named.
	// Matches the existing Python SDK and frontend.
	DataChannelLabel = "fernsicht"

	// DisconnectGrace is how long a peer can sit in the "disconnected"
	// connection state before Cull removes it. Matches Python SDK's 20s.
	DisconnectGrace = 20 * time.Second

	// ICEGatherTimeout caps how long we wait for ICE gathering to
	// complete before posting the answer. Matches Python SDK's 3s.
	ICEGatherTimeout = 3 * time.Second

	// ICEPollMaxDuration caps how long we poll for the viewer's ICE
	// candidates. We keep this long enough for mobile handshakes that
	// need extra NAT traversal time.
	ICEPollMaxDuration = 30 * time.Second

	// ICEPollInterval is the gap between viewer-ICE polls. Matches
	// Python SDK's ICE_POLL_INTERVAL (500ms).
	ICEPollInterval = 500 * time.Millisecond

	// DataChannelArriveTimeout is how long we wait after posting the
	// answer for the viewer's DataChannel to materialize before giving
	// up on the ticket. Matches Python SDK's 20×0.05s = 1s.
	DataChannelArriveTimeout = 1 * time.Second

	// HelloMaxNameLen caps viewer-supplied names from the HELLO frame.
	HelloMaxNameLen = 32
)

// --- Events --------------------------------------------------------------

// EventType discriminates the Event union.
type EventType int

const (
	// EventViewerJoined fires once a viewer's HELLO frame arrives.
	// Manager.Names() will include this viewer afterwards.
	EventViewerJoined EventType = iota
	// EventViewerLeft fires when a viewer's connection ends — either
	// the DataChannel closes or Cull removes a stale peer.
	EventViewerLeft
	// EventViewerError fires when the per-ticket handshake fails. The
	// ticket is dropped; no peer is registered.
	EventViewerError
)

// Event is the payload sent on Manager's events channel for any
// state change worth telling the orchestrator about.
type Event struct {
	Type     EventType
	TicketID string
	Name     string // populated for Joined / Left
	Err      error  // populated for Error
}

// --- TransportClient interface ------------------------------------------

// TransportClient is the subset of *transport.Client that Manager
// depends on. Defined here so peer_test.go can swap in a loopback
// fake without spinning up a real signaling server.
type TransportClient interface {
	PostAnswer(ctx context.Context, ticketID string, answer transport.SessionDescription) error
	PostSenderICE(ctx context.Context, ticketID string, candidates []transport.ICECandidate) error
	PollViewerICE(ctx context.Context, ticketID string, since int) (*transport.ViewerICEResponse, error)
}

// --- Manager -------------------------------------------------------------

// Manager owns the population of viewer peer connections. Methods are
// goroutine-safe; the orchestrator calls them from its dispatcher
// goroutine, while pion callbacks fire from its own internal pool.
type Manager struct {
	transport  TransportClient
	iceServers []webrtc.ICEServer
	events     chan<- Event

	// api is a single shared *webrtc.API used for all PeerConnections.
	// Pion docs recommend constructing the API once and reusing it
	// across PCs to avoid duplicate codec/interceptor setup work and
	// potential races during concurrent handshakes.
	api *webrtc.API

	mu    sync.Mutex
	peers map[string]*viewerPeer

	// closed is set true by Close to stop accepting new tickets
	// (already-in-flight handshakes still get to finish).
	closed atomic.Bool
}

// NewManager returns a Manager wired to the given transport client.
//
// iceServers may be nil for tests using loopback only. events MUST be
// non-nil and SHOULD be buffered (recommend ≥16); Manager uses a
// non-blocking send and drops events to stderr if the channel is full.
func NewManager(client TransportClient, iceServers []webrtc.ICEServer, events chan<- Event) *Manager {
	se := webrtc.SettingEngine{}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))
	return &Manager{
		transport:  client,
		iceServers: iceServers,
		events:     events,
		api:        api,
		peers:      make(map[string]*viewerPeer),
	}
}

// Add starts the handshake for a new viewer ticket. Returns immediately;
// success / failure is reported on the events channel. Subsequent state
// (Joined when HELLO arrives, Left when channel closes, Error if the
// handshake never completes) flows through the same events channel.
//
// If Manager has been Close()d, Add is a no-op.
func (m *Manager) Add(ctx context.Context, ticket transport.Ticket) {
	if m.closed.Load() {
		return
	}
	go m.handleTicket(ctx, ticket)
}

// Broadcast queues frame for delivery to every viewer with an open
// DataChannel. Returns the count of viewers the frame was queued for.
//
// This does NOT immediately call SCTP send; the queue is drained by
// Drain() or by the per-viewer send path triggered on OnOpen.
func (m *Manager) Broadcast(frame string) int {
	m.mu.Lock()
	peers := make([]*viewerPeer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.mu.Unlock()

	count := 0
	for _, p := range peers {
		if p.queueFrame(frame) {
			count++
		}
	}
	return count
}

// SessionFrames queues `frames` for any viewer that hasn't yet been
// caught up. Used for new-viewer "session frames" (ID, START, last
// progress snapshot, current presence). Frames are queued in order.
//
// Returns the number of viewers that received the frames (0 when no
// new viewers since the last call).
func (m *Manager) SessionFrames(frames []string) int {
	if len(frames) == 0 {
		return 0
	}
	m.mu.Lock()
	peers := make([]*viewerPeer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.mu.Unlock()

	count := 0
	for _, p := range peers {
		p.mu.Lock()
		if !p.sessionFramesSent && p.channel != nil {
			for _, f := range frames {
				p.pendingOutbound = append(p.pendingOutbound, f)
			}
			p.sessionFramesSent = true
			count++
		}
		p.mu.Unlock()
	}
	return count
}

// Drain attempts to send every pending outbound frame on every open
// channel. Returns the number of frames actually flushed.
//
// Frames that fail to send (channel closed, SCTP error) stay in the
// queue for next Drain — except when the channel itself has gone
// away, in which case we drop everything for that viewer (Cull will
// pick them up).
func (m *Manager) Drain() int {
	m.mu.Lock()
	peers := make([]*viewerPeer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.mu.Unlock()

	flushed := 0
	for _, p := range peers {
		flushed += p.drain()
	}
	return flushed
}

// Names returns the names of all viewers that have sent HELLO. Order
// is not guaranteed across calls. Used for building V| frames.
func (m *Manager) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.peers))
	for _, p := range m.peers {
		p.mu.Lock()
		if p.name != "" {
			out = append(out, p.name)
		}
		p.mu.Unlock()
	}
	return out
}

// Count returns the total number of registered viewer peers (named or
// not). Differs from len(Names()) by the count of unnamed in-flight
// connections.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.peers)
}

// Cull removes viewers whose pion connection state is failed/closed
// or has been disconnected for >= DisconnectGrace. Returns the names
// of viewers that had names (so the orchestrator can broadcast a
// fresh V| presence frame).
//
// `now` is the wall-clock to compare disconnect timestamps against;
// pass time.Now() in production, synthetic times in tests.
func (m *Manager) Cull(now time.Time) []string {
	m.mu.Lock()
	stale := make([]*viewerPeer, 0)
	for tid, p := range m.peers {
		if p.shouldCull(now) {
			stale = append(stale, p)
			delete(m.peers, tid)
		}
	}
	m.mu.Unlock()

	removed := make([]string, 0, len(stale))
	for _, p := range stale {
		name := p.takeName()
		p.shutdown()
		if name != "" {
			removed = append(removed, name)
		}
		m.emit(Event{Type: EventViewerLeft, TicketID: p.ticketID, Name: name})
	}
	return removed
}

// Close stops accepting new tickets and shuts down every viewer.
//
// Caller is responsible for queueing any final frames (e.g. END|<task>)
// via Broadcast() BEFORE calling Close so they have a chance to drain.
//
// Close blocks up to drainTimeout for outbound queues to flush, then
// closes every PeerConnection — but bounds the per-peer close phase
// at the same drainTimeout so an unresponsive remote (e.g. a browser
// tab that crashed) cannot block the bridge from exiting. Pion
// goroutines from any peer that didn't close in time will be GC'd
// when the process exits.
func (m *Manager) Close(ctx context.Context, drainTimeout time.Duration) error {
	m.closed.Store(true)

	// Phase 1: drain pending outbound queues (bounded by drainTimeout).
	deadline := time.Now().Add(drainTimeout)
	for time.Now().Before(deadline) {
		flushed := m.Drain()
		if flushed == 0 && m.allDrained() {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Snapshot peer list, clear map.
	m.mu.Lock()
	peers := make([]*viewerPeer, 0, len(m.peers))
	for _, p := range m.peers {
		peers = append(peers, p)
	}
	m.peers = map[string]*viewerPeer{}
	m.mu.Unlock()

	// Phase 2: close peers in parallel with a hard deadline. pc.Close()
	// in pion can block on SCTP/DTLS shutdown if the remote is gone;
	// we cap that here so the bridge can exit promptly.
	closed := make(chan struct{})
	go func() {
		var wg sync.WaitGroup
		for _, p := range peers {
			wg.Add(1)
			go func(p *viewerPeer) {
				defer wg.Done()
				p.shutdown()
			}(p)
		}
		wg.Wait()
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(drainTimeout):
		// Forced exit: pion goroutines from stuck peers leak but will
		// die with the process. Acceptable trade-off for guaranteed
		// shutdown latency.
	}
	_ = ctx
	return nil
}

func (m *Manager) allDrained() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.peers {
		p.mu.Lock()
		empty := len(p.pendingOutbound) == 0
		p.mu.Unlock()
		if !empty {
			return false
		}
	}
	return true
}

func (m *Manager) emit(ev Event) {
	if m.events == nil {
		return
	}
	select {
	case m.events <- ev:
	default:
		// Drop on full channel; orchestrator should size it generously.
		// Logging is the orchestrator's responsibility (this package
		// stays I/O-free).
	}
}

// --- Per-ticket handshake ------------------------------------------------

// handleTicket runs the full per-viewer handshake described in the
// package doc comment. Errors at any step result in an
// EventViewerError and the peer is dropped (never added to m.peers).
func (m *Manager) handleTicket(ctx context.Context, ticket transport.Ticket) {
	if ticket.Offer.Type != "offer" || ticket.Offer.SDP == "" {
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: errors.New("invalid ticket offer")})
		return
	}

	pc, err := m.api.NewPeerConnection(webrtc.Configuration{ICEServers: m.iceServers})
	if err != nil {
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: fmt.Errorf("create peer connection: %w", err)})
		return
	}

	vp := &viewerPeer{
		ticketID:        ticket.TicketID,
		pc:              pc,
		pendingOutbound: nil,
	}

	// Buffer ICE candidates as they're gathered. They're flushed to
	// the server after the answer is posted (or as they arrive
	// post-answer if late).
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		ic := iceFromPion(c)
		vp.iceMu.Lock()
		vp.pendingSenderICE = append(vp.pendingSenderICE, ic)
		flushNow := vp.answerPosted
		batch := vp.pendingSenderICE
		if flushNow {
			vp.pendingSenderICE = nil
		}
		vp.iceMu.Unlock()
		if flushNow {
			// Best-effort flush of late candidates. Errors are dropped;
			// the peer will still try to connect with what it has.
			_ = m.transport.PostSenderICE(ctx, ticket.TicketID, batch)
		}
	})

	// Track connection state for the disconnect-grace timer.
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		vp.mu.Lock()
		switch s {
		case webrtc.PeerConnectionStateDisconnected:
			vp.disconnectedSince = time.Now()
		case webrtc.PeerConnectionStateConnected:
			vp.disconnectedSince = time.Time{}
		case webrtc.PeerConnectionStateFailed, webrtc.PeerConnectionStateClosed:
			// Cull will pick this up on its next sweep.
		}
		vp.mu.Unlock()
	})

	// Capture the DataChannel when the viewer (offerer) creates it.
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		if dc.Label() != DataChannelLabel {
			// Non-fernsicht channels are ignored.
			return
		}
		vp.attachDataChannel(dc, m)
	})

	if err := pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  ticket.Offer.SDP,
	}); err != nil {
		_ = pc.Close()
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: fmt.Errorf("set remote description: %w", err)})
		return
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: fmt.Errorf("create answer: %w", err)})
		return
	}

	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: fmt.Errorf("set local description: %w", err)})
		return
	}

	// Wait for ICE gathering to complete (or hit the timeout).
	select {
	case <-gatherDone:
	case <-time.After(ICEGatherTimeout):
	case <-ctx.Done():
		_ = pc.Close()
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: ctx.Err()})
		return
	}

	local := pc.LocalDescription()
	if local == nil {
		_ = pc.Close()
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: errors.New("local description nil after gathering")})
		return
	}
	if err := m.transport.PostAnswer(ctx, ticket.TicketID, transport.SessionDescription{
		Type: "answer",
		SDP:  local.SDP,
	}); err != nil {
		_ = pc.Close()
		m.emit(Event{Type: EventViewerError, TicketID: ticket.TicketID, Err: fmt.Errorf("post answer: %w", err)})
		return
	}

	// Mark answer-posted so any late ICE candidates flush immediately.
	// Also flush whatever we already gathered.
	vp.iceMu.Lock()
	vp.answerPosted = true
	batch := vp.pendingSenderICE
	vp.pendingSenderICE = nil
	vp.iceMu.Unlock()
	if len(batch) > 0 {
		_ = m.transport.PostSenderICE(ctx, ticket.TicketID, batch)
	}

	// Register the peer NOW so Cull / Names / Broadcast see it even
	// while we're still polling for ICE.
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		_ = pc.Close()
		return
	}
	m.peers[ticket.TicketID] = vp
	m.mu.Unlock()

	// Spawn the viewer-ICE poll loop for the rest of the handshake.
	pollCtx, cancel := context.WithCancel(ctx)
	vp.icePollCancel = cancel
	go m.pollViewerICE(pollCtx, vp)
}

// pollViewerICE tails GET /ticket/{id}/ice/viewer until the connection
// is established (or ICEPollMaxDuration elapses). Adds incoming
// candidates to the peer connection.
func (m *Manager) pollViewerICE(ctx context.Context, vp *viewerPeer) {
	deadline := time.Now().Add(ICEPollMaxDuration)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return
		}
		if state := vp.pc.ConnectionState(); state == webrtc.PeerConnectionStateConnected {
			return
		}

		resp, err := m.transport.PollViewerICE(ctx, vp.ticketID, vp.iceRecvSeq)
		if err == nil && resp != nil {
			for _, c := range resp.Candidates {
				if err := vp.pc.AddICECandidate(iceToPion(c)); err != nil {
					// Bad candidate is non-fatal; skip and continue.
					continue
				}
			}
			if resp.Seq > vp.iceRecvSeq {
				vp.iceRecvSeq = resp.Seq
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(ICEPollInterval):
		}
	}
}

// --- viewerPeer ----------------------------------------------------------

type viewerPeer struct {
	ticketID string
	pc       *webrtc.PeerConnection

	mu                sync.Mutex
	channel           *webrtc.DataChannel
	channelOpen       bool
	name              string
	pendingOutbound   []string
	sessionFramesSent bool
	disconnectedSince time.Time

	iceMu            sync.Mutex
	pendingSenderICE []transport.ICECandidate
	answerPosted     bool
	iceRecvSeq       int

	icePollCancel context.CancelFunc
	closed        atomic.Bool
}

// attachDataChannel wires up DataChannel callbacks once the viewer's
// channel arrives at the sender side.
func (vp *viewerPeer) attachDataChannel(dc *webrtc.DataChannel, m *Manager) {
	vp.mu.Lock()
	vp.channel = dc
	vp.mu.Unlock()

	dc.OnOpen(func() {
		vp.mu.Lock()
		vp.channelOpen = true
		queue := append([]string(nil), vp.pendingOutbound...)
		vp.pendingOutbound = nil
		vp.mu.Unlock()

		// Flush anything that was queued before the channel opened.
		for _, frame := range queue {
			_ = dc.SendText(frame)
		}
	})

	dc.OnClose(func() {
		vp.mu.Lock()
		vp.channelOpen = false
		vp.mu.Unlock()
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			vp.handleInbound(string(msg.Data), m)
		}
	})

	// In rare cases the channel is already open by the time we attach
	// the OnOpen callback (pion races). Catch that.
	if dc.ReadyState() == webrtc.DataChannelStateOpen {
		vp.mu.Lock()
		vp.channelOpen = true
		queue := append([]string(nil), vp.pendingOutbound...)
		vp.pendingOutbound = nil
		vp.mu.Unlock()
		for _, frame := range queue {
			_ = dc.SendText(frame)
		}
	}
}

// handleInbound processes a frame received from a viewer. Only HELLO is
// meaningful today; everything else (including K) is ignored silently.
func (vp *viewerPeer) handleInbound(msg string, m *Manager) {
	name, ok := parseHello(msg)
	if !ok {
		return
	}
	vp.mu.Lock()
	first := vp.name == ""
	vp.name = name
	vp.mu.Unlock()
	if first {
		m.emit(Event{Type: EventViewerJoined, TicketID: vp.ticketID, Name: name})
	}
}

// queueFrame appends frame to the per-viewer outbound queue. Returns
// true if the frame was queued (channel exists or will exist), false
// if the viewer has been shut down.
func (vp *viewerPeer) queueFrame(frame string) bool {
	if vp.closed.Load() {
		return false
	}
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if vp.channelOpen {
		// Send immediately to avoid building up a queue for healthy
		// viewers. Errors are silently dropped — pion's send is
		// non-blocking and queued internally to SCTP.
		if vp.channel != nil {
			_ = vp.channel.SendText(frame)
		}
		return true
	}
	// Channel not open yet; queue for the OnOpen callback to flush.
	vp.pendingOutbound = append(vp.pendingOutbound, frame)
	return true
}

// drain sends any queued frames on the open channel and returns the
// number actually flushed.
func (vp *viewerPeer) drain() int {
	if vp.closed.Load() {
		return 0
	}
	vp.mu.Lock()
	if !vp.channelOpen || vp.channel == nil || len(vp.pendingOutbound) == 0 {
		vp.mu.Unlock()
		return 0
	}
	queue := vp.pendingOutbound
	vp.pendingOutbound = nil
	dc := vp.channel
	vp.mu.Unlock()

	count := 0
	for _, f := range queue {
		if err := dc.SendText(f); err != nil {
			// Re-queue the rest for the next drain attempt.
			vp.mu.Lock()
			vp.pendingOutbound = append([]string{f}, append(queue[count+1:], vp.pendingOutbound...)...)
			vp.mu.Unlock()
			return count
		}
		count++
	}
	return count
}

// shouldCull returns true if the peer should be removed. Reads
// vp.pc.ConnectionState() in production; tests use shouldCullWithState
// directly to avoid needing real pion state transitions.
func (vp *viewerPeer) shouldCull(now time.Time) bool {
	return vp.shouldCullWithState(now, vp.pc.ConnectionState())
}

func (vp *viewerPeer) shouldCullWithState(now time.Time, state webrtc.PeerConnectionState) bool {
	if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
		return true
	}
	vp.mu.Lock()
	defer vp.mu.Unlock()
	if state == webrtc.PeerConnectionStateDisconnected &&
		!vp.disconnectedSince.IsZero() &&
		now.Sub(vp.disconnectedSince) >= DisconnectGrace {
		return true
	}
	return false
}

// takeName returns the current name (or empty) and clears it.
func (vp *viewerPeer) takeName() string {
	vp.mu.Lock()
	defer vp.mu.Unlock()
	n := vp.name
	vp.name = ""
	return n
}

// shutdown closes the channel and peer connection. Idempotent.
func (vp *viewerPeer) shutdown() {
	if !vp.closed.CompareAndSwap(false, true) {
		return
	}
	if vp.icePollCancel != nil {
		vp.icePollCancel()
	}
	vp.mu.Lock()
	dc := vp.channel
	vp.mu.Unlock()
	if dc != nil {
		_ = dc.Close()
	}
	_ = vp.pc.Close()
}

// --- Helpers -------------------------------------------------------------

// parseHello extracts a name from a HELLO|<name> wire frame, applying
// the same sanitization as the Python sender (strip pipes, trim, cap
// at HelloMaxNameLen runes). Returns the cleaned name and true if the
// frame was a HELLO; ("", false) otherwise.
func parseHello(msg string) (string, bool) {
	const prefix = "HELLO|"
	if !strings.HasPrefix(msg, prefix) {
		return "", false
	}
	name := strings.TrimSpace(strings.ReplaceAll(msg[len(prefix):], "|", ""))
	runes := []rune(name)
	if len(runes) > HelloMaxNameLen {
		name = string(runes[:HelloMaxNameLen])
	}
	if name == "" {
		return "", false
	}
	return name, true
}

// iceFromPion converts a pion ICECandidate into the wire shape the
// transport package uses.
func iceFromPion(c *webrtc.ICECandidate) transport.ICECandidate {
	init := c.ToJSON()
	out := transport.ICECandidate{Candidate: init.Candidate}
	if init.SDPMid != nil {
		mid := *init.SDPMid
		out.SDPMid = &mid
	}
	if init.SDPMLineIndex != nil {
		idx := int(*init.SDPMLineIndex)
		out.SDPMLineIndex = &idx
	}
	return out
}

// iceToPion converts a wire-shape ICECandidate into the pion init
// struct. Trims an optional "candidate:" prefix that some SDKs emit.
func iceToPion(c transport.ICECandidate) webrtc.ICECandidateInit {
	cand := c.Candidate
	cand = strings.TrimPrefix(cand, "candidate:")
	init := webrtc.ICECandidateInit{Candidate: cand}
	if c.SDPMid != nil {
		mid := *c.SDPMid
		init.SDPMid = &mid
	}
	if c.SDPMLineIndex != nil {
		idx := uint16(*c.SDPMLineIndex)
		init.SDPMLineIndex = &idx
	}
	return init
}
