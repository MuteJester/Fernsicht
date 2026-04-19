//go:build windows

package main

import "os"

// diagSignals returns no signals on Windows — there is no SIGUSR1
// equivalent. Users debugging on Windows should set the
// FERNSICHT_BRIDGE_LOG=debug env var (plan §13).
func diagSignals() []os.Signal {
	return nil
}
