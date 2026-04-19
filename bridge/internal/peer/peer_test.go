package peer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/MuteJester/fernsicht/bridge/internal/transport"
	"github.com/pion/webrtc/v4"
)

// --- Synchronous unit tests ---------------------------------------------

func TestParseHelloHappy(t *testing.T) {
	name, ok := parseHello("HELLO|orion")
	if !ok || name != "orion" {
		t.Errorf("got (%q, %v)", name, ok)
	}
}

func TestParseHelloStripsPipesAndTrims(t *testing.T) {
	name, ok := parseHello("HELLO|  or|ion  ")
	if !ok || name != "orion" {
		t.Errorf("got (%q, %v)", name, ok)
	}
}

func TestParseHelloTruncatesAt32Runes(t *testing.T) {
	long := ""
	for i := 0; i < 64; i++ {
		long += "a"
	}
	name, ok := parseHello("HELLO|" + long)
	if !ok {
		t.Fatal("expected ok")
	}
	if len([]rune(name)) != 32 {
		t.Errorf("expected 32 runes, got %d", len([]rune(name)))
	}
}

func TestParseHelloRuneAware(t *testing.T) {
	name := ""
	for i := 0; i < 40; i++ {
		name += "ñ"
	}
	got, ok := parseHello("HELLO|" + name)
	if !ok || len([]rune(got)) != 32 {
		t.Errorf("rune-aware truncation broken: %q (%d runes)", got, len([]rune(got)))
	}
}

func TestParseHelloRejectsNonHelloFrames(t *testing.T) {
	for _, in := range []string{"K", "ID|x", "P|t1|0.5|...", "HELLO", "HELLO|", "HELLO|   "} {
		t.Run(in, func(t *testing.T) {
			if _, ok := parseHello(in); ok {
				t.Errorf("expected reject")
			}
		})
	}
}

// --- End-to-end handshake helpers ---------------------------------------

// loopbackTransport implements TransportClient by routing answers and
// ICE candidates between the manager-under-test and an in-process pion
// "viewer" peer.
//
// The viewer is the offerer (it created the DataChannel + offer that
// fed into Manager.Add); this transport receives the answer the
// manager produces and applies it back to the viewer, then shuttles
// ICE in both directions.
type loopbackTransport struct {
	t          *testing.T
	viewer     *webrtc.PeerConnection
	answerSeen chan struct{}

	mu              sync.Mutex
	viewerPending   []transport.ICECandidate
	viewerSeq       int
	answerPosted    bool
	postAnswerErr   error
	postSenderErr   error
	pollViewerErr   error
}

func newLoopbackTransport(t *testing.T, viewer *webrtc.PeerConnection) *loopbackTransport {
	lt := &loopbackTransport{
		t:          t,
		viewer:     viewer,
		answerSeen: make(chan struct{}, 1),
	}
	// Capture viewer's outbound ICE for delivery via PollViewerICE.
	viewer.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		lt.mu.Lock()
		lt.viewerPending = append(lt.viewerPending, iceFromPion(c))
		lt.mu.Unlock()
	})
	return lt
}

func (lt *loopbackTransport) PostAnswer(_ context.Context, ticketID string, answer transport.SessionDescription) error {
	if lt.postAnswerErr != nil {
		return lt.postAnswerErr
	}
	if err := lt.viewer.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  answer.SDP,
	}); err != nil {
		return err
	}
	lt.mu.Lock()
	lt.answerPosted = true
	lt.mu.Unlock()
	select {
	case lt.answerSeen <- struct{}{}:
	default:
	}
	return nil
}

func (lt *loopbackTransport) PostSenderICE(_ context.Context, ticketID string, candidates []transport.ICECandidate) error {
	if lt.postSenderErr != nil {
		return lt.postSenderErr
	}
	for _, c := range candidates {
		if err := lt.viewer.AddICECandidate(iceToPion(c)); err != nil {
			return err
		}
	}
	return nil
}

func (lt *loopbackTransport) PollViewerICE(_ context.Context, ticketID string, since int) (*transport.ViewerICEResponse, error) {
	if lt.pollViewerErr != nil {
		return nil, lt.pollViewerErr
	}
	lt.mu.Lock()
	defer lt.mu.Unlock()
	out := &transport.ViewerICEResponse{
		Candidates: append([]transport.ICECandidate(nil), lt.viewerPending...),
		Seq:        lt.viewerSeq + len(lt.viewerPending),
	}
	lt.viewerSeq = out.Seq
	lt.viewerPending = nil
	return out, nil
}

