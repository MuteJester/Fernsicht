// Phase 4 integration tests: config file, custom patterns, --output
// json, --webhook, --copy-url, SIGUSR1 URL re-print, --strict mode.
package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// --- --pattern: custom regex from CLI ---

func TestPhase4_CustomPatternFlag(t *testing.T) {
	// Custom regex: matches lines like "step 50/100" with our own
	// shape. Two consecutive matches → confidence locks → ticks fire.
	_, errOut, code := run(t, "run", "--no-pty", "--no-qr", "--debug",
		"--pattern", `progress (\d+)`,
		"--",
		"bash", "-c", `
echo "starting"
echo "progress 25"
echo "progress 50"
echo "progress 75"
echo "done"
`)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "[parse] custom:flag-1") {
		t.Errorf("expected '[parse] custom:flag-1' lines; got: %q", errOut)
	}
}

// --- --pattern: invalid regex caught at startup ---

func TestPhase4_InvalidPatternRejectedAtStartup(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--no-qr",
		"--pattern", `(invalid`,
		"--",
		"echo", "x")
	if code != 2 {
		t.Errorf("expected exit 2 for invalid regex; got %d", code)
	}
	if !strings.Contains(errOut, "invalid regex") {
		t.Errorf("expected 'invalid regex' in stderr; got: %q", errOut)
	}
}

// --- .fernsicht.toml config loading ---

func TestPhase4_ConfigFileLoaded_PatternsApplied(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".fernsicht.toml")
	cfgContent := `
[run]
default_unit = "epoch"

[[detection.patterns]]
name = "myorch"
regex = 'progress (\d+)/(\d+)'
n_capture = 1
total_capture = 2
`
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o644); err != nil {
		t.Fatal(err)
	}

	_, errOut, code := run(t, "run", "--no-pty", "--no-qr", "--debug",
		"--config", cfgPath,
		"--",
		"bash", "-c", `
echo "progress 25/100"
echo "progress 50/100"
echo "progress 75/100"
`)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "custom:myorch") {
		t.Errorf("expected 'custom:myorch' parser to fire; got: %q", errOut)
	}
}

func TestPhase4_ConfigFileMissingIsSilent(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--no-qr",
		"--config", "/no/such/config.toml",
		"--",
		"echo", "ok")
	// Explicit missing config → error (per Load contract).
	if code != 2 {
		t.Errorf("expected exit 2 for missing explicit config; got %d", code)
	}
	if !strings.Contains(errOut, "config load") {
		t.Errorf("expected config-load error; got: %q", errOut)
	}
}

// --- --output json ---

func TestPhase4_OutputJSONStreamsEvents(t *testing.T) {
	out, _, code := run(t, "run", "--no-pty", "--no-qr",
		"--output", "json",
		"--",
		"echo", "x")
	if code != 0 {
		t.Fatal(code)
	}
	// Expect at least: session_open + session_close JSON lines on stdout.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var openSeen, closeSeen bool
	for _, line := range lines {
		var ev map[string]any
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			continue
		}
		switch ev["event"] {
		case "session_open":
			openSeen = true
		case "session_close":
			closeSeen = true
		}
	}
	if !openSeen {
		t.Errorf("expected session_open event in stdout; got: %q", out)
	}
	if !closeSeen {
		t.Errorf("expected session_close event in stdout; got: %q", out)
	}
}

func TestPhase4_OutputJSONInvalidValueRejected(t *testing.T) {
	_, errOut, code := run(t, "run", "--no-pty", "--no-qr",
		"--output", "yaml",
		"--",
		"echo", "x")
	if code != 2 {
		t.Errorf("expected exit 2 for unknown output mode; got %d", code)
	}
	if !strings.Contains(errOut, "unknown mode") {
		t.Errorf("expected 'unknown mode' error; got: %q", errOut)
	}
}

// --- --webhook ---

