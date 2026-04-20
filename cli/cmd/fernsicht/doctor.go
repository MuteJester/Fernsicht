package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/MuteJester/fernsicht/cli/internal/doctor"
	"github.com/MuteJester/fernsicht/cli/internal/errcatalog"
	"github.com/MuteJester/fernsicht/cli/internal/termui"
)

// doctorCommand handles `fernsicht doctor [--explain Exxx]
// [--server-url URL]`.
//
// Without --explain: runs the diagnostic suite and prints a per-
// check colored line. Exits 0 on PASS/WARN, 1 on FAIL.
//
// With --explain Exxx: looks up the error catalog entry and prints
// the four-line block (summary / cause / hint / docs).
func doctorCommand(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	explain := fs.String("explain", "", "Look up an error code (e.g., E001) instead of running checks.")
	serverURL := fs.String("server-url", "", "Override the signaling-server URL probed by network checks.")
	noColor := fs.Bool("no-color", false, "Don't colorize PASS/FAIL output.")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		return 2
	}

	if *explain != "" {
		entry, ok := errcatalog.Lookup(*explain)
		if !ok {
			fmt.Fprintf(os.Stderr,
				"[fernsicht] error: no error code %q in catalog\n", *explain)
			fmt.Fprintln(os.Stderr,
				"  hint: run `fernsicht doctor --explain LIST` for the full catalog")
			return 1
		}
		fmt.Println(errcatalog.Format(entry))
		return 0
	}

	resolvedURL := *serverURL
	if resolvedURL == "" {
		resolvedURL = os.Getenv("FERNSICHT_SERVER_URL")
	}

	cfg := doctor.Config{ServerURL: resolvedURL}
	checks := doctor.DefaultChecks(cfg)

	useColor := !*noColor && termui.TerminalLikelySupportsOSC8()
	r := &doctor.Runner{Out: os.Stdout, NoColor: !useColor}

	fmt.Println("Fernsicht doctor — running diagnostics...")
	fmt.Println()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	worst := r.Run(ctx, checks)

	fmt.Println()
	switch worst {
	case doctor.StatusPass:
		fmt.Println("All checks passed. fernsicht is ready to wrap.")
		return 0
	case doctor.StatusWarn:
		fmt.Println("All required checks passed; some warnings — see above.")
		return 0
	default:
		fmt.Println("One or more checks failed. Address the FAIL items + hints above.")
		fmt.Println("For a specific error code's docs: fernsicht doctor --explain Exxx")
		return 1
	}
}
