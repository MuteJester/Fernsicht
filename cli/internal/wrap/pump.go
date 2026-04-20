package wrap

import (
	"bytes"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/MuteJester/fernsicht/cli/internal/parse"
)

// Pump consumes the wrapped command's byte stream and (a) forwards
// the bytes to the caller's terminal — minus magic-prefix lines —
// and (b) feeds them through a LineBuffer for parsing.
//
// The Pump is the sole implementation of "Phase 2 reads bytes; Phase
// 3 hands ticks to the bridge." Phase 3 will replace the
// fmt.Fprintln in the OnTick callback with bridge.Tick(...) without
// touching this file.
//
// One Pump per stream: in pipe mode the wrap layer creates two (one
// each for stdout / stderr); in pty mode there's one combined stream
// from the master fd.
//
// Goroutine-safety: not safe for concurrent Write. Each Pump is owned
// by exactly one goroutine driving io.Copy(pump, source).
type Pump struct {
	// Forward receives non-magic bytes byte-faithfully (no
	// transformation). Magic lines are dropped from this stream so
	// they don't leak into log files / downstream pipes.
	Forward io.Writer

	// Registry / Confidence / TUI / EventLog drive the parser side.
	// All optional — a nil Registry disables Tier-1 parsing
	// (`--no-detect`); a nil EventLog silences `[parse]` debug lines.
	Registry   *parse.Registry
	Confidence *parse.Confidence
	TUI        *parse.TUI

	// EventLog is where parser/lifecycle output goes. nil ≡ discard.
	EventLog io.Writer

	// OnTick is called for every "real" tick (Tier-1 match that
	// passes confidence + magic-prefix progress event). Phase 3 wires
	// this to bridge.Tick. Phase 2 leaves it nil; Pump auto-falls
	// back to printing `[parse]` lines via EventLog.
	OnTick func(parse.Tick)

	// OnLifecycle is called for magic start/end/label/url events.
	// Phase 3 wires this to bridge lifecycle calls.
	OnLifecycle func(parse.MagicLine)

	// MagicEnabled / StrictMagic are policy flags.
	//
	// Tier-1 enable/disable is encoded in Registry itself: --no-detect
	// calls Registry.Disable() which empties built-ins. Custom parsers
	// (--pattern, .fernsicht.toml) survive Disable() and continue to
	// fire — they're explicit user-asked-for matches, not noisy
	// auto-detection.
	MagicEnabled bool // false ≡ --no-magic; pass-through, no interception
	StrictMagic  bool // true ≡ --strict-magic; invalid magic → fatal

	// Debug, when true, makes the pump emit `[parse]` lines to
	// EventLog even when OnTick is set — useful for verifying the
	// parser pipeline end-to-end without inspecting bridge state.
	Debug bool

	// OnStrictViolation is invoked once on the first strict-magic
	// violation so the wrap layer can terminate the wrapped command
	// immediately (without it, the violation only surfaces after the
	// command exits naturally — hours later for a long run).
	OnStrictViolation func()

	// FatalErr captures a strict-magic violation; the wrap layer
	// reads it after Pump's reader EOFs and exits 250.
	fatalMu sync.Mutex
	fatalErr error

	// State machine for line-start magic-prefix lookahead.
	state   pumpState
	accum   []byte // held bytes during inspection (max len(MagicPrefix))

	// LineBuffer for the parser side. Created lazily so callers don't
	// have to wire it up.
	lb *parse.LineBuffer
}

type pumpState int

const (
	stAwaitLineStart pumpState = iota
	stInspectPrefix
	stForwardLine
	stSuppressMagic
)

// FatalErr returns the first strict-magic violation seen during the
// Pump's lifetime, or nil. Wrap layer consults this after EOF.
func (p *Pump) FatalErr() error {
	p.fatalMu.Lock()
	defer p.fatalMu.Unlock()
	return p.fatalErr
}

