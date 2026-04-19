// Command fernsicht-bridge is a language-agnostic WebRTC publishing
// daemon for Fernsicht. Language SDKs spawn it as a subprocess and
// exchange newline-delimited JSON over stdin/stdout.
//
// This binary is the thin process-level wrapper around bridge.Run:
//   - Parses --version (and exits) before doing anything else.
//   - Wires SIGINT / SIGTERM to context cancellation so bridge.Run does
//     its §8 graceful shutdown (END active task, drain viewers, emit
//     "closed", exit 0).
//   - Wires SIGUSR1 (Unix only) to a stderr diagnostic dump.
//   - Maps bridge.Run's typed errors to the §4.6 exit codes.
//
// All real protocol logic lives in internal/bridge.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/MuteJester/fernsicht/bridge/internal/bridge"
)

// version is set at build time via -ldflags="-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.BoolVar(showVersion, "v", false, "print version and exit (shorthand)")
	flag.Parse()

	if *showVersion {
		fmt.Printf("fernsicht-bridge %s\n", version)
		os.Exit(0)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handling. SIGINT/SIGTERM cancel the context (triggers
	// graceful close in bridge.Run). Diagnostic signals (SIGUSR1 on
	// Unix; nothing on Windows) trigger a stderr dump.
	sigCh := make(chan os.Signal, 4)
	watch := append([]os.Signal{syscall.SIGINT, syscall.SIGTERM}, diagSignals()...)
	signal.Notify(sigCh, watch...)
	defer signal.Stop(sigCh)

	go func() {
		for {
			select {
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				switch sig {
				case syscall.SIGINT, syscall.SIGTERM:
					cancel()
					return
				default:
					// Any other watched signal is a diagnostic dump
					// request (currently only SIGUSR1 on Unix).
					dumpDiagnostics()
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	err := bridge.RunWithOptions(ctx, os.Stdin, os.Stdout, bridge.Options{
		Version: version,
	})
	os.Exit(exitCode(err))
}

// exitCode maps a bridge.Run return error to a process exit code per
// .private/BRIDGE_IMPLEMENTATION_PLAN.md §4.6.
func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, bridge.ErrProtocolMismatch):
		fmt.Fprintf(os.Stderr, "fernsicht-bridge: %v\n", err)
		return 4
	case errors.Is(err, bridge.ErrSessionFailed):
		fmt.Fprintf(os.Stderr, "fernsicht-bridge: %v\n", err)
		return 3
	default:
		fmt.Fprintf(os.Stderr, "fernsicht-bridge: %v\n", err)
		return 1
	}
}

// dumpDiagnostics writes runtime stats to stderr. Process-level only
// today; bridge-level snapshot (active session, viewer roster,
// pending queue depths) will be wired through bridge.Diagnostics()
// in a follow-up.
func dumpDiagnostics() {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	fmt.Fprintf(os.Stderr, "[fernsicht-bridge diagnostics]\n")
	fmt.Fprintf(os.Stderr, "  version:    %s\n", version)
	fmt.Fprintf(os.Stderr, "  goroutines: %d\n", runtime.NumGoroutine())
	fmt.Fprintf(os.Stderr, "  heap_inuse: %d KB\n", ms.HeapInuse/1024)
	fmt.Fprintf(os.Stderr, "  heap_alloc: %d KB\n", ms.HeapAlloc/1024)
	fmt.Fprintf(os.Stderr, "  num_gc:     %d\n", ms.NumGC)
}
