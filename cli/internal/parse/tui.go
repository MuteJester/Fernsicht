package parse

import "sync"

// TUI tracks whether the wrapped command is rendering a fullscreen
// (alternate-screen) UI. Tools using `rich`, `textual`, ncurses, vim,
// htop, etc. switch into the alternate screen buffer; line-based
// parsing is meaningless once that happens.
//
// We watch for the `\e[?1049h` (and older variants) ANSI sequence
// emitted by the AnsiStripper. When seen, we set Active=true and
// disable Tier-1 auto-detection for the rest of the session (the
// magic prefix still works because it's a prefix-match, not regex).
//
// On exit (`\e[?1049l`), we re-enable auto-detection. Most TUIs
// don't toggle in/out, but a tool that does shouldn't get penalized.

// TUI is a small state container. The wrap layer holds one per
// session and consults Active() before invoking Tier-1 parsers.
//
// Safe for concurrent use: stdout and stderr pumps share one TUI
// per session. Mutex serializes; contention is negligible (events
// are rare).
type TUI struct {
	mu       sync.Mutex
	active   bool
	notified bool // we print one warn line on first detection only
}

// HandleEvent processes an AnsiEvent from the line buffer. Returns
// true if this transition should produce a warn line on stderr (the
// caller owns the writer).
func (t *TUI) HandleEvent(ev AnsiEvent) (warn bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch ev {
	case EventAltScreenEnter:
		t.active = true
		if !t.notified {
			t.notified = true
			return true
		}
	case EventAltScreenExit:
		t.active = false
	}
	return false
}

// Active reports whether we're currently in fullscreen mode. The
// parser registry should be skipped when this is true.
func (t *TUI) Active() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.active
}

// WarnMessage is the human-readable text printed on first alt-screen
// detection. Constant so tests can match against it without
// duplicating string literals.
const WarnMessage = "[fernsicht] warn: detected fullscreen TUI; auto-detection disabled. Use magic prefix (__fernsicht__) for explicit progress."
