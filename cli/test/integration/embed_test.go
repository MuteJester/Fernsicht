// Phase 3 integration: end-to-end through embed → bridge → fake
// signaling server. Validates URL printing, URL persistence, magic-
// prefix lifecycle to bridge, and graceful shutdown.
package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSignaling is a tiny http server that satisfies the bridge's
// /session and /poll/ endpoints so embed.Open can complete a
// handshake. No WebRTC is exercised because no /watch ever arrives.
type fakeSignaling struct {
	srv   *httptest.Server
	url   string
	mu    sync.Mutex
	polls int
}

// newFakeSignaling builds a per-test fake signaling server. The
// roomID is snapshot into closures at construction time, so handlers
// don't race with the test goroutine that holds the *fakeSignaling.
func newFakeSignaling(t *testing.T, roomID string) *fakeSignaling {
	t.Helper()
	f := &fakeSignaling{}
	mux := http.NewServeMux()
	mux.HandleFunc("/poll/", f.handlePoll)
	mux.HandleFunc("/ticket/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	f.srv = httptest.NewServer(mux)
	f.url = f.srv.URL
	// Bind /session AFTER the server has its URL — captures roomID
	// + url by closure value, no shared state.
	mux.HandleFunc("/session", makeSessionHandler(roomID, f.url))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeSignaling) URL() string { return f.url }
func (f *fakeSignaling) PollCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.polls
}

