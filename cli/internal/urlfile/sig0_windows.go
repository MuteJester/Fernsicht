//go:build windows

package urlfile

import "os"

// On Windows, sending Signal(0) is not the standard alive-probe.
// Use os.Interrupt as a placeholder; pidAlive's real test is whether
// FindProcess + Signal returns nil. Windows-specific reaping is in
// Phase 6 polish.
var syscallSig0 os.Signal = os.Interrupt
