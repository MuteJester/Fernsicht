package termui

import (
	"bytes"
	"strings"
	"testing"
)

func TestQR_RendersBlockArt(t *testing.T) {
	var buf bytes.Buffer
	if err := QR(&buf, "https://app.example/#room=abc"); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.ContainsAny(out, "█▀▄") {
		t.Errorf("expected QR block characters in output; got %d bytes:\n%s",
			len(out), out)
	}
	// Should have multiple rows.
	rows := strings.Count(out, "\n")
	if rows < 5 {
		t.Errorf("expected QR to span ≥ 5 rows; got %d", rows)
	}
}

func TestQR_AcceptsLongURLs(t *testing.T) {
	var buf bytes.Buffer
	long := "https://app.fernsicht.space/#room=" + strings.Repeat("x", 60)
	if err := QR(&buf, long); err != nil {
		t.Errorf("QR refused long URL: %v", err)
	}
}

func TestHyperlink_NoTermProgram_ReturnsBare(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "")
	t.Setenv("VTE_VERSION", "")
	t.Setenv("NO_COLOR", "")
	got := Hyperlink("text", "https://x")
	if got != "text" {
		t.Errorf("expected bare text without term signal; got %q", got)
	}
}

func TestHyperlink_RespectsNO_COLOR(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "iTerm.app") // would normally support
	t.Setenv("NO_COLOR", "1")
	got := Hyperlink("text", "https://x")
	if got != "text" {
		t.Errorf("NO_COLOR should suppress hyperlink; got %q", got)
	}
}

func TestHyperlink_RecognizedTerm_EmitsOSC8(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM_PROGRAM", "iTerm.app")
	got := Hyperlink("click", "https://x")
	// OSC 8 is ESC ] 8 ; ; <url> ESC \ <text> ESC ] 8 ; ; ESC \
	if !strings.Contains(got, "\x1b]8;;https://x\x1b\\click\x1b]8;;\x1b\\") {
		t.Errorf("expected OSC 8 envelope; got %q", got)
	}
}

func TestHyperlink_KittyTERMSupported(t *testing.T) {
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM_PROGRAM", "")
	t.Setenv("TERM", "xterm-kitty")
	t.Setenv("VTE_VERSION", "")
	got := Hyperlink("x", "https://y")
	if got == "x" {
		t.Errorf("kitty TERM should support OSC 8; got bare text")
	}
}

func TestQREnabled(t *testing.T) {
	cases := []struct {
		on, off, tty bool
		want         bool
	}{
		{false, false, true, true},
		{false, false, false, false},
		{true, false, false, true},
		{false, true, true, false},
		{true, true, true, false}, // off wins
	}
	for _, tc := range cases {
		got := QREnabled(tc.on, tc.off, tc.tty)
		if got != tc.want {
			t.Errorf("QREnabled(on=%v off=%v tty=%v): got %v want %v",
				tc.on, tc.off, tc.tty, got, tc.want)
		}
	}
}
