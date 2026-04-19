package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MuteJester/fernsicht/bridge/internal/peer"
	"github.com/MuteJester/fernsicht/bridge/internal/transport"
	"github.com/pion/webrtc/v4"
)

// --- Mock subsystems -----------------------------------------------------

// fakeTransport implements the bridge's transportClient interface AND
// peer.TransportClient — both the dispatcher and the (mock) peer
// manager talk through it.
type fakeTransport struct {
	mu sync.Mutex

	openErr      error
	session      *transport.Session
	senderSecret string

	pollErr   error
	pollQueue [][]transport.Ticket
	pollCount int

	postedAnswers []postedAnswer
	postedICE     []postedICE
	pollViewerErr error
}

type postedAnswer struct {
	ticketID string
	answer   transport.SessionDescription
}

type postedICE struct {
	ticketID   string
	candidates []transport.ICECandidate
}

func (f *fakeTransport) OpenSession(_ context.Context, _ transport.SessionConfig) (*transport.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return nil, f.openErr
	}
	if f.session == nil {
		return nil, errors.New("fakeTransport: no session configured")
	}
	return f.session, nil
}

func (f *fakeTransport) PollTickets(_ context.Context, _ string) ([]transport.Ticket, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pollCount++
	if f.pollErr != nil {
		return nil, f.pollErr
	}
	if len(f.pollQueue) == 0 {
		return []transport.Ticket{}, nil
	}
	ts := f.pollQueue[0]
	f.pollQueue = f.pollQueue[1:]
	return ts, nil
}

func (f *fakeTransport) PostAnswer(_ context.Context, ticketID string, answer transport.SessionDescription) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postedAnswers = append(f.postedAnswers, postedAnswer{ticketID, answer})
	return nil
}

func (f *fakeTransport) PostSenderICE(_ context.Context, ticketID string, candidates []transport.ICECandidate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.postedICE = append(f.postedICE, postedICE{ticketID, candidates})
	return nil
}

func (f *fakeTransport) PollViewerICE(_ context.Context, _ string, since int) (*transport.ViewerICEResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pollViewerErr != nil {
		return nil, f.pollViewerErr
	}
	return &transport.ViewerICEResponse{Candidates: nil, Seq: since}, nil
}

func (f *fakeTransport) SetSenderSecret(secret string) {
	f.mu.Lock()
	f.senderSecret = secret
	f.mu.Unlock()
}

// fakePeerManager implements the bridge's peerManager interface.
// Captures every call and lets tests inject peer.Events that the
// dispatcher will pull off the events channel.
type fakePeerManager struct {
	mu sync.Mutex

	addedTickets   []transport.Ticket
	broadcasts     []string
	sessionFrames  [][]string
	cullCalls      int
	closed         bool
	names          []string // tests pre-set this to control Names() return
	events         chan<- peer.Event
}

func (f *fakePeerManager) Add(_ context.Context, ticket transport.Ticket) {
	f.mu.Lock()
	f.addedTickets = append(f.addedTickets, ticket)
	f.mu.Unlock()
}

func (f *fakePeerManager) Broadcast(frame string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcasts = append(f.broadcasts, frame)
	return len(f.names)
}

func (f *fakePeerManager) SessionFrames(frames []string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessionFrames = append(f.sessionFrames, append([]string(nil), frames...))
	return 0 // pretend no new viewers
}

func (f *fakePeerManager) Drain() int { return 0 }

func (f *fakePeerManager) Names() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.names...)
}

func (f *fakePeerManager) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.names)
}

func (f *fakePeerManager) Cull(_ time.Time) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cullCalls++
	return nil
}

func (f *fakePeerManager) Close(_ context.Context, _ time.Duration) error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

// injectEvent ships ev to the events channel the bridge gave us at
// construction time. Used to simulate peer.Manager activity (HELLO
// arrived, viewer left, etc.) from tests.
func (f *fakePeerManager) injectEvent(t *testing.T, ev peer.Event) {
	t.Helper()
	select {
	case f.events <- ev:
	case <-time.After(2 * time.Second):
		t.Fatal("injectEvent: events channel full")
	}
}

