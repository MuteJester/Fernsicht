// Phase 2 integration: parser pipeline running end-to-end through
// the built binary. Validates magic prefix interception, Tier-1
// detection lock-in, strict-magic termination, and TUI bypass.
package integration

import (
	"strings"
	"testing"
)

// --- Magic prefix interception ---

func TestParse_MagicLinesStrippedFromOutput(t *testing.T) {
	out, _, code := run(t, "run", "--no-pty", "--debug", "--",
		"bash", "-c", `echo before; echo "__fernsicht__ progress 50/100"; echo after`)
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if strings.Contains(out, "__fernsicht__") {
		t.Errorf("magic prefix leaked into stdout: %q", out)
	}
	// Surrounding lines preserved.
	if !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Errorf("non-magic lines should pass through; got: %q", out)
	}
}

func TestParse_MagicCompactProducesParseLine(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--debug", "--",
		"bash", "-c", `echo "__fernsicht__ progress 75/100 batch"`)
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if !strings.Contains(errOut, "[parse]") {
		t.Errorf("expected '[parse]' line on stderr; got: %q", errOut)
	}
	// Phase-2 default OnTick prints "n=75 total=100".
	if !strings.Contains(errOut, "n=75 total=100") {
		t.Errorf("expected 'n=75 total=100' in parse output; got: %q", errOut)
	}
}

func TestParse_MagicJSONProducesParseLine(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--debug", "--",
		"bash", "-c", `echo '__fernsicht__ {"value":0.42,"label":"Training"}'`)
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if !strings.Contains(errOut, "[parse]") {
		t.Errorf("expected '[parse]' line on stderr; got: %q", errOut)
	}
	if !strings.Contains(errOut, "value=0.420") {
		t.Errorf("expected 'value=0.420'; got: %q", errOut)
	}
}

// --- Strict-magic ---

func TestParse_StrictMagicExits250OnInvalid(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--strict-magic", "--debug", "--",
		"bash", "-c", `echo "ok"; echo "__fernsicht__ frobulate"; sleep 30`)
	if code != 250 {
		t.Errorf("expected exit 250; got %d (stderr=%q)", code, errOut)
	}
	if !strings.Contains(errOut, "strict-magic") {
		t.Errorf("expected 'strict-magic' in stderr; got: %q", errOut)
	}
}

func TestParse_NonStrictMagic_WarnsAndContinues(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--debug", "--",
		"bash", "-c", `echo "ok"; echo "__fernsicht__ frobulate"; echo "still running"`)
	if code != 0 {
		t.Errorf("expected exit 0 (warn but continue); got %d", code)
	}
	if !strings.Contains(errOut, "warn: invalid magic prefix") {
		t.Errorf("expected warn line; got: %q", errOut)
	}
}

// --- --no-magic disables interception ---

func TestParse_NoMagicLeavesPrefixIntact(t *testing.T) {
	out, _, code := run(t, "run", "--no-pty", "--no-magic", "--debug", "--",
		"bash", "-c", `echo "__fernsicht__ progress 50/100"`)
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if !strings.Contains(out, "__fernsicht__") {
		t.Errorf("with --no-magic, prefix should NOT be stripped; got: %q", out)
	}
}

// --- Tier-1 detection lock-in (acceptance: pip-style tqdm) ---

func TestParse_TqdmLocksAfterTwoMatches(t *testing.T) {
	// Emit five tqdm-shaped lines. Confidence locks at the 2nd match;
	// we expect at least 4 [parse] tick lines.
	script := `
import sys
for i in range(0, 110, 20):
    print(f"Training:  {i}%|{'#'*(i//10):10s}| {i}/100 [00:00<00:00, 1.0it/s]", flush=True)
`
	_, errOut, code := run(t, "run", "--no-pty", "--debug", "--",
		"python3", "-c", script)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	tickCount := strings.Count(errOut, "[parse] tqdm")
	// 6 lines produced; threshold=2 → first locks parser; subsequent
	// 5 fire ticks. (The 1st actually ALSO fires — Match returns
	// true on the 2nd which IS a tick for the line that crossed.)
	if tickCount < 4 {
		t.Errorf("expected at least 4 tqdm parse lines; got %d in: %q",
			tickCount, errOut)
	}
}

// --- Tier-1 false-positive rejection (acceptance) ---

func TestParse_MakeStyleSingleShotIsRejected(t *testing.T) {
	// Single isolated `[1/2]` line surrounded by noise should NOT
	// produce a parse tick — confidence requires 2 matches.
	_, errOut, _ := run(t, "run", "--no-pty", "--debug", "--",
		"bash", "-c", `
echo "starting build"
echo "[1/2] Compiling foo.o"
echo "loading config"
echo "running tests"
echo "done"
`)
	if strings.Contains(errOut, "[parse] fraction-bracket") {
		t.Errorf("single-shot fraction-bracket should NOT produce a tick; stderr: %q", errOut)
	}
}

// --- TUI / alt-screen detection ---

func TestParse_AltScreenDisablesAutoDetect(t *testing.T) {
	// Emit the alt-screen enter escape, then a tqdm-shaped line.
	// With auto-detect disabled by TUI mode, the tqdm line should NOT
	// produce a parse tick. We expect the warn line on stderr.
	script := `
import sys
sys.stdout.write("\x1b[?1049h")  # alt-screen enter
sys.stdout.flush()
# These would normally tick after lock-in; should be suppressed:
for i in range(5):
    print(f"Training: {(i+1)*20}%|####| {(i+1)*20}/100 [00:00<00:00, 1.0it/s]", flush=True)
`
	_, errOut, code := run(t, "run", "--no-pty", "--debug", "--",
		"python3", "-c", script)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "fullscreen TUI") {
		t.Errorf("expected fullscreen-TUI warn; got: %q", errOut)
	}
	if strings.Contains(errOut, "[parse] tqdm") {
		t.Errorf("with TUI active, tqdm parses should be suppressed; got: %q", errOut)
	}
}

// --- Pipe / stderr passthrough still works with parser inserted ---

func TestParse_StderrStillSeparated(t *testing.T) {
	out, errOut, code := run(t, "run", "--no-pty", "--debug", "--",
		"bash", "-c", `echo to-stdout; echo to-stderr 1>&2`)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(out, "to-stdout") {
		t.Errorf("stdout should contain 'to-stdout'; got: %q", out)
	}
	if !strings.Contains(errOut, "to-stderr") {
		t.Errorf("stderr should contain 'to-stderr'; got: %q", errOut)
	}
	if strings.Contains(out, "to-stderr") {
		t.Errorf("stderr leaked into stdout: %q", out)
	}
}

// --- --no-detect disables Tier-1, magic still works ---

func TestParse_NoDetectSilencesTier1(t *testing.T) {
	script := `
for i in range(5):
    print(f"Training: {(i+1)*20}%|####| {(i+1)*20}/100 [00:00<00:00, 1.0it/s]", flush=True)
print("__fernsicht__ progress 99/100", flush=True)
`
	_, errOut, code := run(t, "run", "--no-pty", "--no-detect", "--debug", "--",
		"python3", "-c", script)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if strings.Contains(errOut, "[parse] tqdm") {
		t.Errorf("with --no-detect, tqdm parse should NOT fire; got: %q", errOut)
	}
	if !strings.Contains(errOut, "[parse] magic-compact") {
		t.Errorf("magic prefix should still fire even with --no-detect; got: %q", errOut)
	}
}
