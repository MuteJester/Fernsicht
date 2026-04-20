package wrap

import "strings"

// unbufferEnv lists env vars to set for the wrapped command unless
// the user opts out with --no-unbuffer. Each entry is "KEY=VALUE".
//
// These coax tools that line-buffer when stdout is a tty but
// block-buffer when stdout is a pipe to flush per-line. Without this,
// progress arrives in 4 KB bursts, breaking the live-bar UX.
//
// Existing values in the wrapped command's env are NOT overwritten.
// This lets callers override e.g. PYTHONUNBUFFERED=0 when they need
// the buffered behavior.
var unbufferEnv = []string{
	// Python: most common offender. Setting to "1" forces line
	// buffering for stdout/stderr.
	"PYTHONUNBUFFERED=1",
	// Locale matters for Unicode output (mojibake otherwise).
	// Use a UTF-8 locale by default.
	"PYTHONIOENCODING=UTF-8",
	// Node has no official "unbuffer" env, but some libraries honor
	// this convention. Harmless if ignored.
	"NODE_NO_BUFFER=1",
}

// applyUnbufferEnv returns a copy of env with unbufferEnv vars
// applied. Existing keys in env are preserved (not overwritten);
// only NEW keys are added.
//
// Operating on a copy so the caller's slice (often os.Environ())
// isn't mutated.
func applyUnbufferEnv(env []string) []string {
	have := make(map[string]bool, len(env))
	for _, kv := range env {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		have[kv[:eq]] = true
	}

	out := make([]string, 0, len(env)+len(unbufferEnv))
	out = append(out, env...)
	for _, kv := range unbufferEnv {
		eq := strings.IndexByte(kv, '=')
		if eq <= 0 {
			continue
		}
		if !have[kv[:eq]] {
			out = append(out, kv)
		}
	}
	return out
}
