package integration

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// --- Binary build (once per test binary invocation) ---------------------

var (
	bridgeBinary string
	buildOnce    sync.Once
	buildErr     error
)

// ensureBinary compiles cmd/fernsicht-bridge into a temp location and
// returns the absolute path. The binary is shared across subtests.
func ensureBinary(t *testing.T) string {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fernsicht-bridge-integ-*")
		if err != nil {
			buildErr = fmt.Errorf("mkdir temp: %w", err)
			return
		}
		bin := filepath.Join(dir, "fernsicht-bridge")
		pkg := "../../cmd/fernsicht-bridge"
		cmd := exec.Command("go", "build",
			"-trimpath",
			"-o", bin,
			pkg,
		)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			buildErr = fmt.Errorf("go build: %w", err)
			return
		}
		bridgeBinary = bin
	})
	if buildErr != nil {
		t.Fatalf("build bridge: %v", buildErr)
	}
	return bridgeBinary
}

// --- Bridge-subprocess harness ------------------------------------------

type bridgeProc struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdoutBuf *bufio.Scanner
	stderr    *bytes_Buffer // thread-safe buffer
	events    chan map[string]any

	// done is closed (not sent on) when cmd.Wait() returns, so multiple
	// callers (waitExit + cleanup) can both observe the exit without
	// stealing the value.
	done   chan struct{}
	waitMu sync.Mutex
	waitEr error
}

// bytes_Buffer is a tiny thread-safe byte buffer so we can tee stderr
// for debugging without racing the reader goroutine.
type bytes_Buffer struct {
	mu sync.Mutex
	b  []byte
}

func (b *bytes_Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.b = append(b.b, p...)
	return len(p), nil
}

func (b *bytes_Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.b)
}

// spawnBridge launches the compiled binary and wires stdin/stdout/
// stderr. Events are parsed in a background goroutine and pushed on
// bp.events.
func spawnBridge(t *testing.T) *bridgeProc {
	t.Helper()
	bin := ensureBinary(t)

	cmd := exec.Command(bin)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("StdoutPipe: %v", err)
	}
	stderr := &bytes_Buffer{}
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	events := make(chan map[string]any, 256)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 1<<20)

	bp := &bridgeProc{
		cmd:       cmd,
		stdin:     stdin,
		stdoutBuf: scanner,
		stderr:    stderr,
		events:    events,
		done:      make(chan struct{}),
	}

	// Event-parsing goroutine.
	go func() {
		defer close(events)
		for scanner.Scan() {
			var ev map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &ev); err == nil {
				events <- ev
			}
		}
	}()

	// Reaper — closes bp.done when cmd.Wait returns. Closing (rather
	// than sending) lets multiple receivers (waitExit + Cleanup) both
	// observe the exit without one consuming the signal.
	go func() {
		err := cmd.Wait()
		bp.waitMu.Lock()
		bp.waitEr = err
		bp.waitMu.Unlock()
		close(bp.done)
	}()

	t.Cleanup(func() {
		_ = stdin.Close()
		select {
		case <-bp.done:
		case <-time.After(3 * time.Second):
			_ = cmd.Process.Kill()
			<-bp.done
		}
	})

	return bp
}

// send writes a JSON command line to the bridge subprocess's stdin.
func (bp *bridgeProc) send(t *testing.T, line string) {
	t.Helper()
	if _, err := bp.stdin.Write([]byte(line + "\n")); err != nil {
		t.Fatalf("write stdin: %v (stderr: %s)", err, bp.stderr.String())
	}
}

// expect reads events until one matching eventName arrives.
func (bp *bridgeProc) expect(t *testing.T, eventName string, timeout time.Duration) map[string]any {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-bp.events:
			if !ok {
				t.Fatalf("events channel closed before %q (stderr: %s)", eventName, bp.stderr.String())
			}
			if ev["event"] == eventName {
				return ev
			}
			t.Logf("skip: %v", ev)
		case <-deadline:
			t.Fatalf("timeout waiting for event %q (stderr: %s)", eventName, bp.stderr.String())
			return nil
		}
	}
}

// waitExit blocks until the bridge exits, returning its exit code.
func (bp *bridgeProc) waitExit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-bp.done:
		bp.waitMu.Lock()
		err := bp.waitEr
		bp.waitMu.Unlock()
		if err == nil {
			return 0
		}
		if ee, ok := err.(*exec.ExitError); ok {
			return ee.ExitCode()
		}
		t.Fatalf("unexpected exit error: %v", err)
		return -1
	case <-time.After(timeout):
		t.Fatalf("bridge did not exit within %s (stderr: %s)", timeout, bp.stderr.String())
		return -1
	}
}