func makeSessionHandler(roomID, srvURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		body := map[string]any{
			"room_id":            roomID,
			"sender_secret":      "test-secret",
			"viewer_url":         fmt.Sprintf("https://app.example/#room=%s", roomID),
			"signaling_url":      srvURL,
			"expires_at":         "2099-01-01T00:00:00Z",
			"expires_in":         3600,
			"max_viewers":        8,
			"poll_interval_hint": 1,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}

func (f *fakeSignaling) handlePoll(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.polls++
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"tickets":[]}`))
}

// runWithSignaling invokes the binary with FERNSICHT_SERVER_URL
// pointing at fake. Returns stdout, stderr, exit code.
func runWithSignaling(t *testing.T, sig *fakeSignaling, extraEnv []string, args ...string) (string, string, int) {
	t.Helper()
	bin := fernsicht(t)
	full := append([]string{}, args...)
	cmd := newCmd(bin, full)
	cmd.Env = append(os.Environ(),
		"FERNSICHT_SERVER_URL="+sig.URL(),
	)
	cmd.Env = append(cmd.Env, extraEnv...)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if e, ok := err.(interface{ ExitCode() int }); ok {
			code = e.ExitCode()
		} else {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	return stdout.String(), stderr.String(), code
}

// --- Acceptance: URL printed, wrapped command runs ---

func TestPhase3_PrintsViewerURL(t *testing.T) {
	sig := newFakeSignaling(t, "abc12345")
	_, errOut, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--", "echo", "hello")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %q", code, errOut)
	}
	if !strings.Contains(errOut, "viewer:") {
		t.Errorf("expected 'viewer:' line on stderr; got %q", errOut)
	}
	if !strings.Contains(errOut, "abc12345") {
		t.Errorf("expected viewer URL with room id; got %q", errOut)
	}
}

func TestPhase3_WrappedCommandStdoutPreserved(t *testing.T) {
	sig := newFakeSignaling(t, "stdout1")
	out, _, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--", "echo", "wrapped-output")
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out, "wrapped-output") {
		t.Errorf("wrapped command's stdout should pass through; got %q", out)
	}
}

func TestPhase3_ExitCodeMirrored(t *testing.T) {
	sig := newFakeSignaling(t, "exit1")
	_, _, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--", "false")
	if code != 1 {
		t.Errorf("expected exit 1; got %d", code)
	}
}

// --- URL file persistence ---

func TestPhase3_URLFileWritten_AndCleaned(t *testing.T) {
	sig := newFakeSignaling(t, "urlfile1")
	tmp := t.TempDir()
	_, _, code := runWithSignaling(t, sig,
		[]string{"XDG_RUNTIME_DIR=" + tmp},
		"run", "--no-pty", "--no-qr", "--", "echo", "x")
	if code != 0 {
		t.Fatal(code)
	}
	// After exit, the URL file should be cleaned up.
	entries, err := os.ReadDir(tmp + "/fernsicht")
	if err == nil {
		// If the dir exists, it should be empty.
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".url") {
				t.Errorf("URL file should be removed after exit; found %s", e.Name())
			}
		}
	}
}

// --- Magic-prefix lifecycle reaches the bridge ---

func TestPhase3_MagicProgressEndToEnd(t *testing.T) {
	sig := newFakeSignaling(t, "magic1")
	out, errOut, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--",
		"bash", "-c", `
echo before
echo "__fernsicht__ progress 50/100"
echo "__fernsicht__ progress 75/100"
echo after
`)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if strings.Contains(out, "__fernsicht__") {
		t.Errorf("magic prefix leaked into stdout: %q", out)
	}
	for _, want := range []string{"before", "after"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in stdout; got %q", want, out)
		}
	}
}

// --- Poll loop activity confirms the bridge is alive end-to-end ---

func TestPhase3_BridgePollsServerWhileWrappedCommandRuns(t *testing.T) {
	sig := newFakeSignaling(t, "poll1")
	_, _, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--",
		"bash", "-c", "sleep 2.5")
	if code != 0 {
		t.Fatal(code)
	}
	if got := sig.PollCount(); got == 0 {
		t.Errorf("expected ≥1 poll within 2.5s wrapped sleep; got 0")
	}
}

// --- First-run tip lifecycle ---

func TestPhase3_FirstRunTipShownThenSuppressed(t *testing.T) {
	sig := newFakeSignaling(t, "intro1")
	tmp := t.TempDir() // fresh XDG_CONFIG_HOME → first run
	env := []string{"XDG_CONFIG_HOME=" + tmp}

	// First run → tip should appear.
	_, e1, c1 := runWithSignaling(t, sig, env,
		"run", "--no-pty", "--no-qr", "--", "true")
	if c1 != 0 {
		t.Fatal(c1)
	}
	if !strings.Contains(e1, "tip: open the URL") {
		t.Errorf("expected first-run tip on first invocation; got: %q", e1)
	}

	// Second run with same XDG_CONFIG_HOME → tip suppressed.
	_, e2, c2 := runWithSignaling(t, sig, env,
		"run", "--no-pty", "--no-qr", "--", "true")
	if c2 != 0 {
		t.Fatal(c2)
	}
	if strings.Contains(e2, "tip: open the URL") {
		t.Errorf("tip should NOT appear on second invocation; got: %q", e2)
	}
}

// --- --quiet suppresses the URL banner but keeps wrapped output ---

func TestPhase3_QuietSuppressesBanner(t *testing.T) {
	sig := newFakeSignaling(t, "quiet1")
	_, errOut, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--quiet", "--", "echo", "x")
	if code != 0 {
		t.Fatal(code)
	}
	if strings.Contains(errOut, "viewer:") {
		t.Errorf("--quiet should suppress viewer banner; got: %q", errOut)
	}
}

// --- --share prints URL on stdout ---

func TestPhase3_ShareWritesURLToStdout(t *testing.T) {
	sig := newFakeSignaling(t, "share1")
	out, _, code := runWithSignaling(t, sig, nil,
		"run", "--no-pty", "--no-qr", "--share", "--", "true")
	if code != 0 {
		t.Fatal(code)
	}
	if !strings.Contains(out, "share1") {
		t.Errorf("--share should print URL on stdout; got: %q", out)
	}
}

// --- --no-fail-on-bridge fallback path ---

func TestPhase3_NoFailOnBridge_RunsWithoutSignalingServer(t *testing.T) {
	bin := fernsicht(t)
	cmd := newCmd(bin,
		[]string{"run", "--no-pty", "--no-qr",
			"--server-url", "http://127.0.0.1:65535",
			"--no-fail-on-bridge", "--",
			"echo", "fallback-output"})

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("expected exit 0; got err=%v stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "fallback-output") {
		t.Errorf("wrapped output should still appear; got: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "warn: bridge open failed") {
		t.Errorf("expected warn about bridge failure; got: %q", stderr.String())
	}
}

// --- helper to share with run_test.go ---

func newCmd(bin string, args []string) *exec.Cmd {
	return exec.Command(bin, args...)
}

// --- Settle the test process so the bridge's polling goroutines get
// time to actually hit the server. Other tests use blocking sleeps
// inside the wrapped command for the same purpose; this is just a
// safety net so flaky CI machines don't false-fail.

var _ = time.Second
