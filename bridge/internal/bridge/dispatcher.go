package bridge

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/MuteJester/fernsicht/bridge/internal/peer"
	"github.com/MuteJester/fernsicht/bridge/internal/proto"
	"github.com/MuteJester/fernsicht/bridge/internal/transport"
	"github.com/MuteJester/fernsicht/bridge/internal/wire"
	"github.com/pion/webrtc/v4"
)

// dispatcher is the orchestrator. It is the sole owner of session and
// task state per plan §7. All mutations happen on the goroutine
// running run().
type dispatcher struct {
	in  io.Reader
	out io.Writer

	version           string
	iceServers        []webrtc.ICEServer
	transportFactory  transportFactory
	peerMgrFactory    peerManagerFactory
	now               func() time.Time

	// Subsystem clients. Created lazily — transport on session,
	// peerMgr right after.
	transport transportClient
	peerMgr   peerManager
	backoff   *transport.Backoff

	// State machine.
	validator *proto.OrderingValidator

	// Session info, populated after the `session` command succeeds.
	session *transport.Session
	peerID  string // "bridge-<roomid[:8]>"

	// Active task state (latest START, latest progress).
	activeTaskID    string
	activeTaskLabel string
	latestProgress  *proto.ProgressCmd

	// Channels.
	cmds       chan proto.Command
	peerEvents chan peer.Event
	tickets    chan transport.Ticket
	fatalCh    chan fatalSignal
	writes     chan []byte // single-writer to stdout

	// Signals from goroutines.
	pollLoopCancel context.CancelFunc

	// Closed-by-this-side flag — set when we choose to shut down.
	// Prevents double-close paths.
	closing bool

	// Mutex protects the small amount of state read by goroutines
	// other than the dispatcher (specifically: closing, transport for
	// pollLoop). Most state is touched only from the dispatcher loop.
	mu sync.Mutex
}

type fatalSignal struct {
	code    string
	message string
}

func newDispatcher(in io.Reader, out io.Writer, opts Options) *dispatcher {
	version := opts.Version
	if version == "" {
		version = Version
	}
	tf := opts.TransportFactory
	if tf == nil {
		tf = defaultTransportFactory
	}
	pmf := opts.PeerManagerFactory
	if pmf == nil {
		pmf = defaultPeerManagerFactory
	}
	now := opts.NowFn
	if now == nil {
		now = time.Now
	}
	ice := opts.ICEServers
	if ice == nil {
		ice = defaultICEServers()
	}

	return &dispatcher{
		in:               in,
		out:              out,
		version:          version,
		iceServers:       ice,
		transportFactory: tf,
		peerMgrFactory:   pmf,
		now:              now,
		validator:        proto.NewOrderingValidator(),
		backoff:          transport.DefaultBackoff(),
		cmds:             make(chan proto.Command, commandChanBuffer),
		peerEvents:       make(chan peer.Event, peerEventsChanBuffer),
		tickets:          make(chan transport.Ticket, ticketsChanBuffer),
		fatalCh:          make(chan fatalSignal, 1),
		writes:           make(chan []byte, eventWritesBuffer),
	}
}

// --- Top-level run ------------------------------------------------------

func (d *dispatcher) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Single-writer for stdout.
	writerDone := make(chan struct{})
	go d.runEventWriter(writerDone)
	defer func() {
		close(d.writes)
		<-writerDone
	}()

	// Stdin reader.
	readerDone := make(chan struct{})
	go d.runStdinReader(ctx, readerDone)

	reason, err := d.runLoop(ctx, readerDone)
	d.gracefulShutdown(ctx, reason)
	return err
}

// runLoop is the dispatcher's main select. Returns the close reason
// and any error to surface.
func (d *dispatcher) runLoop(ctx context.Context, readerDone <-chan struct{}) (string, error) {
	publishTick := time.NewTicker(publishInterval)
	drainTick := time.NewTicker(drainInterval)
	cullTick := time.NewTicker(cullInterval)
	defer publishTick.Stop()
	defer drainTick.Stop()
	defer cullTick.Stop()

	for {
		select {
		case cmd, ok := <-d.cmds:
			if !ok {
				return proto.CloseReasonStdinEOF, nil
			}
			if shouldExit, reason, err := d.handleCommand(ctx, cmd); shouldExit {
				return reason, err
			}

		case ev := <-d.peerEvents:
			d.handlePeerEvent(ev)

		case t := <-d.tickets:
			if d.peerMgr != nil {
				d.peerMgr.Add(ctx, t)
			}

		case fatal := <-d.fatalCh:
			d.emit(proto.NewError(fatal.code, fatal.message, true))
			return proto.CloseReasonFatalError, fmt.Errorf("%w: %s", ErrFatal, fatal.message)

		case <-publishTick.C:
			d.publishLatestProgress()

		case <-drainTick.C:
			if d.peerMgr != nil {
				d.peerMgr.Drain()
			}

		case <-cullTick.C:
			d.cullStalePeers()

		case <-readerDone:
			return proto.CloseReasonStdinEOF, nil

		case <-ctx.Done():
			return proto.CloseReasonSignal, nil
		}
	}
}

