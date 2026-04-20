package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MuteJester/fernsicht/cli/internal/urlfile"
)

// urlCommand handles `fernsicht url [--all|--pid N]`.
//
// Behavior (per CLI plan §4.6):
//   - no args, exactly one running session: print just the URL.
//   - no args, multiple sessions: print a table.
//   - --all: print table for ALL discovered sessions.
//   - --pid N: print just that PID's URL.
func urlCommand(args []string) int {
	fs := flag.NewFlagSet("url", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	all := fs.Bool("all", false, "Show every running session in a table.")
	pid := fs.Int("pid", 0, "Show the URL of a specific session by PID.")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		fmt.Fprintln(os.Stderr,
			"Usage: fernsicht url [--all] [--pid PID]")
		return 2
	}

	entries, err := urlfile.Discover()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		return 1
	}

	if *pid > 0 {
		for _, e := range entries {
			if e.PID == *pid {
				fmt.Println(e.URL)
				return 0
			}
		}
		fmt.Fprintf(os.Stderr,
			"[fernsicht] error: no running session with PID %d\n", *pid)
		fmt.Fprintln(os.Stderr,
			"  hint: run `fernsicht url --all` to list running sessions")
		return 1
	}

	if len(entries) == 0 {
		fmt.Fprintln(os.Stderr,
			"[fernsicht] no running fernsicht sessions found.")
		fmt.Fprintln(os.Stderr,
			"  hint: a session is created by `fernsicht run -- <command>`")
		return 1
	}

	if !*all && len(entries) == 1 {
		fmt.Println(entries[0].URL)
		return 0
	}

	// One-or-more sessions in table form.
	printSessionTable(os.Stdout, entries)
	return 0
}

// printSessionTable renders a fixed-column table listing each
// running session: PID, age, URL.
//
// Age is approximate — derived from the URL file's mtime, which
// is when `urlfile.Write` was called by the run subcommand.
func printSessionTable(w io.Writer, entries []urlfile.SessionEntry) {
	// Stable sort: oldest sessions first (most likely the one the
	// user wants to interrupt / reference).
	sort.Slice(entries, func(i, j int) bool {
		ai := mtimeOf(entries[i].Path)
		aj := mtimeOf(entries[j].Path)
		return ai.Before(aj)
	})

	fmt.Fprintf(w, "%-6s  %-10s  %s\n", "PID", "AGE", "URL")
	fmt.Fprintln(w, strings.Repeat("─", 70))
	for _, e := range entries {
		age := "?"
		if t := mtimeOf(e.Path); !t.IsZero() {
			age = formatAge(time.Since(t))
		}
		fmt.Fprintf(w, "%-6d  %-10s  %s\n", e.PID, age, e.URL)
	}
}

func mtimeOf(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

// formatAge produces a compact human-readable duration: "0:42:18",
// "5d12h", etc. Stops mattering past a week.
func formatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	if d < 24*time.Hour {
		h := int(d.Hours())
		return fmt.Sprintf("%dh%02dm", h, int(d.Minutes())%60)
	}
	days := int(d.Hours() / 24)
	return fmt.Sprintf("%dd%02dh", days, int(d.Hours())%24)
}
