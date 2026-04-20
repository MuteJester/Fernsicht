package parse

import (
	"strings"
	"testing"
)

func collect(lb *LineBuffer) (lines []string) {
	lb.Emit = func(stripped, _ []byte) {
		lines = append(lines, string(stripped))
	}
	return
}

func TestLineBuffer_NewlineBoundary(t *testing.T) {
	var lb LineBuffer
	lines := collect(&lb)
	lb.Emit = func(stripped, _ []byte) {
		lines = append(lines, string(stripped))
	}
	_, _ = lb.Write([]byte("a\nb\nc\n"))
	if len(lines) != 3 || lines[0] != "a" || lines[1] != "b" || lines[2] != "c" {
		t.Errorf("got %v", lines)
	}
}

func TestLineBuffer_CarriageReturnBoundary(t *testing.T) {
	// tqdm-style: \r-only progress redraws.
	var lb LineBuffer
	var lines []string
	lb.Emit = func(stripped, _ []byte) {
		lines = append(lines, string(stripped))
	}
	_, _ = lb.Write([]byte("\rTraining: 10%\rTraining: 20%\rTraining: 30%\n"))
	// First "" before initial \r, then three frames, then trailing "".
	want := []string{"", "Training: 10%", "Training: 20%", "Training: 30%"}
	if len(lines) < 4 {
		t.Fatalf("expected at least 4 emissions; got %v", lines)
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("emission %d: got %q, want %q", i, lines[i], w)
		}
	}
}

func TestLineBuffer_StripsAnsi(t *testing.T) {
	var lb LineBuffer
	var lines []string
	lb.Emit = func(stripped, _ []byte) {
		lines = append(lines, string(stripped))
	}
	_, _ = lb.Write([]byte("\x1b[31mhello\x1b[0m world\n"))
	if len(lines) != 1 || lines[0] != "hello world" {
		t.Errorf("got %v", lines)
	}
}

func TestLineBuffer_AltScreenEvent(t *testing.T) {
	var lb LineBuffer
	var sawEnter bool
	lb.EventSink = func(ev AnsiEvent) {
		if ev == EventAltScreenEnter {
			sawEnter = true
		}
	}
	lb.Emit = func(stripped, _ []byte) {}
	// Alt-screen escape inside a line.
	_, _ = lb.Write([]byte("before\x1b[?1049hafter\n"))
	if !sawEnter {
		t.Errorf("expected EventAltScreenEnter to be sunk")
	}
}

func TestLineBuffer_LineLengthCap(t *testing.T) {
	var lb LineBuffer
	lb.MaxLen = 16
	var lines []string
	lb.Emit = func(stripped, _ []byte) {
		lines = append(lines, string(stripped))
	}
	long := strings.Repeat("x", 100)
	_, _ = lb.Write([]byte(long + "\n"))
	// Should emit a truncated line at the cap, then the boundary
	// flushes nothing (overflow already emitted).
	if len(lines) == 0 {
		t.Fatal("expected at least one emission")
	}
	if len(lines[0]) > lb.MaxLen {
		t.Errorf("expected emission length <= MaxLen=%d; got %d", lb.MaxLen, len(lines[0]))
	}
}

func TestLineBuffer_HandlesPartialLineAtFlush(t *testing.T) {
	var lb LineBuffer
	var lines []string
	lb.Emit = func(stripped, _ []byte) {
		lines = append(lines, string(stripped))
	}
	_, _ = lb.Write([]byte("partial without newline"))
	if len(lines) != 0 {
		t.Errorf("should not emit before boundary or Flush; got %v", lines)
	}
	lb.Flush()
	if len(lines) != 1 || lines[0] != "partial without newline" {
		t.Errorf("Flush should emit pending; got %v", lines)
	}
}
