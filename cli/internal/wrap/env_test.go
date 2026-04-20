package wrap

import (
	"slices"
	"testing"
)

func TestApplyUnbufferEnv_AddsMissingKeys(t *testing.T) {
	in := []string{"FOO=bar", "BAZ=qux"}
	out := applyUnbufferEnv(in)

	// Original entries preserved.
	for _, kv := range in {
		if !slices.Contains(out, kv) {
			t.Errorf("missing original env entry: %q", kv)
		}
	}

	// All unbuffer entries added.
	for _, kv := range unbufferEnv {
		if !slices.Contains(out, kv) {
			t.Errorf("expected unbuffer entry: %q", kv)
		}
	}
}

func TestApplyUnbufferEnv_PreservesUserOverrides(t *testing.T) {
	// User explicitly sets PYTHONUNBUFFERED=0; we must not clobber it.
	in := []string{"PYTHONUNBUFFERED=0", "PATH=/usr/bin"}
	out := applyUnbufferEnv(in)

	if !slices.Contains(out, "PYTHONUNBUFFERED=0") {
		t.Errorf("expected user override PYTHONUNBUFFERED=0 to survive; got %v", out)
	}
	if slices.Contains(out, "PYTHONUNBUFFERED=1") {
		t.Errorf("did not expect PYTHONUNBUFFERED=1 to be added on top of user's 0; got %v", out)
	}
}

func TestApplyUnbufferEnv_DoesNotMutateInput(t *testing.T) {
	in := []string{"FOO=bar"}
	inLen := len(in)
	_ = applyUnbufferEnv(in)
	if len(in) != inLen {
		t.Errorf("applyUnbufferEnv mutated input slice: had %d, now %d", inLen, len(in))
	}
}

func TestApplyUnbufferEnv_HandlesEmptyInput(t *testing.T) {
	out := applyUnbufferEnv(nil)
	for _, kv := range unbufferEnv {
		if !slices.Contains(out, kv) {
			t.Errorf("expected unbuffer entry %q in output for empty input", kv)
		}
	}
}

func TestApplyUnbufferEnv_SkipsMalformedEntries(t *testing.T) {
	// Entries without `=` should be ignored gracefully (not crash).
	in := []string{"NOEQUALS", "OK=val"}
	out := applyUnbufferEnv(in)
	if !slices.Contains(out, "NOEQUALS") {
		t.Errorf("malformed entry should pass through unchanged; got %v", out)
	}
}
