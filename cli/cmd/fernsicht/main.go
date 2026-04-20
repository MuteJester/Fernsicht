// Command fernsicht wraps any shell command and broadcasts its
// progress to a viewer URL.
//
// Subcommand surface (one file per command in this package):
//
//   run         — wrap a command; broadcast progress (run.go)
//   url         — print URL of running session(s) (url.go)
//   doctor      — diagnose installation / network (doctor.go)
//   magic       — magic-prefix protocol reference (magic.go)
//   completion  — generate shell completion scripts (completion.go)
//   update      — check for / install a newer version (update.go)
//   version     — print version + build info (this file)
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	// Import bridge/pkg/embed so the linker pulls the bridge code
	// (and pion/webrtc transitively) into our binary.
	_ "github.com/MuteJester/fernsicht/bridge/pkg/embed"
)

// version is overridden at link time via -ldflags="-X main.version=...".
var version = "0.0.0-dev"

// commit is the git short-sha, set via -ldflags at release time.
var commit = "dev"

// buildDate is the build timestamp, set via -ldflags at release time.
var buildDate = "unknown"

func main() {
	if len(os.Args) < 2 {
		printHelp(os.Stdout)
		os.Exit(0)
	}

	cmd := os.Args[1]
	switch cmd {
	case "--version", "-V", "version":
		printVersion(os.Stdout)
	case "--help", "-h", "help":
		printHelp(os.Stdout)
	case "run":
		os.Exit(runCommand(os.Args[2:]))
	case "url":
		os.Exit(urlCommand(os.Args[2:]))
	case "doctor":
		os.Exit(doctorCommand(os.Args[2:]))
	case "magic":
		os.Exit(magicCommand(os.Args[2:]))
	case "completion":
		os.Exit(completionCommand(os.Args[2:]))
	case "update":
		os.Exit(updateCommand(os.Args[2:]))
	default:
		fmt.Fprintf(os.Stderr, "fernsicht: unknown command %q\n", cmd)
		// Suggest closest match — most "unknown" cases are typos.
		if suggestion := suggestCommand(cmd); suggestion != "" {
			fmt.Fprintf(os.Stderr, "  did you mean: fernsicht %s?\n", suggestion)
		}
		fmt.Fprintln(os.Stderr, "Run `fernsicht --help` for the full list.")
		os.Exit(2)
	}
}

// suggestCommand picks the most-similar known subcommand to user
// input. Tiny Levenshtein-style scorer — no external deps.
func suggestCommand(typed string) string {
	candidates := []string{
		"run", "url", "doctor", "magic",
		"completion", "update", "version", "help",
	}
	best := ""
	bestScore := -1
	for _, c := range candidates {
		score := commonPrefixLen(typed, c)
		// Bonus if same length (likely a typo, not a different word).
		if len(typed) == len(c) {
			score += 2
		}
		if score > bestScore && score >= 2 {
			bestScore = score
			best = c
		}
	}
	return best
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "fernsicht %s\n", version)
	fmt.Fprintf(w, "  commit:   %s\n", commit)
	fmt.Fprintf(w, "  built:    %s\n", buildDate)
	fmt.Fprintf(w, "  go:       %s\n", runtime.Version())
	fmt.Fprintf(w, "  os/arch:  %s/%s\n", runtime.GOOS, runtime.GOARCH)
}

// printHelp renders the top-level help. Layout matches CLI plan §4.5.
func printHelp(w io.Writer) {
	fmt.Fprint(w, `fernsicht — watch any command's progress from anywhere.

USAGE
    fernsicht <command> [flags]
    fernsicht run -- <wrapped command> [args...]

COMMANDS
    run         Wrap a command; broadcast its progress to a viewer URL.
    url         Print the viewer URL of a currently-running session.
    doctor      Check that fernsicht can reach the signaling server.
    magic       Show the magic-prefix protocol reference.
    completion  Generate shell completion scripts.
    update      Check for or install a newer version.
    version     Print version + build info.

EXAMPLES
    # Wrap a Python script:
    fernsicht run -- python train.py

    # Get the URL of a running session (in another terminal):
    fernsicht url

    # Check that fernsicht is working:
    fernsicht doctor

    # Tab completion for your shell (one-time):
    fernsicht completion bash > /etc/bash_completion.d/fernsicht

LEARN MORE
    fernsicht <subcommand> --help   per-subcommand reference
    https://github.com/MuteJester/Fernsicht

    Privacy: no telemetry, no accounts. Progress flows peer-to-peer
    via WebRTC after a brief handshake; the data stays between you
    and your viewers.
`)
}
