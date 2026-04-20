package termui

import (
	"fmt"
	"os"
	"strings"
)

// Hyperlink wraps text with the OSC 8 escape so modern terminals
// render it as a clickable link to url.
//
// Sequence shape (per https://gist.github.com/egmontkob/eb114294efbcd5adb1944c9f3cb5feda):
//
//	\e]8;;<url>\e\\<text>\e]8;;\e\\
//
// On terminals that don't understand OSC 8, the escapes are silently
// stripped (or ignored) — the text appears bare. We only emit them
// when we have a heuristic signal the terminal supports it; otherwise
// return text unchanged.
func Hyperlink(text, url string) string {
	if !TerminalLikelySupportsOSC8() {
		return text
	}
	return fmt.Sprintf("\x1b]8;;%s\x1b\\%s\x1b]8;;\x1b\\", url, text)
}

// TerminalLikelySupportsOSC8 returns true when we have a positive
// signal the user's terminal handles OSC 8. Heuristic, not exact —
// we err on the side of NOT emitting (plain text is harmless;
// stray escapes are visible junk on unsupporting terminals).
//
// Honors NO_COLOR (per https://no-color.org), which is also a "do
// not decorate output" signal.
func TerminalLikelySupportsOSC8() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	// TERM_PROGRAM is set by terminals that opt into supplying it.
	// Maps known-supporting values.
	switch os.Getenv("TERM_PROGRAM") {
	case "iTerm.app", "vscode", "WezTerm", "Hyper", "Tabby":
		return true
	}

	// Some terminals set TERM to recognizable values.
	term := os.Getenv("TERM")
	switch {
	case strings.HasPrefix(term, "xterm-kitty"):
		return true
	case strings.HasPrefix(term, "wezterm"):
		return true
	case strings.HasPrefix(term, "alacritty"):
		// Alacritty supports OSC 8 since v0.13 (early 2024). Most
		// installs are well past that by now.
		return true
	}

	// gnome-terminal / VTE-based terminals: COLORTERM=truecolor + a
	// VTE marker. Best detected via VTE_VERSION (>= 0.50 supports OSC 8).
	if vte := os.Getenv("VTE_VERSION"); vte >= "5000" {
		return true
	}

	return false
}