// --- Test harness --------------------------------------------------------

// runHarness wires Run with the given fakes, returns:
//   - in: caller writes JSON commands here (then closes for EOF)
//   - events: caller reads decoded event objects from here
//   - done: closed when Run returns
//   - runErr: populated when done closes; the returned error from Run
type runHarness struct {
	in       *io.PipeWriter
	events   <-chan map[string]any
	done     chan struct{}
	runErr   error
	runErrMu sync.Mutex
}

func (h *runHarness) waitDone(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-h.done:
	case <-time.After(timeout):
		t.Fatal("Run did not return within timeout")
	}
}

func (h *runHarness) err() error {
	h.runErrMu.Lock()
	defer h.runErrMu.Unlock()
	return h.runErr
}

// startBridge launches Run with fake subsystems. Returns the harness +
// the captured fakes so tests can inspect/inject.
func startBridge(t *testing.T) (*runHarness, *fakeTransport, *fakePeerManager) {
	t.Helper()
	ft := &fakeTransport{
		session: &transport.Session{
			RoomID:           "abc12345",
			SenderSecret:     "test-secret",
			ViewerURL:        "https://app.fernsicht.space/#room=abc12345",
			ExpiresIn:        3600,
			MaxViewers:       8,
			PollIntervalHint: 1, // 1 second so tests are fast
		},
	}
	fpm := &fakePeerManager{}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	events := make(chan map[string]any, 64)
	go func() {
		defer close(events)
		scanner := bufio.NewScanner(outR)
		scanner.Buffer(make([]byte, 64*1024), 1<<20)
		for scanner.Scan() {
			var ev map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
				events <- ev
			}
		}
	}()

	h := &runHarness{
		in:     inW,
		events: events,
		done:   make(chan struct{}),
	}

	go func() {
		defer close(h.done)
		err := RunWithOptions(context.Background(), inR, outW, Options{
			Version:          "bridge-test",
			TransportFactory: func(_ string) transportClient { return ft },
			PeerManagerFactory: func(_ peer.TransportClient, _ []webrtc.ICEServer, ev chan<- peer.Event) peerManager {
				fpm.events = ev
				return fpm
			},
		})
		// Closing outW signals the reader goroutine to drain and exit.
		_ = outW.Close()
		h.runErrMu.Lock()
		h.runErr = err
		h.runErrMu.Unlock()
	}()

	t.Cleanup(func() {
		_ = inW.Close()
		_ = inR.Close()
	})

	return h, ft, fpm
}

// sendCmd writes a JSON command line to the bridge's stdin.
func sendCmd(t *testing.T, h *runHarness, payload string) {
	t.Helper()
	if _, err := h.in.Write([]byte(payload + "\n")); err != nil {
		t.Fatalf("write cmd: %v", err)
	}
}

// expectEvent reads events until one matches eventName, failing on
// timeout. Returns the matched event.
func expectEvent(t *testing.T, h *runHarness, eventName string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-h.events:
			if !ok {
				t.Fatalf("events channel closed before %q arrived", eventName)
			}
			if ev["event"] == eventName {
				return ev
			}
			t.Logf("skip event: %v", ev)
		case <-deadline:
			t.Fatalf("timed out waiting for event %q", eventName)
			return nil
		}
	}
}

// --- Tests ---------------------------------------------------------------

func TestHelloAck(t *testing.T) {
	h, _, _ := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	ev := expectEvent(t, h, "hello_ack", 2*time.Second)
	if ev["bridge_version"] != "bridge-test" {
		t.Errorf("bridge_version: %v", ev["bridge_version"])
	}
	if ev["protocol"].(float64) != 1 {
		t.Errorf("protocol: %v", ev["protocol"])
	}

	_ = h.in.Close() // EOF → graceful close
	h.waitDone(t, 3*time.Second)
}