// setupViewer builds an in-process pion peer that plays the viewer
// role: creates a "fernsicht" DataChannel, generates an offer, waits
// for ICE gathering. Returns the PC, the DataChannel, the
// loopbackTransport that knows about it, and the ticket payload to
// hand to Manager.Add.
//
// `suffix` lets multiple viewers in the same test get distinct ticket
// IDs (so a muxing transport can route them correctly). Pass "" for
// single-viewer tests.
func setupViewer(t *testing.T, suffix string) (*webrtc.PeerConnection, *webrtc.DataChannel, *loopbackTransport, transport.Ticket) {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create viewer pc: %v", err)
	}

	dc, err := pc.CreateDataChannel(DataChannelLabel, &webrtc.DataChannelInit{Ordered: pointerTrue()})
	if err != nil {
		t.Fatalf("create viewer datachannel: %v", err)
	}

	lt := newLoopbackTransport(t, pc)

	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local description: %v", err)
	}
	select {
	case <-gatherDone:
	case <-time.After(3 * time.Second):
		t.Fatalf("viewer ICE gathering timed out")
	}

	local := pc.LocalDescription()
	if local == nil {
		t.Fatalf("viewer LocalDescription nil")
	}

	ticketID := "test-ticket-" + t.Name()
	if suffix != "" {
		ticketID += "-" + suffix
	}
	return pc, dc, lt, transport.Ticket{
		TicketID: ticketID,
		Offer:    transport.SessionDescription{Type: "offer", SDP: local.SDP},
	}
}

func pointerTrue() *bool { b := true; return &b }

// --- End-to-end handshake tests -----------------------------------------

func TestSingleViewerHandshakeAndHello(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pion handshake in -short mode")
	}

	viewerPC, viewerDC, lt, ticket := setupViewer(t, "")
	defer viewerPC.Close()

	// Track when the viewer's DataChannel opens so we can send HELLO.
	viewerOpen := make(chan struct{})
	viewerDC.OnOpen(func() { close(viewerOpen) })

	// Track frames received on the viewer side.
	viewerInbox := make(chan string, 64)
	viewerDC.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			viewerInbox <- string(msg.Data)
		}
	})

	events := make(chan Event, 16)
	mgr := NewManager(lt, nil, events)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr.Add(ctx, ticket)

	// Wait for the manager to post its answer back to the viewer.
	select {
	case <-lt.answerSeen:
	case <-ctx.Done():
		t.Fatalf("answer never posted: %v", ctx.Err())
	}

	// Wait for both DataChannels to open.
	select {
	case <-viewerOpen:
	case <-time.After(8 * time.Second):
		t.Fatalf("viewer DataChannel never opened (state=%v conn=%v)", viewerDC.ReadyState(), viewerPC.ConnectionState())
	}

	// Send HELLO from the viewer.
	if err := viewerDC.SendText("HELLO|vega"); err != nil {
		t.Fatalf("send HELLO: %v", err)
	}

	// Manager should emit ViewerJoined with name "vega".
	got := waitForEvent(t, events, EventViewerJoined, 3*time.Second)
	if got.Name != "vega" {
		t.Errorf("expected name vega, got %q", got.Name)
	}
	// Names() should now include "vega".
	if names := mgr.Names(); len(names) != 1 || names[0] != "vega" {
		t.Errorf("Names() = %v", names)
	}

	// Broadcast a frame; the viewer should receive it.
	n := mgr.Broadcast("ID|sender-1")
	if n != 1 {
		t.Errorf("Broadcast queued for %d viewers, want 1", n)
	}
	select {
	case got := <-viewerInbox:
		if got != "ID|sender-1" {
			t.Errorf("viewer received %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("viewer never received broadcast")
	}

	// Closing manager should cleanly drop the peer.
	if err := mgr.Close(ctx, 1*time.Second); err != nil {
		t.Errorf("Close: %v", err)
	}
	if mgr.Count() != 0 {
		t.Errorf("Count after Close: %d", mgr.Count())
	}
}

func TestMultipleViewersBroadcastReachesAll(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pion handshake in -short mode")
	}

	const N = 2 // 2 viewers — the plan calls for "2+"; more is slow under -race
	viewers := make([]*webrtc.PeerConnection, N)
	dcs := make([]*webrtc.DataChannel, N)
	transports := make([]*loopbackTransport, N)
	tickets := make([]transport.Ticket, N)
	inboxes := make([]chan string, N)
	opens := make([]chan struct{}, N)

	for i := 0; i < N; i++ {
		pc, dc, lt, ticket := setupViewer(t, fmt.Sprintf("v%d", i))
		viewers[i] = pc
		dcs[i] = dc
		transports[i] = lt
		tickets[i] = ticket

		inbox := make(chan string, 64)
		opened := make(chan struct{})
		dc.OnOpen(func() { close(opened) })
		dc.OnMessage(func(msg webrtc.DataChannelMessage) {
			if msg.IsString {
				inbox <- string(msg.Data)
			}
		})
		inboxes[i] = inbox
		opens[i] = opened
		t.Cleanup(func() { pc.Close() })
	}

	// Multiplex transport: route by ticket ID.
	mux := &muxTransport{routes: map[string]*loopbackTransport{}}
	for i, ticket := range tickets {
		mux.routes[ticket.TicketID] = transports[i]
	}

	events := make(chan Event, 32)
	mgr := NewManager(mux, nil, events)

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	for _, ticket := range tickets {
		mgr.Add(ctx, ticket)
	}

	// Wait for all viewer channels to open.
	for i, opened := range opens {
		select {
		case <-opened:
		case <-time.After(8 * time.Second):
			t.Fatalf("viewer %d channel never opened (conn=%v)", i, viewers[i].ConnectionState())
		}
	}

	// Each viewer identifies itself.
	names := []string{"vega", "orion"}
	for i, n := range names {
		if err := dcs[i].SendText("HELLO|" + n); err != nil {
			t.Fatalf("viewer %d HELLO: %v", i, err)
		}
	}

	// Drain N joined events.
	joined := map[string]bool{}
	for i := 0; i < N; i++ {
		ev := waitForEvent(t, events, EventViewerJoined, 3*time.Second)
		joined[ev.Name] = true
	}
	for _, n := range names {
		if !joined[n] {
			t.Errorf("missing joined event for %q", n)
		}
	}

	// Broadcast should reach both.
	if got := mgr.Broadcast("P|t1|0.5|-|-|-|-|-|it"); got != N {
		t.Errorf("Broadcast count = %d, want %d", got, N)
	}
	for i, inbox := range inboxes {
		select {
		case got := <-inbox:
			if got != "P|t1|0.5|-|-|-|-|-|it" {
				t.Errorf("viewer %d received %q", i, got)
			}
		case <-time.After(2 * time.Second):
			t.Errorf("viewer %d never received broadcast", i)
		}
	}

	if mgr.Count() != N {
		t.Errorf("Count = %d, want %d", mgr.Count(), N)
	}
	if got := mgr.Names(); len(got) != N {
		t.Errorf("Names count = %d, want %d", len(got), N)
	}
}

