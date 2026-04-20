// Package clipboard wraps the OS-specific "copy text to system
// clipboard" tool. Used by --copy-url so users can paste the viewer
// URL into Slack / browser without re-typing.
//
// We shell out to xclip (Linux X11) / wl-copy (Linux Wayland) /
// pbcopy (macOS) / clip.exe (Windows + WSL). Pure-Go clipboard
// libraries either need CGO (defeats our static-binary goal) or have
// known reliability issues across DEs.
package clipboard

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

// ErrNoClipboard means we couldn't find any usable clipboard tool.
// Callers should warn the user and continue without copying.
var ErrNoClipboard = errors.New("clipboard: no supported tool found")

// Copy attempts to put text on the system clipboard. Returns nil on
// success, ErrNoClipboard if no tool was found, or a wrapped error
// with the tool's stderr if the tool failed.
func Copy(text string) error {
	cmd, err := pickCommand()
	if err != nil {
		return err
	}
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("clipboard: %s failed: %w (output: %s)",
			cmd.Path, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// pickCommand returns the highest-priority available tool for this
// platform, configured to read from stdin.
func pickCommand() (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("pbcopy"); err == nil {
			return exec.Command(path), nil
		}
	case "windows":
		if path, err := exec.LookPath("clip.exe"); err == nil {
			return exec.Command(path), nil
		}
		// WSL also has clip.exe via Windows interop.
		if path, err := exec.LookPath("clip"); err == nil {
			return exec.Command(path), nil
		}
	default: // unix-ish
		// Wayland sessions typically have wl-copy; X11 has xclip /
		// xsel. Check in order of "most modern first."
		if path, err := exec.LookPath("wl-copy"); err == nil {
			return exec.Command(path), nil
		}
		if path, err := exec.LookPath("xclip"); err == nil {
			return exec.Command(path, "-selection", "clipboard"), nil
		}
		if path, err := exec.LookPath("xsel"); err == nil {
			return exec.Command(path, "--clipboard", "--input"), nil
		}
		// WSL detection: clip.exe is available as a Windows interop binary.
		if path, err := exec.LookPath("clip.exe"); err == nil {
			return exec.Command(path), nil
		}
	}
	return nil, ErrNoClipboard
}

// Available reports whether any clipboard tool is on PATH. Useful
// for the run subcommand to decide whether --copy-url will work
// before actually trying.
func Available() bool {
	_, err := pickCommand()
	return err == nil
}
