package termui

import (
	"fmt"
	"io"
)

// FirstRunTip is the one-time onboarding message printed on a user's
// first `fernsicht run`. Constant so tests can match against it.
const FirstRunTip = `[fernsicht] tip: open the URL on your phone — viewers connect peer-to-peer; nothing
            flows through our servers after the brief handshake. Your data
            stays between you and your viewers. (No telemetry, no accounts.)`

// SecondTip is the secondary one-line hint about advanced help. Kept
// short so the first-run experience doesn't feel like a wall of text.
const SecondTip = "[fernsicht] tip: see `fernsicht run --help` for advanced options."

// PrintFirstRunTips writes both tips to w (plus a trailing blank line).
// Caller decides when to invoke this — typically only when
// IsFirstRun() returns true and the user hasn't passed --quiet.
func PrintFirstRunTips(w io.Writer) {
	fmt.Fprintln(w, FirstRunTip)
	fmt.Fprintln(w, SecondTip)
	fmt.Fprintln(w)
}

// PrintViewerURLBanner writes the canonical "viewer URL" line
// (clickable in supporting terminals) to w. Caller is responsible
// for any QR rendering separately.
func PrintViewerURLBanner(w io.Writer, url string) {
	link := Hyperlink(url, url)
	fmt.Fprintf(w, "[fernsicht] viewer: %s\n", link)
}

// PrintAntiTelemetryNote writes the brief "no telemetry" line. We
// repeat this in the first-run tip and again here for users who
// already saw the tip but want to be reminded; it's two extra bytes
// in stderr, well worth the trust signal.
func PrintAntiTelemetryNote(w io.Writer) {
	fmt.Fprintln(w, "[fernsicht] (no telemetry — we never collect usage data)")
}
