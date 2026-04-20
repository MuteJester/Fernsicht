package parse

import (
	"sync"
	"time"
)

// Confidence implements the parser-locking heuristic from CLI plan
// §5.4. The problem it solves: any single regex match could be a
// coincidence (e.g., a Make line `[1/2] Compiling foo.o` shouldn't
// drive the bar). We require a parser to "earn" the right to tick by
// matching multiple times in a sliding window before we believe it.
//
// State machine (per session):
//
//   1. UNLOCKED: any parser may match. Each match is recorded.
//   2. When some parser P crosses Threshold matches in Window,
//      promote P to ACTIVE. Discard counters for other parsers.
//   3. ACTIVE: only P's matches produce ticks. Other parsers'
//      matches are silently ignored.
//   4. If P doesn't match for UnlockAfter, return to UNLOCKED.
//      Counters reset.
//
// The wrap layer holds one Confidence per session and consults it
// before forwarding a Tier-1 match to the bridge.
//
// Magic-prefix matches BYPASS confidence — explicit user intent
// always wins.

// ConfidenceConfig is the tunable. Defaults match plan §5.4.
type ConfidenceConfig struct {
	// Threshold is the default number of matches a parser needs in
	// the rolling window before it locks in. Per-parser overrides
	// (PerParserThreshold) take precedence.
	Threshold int

	// PerParserThreshold lets noisier parsers require more evidence.
	// Default: bare-percent → 3 (any log line mentioning "X%" matches
	// it, so 2 unrelated mentions could otherwise lock).
	PerParserThreshold map[string]int

	// Window is how far back we count matches when deciding to lock.
	Window time.Duration

	// UnlockAfter is the silence after which an active parser is
	// dropped and we return to UNLOCKED state.
	UnlockAfter time.Duration
}

// DefaultConfidenceConfig is what production code uses. Tunable per
// §5.3 in Phase 4.
func DefaultConfidenceConfig() ConfidenceConfig {
	return ConfidenceConfig{
		Threshold: 2,
		PerParserThreshold: map[string]int{
			// bare-percent is the noisiest — log lines like
			// "error rate: 42%" and "Allocating 85%" both match.
			// Require 3 matches in window before trusting it.
			"bare-percent": 3,
		},
		Window:      5 * time.Second,
		UnlockAfter: 30 * time.Second,
	}
}

// thresholdFor returns the threshold for parserName, falling back to
// the default if no per-parser override.
func (c *ConfidenceConfig) thresholdFor(parserName string) int {
	if t, ok := c.PerParserThreshold[parserName]; ok && t > 0 {
		return t
	}
	return c.Threshold
}

// Confidence tracks lock state for one session.
//
// Safe for concurrent use: stdout and stderr pumps in pipe mode each
// call Match from their own goroutines and share the same Confidence
// instance. The mutex serializes; contention is negligible (one
// Match per parsed line).
type Confidence struct {
	mu     sync.Mutex
	cfg    ConfidenceConfig
	active string                       // empty = unlocked
	hist   map[string][]time.Time       // parser → recent match times
	last   time.Time                    // last match for active parser
}

// NewConfidence builds a Confidence with the given config (or default
// if zero-value).
func NewConfidence(cfg ConfidenceConfig) *Confidence {
	if cfg.Threshold == 0 && cfg.Window == 0 && cfg.UnlockAfter == 0 {
		cfg = DefaultConfidenceConfig()
	}
	if cfg.PerParserThreshold == nil {
		cfg.PerParserThreshold = DefaultConfidenceConfig().PerParserThreshold
	}
	return &Confidence{
		cfg:  cfg,
		hist: map[string][]time.Time{},
	}
}

// Match records that parser parserName matched at time t. Returns
// true if this match should produce a real tick (parser is or just
// became active).
//
// Caller invokes this AFTER the parser regex matches; Match decides
// whether to actually act on it.
//
// Safe to call concurrently — internally serialized.
func (c *Confidence) Match(parserName string, t time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Step 1: handle unlock-after-silence for the currently active
	// parser. If the active parser has been silent past UnlockAfter,
	// reset state — the wrapped command may have moved on to a new
	// phase that uses a different progress format.
	if c.active != "" && !c.last.IsZero() && t.Sub(c.last) > c.cfg.UnlockAfter {
		c.active = ""
		// Clear history entirely — don't carry stale matches into the
		// next ramp-up.
		for k := range c.hist {
			delete(c.hist, k)
		}
	}

	// Step 2: if locked, only the active parser may tick.
	if c.active != "" {
		if c.active == parserName {
			c.last = t
			return true
		}
		// Different parser; silently swallow.
		return false
	}

	// Step 3: unlocked — record this match. If parserName has hit the
	// threshold within the window, promote it.
	cutoff := t.Add(-c.cfg.Window)
	times := c.hist[parserName]
	// Trim left side past the window.
	for len(times) > 0 && times[0].Before(cutoff) {
		times = times[1:]
	}
	times = append(times, t)
	c.hist[parserName] = times

	if len(times) >= c.cfg.thresholdFor(parserName) {
		c.active = parserName
		c.last = t
		// Don't carry other parsers' history into the active phase.
		for k := range c.hist {
			if k != parserName {
				delete(c.hist, k)
			}
		}
		return true
	}
	return false
}

// Active returns the currently locked parser's name, or "" if none.
// For diagnostics / status display.
func (c *Confidence) Active() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}