// --- Stdin reader -------------------------------------------------------

func (d *dispatcher) runStdinReader(ctx context.Context, done chan<- struct{}) {
	defer close(done)
	scanner := bufio.NewScanner(d.in)
	scanner.Buffer(make([]byte, 64*1024), proto.MaxLineLength)

	for scanner.Scan() {
		if ctx.Err() != nil {
			return
		}
		line := scanner.Bytes()
		// Tolerant parse: invalid lines surface as INVALID_COMMAND
		// events; we keep reading.
		cmd, err := proto.ParseCommand(line)
		if err != nil {
			d.emit(proto.NewError(proto.ErrCodeInvalidCommand, err.Error(), false))
			continue
		}
		select {
		case d.cmds <- cmd:
		case <-ctx.Done():
			return
		}
	}

	// Detect "line too long" specifically so we can return a
	// targeted error code. bufio.Scanner does not return a typed
	// error for this; the Token-too-long case shows up as
	// scanner.Err() == bufio.ErrTooLong.
	if err := scanner.Err(); err != nil {
		if errors.Is(err, bufio.ErrTooLong) {
			d.emit(proto.NewError(proto.ErrCodeLineTooLong,
				fmt.Sprintf("stdin line exceeded %d bytes", proto.MaxLineLength), false))
		}
		// Other errors (closed pipe, etc.) just end the reader; the
		// dispatcher treats it as stdin EOF.
	}
	close(d.cmds)
}

// --- Event writer (single-writer to stdout) -----------------------------

func (d *dispatcher) runEventWriter(done chan<- struct{}) {
	defer close(done)
	for buf := range d.writes {
		_, _ = d.out.Write(buf)
	}
}

// emit serializes any *Event from package proto and ships it to stdout.
// Must NOT block the caller — the writes channel is buffered; on
// overflow we drop the event silently (the orchestrator should size
// writes generously).
func (d *dispatcher) emit(e any) {
	data, err := marshalEvent(e)
	if err != nil {
		// Synchronously write a fallback error. If even this fails,
		// give up — there's nowhere else to surface it.
		fallback := []byte(`{"event":"error","code":"INTERNAL","message":"failed to marshal event","fatal":false}` + "\n")
		select {
		case d.writes <- fallback:
		default:
		}
		return
	}
	select {
	case d.writes <- data:
	default:
	}
}

// marshalEvent uses proto.WriteEventLine on a discard-and-capture
// buffer. We could call json.Marshal directly, but going through
// WriteEventLine ensures the trailing newline is consistent.
func marshalEvent(e any) ([]byte, error) {
	var buf serBuffer
	if err := proto.WriteEventLine(&buf, e); err != nil {
		return nil, err
	}
	return buf.bytes(), nil
}

type serBuffer struct{ b []byte }

func (s *serBuffer) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *serBuffer) bytes() []byte                { return s.b }

// --- Command handling ---------------------------------------------------

// handleCommand applies cmd. Returns (shouldExit, reason, err) — when
// shouldExit is true, the dispatcher exits the runLoop with that
// reason and error.
func (d *dispatcher) handleCommand(ctx context.Context, cmd proto.Command) (bool, string, error) {
	if err := d.validator.Validate(cmd); err != nil {
		if errors.Is(err, proto.ErrSilentDrop) {
			return false, "", nil
		}
		d.emit(proto.NewError(proto.ErrCodeInvalidCommand, err.Error(), false))
		return false, "", nil
	}
	d.validator.Mark(cmd)

	switch c := cmd.(type) {
	case proto.HelloCmd:
		return d.handleHello(c)
	case proto.SessionCmd:
		return d.handleSession(ctx, c)
	case proto.StartCmd:
		d.handleStart(c)
	case proto.ProgressCmd:
		d.handleProgress(c)
	case proto.EndCmd:
		d.handleEnd(c)
	case proto.PingCmd:
		d.emit(proto.NewPong(c.ID))
	case proto.CloseCmd:
		return true, proto.CloseReasonSDKClose, nil
	default:
		d.emit(proto.NewError(proto.ErrCodeInvalidCommand,
			fmt.Sprintf("unhandled command op: %s", cmd.Op()), false))
	}
	return false, "", nil
}

