// Black-box integration tests: build the fernsicht binary, invoke it,
// assert on observable behavior. Validates Phase 1 acceptance.
//
// These tests are excluded from unit runs (`go test ./internal/...`)
// because they build a binary, which is slow. Run them explicitly:
//
//   go test ./test/integration/...
package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	binPath     string
	binBuildErr error
	binOnce     sync.Once
	binTmpDir   string

	sharedFake *sharedFakeServer
)

// TestMain owns the lifecycle of the built binary's tempdir + the
// process-wide fake signaling server. Setting FERNSICHT_SERVER_URL
// in os.Environ() means every test that doesn't override it gets a
// fast-responding fake instead of trying production (which would
// time out at 30s per test).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "fernsicht-cli-itest-")
	if err != nil {
		panic(err)
	}
	binTmpDir = dir

	sharedFake = startSharedFake()
	_ = os.Setenv("FERNSICHT_SERVER_URL", sharedFake.URL())

	code := m.Run()

	sharedFake.Close()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// fernsicht returns the path to a freshly-built binary, building once
// per test process. Skips on Windows (Phase 1 uses pipe-mode fallback
// there but the integration tests assume a unix shell environment).
func fernsicht(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("integration tests require unix shell tools")
	}
	binOnce.Do(func() {
		out := filepath.Join(binTmpDir, "fernsicht")
		cmd := exec.Command("go", "build", "-o", out,
			"github.com/MuteJester/fernsicht/cli/cmd/fernsicht")
		cmd.Dir = "../.." // run from cli/ directory
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			binBuildErr = err
			t.Logf("build stderr: %s", stderr.String())
			return
		}
		binPath = out
	})
	if binBuildErr != nil {
		t.Fatalf("failed to build fernsicht binary: %v", binBuildErr)
	}
	return binPath
}

// run invokes the binary with the given args and returns stdout,
// stderr, and the exit code.
func run(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(fernsicht(t), args...)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	exitCode = 0
	if err != nil {
		var exitErr *exec.ExitError
		if eErr, ok := err.(*exec.ExitError); ok {
			exitErr = eErr
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error running binary: %v", err)
		}
	}
	return so.String(), se.String(), exitCode
}

// --- Acceptance: `fernsicht run -- ls -la` ~= `ls -la` --------------

func TestIntegration_PassesThroughLsOutput(t *testing.T) {
	// Compare `fernsicht run -- ls /` against `ls /`. They should
	// produce the same lines (modulo timing); we just check that
	// well-known top-level entries (e.g., "etc", "usr") appear.
	out, _, code := run(t, "run", "--", "ls", "/")
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	for _, want := range []string{"usr", "etc"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected ls output to include %q; got:\n%s", want, out)
		}
	}
}

// --- Acceptance: `fernsicht run -- false` exits 1 -------------------

func TestIntegration_MirrorsFalseExit(t *testing.T) {
	_, _, code := run(t, "run", "--", "false")
	if code != 1 {
		t.Errorf("expected exit 1 from `false`; got %d", code)
	}
}

func TestIntegration_MirrorsArbitraryExit(t *testing.T) {
	_, _, code := run(t, "run", "--", "sh", "-c", "exit 73")
	if code != 73 {
		t.Errorf("expected exit 73; got %d", code)
	}
}

// --- Stdio passthrough --------------------------------------------

func TestIntegration_StdoutPassthrough(t *testing.T) {
	out, _, code := run(t, "run", "--", "echo", "alpha", "beta")
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if got := strings.TrimSpace(out); got != "alpha beta" {
		t.Errorf("expected stdout 'alpha beta'; got %q", got)
	}
}

func TestIntegration_StderrPassthrough(t *testing.T) {
	_, errOut, code := run(t, "run", "--", "sh", "-c", "echo to-stderr 1>&2")
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if !strings.Contains(errOut, "to-stderr") {
		t.Errorf("expected 'to-stderr' on stderr; got: %q", errOut)
	}
}

// --- Acceptance: isatty check via --no-pty -------------------------

