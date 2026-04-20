package wrap

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// --- Basic exit-code mirroring -------------------------------------

func TestRun_MirrorsZeroExit(t *testing.T) {
	res, err := Run(context.Background(), Options{
		Command: "true",
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit 0; got %d", res.ExitCode)
	}
}

func TestRun_MirrorsNonZeroExit(t *testing.T) {
	res, err := Run(context.Background(), Options{
		Command: "false",
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 1 {
		t.Errorf("expected exit 1; got %d", res.ExitCode)
	}
}

func TestRun_MirrorsArbitraryExitCode(t *testing.T) {
	// Use sh -c "exit 42" to verify mid-range exit codes pass through.
	res, err := Run(context.Background(), Options{
		Command: "sh",
		Args:    []string{"-c", "exit 42"},
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 42 {
		t.Errorf("expected exit 42; got %d", res.ExitCode)
	}
}

// --- Stdio passthrough --------------------------------------------

func TestRun_StdoutPassthrough_PipeMode(t *testing.T) {
	var stdout bytes.Buffer
	res, err := Run(context.Background(), Options{
		Command: "echo",
		Args:    []string{"hello"},
		NoPty:   true, // pipe mode
		Stdout:  &stdout,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("expected exit 0; got %d", res.ExitCode)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hello" {
		t.Errorf("expected stdout 'hello'; got %q", got)
	}
}

func TestRun_StderrSeparatedFromStdout_PipeMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	_, err := Run(context.Background(), Options{
		Command: "sh",
		Args:    []string{"-c", "echo out; echo err 1>&2"},
		NoPty:   true,
		Stdout:  &stdout,
		Stderr:  &stderr,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "out" {
		t.Errorf("stdout: expected 'out'; got %q", got)
	}
	if got := strings.TrimSpace(stderr.String()); got != "err" {
		t.Errorf("stderr: expected 'err'; got %q", got)
	}
}

// --- Pty mode ------------------------------------------------------

func TestRun_PtyAllocatesTty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty not supported on Windows in Phase 1")
	}
	// Try to allocate a pty via the same path Run uses. If the test
	// environment forbids pty allocation (sandboxed CI / restricted
	// container), skip — Run's runtime fallback to pipe mode covers
	// that case and is exercised by other tests.
	probe := exec.Command("sh", "-c", "tty; exit 0")
	configureProcessGroup(probe)
	master, err := startPty(probe)
	if err != nil {
		t.Skipf("pty allocation unavailable in this environment: %v", err)
	}
	defer master.Close()

	var out bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&out, master)
		close(done)
	}()
	_ = probe.Wait() // `tty` returns non-zero when not a real tty; doesn't matter.
	master.Close()
	<-done

	// `tty` prints the device path of stdin. With a pty allocated,
	// we expect /dev/pts/N or similar.
	if !strings.Contains(out.String(), "/dev/") {
		t.Errorf("expected `tty` output to mention a device path; got %q",
			out.String())
	}
}

// --- Process-group signaling --------------------------------------

func TestRun_SIGINTKillsEntireProcessTree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signaling not implemented on Windows")
	}
	// The point of process-group signaling: a wrapped command that
	// spawns children, then we send a single SIGINT, and EVERY
	// member of the group dies. Without setpgid, the children would
	// outlive the parent we directly signaled.
	//
	// We use a Python helper that spawns a child process, records
	// its pid to a file, then sleeps. SIGINT to the group should
	// kill both the parent and the child.
	tmp, err := os.MkdirTemp("", "wrap-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	pidfile := filepath.Join(tmp, "child.pid")

	// Python so signal handling is predictable across distros.
	// Parent: spawn `sleep 60` as a separate process, record its
	// pid, then sleep ourselves. Both processes are in the same
	// pgid (created by configureProcessGroup on the parent).
	script := fmt.Sprintf(`
import os, subprocess, sys, time
p = subprocess.Popen(["sleep", "60"])
open(%q, "w").write(str(p.pid))
sys.stdout.flush()
time.sleep(60)
`, pidfile)

	sigCh := make(chan os.Signal, 1)
	done := make(chan Result)
	go func() {
		res, _ := Run(context.Background(), Options{
			Command:      "python3",
			Args:         []string{"-c", script},
			Stdout:       io.Discard,
			Stderr:       io.Discard,
			SignalSource: sigCh,
		})
		done <- res
	}()

	// Wait for the child pid to be recorded.
	deadline := time.Now().Add(3 * time.Second)
	var childPid int
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(pidfile); err == nil && len(data) > 0 {
			fmt.Sscanf(string(data), "%d", &childPid)
			if childPid > 0 {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if childPid == 0 {
		t.Skip("python3 / sleep child didn't start within 3s; environment may lack python3")
	}

	// SIGINT to the wrapper → forwarded to the group → both die.
	sigCh <- syscall.SIGINT

	select {
	case <-done:
		// Give the kernel a moment to reap the child.
		time.Sleep(50 * time.Millisecond)
		// kill(pid, 0) probes existence: nil → still running.
		if err := syscall.Kill(childPid, 0); err == nil {
			_ = syscall.Kill(childPid, syscall.SIGKILL)
			t.Errorf("child (pid %d) survived SIGINT to its parent's group", childPid)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return within 5s of SIGINT")
	}
}

func TestRun_DoubleSIGINTEscalatesToKill(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process group signaling not implemented on Windows")
	}
	// Use a script that traps SIGINT and ignores it. Single SIGINT
	// shouldn't kill it; second SIGINT within the override window
	// should escalate to SIGKILL.
	sigCh := make(chan os.Signal, 2)

	done := make(chan Result)
	go func() {
		res, _ := Run(context.Background(), Options{
			Command:               "bash",
			Args:                  []string{"-c", "trap '' INT; sleep 30"},
			Stdout:                io.Discard,
			Stderr:                io.Discard,
			SignalSource:          sigCh,
			SecondInterruptWindow: 1 * time.Second,
		})
		done <- res
	}()

	// Give bash a moment to install the trap.
	time.Sleep(200 * time.Millisecond)

	// First SIGINT — bash ignores it.
	sigCh <- syscall.SIGINT

	// Second within window — escalates to SIGKILL.
	time.Sleep(50 * time.Millisecond)
	sigCh <- syscall.SIGINT

	select {
	case res := <-done:
		// ExitCode should reflect signal termination.
		// SIGKILL == 9, so exit code = 128+9 = 137.
		if res.ExitCode != 128+int(syscall.SIGKILL) {
			t.Errorf("expected exit %d (SIGKILL); got %d",
				128+int(syscall.SIGKILL), res.ExitCode)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run didn't return within 3s after double SIGINT")
	}
}

// --- Error paths ---------------------------------------------------

func TestRun_NonexistentCommand_ReturnsErr(t *testing.T) {
	_, err := Run(context.Background(), Options{
		Command: "/no/such/command/please",
		Stdout:  io.Discard,
		Stderr:  io.Discard,
	})
	if err == nil {
		t.Error("expected error for nonexistent command")
	}
}

// --- Unbuffer env --------------------------------------------------

func TestRun_AppliesUnbufferEnv(t *testing.T) {
	var stdout bytes.Buffer
	_, err := Run(context.Background(), Options{
		Command: "sh",
		Args:    []string{"-c", "echo $PYTHONUNBUFFERED"},
		Env:     []string{"PATH=" + os.Getenv("PATH")},
		NoPty:   true,
		Stdout:  &stdout,
		Stderr:  io.Discard,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "1" {
		t.Errorf("expected PYTHONUNBUFFERED=1 in wrapped env; got %q", got)
	}
}

func TestRun_NoUnbufferDoesNotSetEnv(t *testing.T) {
	var stdout bytes.Buffer
	_, err := Run(context.Background(), Options{
		Command:    "sh",
		Args:       []string{"-c", "echo \"[${PYTHONUNBUFFERED:-unset}]\""},
		Env:        []string{"PATH=" + os.Getenv("PATH")},
		NoPty:      true,
		NoUnbuffer: true,
		Stdout:     &stdout,
		Stderr:     io.Discard,
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "[unset]" {
		t.Errorf("expected PYTHONUNBUFFERED unset with --no-unbuffer; got %q", got)
	}
}
