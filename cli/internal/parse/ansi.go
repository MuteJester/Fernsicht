package parse

// ANSI escape sequence stripping.
//
// We strip escapes from PARSER input only — the forwarded output to
// the caller's terminal keeps every byte intact so colors and cursor
// moves still render correctly.
//
// Sequences we recognize:
//
//   CSI    `\e[ ... <final letter in 0x40..0x7e>`
//   OSC    `\e] ... <BEL=0x07 or ST=\e\\>`
//   1-char `\e <single char>` (e.g., `\e=`, `\eD`)
//
// Anything else after `\e` we treat as a one-off and consume.
//
// Two side effects beyond stripping:
//
//   - alt-screen detection: signals when we see the "enter alternate
//     screen" CSI sequence `\e[?1049h`. The TUI manager uses this
//     to disable Tier-1 auto-detection for the rest of the session.
//
//   - alt-screen exit detection: `\e[?1049l` re-enables auto-detect
//     in case the wrapped command leaves and re-enters fullscreen.

const (
	esc      byte = 0x1b // ESC
	bel      byte = 0x07 // BEL — terminates OSC
	csiStart byte = '['
	oscStart byte = ']'
	stTail   byte = '\\' // the second byte of String Terminator (ESC \\)
)

// AnsiStripper is a stateful byte transformer. Feed it bytes via
// Write; it appends "visible" (non-escape) bytes to dst and emits
// AnsiEvent values for control sequences that callers care about
// (currently only alt-screen enter/exit).
//
// The struct keeps state across Write calls so escapes split across
// chunk boundaries are handled correctly.
type AnsiStripper struct {
	state    ansiState
	csiParam []byte // accumulated CSI parameter bytes (for alt-screen detection)
}

type ansiState int

const (
	stNormal ansiState = iota
	stEsc              // saw ESC, awaiting the next byte
	stCSI              // inside a CSI sequence
	stOSC              // inside an OSC sequence
	stOSCEsc           // inside OSC, just saw ESC (might be ST)
)

// AnsiEvent describes a structural sequence the stripper recognized.
// Only event types we ACT on are reported; everything else is
// silently consumed.
type AnsiEvent int

const (
	EventNone AnsiEvent = iota
	EventAltScreenEnter
	EventAltScreenExit
)

// Strip processes p and appends the "visible" bytes (non-escape) to
// dst. Returns dst and a slice of events seen during this Write.
//
// State is preserved across calls so escapes split across chunks
// are handled. Safe to call on an empty input.
func (s *AnsiStripper) Strip(dst, p []byte) ([]byte, []AnsiEvent) {
	var events []AnsiEvent
	for _, b := range p {
		switch s.state {
		case stNormal:
			if b == esc {
				s.state = stEsc
				continue
			}
			dst = append(dst, b)

		case stEsc:
			switch b {
			case csiStart:
				s.state = stCSI
				s.csiParam = s.csiParam[:0]
			case oscStart:
				s.state = stOSC
			default:
				// Single-character escape (e.g. \e=, \eD) or unknown.
				// Consume the byte and return to normal.
				s.state = stNormal
			}

		case stCSI:
			// Final byte is in 0x40..0x7e; everything before is
			// parameter / intermediate. Accumulate params for the
			// alt-screen check we do at the final byte.
			if b >= 0x40 && b <= 0x7e {
				if ev := classifyCSI(s.csiParam, b); ev != EventNone {
					events = append(events, ev)
				}
				s.csiParam = s.csiParam[:0]
				s.state = stNormal
			} else {
				// Append the param byte (cap at 64 to avoid pathological growth).
				if len(s.csiParam) < 64 {
					s.csiParam = append(s.csiParam, b)
				}
			}

		case stOSC:
			switch b {
			case bel:
				s.state = stNormal
			case esc:
				s.state = stOSCEsc
			default:
				// part of OSC payload; ignore
			}

		case stOSCEsc:
			if b == stTail {
				// String Terminator (ESC \) seen; OSC done.
				s.state = stNormal
			} else {
				// False alarm — we're back inside OSC, this byte is
				// part of the payload.
				s.state = stOSC
			}
		}
	}
	return dst, events
}

// classifyCSI inspects a completed CSI sequence (its parameter bytes
// + final letter) and returns the event we care about, if any.
//
// Alt-screen sequences are private CSIs (`?` prefix):
//
//   \e[?1049h   enter alternate screen buffer (and save cursor)
//   \e[?1049l   leave alternate screen buffer
//
// Some terminals also use `\e[?47h` / `\e[?47l` (older form); we
// recognize that too.
func classifyCSI(params []byte, final byte) AnsiEvent {
	switch final {
	case 'h':
		if isAltScreenParam(params) {
			return EventAltScreenEnter
		}
	case 'l':
		if isAltScreenParam(params) {
			return EventAltScreenExit
		}
	}
	return EventNone
}

func isAltScreenParam(params []byte) bool {
	s := string(params)
	return s == "?1049" || s == "?47" || s == "?1047" || s == "?1048"
}
