package parse

// Performance benchmarks per CLI plan §13.6.
//
// Targets:
//   - Parser dispatch: ≥ 1M lines/sec single-threaded
//   - ANSI strip:      ≥ 100 MB/sec
//   - Magic-prefix:    ≥ 5M lines/sec (it's just strings.HasPrefix)
//
// Run with: go test -bench=. -benchmem ./internal/parse/
//
// These guard against accidental performance regressions. Phase 6
// pre-release ritual: `make bench` and compare against the targets.

import (
	"bytes"
	"strings"
	"testing"
)

func BenchmarkRegistry_DispatchTqdm(b *testing.B) {
	r := NewRegistry()
	line := "Training:  50%|█████     | 50/100 [00:42<00:42,  1.18it/s]"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = r.MatchFirst(line)
	}
}

func BenchmarkRegistry_DispatchNoMatch(b *testing.B) {
	r := NewRegistry()
	line := "regular log line with no progress info, just text"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = r.MatchFirst(line)
	}
}

func BenchmarkRegistry_DispatchMixed(b *testing.B) {
	r := NewRegistry()
	lines := []string{
		"starting build",
		"Training:  10%|█         | 10/100 [00:08<01:14,  1.20it/s]",
		"loading config",
		"Step 5",
		"writing to /var/log/app.log",
		"[1/2] Compiling foo.o",
		"42% complete",
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		line := lines[i%len(lines)]
		_, _, _ = r.MatchFirst(line)
	}
}

func BenchmarkAnsiStripper_NoEscapes(b *testing.B) {
	in := bytes.Repeat([]byte("plain ASCII text without any escape sequences "), 100)
	dst := make([]byte, 0, len(in))
	var s AnsiStripper
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dst = dst[:0]
		dst, _ = s.Strip(dst, in)
	}
}

func BenchmarkAnsiStripper_HeavyEscapes(b *testing.B) {
	// Worst-case throughput: roughly 1 in 5 bytes is in an ANSI
	// escape (mimics colored progress output).
	in := bytes.Repeat([]byte("\x1b[31mred\x1b[0m \x1b[32mgreen\x1b[0m text "), 50)
	dst := make([]byte, 0, len(in))
	var s AnsiStripper
	b.SetBytes(int64(len(in)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		dst = dst[:0]
		dst, _ = s.Strip(dst, in)
	}
}

func BenchmarkMagicParse_Hit(b *testing.B) {
	line := `__fernsicht__ {"value":0.5,"n":50,"total":100,"label":"Training"}`
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = MagicParse(line)
	}
}

func BenchmarkMagicParse_Miss(b *testing.B) {
	// Line that doesn't start with magic prefix — most common case.
	// Should be a single string compare.
	line := "Training: 50%|████| 50/100 [00:00<00:00, 1.0it/s]"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = MagicParse(line)
	}
}

func BenchmarkMagicParse_Compact(b *testing.B) {
	line := "__fernsicht__ progress 50/100 batch"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _, _ = MagicParse(line)
	}
}

func BenchmarkLineBuffer_PlainText(b *testing.B) {
	chunk := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog\n", 100))
	emitted := 0
	lb := &LineBuffer{Emit: func(_, _ []byte) { emitted++ }}
	b.SetBytes(int64(len(chunk)))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = lb.Write(chunk)
	}
	_ = emitted
}

func BenchmarkConfidence_LockedFastPath(b *testing.B) {
	c := NewConfidence(ConfidenceConfig{Threshold: 2})
	// Lock it.
	now := nowFunc()
	c.Match("tqdm", now)
	c.Match("tqdm", now)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		c.Match("tqdm", now)
	}
}
