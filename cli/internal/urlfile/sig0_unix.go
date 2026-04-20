//go:build unix

package urlfile

import "syscall"

// syscallSig0 is the "no-op signal" used by pidAlive to probe
// whether a process exists. kill(pid, 0) returns ESRCH if not.
var syscallSig0 = syscall.Signal(0)
