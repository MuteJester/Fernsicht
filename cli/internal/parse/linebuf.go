package parse

// LineBuffer accumulates bytes from a stream and emits "logical lines"
// to a callback. A logical line is everything between two boundaries;
// boundary bytes are `\n` and `\r` (both treated as end-of-line so
// tqdm-style `\r`-only progress redraws are seen as discrete lines).
//
// `\r\n` produces an empty trailing emit; the parser registry skips
// empty lines so this is harmless.
//
// Lines longer than MaxLen bytes are truncated and emitted with a
// `[fernsicht] warn: line truncated at N bytes` once per overflow run
// (subsequent appended bytes are dropped until the next boundary).
// This protects us from OOM on pathological input (a tool accidentally
// piping a binary file through us).

const (
	// DefaultMaxLineLen caps any single buffered line. Per CLI plan §7.8.
	DefaultMaxLineLen = 1 << 20 // 1 MB
)

// LineEmitter is called once per logical line. The argument is the
// stripped (no ANSI) line contents, NOT including the boundary byte.
//
// The slice is reused across calls — DO NOT retain it. Copy if you
// need to keep it.
type LineEmitter func(stripped []byte, raw []byte)

// LineBuffer turns a byte stream into logical lines. The buffer holds
// the raw bytes received since the last boundary; on emit it also
// runs the bytes through an AnsiStripper and reports any structural
// events to the caller via the EventSink.
type LineBuffer struct {
	MaxLen    int          // max bytes per line; 0 → DefaultMaxLineLen
	Emit      LineEmitter  // called per logical line
	EventSink func(AnsiEvent)

	// internal state
	raw      []byte         // raw bytes since last boundary
	overflow bool
	stripper AnsiStripper
	scratch  []byte         // reusable buffer for stripped output
}

// Write appends p to the buffer, dispatching emit for each boundary.
//
// Always consumes all of p; the io.Writer-style int return is the
// length of p for convenience. Errors aren't possible at this layer.
func (lb *LineBuffer) Write(p []byte) (int, error) {
	maxLen := lb.MaxLen
	if maxLen <= 0 {
		maxLen = DefaultMaxLineLen
	}
	for _, b := range p {
		if b == '\n' || b == '\r' {
			lb.flush()
			continue
		}
		if len(lb.raw) >= maxLen {
			if !lb.overflow {
				lb.overflow = true
				// Emit the truncated line so a parser still sees the
				// portion before the cap. Subsequent bytes are dropped
				// until the next boundary.
				lb.flush()
			}
			continue
		}
		lb.raw = append(lb.raw, b)
	}
	return len(p), nil
}

// Flush manually emits whatever's currently buffered. Useful at EOF
// when the wrapped command exits without a trailing newline.
func (lb *LineBuffer) Flush() {
	if len(lb.raw) > 0 {
		lb.flush()
	}
}

func (lb *LineBuffer) flush() {
	stripped := lb.scratch[:0]
	stripped, events := lb.stripper.Strip(stripped, lb.raw)

	if lb.EventSink != nil {
		for _, ev := range events {
			lb.EventSink(ev)
		}
	}

	if lb.Emit != nil {
		lb.Emit(stripped, lb.raw)
	}

	// Reset for next line. Keep underlying arrays for reuse.
	lb.raw = lb.raw[:0]
	lb.scratch = stripped[:0]
	lb.overflow = false
}
