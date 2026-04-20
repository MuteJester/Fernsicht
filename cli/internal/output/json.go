// Package output handles --output {text,json} mode for `fernsicht run`.
//
// JSON-lines mode prints one JSON object per line on stdout, suitable
// for embedding in shell pipelines / CI logs:
//
//   {"event":"session_open","viewer_url":"...","room_id":"...","ts":"..."}
//   {"event":"session_close","exit_code":0,"duration_sec":182,...}
//
// Wrapped command's stdio is NOT included in the JSON stream — it
// continues to flow through the standard pump (forwarded to the
// caller's stdout/stderr). JSON events go to a separate writer
// (which the run subcommand sets to os.Stdout).
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Mode is the user's --output choice.
type Mode int

const (
	ModeText Mode = iota
	ModeJSON
)

// ParseMode converts a CLI string to a Mode value. Unknown values
// produce an error so the user sees the typo immediately.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "", "text":
		return ModeText, nil
	case "json":
		return ModeJSON, nil
	}
	return ModeText, fmt.Errorf("output: unknown mode %q (expected text or json)", s)
}

// Emitter writes structured events to w. Methods are safe for
// concurrent use (the wrapped command's exit + the bridge's async
// events can race; we serialize via mu).
type Emitter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewEmitter constructs an Emitter writing to w. Pass nil for w to
// drop all events (useful when --output text — caller switches to
// the text-mode logging path).
func NewEmitter(w io.Writer) *Emitter {
	return &Emitter{w: w}
}

// SessionOpen records the session-startup event.
func (e *Emitter) SessionOpen(roomID, viewerURL string) {
	e.emit(map[string]any{
		"event":      "session_open",
		"room_id":    roomID,
		"viewer_url": viewerURL,
		"ts":         time.Now().UTC().Format(time.RFC3339),
	})
}

// SessionClose records the session-end event.
func (e *Emitter) SessionClose(exitCode int, durationSec float64, viewerCountMax int) {
	e.emit(map[string]any{
		"event":            "session_close",
		"exit_code":        exitCode,
		"duration_sec":     durationSec,
		"viewer_count_max": viewerCountMax,
		"ts":               time.Now().UTC().Format(time.RFC3339),
	})
}

// Tick records a progress observation. Optional — most users only
// want session_open/close, but JSON consumers building dashboards
// may want the full stream.
func (e *Emitter) Tick(taskID string, value float64, n, total int) {
	e.emit(map[string]any{
		"event":   "tick",
		"task_id": taskID,
		"value":   value,
		"n":       n,
		"total":   total,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
}

// emit serializes one event as a single line + newline.
func (e *Emitter) emit(payload map[string]any) {
	if e == nil || e.w == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return // shouldn't happen with map[string]any
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	_, _ = e.w.Write(data)
	_, _ = e.w.Write([]byte("\n"))
}