func TestSessionFramesOnlyDeliveredOnce(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping pion handshake in -short mode")
	}

	viewerPC, viewerDC, lt, ticket := setupViewer(t, "")
	defer viewerPC.Close()

	opened := make(chan struct{})
	viewerDC.OnOpen(func() { close(opened) })
	inbox := make(chan string, 64)
	viewerDC.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			inbox <- string(msg.Data)
		}
	})

	events := make(chan Event, 16)
	mgr := NewManager(lt, nil, events)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mgr.Add(ctx, ticket)
	<-lt.answerSeen
	select {
	case <-opened:
	case <-time.After(8 * time.Second):
		t.Fatal("channel never opened")
	}

	frames := []string{"ID|sender-x", "START|t1|Demo", "P|t1|0.5|-|-|-|-|-|it"}
	if got := mgr.SessionFrames(frames); got != 1 {
		t.Errorf("first SessionFrames count = %d, want 1", got)
	}

	// Second call should be a no-op (sessionFramesSent is sticky).
	if got := mgr.SessionFrames(frames); got != 0 {
		t.Errorf("second SessionFrames count = %d, want 0", got)
	}

	// Drain to actually push them out.
	mgr.Drain()

	// Viewer should receive exactly len(frames) messages, in order.
	for i, want := range frames {
		select {
		case got := <-inbox:
			if got != want {
				t.Errorf("frame %d: got %q, want %q", i, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("frame %d never arrived", i)
		}
	}
}

// muxTransport routes per-ticket to a child loopbackTransport.
type muxTransport struct {
	routes map[string]*loopbackTransport
}

func (m *muxTransport) PostAnswer(ctx context.Context, ticketID string, answer transport.SessionDescription) error {
	t, ok := m.routes[ticketID]
	if !ok {
		return errors.New("unknown ticket")
	}
	return t.PostAnswer(ctx, ticketID, answer)
}
func (m *muxTransport) PostSenderICE(ctx context.Context, ticketID string, candidates []transport.ICECandidate) error {
	t, ok := m.routes[ticketID]
	if !ok {
		return errors.New("unknown ticket")
	}
	return t.PostSenderICE(ctx, ticketID, candidates)
}
func (m *muxTransport) PollViewerICE(ctx context.Context, ticketID string, since int) (*transport.ViewerICEResponse, error) {
	t, ok := m.routes[ticketID]
	if !ok {
		return nil, errors.New("unknown ticket")
	}
	return t.PollViewerICE(ctx, ticketID, since)
}

// --- shouldCull tests (no real pion handshake required) -------
// We test the predicate directly because pion doesn't expose a way to
// force a *PeerConnection into Failed/Disconnected for unit tests.
// Cull's loop itself is integration-tested via the handshake tests.

func TestShouldCullFailedAndClosed(t *testing.T) {
	vp := &viewerPeer{ticketID: "tk"}
	now := time.Now()
	if !vp.shouldCullWithState(now, webrtc.PeerConnectionStateFailed) {
		t.Error("Failed should be culled")
	}
	if !vp.shouldCullWithState(now, webrtc.PeerConnectionStateClosed) {
		t.Error("Closed should be culled")
	}
	if vp.shouldCullWithState(now, webrtc.PeerConnectionStateConnected) {
		t.Error("Connected should NOT be culled")
	}
	if vp.shouldCullWithState(now, webrtc.PeerConnectionStateNew) {
		t.Error("New should NOT be culled")
	}
}

func TestShouldCullRespectsDisconnectGrace(t *testing.T) {
	now := time.Now()

	// Disconnected with no timestamp recorded — don't cull (we haven't
	// seen the disconnect transition yet).
	noTimestamp := &viewerPeer{ticketID: "tk-no-ts"}
	if noTimestamp.shouldCullWithState(now, webrtc.PeerConnectionStateDisconnected) {
		t.Error("disconnected without timestamp should not be culled")
	}

	// Disconnected 10s ago — within grace, don't cull.
	recent := &viewerPeer{ticketID: "tk-recent"}
	recent.disconnectedSince = now.Add(-10 * time.Second)
	if recent.shouldCullWithState(now, webrtc.PeerConnectionStateDisconnected) {
		t.Error("recent disconnect was culled prematurely")
	}

	// Disconnected 30s ago — past grace, cull.
	stale := &viewerPeer{ticketID: "tk-stale"}
	stale.disconnectedSince = now.Add(-30 * time.Second)
	if !stale.shouldCullWithState(now, webrtc.PeerConnectionStateDisconnected) {
		t.Error("stale disconnect should have been culled")
	}
}

func TestNamesExcludesUnnamedViewers(t *testing.T) {
	mgr := newTestManagerNoTransport(t)
	a := newBareViewer(t, "tk-a")
	a.name = "vega"
	b := newBareViewer(t, "tk-b")
	// b unnamed (HELLO not yet arrived)
	mgr.peers["tk-a"] = a
	mgr.peers["tk-b"] = b

	names := mgr.Names()
	if len(names) != 1 || names[0] != "vega" {
		t.Errorf("Names = %v, want [vega]", names)
	}
	if mgr.Count() != 2 {
		t.Errorf("Count = %d, want 2", mgr.Count())
	}
}

// --- Test fixtures ------------------------------------------------------

func newTestManagerNoTransport(t *testing.T) *Manager {
	t.Helper()
	return NewManager(nil, nil, make(chan Event, 32))
}

// newBareViewer creates a viewerPeer with a real (idle) *PeerConnection
// so that tests of Names/Count can exercise map operations without
// running the full WebRTC handshake.
func newBareViewer(t *testing.T, ticketID string) *viewerPeer {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create pc: %v", err)
	}
	t.Cleanup(func() { pc.Close() })
	return &viewerPeer{ticketID: ticketID, pc: pc}
}