func TestHelloProtocolMismatch(t *testing.T) {
	h, _, _ := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":99}`)
	ev := expectEvent(t, h, "error", 2*time.Second)
	if ev["code"] != "PROTOCOL_VERSION_MISMATCH" {
		t.Errorf("code: %v", ev["code"])
	}
	if ev["fatal"] != true {
		t.Errorf("fatal: %v", ev["fatal"])
	}
	// Bridge should exit on its own.
	h.waitDone(t, 3*time.Second)
	if h.err() == nil {
		t.Error("expected non-nil error on protocol mismatch")
	}
}

func TestSessionOpensAndStartsPollLoop(t *testing.T) {
	h, ft, fpm := startBridge(t)

	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)

	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	ev := expectEvent(t, h, "session_ready", 2*time.Second)
	if ev["room_id"] != "abc12345" {
		t.Errorf("room_id: %v", ev["room_id"])
	}
	if ft.senderSecret != "test-secret" {
		t.Errorf("transport.senderSecret = %q, want test-secret", ft.senderSecret)
	}
	if fpm.events == nil {
		t.Error("peer manager events channel never wired up")
	}

	// Wait long enough for at least one poll tick (PollIntervalHint = 1s).
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ft.mu.Lock()
		count := ft.pollCount
		ft.mu.Unlock()
		if count >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	ft.mu.Lock()
	if ft.pollCount < 1 {
		t.Errorf("pollCount = %d, want ≥1", ft.pollCount)
	}
	ft.mu.Unlock()

	_ = h.in.Close()
	h.waitDone(t, 3*time.Second)
}

func TestStartProgressEndBroadcast(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	sendCmd(t, h, `{"op":"start","task_id":"t1","label":"Training"}`)
	sendCmd(t, h, `{"op":"progress","task_id":"t1","value":0.5,"n":50,"total":100}`)
	sendCmd(t, h, `{"op":"end","task_id":"t1"}`)

	// Wait for broadcasts to propagate (commands are async).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fpm.mu.Lock()
		bs := append([]string(nil), fpm.broadcasts...)
		fpm.mu.Unlock()
		if len(bs) >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fpm.mu.Lock()
	defer fpm.mu.Unlock()
	if len(fpm.broadcasts) < 3 {
		t.Fatalf("expected ≥3 broadcasts, got %v", fpm.broadcasts)
	}
	// First three should be START, P, END in order.
	if !strings.HasPrefix(fpm.broadcasts[0], "START|t1|Training") {
		t.Errorf("[0] = %q", fpm.broadcasts[0])
	}
	if !strings.HasPrefix(fpm.broadcasts[1], "P|t1|0.5000") {
		t.Errorf("[1] = %q", fpm.broadcasts[1])
	}
	if fpm.broadcasts[2] != "END|t1" {
		t.Errorf("[2] = %q", fpm.broadcasts[2])
	}
}

func TestImplicitEndOnNewStart(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	sendCmd(t, h, `{"op":"start","task_id":"t1","label":"First"}`)
	sendCmd(t, h, `{"op":"start","task_id":"t2","label":"Second"}`)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fpm.mu.Lock()
		n := len(fpm.broadcasts)
		fpm.mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fpm.mu.Lock()
	defer fpm.mu.Unlock()
	// Want: START|t1|First, END|t1, START|t2|Second
	if len(fpm.broadcasts) < 3 {
		t.Fatalf("got %v", fpm.broadcasts)
	}
	if !strings.HasPrefix(fpm.broadcasts[0], "START|t1|First") {
		t.Errorf("[0] = %q", fpm.broadcasts[0])
	}
	if fpm.broadcasts[1] != "END|t1" {
		t.Errorf("[1] = %q (expected implicit END|t1)", fpm.broadcasts[1])
	}
	if !strings.HasPrefix(fpm.broadcasts[2], "START|t2|Second") {
		t.Errorf("[2] = %q", fpm.broadcasts[2])
	}
}

func TestProgressBeforeStartIsNonFatalError(t *testing.T) {
	h, _, _ := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	sendCmd(t, h, `{"op":"progress","task_id":"phantom","value":0.5}`)
	ev := expectEvent(t, h, "error", 2*time.Second)
	if ev["code"] != "NO_ACTIVE_TASK" {
		t.Errorf("code: %v", ev["code"])
	}
	if ev["fatal"] != false {
		t.Errorf("fatal: %v", ev["fatal"])
	}
}