// --- Pion viewer + ICE routing loop -------------------------------------

// viewerSession wraps a pion PC configured as the viewer (offerer).
// It owns an ICE-routing goroutine that shuttles candidates between
// its PC and the fakeSignalingServer for a specific ticketID.
type viewerSession struct {
	pc    *webrtc.PeerConnection
	dc    *webrtc.DataChannel
	inbox chan string
	open  chan struct{}
}

func setupViewer(t *testing.T, fs *fakeSignalingServer, ticketID string) *viewerSession {
	t.Helper()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create viewer pc: %v", err)
	}
	t.Cleanup(func() { _ = pc.Close() })

	dc, err := pc.CreateDataChannel("fernsicht", &webrtc.DataChannelInit{Ordered: boolPtr(true)})
	if err != nil {
		t.Fatalf("create data channel: %v", err)
	}

	inbox := make(chan string, 64)
	open := make(chan struct{})
	var openOnce sync.Once
	dc.OnOpen(func() { openOnce.Do(func() { close(open) }) })
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		if msg.IsString {
			inbox <- string(msg.Data)
		}
	})

	// Forward viewer-gathered ICE into the fake server so the bridge
	// will receive it on its next PollViewerICE call.
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		init := c.ToJSON()
		cand := map[string]any{"candidate": init.Candidate}
		if init.SDPMid != nil {
			cand["sdpMid"] = *init.SDPMid
		}
		if init.SDPMLineIndex != nil {
			cand["sdpMLineIndex"] = int(*init.SDPMLineIndex)
		}
		fs.PushViewerICE(ticketID, cand)
	})

	// Generate offer, wait for ICE gather, queue as a ticket.
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	gatherDone := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatalf("set local desc: %v", err)
	}
	select {
	case <-gatherDone:
	case <-time.After(3 * time.Second):
		t.Fatal("viewer ICE gather timeout")
	}

	// Queue the fully-gathered offer as a ticket. (We include all
	// candidates embedded in the SDP, so trickle ICE is somewhat
	// redundant for the offer — but the bridge's sender-ICE path still
	// needs to flow.)
	fs.QueueTicket(ticketID, pc.LocalDescription().SDP)

	// Background goroutine to pull sender-ICE from the fake server
	// into the viewer's PC. This runs until the viewer's connection
	// is established or the test cleans up.
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			if pc.ConnectionState() == webrtc.PeerConnectionStateConnected {
				return
			}
			cands := fs.DrainSenderICE(ticketID)
			for _, c := range cands {
				candStr, _ := c["candidate"].(string)
				if candStr == "" {
					continue
				}
				// Strip the "candidate:" prefix pion emits.
				candStr = strings.TrimPrefix(candStr, "candidate:")
				init := webrtc.ICECandidateInit{Candidate: candStr}
				if mid, ok := c["sdpMid"].(string); ok {
					init.SDPMid = &mid
				}
				if idx, ok := c["sdpMLineIndex"].(float64); ok {
					u := uint16(idx)
					init.SDPMLineIndex = &u
				}
				_ = pc.AddICECandidate(init)
			}
		}
	}()

	return &viewerSession{pc: pc, dc: dc, inbox: inbox, open: open}
}

// applyAnswer sets the bridge's SDP answer on the viewer PC, which
// kicks off the remaining handshake.
func (v *viewerSession) applyAnswer(t *testing.T, sdp string) {
	t.Helper()
	err := v.pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeAnswer,
		SDP:  sdp,
	})
	if err != nil {
		t.Fatalf("set remote desc: %v", err)
	}
}

func boolPtr(b bool) *bool { return &b }

// --- The actual end-to-end test -----------------------------------------