// TestCullRemovesClosedPC exercises Cull's integration path:
// closing the underlying PC moves its state to Closed, after which
// Cull should remove it and emit a ViewerLeft event for any named
// viewer.
func TestCullRemovesClosedPC(t *testing.T) {
	events := make(chan Event, 16)
	mgr := NewManager(nil, nil, events)

	named := newBareViewer(t, "tk-named")
	named.name = "vega"
	unnamed := newBareViewer(t, "tk-unnamed")
	healthy := newBareViewer(t, "tk-healthy")
	mgr.peers["tk-named"] = named
	mgr.peers["tk-unnamed"] = unnamed
	mgr.peers["tk-healthy"] = healthy

	// Force two of the PCs into Closed state.
	if err := named.pc.Close(); err != nil {
		t.Fatalf("close named: %v", err)
	}
	if err := unnamed.pc.Close(); err != nil {
		t.Fatalf("close unnamed: %v", err)
	}

	// Give pion a moment to surface the state change.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if named.pc.ConnectionState() == webrtc.PeerConnectionStateClosed &&
			unnamed.pc.ConnectionState() == webrtc.PeerConnectionStateClosed {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	removed := mgr.Cull(time.Now())

	if _, exists := mgr.peers["tk-named"]; exists {
		t.Error("named closed peer was not culled")
	}
	if _, exists := mgr.peers["tk-unnamed"]; exists {
		t.Error("unnamed closed peer was not culled")
	}
	if _, exists := mgr.peers["tk-healthy"]; !exists {
		t.Error("healthy peer was culled")
	}

	// Only the named peer's name should have surfaced in `removed`.
	if len(removed) != 1 || removed[0] != "vega" {
		t.Errorf("removed names = %v, want [vega]", removed)
	}

	// Both culled peers should have emitted EventViewerLeft.
	leftCount := 0
	deadline = time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && leftCount < 2 {
		select {
		case ev := <-events:
			if ev.Type == EventViewerLeft {
				leftCount++
			}
		case <-time.After(50 * time.Millisecond):
		}
	}
	if leftCount != 2 {
		t.Errorf("expected 2 ViewerLeft events, got %d", leftCount)
	}
}

// TestEmitDropsOnFullChannel verifies the non-blocking emit path: when
// the events channel is full, events are silently dropped (orchestrator
// is responsible for sizing the channel; the manager must never block).
func TestEmitDropsOnFullChannel(t *testing.T) {
	// 0-size channel = always full from emit's perspective.
	events := make(chan Event)
	mgr := NewManager(nil, nil, events)

	// Should not block. If it does, the test will time out.
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			mgr.emit(Event{Type: EventViewerError, TicketID: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("emit blocked on full channel")
	}
}

// TestEmitNilChannelIsNoop covers the defensive nil-channel branch.
func TestEmitNilChannelIsNoop(t *testing.T) {
	mgr := NewManager(nil, nil, nil)
	// Should not panic.
	mgr.emit(Event{Type: EventViewerError, TicketID: "x"})
}

// TestAddNoopAfterClose verifies Manager.Add silently returns when
// the manager has been Closed (a closed manager should not start new
// handshake goroutines).
func TestAddNoopAfterClose(t *testing.T) {
	events := make(chan Event, 4)
	mgr := NewManager(&loopbackTransport{}, nil, events)
	if err := mgr.Close(context.Background(), 50*time.Millisecond); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// After Close, Add should be a no-op (no goroutine spawned).
	mgr.Add(context.Background(), transport.Ticket{TicketID: "tk"})
	// Give any goroutine that did spawn a chance to register a peer.
	time.Sleep(50 * time.Millisecond)
	if mgr.Count() != 0 {
		t.Errorf("Count after Close+Add = %d, want 0", mgr.Count())
	}
}

// TestHandleTicketRejectsBadOffer covers the early-return branch when
// the ticket's Offer is malformed.
func TestHandleTicketRejectsBadOffer(t *testing.T) {
	events := make(chan Event, 4)
	mgr := NewManager(&loopbackTransport{}, nil, events)
	mgr.Add(context.Background(), transport.Ticket{
		TicketID: "tk-bad",
		Offer:    transport.SessionDescription{Type: "answer", SDP: "wrong"},
	})
	got := waitForEvent(t, events, EventViewerError, 1*time.Second)
	if got.TicketID != "tk-bad" {
		t.Errorf("ticket ID: %q", got.TicketID)
	}
	if got.Err == nil {
		t.Error("expected non-nil Err")
	}
	if mgr.Count() != 0 {
		t.Errorf("Count after bad offer = %d, want 0", mgr.Count())
	}
}

// waitForEvent reads from events until it sees one matching wantType,
// failing the test if the timeout fires first.
func waitForEvent(t *testing.T, events <-chan Event, wantType EventType, timeout time.Duration) Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-events:
			if ev.Type == wantType {
				return ev
			}
			// Skip events of other types but log them for debug.
			t.Logf("skipping unrelated event: %+v", ev)
		case <-deadline:
			t.Fatalf("timed out waiting for event type %d", wantType)
			return Event{}
		}
	}
}
