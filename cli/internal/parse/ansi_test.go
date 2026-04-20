package parse

import (
	"bytes"
	"slices"
	"testing"
)

func TestAnsi_StripsCSI(t *testing.T) {
	cases := []struct {
		name  string
		in    string
		visible string
	}{
		{"plain", "hello", "hello"},
		{"color-on-only", "\x1b[31mred", "red"},
		{"color-around", "\x1b[31mred\x1b[0m text", "red text"},
		{"clear-line", "\x1b[2K\rTraining: 50%", "Training: 50%"},
		{"sgr-multi", "\x1b[1;31;42mboldred-greenbg", "boldred-greenbg"},
		{"cursor-up", "\x1b[10Aback", "back"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var s AnsiStripper
			out, _ := s.Strip(nil, []byte(tc.in))
			// `\r` is not an ANSI escape; it stays in the visible stream.
			out = bytes.ReplaceAll(out, []byte{'\r'}, nil)
			if string(out) != tc.visible {
				t.Errorf("stripped %q: got %q, want %q", tc.in, out, tc.visible)
			}
		})
	}
}

func TestAnsi_StripsOSC(t *testing.T) {
	// OSC ends with BEL (\x07) or ST (ESC \).
	in := []byte("a\x1b]0;set-window-title\x07b")
	var s AnsiStripper
	out, _ := s.Strip(nil, in)
	if string(out) != "ab" {
		t.Errorf("OSC strip: got %q, want %q", out, "ab")
	}

	// ST-terminated OSC.
	in = []byte("a\x1b]0;title\x1b\\b")
	s = AnsiStripper{}
	out, _ = s.Strip(nil, in)
	if string(out) != "ab" {
		t.Errorf("OSC ST strip: got %q, want %q", out, "ab")
	}
}

func TestAnsi_DetectsAltScreenEnter(t *testing.T) {
	in := []byte("before\x1b[?1049hafter")
	var s AnsiStripper
	_, events := s.Strip(nil, in)
	if !slices.Contains(events, EventAltScreenEnter) {
		t.Errorf("expected EventAltScreenEnter; got %v", events)
	}
}

func TestAnsi_DetectsAltScreenExit(t *testing.T) {
	in := []byte("\x1b[?1049lafter")
	var s AnsiStripper
	_, events := s.Strip(nil, in)
	if !slices.Contains(events, EventAltScreenExit) {
		t.Errorf("expected EventAltScreenExit; got %v", events)
	}
}

func TestAnsi_OldAltScreenForms(t *testing.T) {
	// Older terminals: \e[?47h
	var s AnsiStripper
	_, events := s.Strip(nil, []byte("\x1b[?47h"))
	if !slices.Contains(events, EventAltScreenEnter) {
		t.Errorf("expected enter for ?47h; got %v", events)
	}
	s = AnsiStripper{}
	_, events = s.Strip(nil, []byte("\x1b[?47l"))
	if !slices.Contains(events, EventAltScreenExit) {
		t.Errorf("expected exit for ?47l; got %v", events)
	}
}

func TestAnsi_HandlesSplitEscapeAcrossWrites(t *testing.T) {
	// Common in real I/O: ANSI sequence split across two read chunks.
	var s AnsiStripper
	out := []byte{}
	out, _ = s.Strip(out, []byte("hello\x1b"))
	out, _ = s.Strip(out, []byte("[31mred\x1b[0m"))
	if string(out) != "hellored" {
		t.Errorf("split-write strip: got %q, want %q", out, "hellored")
	}
}

func TestAnsi_OneCharEscape(t *testing.T) {
	// \eD = "Index" (move cursor down). Single-char after ESC.
	var s AnsiStripper
	out, _ := s.Strip(nil, []byte("a\x1bDb"))
	if string(out) != "ab" {
		t.Errorf("1-char escape strip: got %q, want %q", out, "ab")
	}
}

func TestAnsi_DoesNotConfuseAltScreenWithSimilar(t *testing.T) {
	// \e[1049h would NOT be alt-screen (no `?` prefix).
	var s AnsiStripper
	_, events := s.Strip(nil, []byte("\x1b[1049h"))
	if slices.Contains(events, EventAltScreenEnter) {
		t.Errorf("?-less form should NOT trigger alt-screen event")
	}
}
