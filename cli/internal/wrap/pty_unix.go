//go:build unix

package wrap

import (
	"errors"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// startPty starts cmd attached to a freshly-allocated pty. Returns
// the master fd; the slave is wired to cmd's stdin/stdout/stderr.
//
// The wrapped command sees a real tty — important for tools that
// disable color / progress bars when isatty(stdout) is false.
//
// Caller is responsible for closing the master and forwarding bytes
// to/from it (see runPty in wrap.go).
func startPty(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	master, err := pty.Start(cmd)
	if err != nil {
		return nil, err
	}

	// Best-effort: copy the parent terminal's window size onto the
	// pty. If the parent stdout isn't a tty, InheritSize is a no-op
	// at the syscall level — safe to call unconditionally.
	if os.Stdout != nil {
		_ = pty.InheritSize(os.Stdout, master)
	}
	return master, nil
}

// makeTerminalRaw puts the parent terminal into raw mode so single
// keystrokes pass through to the wrapped command immediately (instead
// of being buffered until newline by the cooked-mode terminal driver).
//
// Returns a restore function the caller MUST defer; otherwise the
// terminal stays in raw mode after exit and the user's shell becomes
// unusable (no echo, no line editing).
//
// Reserved for Phase 6 polish — not invoked by the Phase 1 pump loop
// because raw mode interacts badly with caller signal delivery
// (Ctrl-C in raw mode no longer becomes SIGINT automatically).
//
//nolint:unused // referenced from Phase 6 onwards
func makeTerminalRaw(fd int) (restore func(), err error) {
	if !term.IsTerminal(fd) {
		return nil, errors.New("wrap: stdin is not a terminal")
	}
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() { _ = term.Restore(fd, oldState) }, nil
}