func TestPingPong(t *testing.T) {
	h, _, _ := startBridge(t)
	// Ping is allowed even before hello.
	sendCmd(t, h, `{"op":"ping","id":"p1"}`)
	ev := expectEvent(t, h, "pong", 2*time.Second)
	if ev["id"] != "p1" {
		t.Errorf("id: %v", ev["id"])
	}
	_ = h.in.Close()
	h.waitDone(t, 3*time.Second)
}

func TestInvalidCommandIsNonFatal(t *testing.T) {
	h, _, _ := startBridge(t)
	sendCmd(t, h, `{not json}`)
	ev := expectEvent(t, h, "error", 2*time.Second)
	if ev["code"] != "INVALID_COMMAND" {
		t.Errorf("code: %v", ev["code"])
	}
	if ev["fatal"] != false {
		t.Errorf("fatal: %v", ev["fatal"])
	}

	// Bridge should still be alive — send a valid command.
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	_ = h.in.Close()
	h.waitDone(t, 3*time.Second)
}

func TestBeforeHelloIsRejected(t *testing.T) {
	h, _, _ := startBridge(t)
	sendCmd(t, h, `{"op":"start","task_id":"t1","label":"x"}`)
	ev := expectEvent(t, h, "error", 2*time.Second)
	if ev["code"] != "INVALID_COMMAND" {
		t.Errorf("code: %v", ev["code"])
	}
}

func TestCloseTriggersGracefulShutdown(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)
	sendCmd(t, h, `{"op":"start","task_id":"t1","label":"Running"}`)

	sendCmd(t, h, `{"op":"close"}`)
	ev := expectEvent(t, h, "closed", 3*time.Second)
	if ev["reason"] != "sdk_close" {
		t.Errorf("reason: %v", ev["reason"])
	}

	// Active task should have triggered an END broadcast on close.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fpm.mu.Lock()
		closed := fpm.closed
		bs := append([]string(nil), fpm.broadcasts...)
		fpm.mu.Unlock()
		if closed && hasFrame(bs, "END|t1") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fpm.mu.Lock()
	defer fpm.mu.Unlock()
	if !fpm.closed {
		t.Error("peer manager Close not called")
	}
	if !hasFrame(fpm.broadcasts, "END|t1") {
		t.Errorf("expected END|t1 broadcast, got %v", fpm.broadcasts)
	}

	h.waitDone(t, 3*time.Second)
}

func TestStdinEOFTriggersGracefulShutdown(t *testing.T) {
	h, _, _ := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)

	_ = h.in.Close() // EOF
	ev := expectEvent(t, h, "closed", 3*time.Second)
	if ev["reason"] != "stdin_eof" {
		t.Errorf("reason: %v", ev["reason"])
	}
	h.waitDone(t, 3*time.Second)
}

func TestViewerJoinedEventBroadcastsPresence(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	// Pretend a viewer named vega arrived.
	fpm.mu.Lock()
	fpm.names = []string{"vega"}
	fpm.mu.Unlock()
	fpm.injectEvent(t, peer.Event{Type: peer.EventViewerJoined, Name: "vega"})

	ev := expectEvent(t, h, "viewer_joined", 2*time.Second)
	if ev["name"] != "vega" {
		t.Errorf("name: %v", ev["name"])
	}
	count := expectEvent(t, h, "viewer_count", 2*time.Second)
	if count["count"].(float64) != 1 {
		t.Errorf("count: %v", count["count"])
	}

	// Should have broadcast a V|vega frame.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fpm.mu.Lock()
		bs := append([]string(nil), fpm.broadcasts...)
		fpm.mu.Unlock()
		if hasFrame(bs, "V|vega") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fpm.mu.Lock()
	defer fpm.mu.Unlock()
	if !hasFrame(fpm.broadcasts, "V|vega") {
		t.Errorf("expected V|vega broadcast, got %v", fpm.broadcasts)
	}
}

