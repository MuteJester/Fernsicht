// Package parse hosts the wrapped command's progress-detection
// pipeline:
//
//   raw bytes → ANSI-stripped lines → magic-prefix check → Tier-1
//   regex parsers → confidence locking → Tick events
//
// All parsers are pure (line in, Tick out, no I/O). The wrap package
// drives the pipeline and forwards Tick events to the bridge (Phase
// 3) or stderr (Phase 2 verification).
package parse

import (
	"strings"
	"time"
)

// Tick is one parsed progress observation.
//
// Mirrors bridge/pkg/embed.Tick; we keep the type local to parse so
// the package is self-contained and testable. wrap converts between
// the two when forwarding.
type Tick struct {
	// TaskID is set by magic-prefix events that explicitly nest tasks.
	// Empty for Tier-1 detections — wrap supplies a default.
	TaskID string

	// Value is the progress fraction in [0, 1]. Computed from N/Total
	// when not provided directly.
	Value float64

	// N and Total are item counts; either may be zero/missing.
	N     int
	Total int

	// Optional metadata from magic-prefix lifecycle frames.
	Label string
	Unit  string

	// Source is the parser that produced this Tick (e.g., "tqdm",
	// "magic-json"). Useful for diagnostics + confidence locking.
	Source string
}

// Parser tries to extract a Tick from one logical line.
//
// Returns (tick, true) on a match, (zero, false) otherwise. The
// returned Tick's Source field is set by the registry, not the parser
// (parsers shouldn't have to know their own name).
type Parser interface {
	Name() string
	Match(line string) (Tick, bool)
}

// Registry holds Tier-1 parsers in priority order. Lower index = higher
// priority. The first parser that matches wins (subject to confidence
// locking — see Confidence).
type Registry struct {
	parsers []Parser
	custom  []Parser // user-supplied (config / --pattern); appended after built-ins
}

// NewRegistry returns the default Tier-1 registry per CLI plan §5.1:
//
//   1. tqdm-default — tqdm/pip-style with bar + N/Total.
//   2. fraction-bracket — bracketed [N/M].
//   3. fraction-of — "N of M" / "N/M" without brackets.
//   4. step-keyword — "step|epoch|iteration|iter|batch N[/M]".
//   5. bare-percent — "N%". Lowest priority because it's noisy.
//
// Order matters: stricter / more-specific parsers come first so they
// claim a line before noisier ones see it.
func NewRegistry() *Registry {
	return &Registry{
		parsers: []Parser{
			tqdmParser{},
			fractionBracketParser{},
			fractionOfParser{},
			stepKeywordParser{},
			barePercentParser{},
		},
	}
}

// AddCustom appends a user-supplied parser to the registry, after all
// built-ins. Used for `--pattern` and `.fernsicht.toml` patterns
// (Phase 4).
func (r *Registry) AddCustom(p Parser) {
	r.custom = append(r.custom, p)
}

// Disable empties the built-in parser slice so only custom ones run.
// Used for `--no-detect`.
func (r *Registry) Disable() {
	r.parsers = nil
}

// All returns every parser in priority order (built-ins first, then
// custom). Read-only — callers must not mutate.
func (r *Registry) All() []Parser {
	out := make([]Parser, 0, len(r.parsers)+len(r.custom))
	out = append(out, r.parsers...)
	out = append(out, r.custom...)
	return out
}

// MatchFirst tries each parser in priority order, returning the first
// match. Returns (Tick{}, "", false) if no parser matched.
//
// The returned name is the matching parser's Name() — the caller (or
// confidence layer) tags the Tick.Source with this.
func (r *Registry) MatchFirst(line string) (Tick, string, bool) {
	if line == "" || strings.TrimSpace(line) == "" {
		return Tick{}, "", false
	}
	for _, p := range r.All() {
		if t, ok := p.Match(line); ok {
			t.Source = p.Name()
			return t, p.Name(), true
		}
	}
	return Tick{}, "", false
}

// computeValue is a small helper used by Tier-1 parsers that capture
// N + Total but not Value directly.
func computeValue(n, total int) float64 {
	if total <= 0 {
		return 0
	}
	v := float64(n) / float64(total)
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// nowFunc is overridable in tests so confidence-locking timing is
// deterministic.
var nowFunc = time.Now
