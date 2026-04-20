//go:build windows

package wrap

import (
	"errors"
	"io"
	"os/exec"
)

// startPty on Windows is a Phase-1 stub. ConPTY (the Windows pty
// API) is available on Win10 1809+ but plumbing it through requires
// CreatePseudoConsole + STARTUPINFOEX wiring that we don't want to
// land in Phase 1.
//
// For now: error out so the caller falls back to pipe mode. Phase 6
// adds proper ConPTY support; meanwhile, Windows users get the same
// feature set as `--no-pty`.
func startPty(cmd *exec.Cmd) (io.ReadWriteCloser, error) {
	return nil, errors.New("pty: not yet supported on Windows; use --no-pty")
}

// makeTerminalRaw — Windows stub. Console raw-mode handling is
// possible via SetConsoleMode but isn't needed in pipe mode.
func makeTerminalRaw(fd int) (restore func(), err error) {
	return nil, errors.New("wrap: terminal raw mode not supported on Windows")
}
