//go:build unix

package wrap

import (
	"errors"
	"os/exec"
	"syscall"
)

// configureProcessGroup sets cmd to run in its own process group
// (Setpgid: true, Pgid: 0 → "create new group with pgid = pid").
//
// This means children spawned by the wrapped command join the same
// group, and we can deliver signals to the whole tree at once via
// signalGroup() / killGroup(). Without this, a wrapped `bash -c
// 'sleep 60 & wait'` would orphan its sleep child on Ctrl-C.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pgid = 0 // 0 → use child's pid as the new pgid
}

// signalGroup delivers sig to every process in cmd's process group.
// The negative pid is the standard kill(2) trick for "all in group."
//
// Returns nil if cmd hasn't started, has exited, or the signal
// landed; an OS-level error otherwise. Errors during shutdown are
// usually safe to log-and-ignore.
func signalGroup(cmd *exec.Cmd, sig syscall.Signal) error {
	if cmd == nil || cmd.Process == nil {
		return errors.New("wrap: cmd not started")
	}
	pid := cmd.Process.Pid
	// kill(-pid, sig) signals the whole group whose pgid == pid.
	return syscall.Kill(-pid, sig)
}

// killGroup is the escalation path: SIGKILL the whole tree. Used when
// graceful SIGTERM didn't take effect inside the deadline.
func killGroup(cmd *exec.Cmd) error {
	return signalGroup(cmd, syscall.SIGKILL)
}