// TestEndToEndHandshakeAndBroadcast drives the full §4 protocol:
//
//	hello → hello_ack
//	session → session_ready
//	viewer queues a ticket; bridge handshakes via the fake server
//	viewer DataChannel opens; viewer sends HELLO → bridge emits joined + count
//	start / progress / end via stdin → viewer receives matching wire frames
//	close → bridge emits closed; process exits 0
func TestEndToEndHandshakeAndBroadcast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = ctx

	fs := newFakeSignalingServer(t)
	bp := spawnBridge(t)

	// --- hello
	bp.send(t, `{"op":"hello","sdk":"integ","sdk_version":"0.0.0","protocol":1}`)
	ack := bp.expect(t, "hello_ack", 3*time.Second)
	if ack["protocol"].(float64) != 1 {
		t.Errorf("protocol in ack: %v", ack["protocol"])
	}

	// --- session
	bp.send(t, fmt.Sprintf(`{"op":"session","base_url":"%s"}`, fs.URL()))
	ready := bp.expect(t, "session_ready", 3*time.Second)
	if ready["room_id"] != fs.RoomID() {
		t.Errorf("session_ready.room_id = %v, want %v", ready["room_id"], fs.RoomID())
	}

	// --- viewer setup + handshake
	const ticketID = "integ-ticket-1"
	viewer := setupViewer(t, fs, ticketID)

	// Wait for the bridge to post its answer (bridge polls on its
	// timer; PollIntervalHint in the fake is 1s).
	ans := fs.WaitForAnswer(t, ticketID, 10*time.Second)
	viewer.applyAnswer(t, ans.SDP)

	// DataChannel should open on the viewer side.
	select {
	case <-viewer.open:
	case <-time.After(10 * time.Second):
		t.Fatalf("viewer DataChannel never opened (conn=%v, stderr: %s)",
			viewer.pc.ConnectionState(), bp.stderr.String())
	}

	// --- HELLO from the viewer → bridge emits viewer_joined + viewer_count
	if err := viewer.dc.SendText("HELLO|vega"); err != nil {
		t.Fatalf("send HELLO: %v", err)
	}
	joined := bp.expect(t, "viewer_joined", 3*time.Second)
	if joined["name"] != "vega" {
		t.Errorf("viewer_joined.name = %v", joined["name"])
	}
	count := bp.expect(t, "viewer_count", 3*time.Second)
	if count["count"].(float64) != 1 {
		t.Errorf("viewer_count.count = %v", count["count"])
	}

	// --- start / progress / end via stdin → viewer receives matching frames
	bp.send(t, `{"op":"start","task_id":"t1","label":"Training"}`)
	bp.send(t, `{"op":"progress","task_id":"t1","value":0.5,"n":50,"total":100}`)
	bp.send(t, `{"op":"end","task_id":"t1"}`)

	// The viewer should receive a V| (from the join broadcast), then
	// START, then P, then END. The ordering of V| vs START may
	// interleave with the viewer_joined event — so we look for
	// specific prefixes rather than exact ordering.
	gotStart := false
	gotProgress := false
	gotEnd := false
	deadline := time.After(5 * time.Second)
drain:
	for {
		select {
		case msg := <-viewer.inbox:
			switch {
			case strings.HasPrefix(msg, "START|t1|Training"):
				gotStart = true
			case strings.HasPrefix(msg, "P|t1|0.5000"):
				gotProgress = true
			case msg == "END|t1":
				gotEnd = true
			}
			if gotStart && gotProgress && gotEnd {
				break drain
			}
		case <-deadline:
			break drain
		}
	}
	if !gotStart {
		t.Errorf("viewer never received START frame")
	}
	if !gotProgress {
		t.Errorf("viewer never received P frame")
	}
	if !gotEnd {
		t.Errorf("viewer never received END frame")
	}

	// --- close → clean exit
	// peer.Manager.Close caps the per-peer close phase at the drain
	// timeout, so even an unresponsive viewer can't keep the bridge
	// alive past gracefulCloseBudget (2s).
	bp.send(t, `{"op":"close"}`)
	closedEv := bp.expect(t, "closed", 10*time.Second)
	if closedEv["reason"] != "sdk_close" {
		t.Errorf("closed.reason = %v", closedEv["reason"])
	}
	if code := bp.waitExit(t, 10*time.Second); code != 0 {
		t.Errorf("exit code = %d, want 0 (stderr: %s)", code, bp.stderr.String())
	}
}

// TestEndToEndCleanCloseViaStdinEOF exercises the simpler close-via-EOF
// path (no `{"op":"close"}` command), to isolate whether issues with
// the `close` command path are command-specific or general.
func TestEndToEndCleanCloseViaStdinEOF(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in -short mode")
	}

	bp := spawnBridge(t)
	bp.send(t, `{"op":"hello","sdk":"integ","sdk_version":"0.0.0","protocol":1}`)
	bp.expect(t, "hello_ack", 3*time.Second)

	if err := bp.stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}
	closedEv := bp.expect(t, "closed", 5*time.Second)
	if closedEv["reason"] != "stdin_eof" {
		t.Errorf("closed.reason = %v", closedEv["reason"])
	}
	if code := bp.waitExit(t, 5*time.Second); code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
}