func (d *dispatcher) handleHello(c proto.HelloCmd) (bool, string, error) {
	if c.Protocol != proto.SupportedProtocolVersion {
		d.emit(proto.NewError(proto.ErrCodeProtocolMismatch,
			fmt.Sprintf("SDK protocol %d != bridge protocol %d", c.Protocol, proto.SupportedProtocolVersion),
			true))
		return true, proto.CloseReasonFatalError,
			fmt.Errorf("%w: sdk=%d bridge=%d", ErrProtocolMismatch, c.Protocol, proto.SupportedProtocolVersion)
	}
	d.emit(proto.NewHelloAck(d.version))
	return false, "", nil
}

func (d *dispatcher) handleSession(ctx context.Context, c proto.SessionCmd) (bool, string, error) {
	d.transport = d.transportFactory(c.BaseURL)
	cfg := transport.SessionConfig{
		APIKey:     c.JoinSecret,
		MaxViewers: c.MaxViewers,
	}
	session, err := d.transport.OpenSession(ctx, cfg)
	if err != nil {
		d.emit(proto.NewError(proto.ErrCodeSessionFailed, err.Error(), true))
		return true, proto.CloseReasonFatalError, fmt.Errorf("%w: %v", ErrSessionFailed, err)
	}
	d.session = session
	d.transport.SetSenderSecret(session.SenderSecret)
	d.peerID = derivePeerID(session.RoomID)

	// Build the peer manager now that we have a transport with the
	// secret populated. The peer manager talks to the same transport
	// (via the peer.TransportClient subset interface).
	d.peerMgr = d.peerMgrFactory(d.transport.(peer.TransportClient), d.iceServers, d.peerEvents)

	// Start the poll loop.
	pollCtx, cancel := context.WithCancel(ctx)
	d.pollLoopCancel = cancel
	go d.runPollLoop(pollCtx, session)

	d.emit(proto.NewSessionReady(
		session.RoomID,
		session.SenderSecret,
		session.ViewerURL,
		session.ExpiresAt,
		session.ExpiresIn,
		session.MaxViewers,
		session.PollIntervalHint,
	))
	return false, "", nil
}

func (d *dispatcher) handleStart(c proto.StartCmd) {
	// Per §4.5: a new start while another is active implicitly ends
	// the previous one.
	if d.activeTaskID != "" && d.activeTaskID != c.TaskID {
		d.broadcast(wire.End(d.activeTaskID))
	}
	d.activeTaskID = c.TaskID
	d.activeTaskLabel = c.Label
	d.latestProgress = nil
	d.broadcast(wire.Start(c.TaskID, c.Label))
}

func (d *dispatcher) handleProgress(c proto.ProgressCmd) {
	if d.activeTaskID == "" || c.TaskID != d.activeTaskID {
		// §4.5: drop with non-fatal error.
		d.emit(proto.NewError(proto.ErrCodeNoActiveTask,
			fmt.Sprintf("progress for unknown task %q", c.TaskID), false))
		return
	}
	d.latestProgress = &c
	d.broadcast(progressFrame(c))
}

func (d *dispatcher) handleEnd(c proto.EndCmd) {
	if d.activeTaskID == "" || c.TaskID != d.activeTaskID {
		d.emit(proto.NewError(proto.ErrCodeNoActiveTask,
			fmt.Sprintf("end for unknown task %q", c.TaskID), false))
		return
	}
	d.broadcast(wire.End(c.TaskID))
	d.activeTaskID = ""
	d.activeTaskLabel = ""
	d.latestProgress = nil
}

// publishLatestProgress is called on the publish ticker. It also
// catches up newly-connected viewers via SessionFrames.
func (d *dispatcher) publishLatestProgress() {
	if d.peerMgr == nil {
		return
	}
	// Build session frames for any viewer that hasn't received them.
	if d.activeTaskID != "" {
		frames := []string{
			wire.Identity(d.peerID),
			wire.Start(d.activeTaskID, d.activeTaskLabel),
		}
		if d.latestProgress != nil {
			frames = append(frames, progressFrame(*d.latestProgress))
		}
		if names := d.peerMgr.Names(); len(names) > 0 {
			frames = append(frames, wire.Presence(names))
		}
		d.peerMgr.SessionFrames(frames)
	}
}

func (d *dispatcher) cullStalePeers() {
	if d.peerMgr == nil {
		return
	}
	if removed := d.peerMgr.Cull(d.now()); len(removed) > 0 {
		// Refresh presence on cull (loss of named viewers).
		d.broadcastPresence()
		for _, name := range removed {
			d.emit(proto.NewViewerLeft(name))
		}
		d.emit(proto.NewViewerCount(d.peerMgr.Names()))
	}
}

