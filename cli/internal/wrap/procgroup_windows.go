//go:build windows

package wrap

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// configureProcessGroup is a Windows stub. Phase 1 doesn't implement
// Job Object-based process tree management; that requires more
// platform-specific syscalls than we want to land in the first phase.
// Wrapped commands that spawn children may orphan them on Ctrl-C.
//
// Phase 6 (polish) revisits Windows process-tree handling using
// golang.org/x/sys/windows Job Objects with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
func configureProcessGroup(cmd *exec.Cmd) {
	// Best-effort: detach from console so Ctrl-C in the parent
	// doesn't propagate twice. This is a known-limited stand-in.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// signalGroup on Windows can only really kill the immediate child.
// SIGINT / SIGTERM aren't first-class Windows signals; the closest
// equivalent for graceful shutdown is sending CTRL_BREAK_EVENT to
// the process group, which works only because we set
// CREATE_NEW_PROCESS_GROUP above.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("wrap: cmd not started")
	}
	// Best-effort signal forwarding. SIGKILL/SIGTERM mapped to
	// process termination via Process.Signal/Kill.
	switch sig {
	case syscall.SIGKILL:
		return cmd.Process.Kill()
	default:
		return cmd.Process.Signal(os.Interrupt)
	}
}

func killGroup(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("wrap: cmd not started")
	}
	return cmd.Process.Kill()
}
