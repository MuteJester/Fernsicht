package parse

import (
	"testing"
	"time"
)

func mkConf(threshold int, window, unlock time.Duration) *Confidence {
	return NewConfidence(ConfidenceConfig{
		Threshold:   threshold,
		Window:      window,
		UnlockAfter: unlock,
	})
}

func TestConfidence_SingleMatchDoesNotLock(t *testing.T) {
	c := mkConf(2, 5*time.Second, 30*time.Second)
	now := time.Unix(0, 0)
	if c.Match("tqdm", now) {
		t.Error("first match should NOT tick (threshold=2)")
	}
	if c.Active() != "" {
		t.Errorf("active should be empty after single match; got %q", c.Active())
	}
}

func TestConfidence_TwoMatchesLockIn(t *testing.T) {
	c := mkConf(2, 5*time.Second, 30*time.Second)
	now := time.Unix(0, 0)
	c.Match("tqdm", now)
	if !c.Match("tqdm", now.Add(time.Second)) {
		t.Error("second match within window should tick")
	}
	if c.Active() != "tqdm" {
		t.Errorf("expected tqdm active; got %q", c.Active())
	}
}

func TestConfidence_OtherParserSuppressedAfterLock(t *testing.T) {
	c := mkConf(2, 5*time.Second, 30*time.Second)
	now := time.Unix(0, 0)
	c.Match("tqdm", now)
	c.Match("tqdm", now.Add(time.Second))
	if c.Match("bare-percent", now.Add(2*time.Second)) {
		t.Error("non-active parser should be suppressed after lock")
	}
}

func TestConfidence_FalsePositiveRejected(t *testing.T) {
	// Single Make-style line `[1/2] Compiling foo.o` should NOT tick.
	c := mkConf(2, 5*time.Second, 30*time.Second)
	if c.Match("fraction-bracket", time.Unix(0, 0)) {
		t.Error("single fraction-bracket match should not tick")
	}
}

func TestConfidence_StaleMatchesFallOutOfWindow(t *testing.T) {
	c := mkConf(2, 1*time.Second, 30*time.Second)
	now := time.Unix(0, 0)
	c.Match("tqdm", now)
	// Second match 2s later — first has fallen out of the 1s window.
	if c.Match("tqdm", now.Add(2*time.Second)) {
		t.Error("second match outside window should NOT lock (only 1 in window)")
	}
}

func TestConfidence_UnlocksAfterSilence(t *testing.T) {
	c := mkConf(2, 5*time.Second, 10*time.Second)
	now := time.Unix(0, 0)
	c.Match("tqdm", now)
	c.Match("tqdm", now.Add(time.Second))
	// 11s later — unlocked.
	if c.Match("tqdm", now.Add(12*time.Second)) {
		t.Error("after unlock, single match shouldn't immediately tick again")
	}
	if c.Active() != "" {
		t.Errorf("expected unlocked; got active=%q", c.Active())
	}
}

func TestConfidence_RealWorldFalsePositiveCorpus(t *testing.T) {
	// Send the false-positives corpus through registry+confidence;
	// expect ZERO ticks.
	lines := loadCorpus(t, "false_positives.txt")
	r := NewRegistry()
	c := mkConf(2, 5*time.Second, 30*time.Second)
	now := time.Unix(0, 0)
	ticks := 0
	for i, line := range lines {
		if _, name, ok := r.MatchFirst(line); ok {
			t := now.Add(time.Duration(i) * time.Second)
			if c.Match(name, t) {
				ticks++
			}
		}
	}
	if ticks > 0 {
		t.Errorf("false-positive corpus produced %d ticks (expected 0)", ticks)
	}
}

func TestConfidence_TqdmCorpusLocksAndTicks(t *testing.T) {
	// A run of 7 real tqdm lines should lock in early and tick on
	// every subsequent line.
	lines := loadCorpus(t, "tqdm_outputs.txt")
	r := NewRegistry()
	c := mkConf(2, 5*time.Second, 30*time.Second)
	now := time.Unix(0, 0)
	ticks := 0
	for i, line := range lines {
		if _, name, ok := r.MatchFirst(line); ok {
			ts := now.Add(time.Duration(i) * 100 * time.Millisecond)
			if c.Match(name, ts) {
				ticks++
			}
		}
	}
	// We expect at least N-1 ticks (first match warms up the counter).
	if ticks < len(lines)-1 {
		t.Errorf("tqdm corpus produced %d ticks of %d lines (expected >= %d)",
			ticks, len(lines), len(lines)-1)
	}
	if c.Active() != "tqdm" {
		t.Errorf("expected tqdm active after corpus; got %q", c.Active())
	}
}