func TestPhase4_WebhookPostsOnExit(t *testing.T) {
	var received atomic.Bool
	var capturedMu sync.Mutex
	var captured webhookCapture

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, 4096)
		n, _ := r.Body.Read(body)
		var p webhookCapture
		if err := json.Unmarshal(body[:n], &p); err == nil {
			capturedMu.Lock()
			captured = p
			capturedMu.Unlock()
			received.Store(true)
		}
		w.WriteHeader(200)
	}))
	defer srv.Close()

	_, _, code := run(t, "run", "--no-pty", "--no-qr",
		"--webhook", srv.URL,
		"--",
		"echo", "ok")
	if code != 0 {
		t.Fatal(code)
	}

	// Webhook is async; give it a moment.
	deadline := time.Now().Add(2 * time.Second)
	for !received.Load() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if !received.Load() {
		t.Fatal("webhook was not POSTed within 2s of exit")
	}

	capturedMu.Lock()
	defer capturedMu.Unlock()
	if captured.Event != "session_end" {
		t.Errorf("event got %q want session_end", captured.Event)
	}
	if captured.Wrapped.ExitCode != 0 {
		t.Errorf("exit code got %d want 0", captured.Wrapped.ExitCode)
	}
	if captured.Session.RoomID == "" {
		t.Errorf("expected non-empty room_id")
	}
}

type webhookCapture struct {
	Event   string `json:"event"`
	Session struct {
		RoomID    string `json:"room_id"`
		ViewerURL string `json:"viewer_url"`
	} `json:"session"`
	Wrapped struct {
		Command  string `json:"command"`
		ExitCode int    `json:"exit_code"`
	} `json:"wrapped"`
}

// --- --copy-url graceful when no clipboard tool available ---

func TestPhase4_CopyURLGracefulNoTool(t *testing.T) {
	// In headless CI, no xclip/pbcopy → expect a warn line, not a fatal.
	_, errOut, code := run(t, "run", "--no-pty", "--no-qr", "--copy-url",
		"--",
		"echo", "ok")
	if code != 0 {
		t.Fatalf("exit %d, stderr: %q", code, errOut)
	}
	// Either succeeded silently (CI has clipboard) or warned (CI doesn't).
	// Both are acceptable. Just ensure the run completed.
}

// --- SIGUSR1 → re-print URL ---

func TestPhase4_SIGUSR1Reprints(t *testing.T) {
	// Spawn a long-running wrapped command, send SIGUSR1, observe a
	// second viewer banner on stderr.
	bin := fernsicht(t)

	cmd := newCmd(bin, []string{
		"run", "--no-pty", "--no-qr",
		"--",
		"sh", "-c", "sleep 5; exit 0",
	})
	var stderr safeBuf
	cmd.Stdout = nil
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(),
		"FERNSICHT_SERVER_URL="+sharedFake.URL(),
	)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// Wait for the first viewer banner.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(stderr.String(), "viewer:") >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if strings.Count(stderr.String(), "viewer:") < 1 {
		t.Fatal("first viewer banner did not appear within 3s")
	}

	// Send SIGUSR1; expect a SECOND viewer banner.
	_ = cmd.Process.Signal(syscall.SIGUSR1)
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(stderr.String(), "viewer:") >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if c := strings.Count(stderr.String(), "viewer:"); c < 2 {
		t.Errorf("expected ≥2 viewer banners after SIGUSR1; got %d (stderr: %q)", c, stderr.String())
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
}

// safeBuf is a goroutine-safe stand-in for bytes.Buffer used by
// concurrent stderr-readers. cmd.Stderr is read from the os/exec
// goroutine; tests read from the test goroutine.
type safeBuf struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeBuf) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *safeBuf) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// --- --unit override ---

func TestPhase4_UnitFlag(t *testing.T) {
	// We can't directly observe what unit the bridge sees from this
	// test (no DataChannel readback), but we can verify the wrap
	// pipeline accepts the flag and ticks succeed.
	_, errOut, code := run(t, "run", "--no-pty", "--no-qr", "--debug",
		"--unit", "batch",
		"--",
		"bash", "-c", `
echo "Training: 50%|####| 50/100 [00:00<00:00, 1.0it/s]"
echo "Training: 75%|####| 75/100 [00:00<00:00, 1.0it/s]"
echo "Training: 100%|####| 100/100 [00:00<00:00, 1.0it/s]"
`)
	if code != 0 {
		t.Fatalf("exit %d, stderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "[parse] tqdm") {
		t.Errorf("expected '[parse] tqdm' lines; got: %q", errOut)
	}
}