// --- Peer-event handling -----------------------------------------------

func (d *dispatcher) handlePeerEvent(ev peer.Event) {
	switch ev.Type {
	case peer.EventViewerJoined:
		d.emit(proto.NewViewerJoined(ev.Name))
		d.broadcastPresence()
		d.emit(proto.NewViewerCount(d.peerMgr.Names()))
	case peer.EventViewerLeft:
		if ev.Name != "" {
			d.emit(proto.NewViewerLeft(ev.Name))
			d.broadcastPresence()
			d.emit(proto.NewViewerCount(d.peerMgr.Names()))
		}
	case peer.EventViewerError:
		msg := ""
		if ev.Err != nil {
			msg = ev.Err.Error()
		}
		d.emit(proto.NewError(proto.ErrCodeTicketHandlingFailed, msg, false))
	}
}

// --- Poll loop ----------------------------------------------------------

func (d *dispatcher) runPollLoop(ctx context.Context, session *transport.Session) {
	interval := time.Duration(session.PollIntervalHint) * time.Second
	if interval <= 0 {
		interval = defaultPollInterval
	}

	for ctx.Err() == nil {
		tickets, err := d.transport.PollTickets(ctx, session.RoomID)
		if err == nil {
			d.backoff.RecordSuccess()
			for _, t := range tickets {
				select {
				case d.tickets <- t:
				case <-ctx.Done():
					return
				}
			}
			select {
			case <-time.After(interval):
			case <-ctx.Done():
				return
			}
			continue
		}

		// Fatal upstream errors.
		if errors.Is(err, transport.ErrInvalidSecret) || errors.Is(err, transport.ErrRoomNotFound) {
			d.signalFatal(proto.ErrCodeSessionFailed, err.Error())
			return
		}

		// Transient: backoff and possibly alert/fatal.
		ev, delay := d.backoff.RecordFailure(d.now())
		switch ev {
		case transport.BackoffAlert:
			d.emit(proto.NewError(proto.ErrCodeSignalingUnreachable, err.Error(), false))
		case transport.BackoffFatal:
			d.signalFatal(proto.ErrCodeSignalingUnreachable, err.Error())
			return
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
	}
}

func (d *dispatcher) signalFatal(code, message string) {
	select {
	case d.fatalCh <- fatalSignal{code: code, message: message}:
	default:
		// fatalCh is buffered 1; a second fatal is redundant.
	}
}

// --- Helpers -----------------------------------------------------------

// broadcast queues frame for all current viewers and triggers an
// immediate drain so it goes out without waiting for the tick.
func (d *dispatcher) broadcast(frame string) {
	if d.peerMgr == nil {
		return
	}
	d.peerMgr.Broadcast(frame)
}

// broadcastPresence sends a fresh V| frame containing the current
// viewer roster to all connected viewers.
func (d *dispatcher) broadcastPresence() {
	if d.peerMgr == nil {
		return
	}
	d.peerMgr.Broadcast(wire.Presence(d.peerMgr.Names()))
}

// gracefulShutdown is the §8 close path:
//  1. Stop the poll loop (no new tickets).
//  2. If a task is active, broadcast END.
//  3. Drain outbound queues for up to gracefulCloseBudget.
//  4. Close all peer connections.
//  5. Emit `closed` with the reason.
func (d *dispatcher) gracefulShutdown(ctx context.Context, reason string) {
	if d.pollLoopCancel != nil {
		d.pollLoopCancel()
	}
	if d.peerMgr != nil {
		if d.activeTaskID != "" {
			d.peerMgr.Broadcast(wire.End(d.activeTaskID))
		}
		_ = d.peerMgr.Close(ctx, gracefulCloseBudget)
	}
	d.emit(proto.NewClosed(reason))
}

// derivePeerID matches the Python SDK's `py-<roomid[:8]>` convention
// but uses `bridge-` to distinguish the source.
func derivePeerID(roomID string) string {
	prefix := "bridge-"
	if len(roomID) <= 8 {
		return prefix + roomID
	}
	return prefix + roomID[:8]
}

// progressFrame translates a parsed proto.ProgressCmd back into the
// pipe-delimited wire frame.
func progressFrame(c proto.ProgressCmd) string {
	value := 0.0
	if c.Value != nil {
		value = *c.Value
	}
	return wire.Progress(c.TaskID, value, wire.ProgressOpts{
		Elapsed: c.Elapsed,
		ETA:     c.ETA,
		N:       c.N,
		Total:   c.Total,
		Rate:    c.Rate,
		Unit:    c.Unit,
	})
}
