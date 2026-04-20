//go:build unix

package wrap

import (
	"os"
	"syscall"
)

// defaultSignalSet returns the signals the wrap layer subscribes to
// when Options.SignalSource isn't injected. Unix supports the full
// kit including SIGUSR1 (used by run for URL re-print).
func defaultSignalSet() []os.Signal {
	return []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
		syscall.SIGQUIT, syscall.SIGHUP,
		syscall.SIGUSR1,
	}
}

// isReprintSignal reports whether sig is the "re-print URL" signal.
// SIGUSR1 on unix; no equivalent on Windows.
func isReprintSignal(sig syscall.Signal) bool {
	return sig == syscall.SIGUSR1
}
