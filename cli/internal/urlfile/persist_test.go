package urlfile

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefault_HonorsXDG(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
	got := Default(12345)
	if !strings.Contains(got, "/run/user/1000/fernsicht/12345.url") {
		t.Errorf("expected XDG path; got %q", got)
	}
}

func TestDefault_FallsBackToTmp(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	got := Default(12345)
	if !strings.HasSuffix(got, "fernsicht-12345.url") {
		t.Errorf("expected tmp fallback; got %q", got)
	}
}

func TestWriteAndRead_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.url")
	url := "https://app.example/#room=abc12345&role=viewer"
	if err := Write(path, url); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != url {
		t.Errorf("got %q want %q", got, url)
	}
}

func TestWrite_Atomic_TmpFileGone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.url")
	if err := Write(path, "url"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Errorf("temp file should be gone after rename; got err=%v", err)
	}
}

func TestRemove_Idempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.url")
	// Doesn't exist yet — should not error.
	if err := Remove(path); err != nil {
		t.Errorf("Remove of missing file should be nil; got %v", err)
	}
	// Create then remove.
	_ = os.WriteFile(path, []byte("x"), 0o600)
	if err := Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("file should be gone after Remove; got err=%v", err)
	}
	// Remove again — still no error.
	if err := Remove(path); err != nil {
		t.Errorf("second Remove should be nil; got %v", err)
	}
}

func TestRead_RejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.url")
	_ = os.WriteFile(path, []byte("\n\n  \n"), 0o600)
	if _, err := Read(path); err == nil {
		t.Error("expected error reading empty file")
	}
}

func TestParsePIDFromName(t *testing.T) {
	cases := []struct {
		name    string
		wantPID int
		wantOK  bool
	}{
		{"12345.url", 12345, true},
		{"fernsicht-12345.url", 12345, true},
		{"random.url", 0, false},
		{"12345", 0, false}, // no .url suffix
		{"", 0, false},
	}
	for _, tc := range cases {
		pid, ok := parsePIDFromName(tc.name)
		if pid != tc.wantPID || ok != tc.wantOK {
			t.Errorf("%q: got (%d,%v) want (%d,%v)",
				tc.name, pid, ok, tc.wantPID, tc.wantOK)
		}
	}
}

func TestPidAlive(t *testing.T) {
	// Our own PID — alive.
	if !pidAlive(os.Getpid()) {
		t.Error("expected own PID to be alive")
	}
	// A pid that's almost certainly free (above MAX_PID range).
	if runtime.GOOS != "windows" && pidAlive(0x7fffffff) {
		t.Error("expected pid 0x7fffffff to be unreachable")
	}
}

func TestDiscover_FindsLiveOurURLFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", tmp)
	path := Default(os.Getpid())
	if err := Write(path, "https://x"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Remove(path) })

	entries, err := Discover()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		if e.PID == os.Getpid() {
			found = true
			if e.URL != "https://x" {
				t.Errorf("Discover URL mismatch: got %q want https://x", e.URL)
			}
		}
	}
	if !found {
		t.Errorf("Discover did not find our pid=%d entry", os.Getpid())
	}
}
