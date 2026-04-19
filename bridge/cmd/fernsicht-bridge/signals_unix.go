//go:build !windows

package main

import (
	"os"
	"syscall"
)

// diagSignals returns the OS signals the diagnostic-dump path should
// be wired to. SIGUSR1 is the conventional Unix "dump state" signal.
func diagSignals() []os.Signal {
	return []os.Signal{syscall.SIGUSR1}
}
