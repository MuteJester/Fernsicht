//go:build windows

package wrap

import (
	"os"
	"syscall"
)

// defaultSignalSet on Windows is much smaller — Windows doesn't have
// the unix signal model. Console events become SIGINT (Ctrl-C) and
// SIGBREAK (Ctrl-Break); SIGTERM is recognized when sent via
// taskkill /F.
//
// SIGUSR1 doesn't exist; the URL re-print feature is unix-only for
// Phase 4. Phase 6 polish may add a named-pipe alternative.
func defaultSignalSet() []os.Signal {
	return []os.Signal{
		syscall.SIGINT, syscall.SIGTERM,
	}
}

// isReprintSignal — Windows has no SIGUSR1 equivalent. Always false;
// the run subcommand falls back to printing the URL only at startup.
func isReprintSignal(sig syscall.Signal) bool {
	return false
}