func TestSessionFailureIsFatal(t *testing.T) {
	h, ft, _ := startBridge(t)
	ft.openErr = errors.New("server returned 403")

	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)

	ev := expectEvent(t, h, "error", 2*time.Second)
	if ev["code"] != "SESSION_FAILED" {
		t.Errorf("code: %v", ev["code"])
	}
	if ev["fatal"] != true {
		t.Errorf("fatal: %v", ev["fatal"])
	}
	h.waitDone(t, 3*time.Second)
	if h.err() == nil {
		t.Error("expected non-nil error on session failure")
	}
}

func TestPublishTickerCatchesUpNewViewers(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	sendCmd(t, h, `{"op":"start","task_id":"t1","label":"Demo"}`)
	sendCmd(t, h, `{"op":"progress","task_id":"t1","value":0.42,"n":42,"total":100}`)
	fpm.mu.Lock()
	fpm.names = []string{"vega"}
	fpm.mu.Unlock()

	// Wait for at least one publish tick (publishInterval = 500ms).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		fpm.mu.Lock()
		n := len(fpm.sessionFrames)
		fpm.mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	fpm.mu.Lock()
	defer fpm.mu.Unlock()
	if len(fpm.sessionFrames) == 0 {
		t.Fatal("publish ticker never called SessionFrames")
	}
	first := fpm.sessionFrames[0]
	if len(first) < 4 {
		t.Fatalf("expected ≥4 session frames (ID, START, P, V), got %v", first)
	}
	if !strings.HasPrefix(first[0], "ID|bridge-") {
		t.Errorf("[0] = %q", first[0])
	}
	if !strings.HasPrefix(first[1], "START|t1|Demo") {
		t.Errorf("[1] = %q", first[1])
	}
	if !strings.HasPrefix(first[2], "P|t1|0.4200") {
		t.Errorf("[2] = %q", first[2])
	}
	if first[3] != "V|vega" {
		t.Errorf("[3] = %q", first[3])
	}
}

// cullingPeerManager extends fakePeerManager so Cull returns a name on
// the first call (so tests can drive the Cull→ViewerLeft path).
type cullingPeerManager struct {
	fakePeerManager
	cullReturn []string
}

func (c *cullingPeerManager) Cull(now time.Time) []string {
	c.mu.Lock()
	c.cullCalls++
	c.mu.Unlock()
	out := c.cullReturn
	c.cullReturn = nil
	return out
}

func TestCullTickerEmitsViewerLeft(t *testing.T) {
	cpm := &cullingPeerManager{cullReturn: []string{"orion"}}
	cpm.names = []string{}
	ft := &fakeTransport{
		session: &transport.Session{
			RoomID: "abc12345", SenderSecret: "sec",
			ViewerURL: "url", ExpiresIn: 3600, MaxViewers: 8, PollIntervalHint: 1,
		},
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	events := make(chan map[string]any, 64)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var ev map[string]any
			if err := json.Unmarshal(sc.Bytes(), &ev); err == nil {
				events <- ev
			}
		}
	}()
	h := &runHarness{in: inW, events: events, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		err := RunWithOptions(context.Background(), inR, outW, Options{
			Version:          "bridge-test",
			TransportFactory: func(_ string) transportClient { return ft },
			PeerManagerFactory: func(_ peer.TransportClient, _ []webrtc.ICEServer, ev chan<- peer.Event) peerManager {
				cpm.events = ev
				return cpm
			},
		})
		_ = outW.Close()
		h.runErrMu.Lock()
		h.runErr = err
		h.runErrMu.Unlock()
	}()
	t.Cleanup(func() { _ = inW.Close() })

	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	// Cull ticker is 5s; for the test we accept a longer wait.
	ev := expectEvent(t, h, "viewer_left", 7*time.Second)
	if ev["name"] != "orion" {
		t.Errorf("name: %v", ev["name"])
	}
}