// recordFatal stores the first strict-magic violation. Subsequent
// violations are silently dropped (we already have something to
// surface). Returns true if THIS call was the first.
func (p *Pump) recordFatal(err error) bool {
	p.fatalMu.Lock()
	defer p.fatalMu.Unlock()
	if p.fatalErr == nil {
		p.fatalErr = err
		return true
	}
	return false
}

// init lazily wires the LineBuffer.
func (p *Pump) init() {
	if p.lb != nil {
		return
	}
	p.lb = &parse.LineBuffer{
		Emit:      p.emitLine,
		EventSink: p.handleEvent,
	}
}

// Write satisfies io.Writer — each call processes a chunk of bytes
// from the wrapped command's pipe / pty.
//
// The state machine has four states:
//   - stAwaitLineStart: at the start of a fresh line; about to
//     decide whether it's magic.
//   - stInspectPrefix: we've seen 1..len(MagicPrefix)-1 bytes that
//     are still consistent with the magic prefix; deciding.
//   - stForwardLine: confirmed non-magic; stream rest of line.
//   - stSuppressMagic: confirmed magic; drop bytes from Forward but
//     keep feeding linebuf for parsing.
//
// All forwarded bytes pass through linebuf too so its emit/event
// pipeline runs uniformly.
func (p *Pump) Write(chunk []byte) (int, error) {
	p.init()

	for _, b := range chunk {
		p.processByte(b)
		if b == '\n' || b == '\r' {
			// Reset to await next line start regardless of state.
			p.state = stAwaitLineStart
		}
	}
	return len(chunk), nil
}

// Flush emits any buffered partial line (e.g., process exited mid-
// line). The wrap layer calls this after the source EOFs.
func (p *Pump) Flush() {
	p.init()
	// If we were holding accum bytes for prefix inspection, flush
	// them as forwarded text + lineBuf.
	if len(p.accum) > 0 {
		_, _ = p.Forward.Write(p.accum)
		_, _ = p.lb.Write(p.accum)
		p.accum = p.accum[:0]
	}
	p.lb.Flush()
}

// processByte routes one byte through the state machine WITHOUT
// resetting state on \n/\r — that's done in Write so the boundary
// byte itself is processed first.
func (p *Pump) processByte(b byte) {
	switch p.state {
	case stAwaitLineStart:
		if !p.MagicEnabled || b != parse.MagicPrefix[0] {
			p.passThrough(b)
			if b != '\n' && b != '\r' {
				p.state = stForwardLine
			}
			return
		}
		// Possible magic prefix start.
		p.accum = append(p.accum[:0], b)
		p.state = stInspectPrefix

	case stInspectPrefix:
		p.accum = append(p.accum, b)
		expected := parse.MagicPrefix[len(p.accum)-1]
		if b != expected {
			// Mismatch — flush accum and switch to forwarding.
			_, _ = p.Forward.Write(p.accum)
			_, _ = p.lb.Write(p.accum)
			p.accum = p.accum[:0]
			p.state = stForwardLine
			return
		}
		if len(p.accum) == len(parse.MagicPrefix) {
			// Full prefix matched — switch to suppress mode. Feed
			// the prefix bytes into linebuf so the emit callback
			// recognizes them.
			_, _ = p.lb.Write(p.accum)
			p.accum = p.accum[:0]
			p.state = stSuppressMagic
		}
		// else: still inspecting; hold bytes.

	case stForwardLine:
		p.passThrough(b)

	case stSuppressMagic:
		// Don't forward — but feed parser so emit callback sees the line.
		_, _ = p.lb.Write([]byte{b})
	}
}

// passThrough writes b to Forward and feeds it to the LineBuffer.
func (p *Pump) passThrough(b byte) {
	_, _ = p.Forward.Write([]byte{b})
	_, _ = p.lb.Write([]byte{b})
}