func TestIntegration_PythonIsattyFalseWithNoPty(t *testing.T) {
	// In pipe mode (--no-pty), the wrapped command's stdout is a
	// pipe, so isatty(stdout) is False. Even when the caller's
	// stdout is a pipe (as in this test), this is the explicit
	// expectation.
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	out, _, code := run(t, "run", "--no-pty", "--",
		"python3", "-c", "import sys; print(sys.stdout.isatty())")
	if code != 0 {
		t.Fatalf("expected exit 0; got %d (stdout=%q)", code, out)
	}
	if got := strings.TrimSpace(out); got != "False" {
		t.Errorf("expected 'False' (stdout not a tty in --no-pty); got %q", got)
	}
}

// --- Unbuffer env applied -----------------------------------------

func TestIntegration_PythonUnbufferedEnvSet(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	out, _, code := run(t, "run", "--no-pty", "--",
		"python3", "-c", "import os; print(os.environ.get('PYTHONUNBUFFERED'))")
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if got := strings.TrimSpace(out); got != "1" {
		t.Errorf("expected PYTHONUNBUFFERED=1; got %q", got)
	}
}

func TestIntegration_NoUnbufferEnvOmitted(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	out, _, code := run(t, "run", "--no-pty", "--no-unbuffer", "--",
		"python3", "-c", "import os; print(os.environ.get('PYTHONUNBUFFERED', 'unset'))")
	if code != 0 {
		t.Fatalf("expected exit 0; got %d", code)
	}
	if got := strings.TrimSpace(out); got != "unset" {
		t.Errorf("expected PYTHONUNBUFFERED unset; got %q", got)
	}
}

// --- Error paths -------------------------------------------------

func TestIntegration_MissingSeparatorSuggestsFix(t *testing.T) {
	_, errOut, code := run(t, "run", "python", "train.py")
	if code != 2 {
		t.Errorf("expected exit 2; got %d", code)
	}
	if !strings.Contains(errOut, "Did you mean: fernsicht run -- python train.py") {
		t.Errorf("expected helpful suggestion; got: %q", errOut)
	}
}

func TestIntegration_NoArgsToRun(t *testing.T) {
	_, errOut, code := run(t, "run")
	if code != 2 {
		t.Errorf("expected exit 2; got %d", code)
	}
	if !strings.Contains(errOut, "missing wrapped command") {
		t.Errorf("expected 'missing wrapped command' message; got: %q", errOut)
	}
}

func TestIntegration_NonexistentWrappedCommand(t *testing.T) {
	_, errOut, code := run(t, "run", "--", "/no/such/cmd/please")
	// Wrap-level failure → exit 254 (per Phase 1 design).
	if code != 254 {
		t.Errorf("expected exit 254 for nonexistent command; got %d", code)
	}
	if !strings.Contains(errOut, "[fernsicht] error:") {
		t.Errorf("expected '[fernsicht] error:' prefix; got: %q", errOut)
	}
}

func TestIntegration_UnknownFlag(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-such-flag", "--", "echo", "x")
	if code != 2 {
		t.Errorf("expected exit 2; got %d", code)
	}
	if !strings.Contains(errOut, "flag provided but not defined") {
		t.Errorf("expected flag error; got: %q", errOut)
	}
}

// --- --version / --help still work --------------------------------

func TestIntegration_VersionWorks(t *testing.T) {
	out, _, code := run(t, "--version")
	if code != 0 {
		t.Errorf("expected exit 0; got %d", code)
	}
	if !strings.Contains(out, "fernsicht") {
		t.Errorf("expected 'fernsicht' in version output; got: %q", out)
	}
}

func TestIntegration_HelpWorks(t *testing.T) {
	out, _, code := run(t, "--help")
	if code != 0 {
		t.Errorf("expected exit 0; got %d", code)
	}
	if !strings.Contains(out, "USAGE") {
		t.Errorf("expected 'USAGE' section in help; got first 100 chars: %q",
			out[:min(100, len(out))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