func TestPeerEventViewerLeftEmitsAndUpdatesPresence(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	// First simulate a viewer joining, then leaving.
	fpm.mu.Lock()
	fpm.names = []string{"vega", "orion"}
	fpm.mu.Unlock()

	fpm.injectEvent(t, peer.Event{Type: peer.EventViewerJoined, Name: "vega"})
	expectEvent(t, h, "viewer_joined", 2*time.Second)
	expectEvent(t, h, "viewer_count", 2*time.Second)

	// Now simulate Vega leaving; Names list shrinks.
	fpm.mu.Lock()
	fpm.names = []string{"orion"}
	fpm.mu.Unlock()
	fpm.injectEvent(t, peer.Event{Type: peer.EventViewerLeft, Name: "vega"})

	left := expectEvent(t, h, "viewer_left", 2*time.Second)
	if left["name"] != "vega" {
		t.Errorf("name: %v", left["name"])
	}
	count := expectEvent(t, h, "viewer_count", 2*time.Second)
	if count["count"].(float64) != 1 {
		t.Errorf("count after leave: %v", count["count"])
	}
}

func TestPeerEventViewerErrorEmitsTicketHandlingFailed(t *testing.T) {
	h, _, fpm := startBridge(t)
	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	fpm.injectEvent(t, peer.Event{
		Type:     peer.EventViewerError,
		TicketID: "tk-bad",
		Err:      errors.New("malformed offer"),
	})

	ev := expectEvent(t, h, "error", 2*time.Second)
	if ev["code"] != "TICKET_HANDLING_FAILED" {
		t.Errorf("code: %v", ev["code"])
	}
	if ev["fatal"] != false {
		t.Errorf("fatal: %v", ev["fatal"])
	}
}

func TestPollLoopFatalOnInvalidSecret(t *testing.T) {
	cpm := &fakePeerManager{}
	ft := &fakeTransport{
		session: &transport.Session{
			RoomID: "abc12345", SenderSecret: "sec",
			ViewerURL: "url", ExpiresIn: 3600, MaxViewers: 8, PollIntervalHint: 1,
		},
		pollErr: transport.ErrInvalidSecret,
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	events := make(chan map[string]any, 64)
	go func() {
		defer close(events)
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			var ev map[string]any
			if err := json.Unmarshal(sc.Bytes(), &ev); err == nil {
				events <- ev
			}
		}
	}()
	h := &runHarness{in: inW, events: events, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		err := RunWithOptions(context.Background(), inR, outW, Options{
			Version:          "bridge-test",
			TransportFactory: func(_ string) transportClient { return ft },
			PeerManagerFactory: func(_ peer.TransportClient, _ []webrtc.ICEServer, ev chan<- peer.Event) peerManager {
				cpm.events = ev
				return cpm
			},
		})
		_ = outW.Close()
		h.runErrMu.Lock()
		h.runErr = err
		h.runErrMu.Unlock()
	}()
	t.Cleanup(func() { _ = inW.Close() })

	sendCmd(t, h, `{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}`)
	expectEvent(t, h, "hello_ack", 2*time.Second)
	sendCmd(t, h, `{"op":"session","base_url":"https://x"}`)
	expectEvent(t, h, "session_ready", 2*time.Second)

	// PollIntervalHint is 1s, so the poll loop should fire within ~2s.
	ev := expectEvent(t, h, "error", 5*time.Second)
	if ev["code"] != "SESSION_FAILED" {
		t.Errorf("code: %v", ev["code"])
	}
	if ev["fatal"] != true {
		t.Errorf("fatal: %v", ev["fatal"])
	}
	h.waitDone(t, 5*time.Second)
}

func TestDerivePeerID(t *testing.T) {
	cases := map[string]string{
		"":         "bridge-",
		"abcd":     "bridge-abcd",
		"abcdefgh": "bridge-abcdefgh",
		"abcdefghxyz": "bridge-abcdefgh",
	}
	for in, want := range cases {
		if got := derivePeerID(in); got != want {
			t.Errorf("derivePeerID(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- helpers -------------------------------------------------------------

func hasFrame(bs []string, want string) bool {
	for _, f := range bs {
		if f == want {
			return true
		}
	}
	return false
}