// emitLine is the LineBuffer's per-line callback.
func (p *Pump) emitLine(stripped, raw []byte) {
	if len(bytes.TrimSpace(stripped)) == 0 {
		return
	}
	line := string(stripped)

	// Magic prefix takes precedence over Tier-1 parsing.
	if p.MagicEnabled {
		if mp, ok, err := parse.MagicParse(line); ok {
			p.handleMagic(mp, line, err)
			return
		}
	}

	// Tier-1 + custom-pattern matching. Registry.Disable() (called by
	// --no-detect upstream) empties the built-in slice, so MatchFirst
	// will only return custom-parser matches in that case.
	if p.Registry == nil {
		return
	}
	if p.TUI != nil && p.TUI.Active() {
		return
	}

	tk, name, ok := p.Registry.MatchFirst(line)
	if !ok {
		return
	}
	if p.Confidence != nil && !p.Confidence.Match(name, nowFunc()) {
		return
	}
	p.fireTick(tk)
}

// handleMagic processes a magic-prefix line. Called from emitLine.
// Lines arrive here AFTER the suppression state machine already
// stripped them from Forward; our job is purely parsing semantics.
func (p *Pump) handleMagic(mp parse.MagicLine, line string, err error) {
	if err != nil {
		// Always warn (regardless of strict).
		fmt.Fprintf(p.errLogOrStderr(),
			"[fernsicht] warn: invalid magic prefix: %v (line: %q)\n", err, line)
		if p.StrictMagic {
			first := p.recordFatal(fmt.Errorf("strict-magic: %v", err))
			if first && p.OnStrictViolation != nil {
				// Fire-and-forget — wrap layer kills the process group.
				p.OnStrictViolation()
			}
		}
		return
	}

	switch mp.Event {
	case parse.MagicProgress:
		p.fireTick(mp.Tick)
	case parse.MagicStart, parse.MagicEnd, parse.MagicLabel, parse.MagicURL:
		p.fireLifecycle(mp)
	}
}

// handleEvent receives ANSI structural events (alt-screen enter/exit)
// from the LineBuffer.
func (p *Pump) handleEvent(ev parse.AnsiEvent) {
	if p.TUI == nil {
		return
	}
	if warn := p.TUI.HandleEvent(ev); warn {
		fmt.Fprintln(p.errLogOrStderr(), parse.WarnMessage)
	}
}

func (p *Pump) fireTick(t parse.Tick) {
	if p.Debug || p.OnTick == nil {
		w := p.errLogOrStderr()
		fmt.Fprintf(w, "[parse] %s n=%d total=%d value=%.3f",
			t.Source, t.N, t.Total, t.Value)
		if t.Unit != "" {
			fmt.Fprintf(w, " unit=%s", t.Unit)
		}
		if t.Label != "" {
			fmt.Fprintf(w, " label=%q", t.Label)
		}
		fmt.Fprintln(w)
	}
	if p.OnTick != nil {
		p.OnTick(t)
	}
}

func (p *Pump) fireLifecycle(mp parse.MagicLine) {
	if p.Debug || p.OnLifecycle == nil {
		w := p.errLogOrStderr()
		fmt.Fprintf(w, "[parse] magic event=%s", mp.Event)
		if mp.Label != "" {
			fmt.Fprintf(w, " label=%q", mp.Label)
		}
		if mp.TaskID != "" {
			fmt.Fprintf(w, " task_id=%s", mp.TaskID)
		}
		fmt.Fprintln(w)
	}
	if p.OnLifecycle != nil {
		p.OnLifecycle(mp)
	}
}

// errLogOrStderr returns EventLog or, if nil, io.Discard.
func (p *Pump) errLogOrStderr() io.Writer {
	if p.EventLog != nil {
		return p.EventLog
	}
	return io.Discard
}

// nowFunc is overridable for tests so confidence-locking timing is
// deterministic.
var nowFunc = time.Now
