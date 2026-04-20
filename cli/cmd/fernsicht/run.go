package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MuteJester/fernsicht/bridge/pkg/embed"
	"github.com/MuteJester/fernsicht/cli/internal/cfg"
	"github.com/MuteJester/fernsicht/cli/internal/clipboard"
	"github.com/MuteJester/fernsicht/cli/internal/output"
	"github.com/MuteJester/fernsicht/cli/internal/parse"
	"github.com/MuteJester/fernsicht/cli/internal/termui"
	"github.com/MuteJester/fernsicht/cli/internal/urlfile"
	"github.com/MuteJester/fernsicht/cli/internal/webhook"
	"github.com/MuteJester/fernsicht/cli/internal/wrap"
	"golang.org/x/term"
)

// runCommand handles `fernsicht run [flags] -- <command> [args...]`.
//
// Phase 4 expands Phase 3 with: .fernsicht.toml config, custom regex
// patterns, JSON-lines output mode, webhook POST on exit, clipboard
// integration, SIGUSR1 re-print, and --strict bridge-failure mode.
func runCommand(args []string) int {
	flagArgs, cmdArgs, ok := splitAtSeparator(args)
	if !ok {
		printRunMissingSeparator(args, os.Stderr)
		return 2
	}
	if len(cmdArgs) == 0 {
		fmt.Fprintln(os.Stderr,
			"[fernsicht] error: no command after `--`. Usage: fernsicht run [flags] -- <command> [args...]")
		return 2
	}

	flags, ferr := parseRunFlags(flagArgs)
	if ferr != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", ferr)
		fmt.Fprintln(os.Stderr,
			"Run `fernsicht run --help` for the supported flags.")
		return 2
	}

	// Resolve --output mode early so error paths can use the JSON
	// emitter consistently.
	outMode, err := output.ParseMode(flags.outputMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		return 2
	}

	// Load .fernsicht.toml (if any). Soft fail: missing config is
	// fine; only PARSE errors abort.
	configFile, _, err := cfg.SearchAndLoad(flags.config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: config load: %v\n", err)
		return 2
	}
	mergeConfigDefaults(&flags, configFile)

	// Compile custom patterns from --pattern + config. Surface invalid
	// regexes at startup so the user sees the typo immediately.
	customParsers, err := compileCustomPatterns(flags.patterns, configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		return 2
	}

	// Resolve config: flag > env > .fernsicht.toml > default.
	serverURL := flags.serverURL
	if serverURL == "" {
		serverURL = os.Getenv("FERNSICHT_SERVER_URL")
	}
	if serverURL == "" {
		serverURL = "https://signal.fernsicht.space"
	}
	joinSecret := flags.joinSecret
	if joinSecret == "" {
		joinSecret = os.Getenv("FERNSICHT_JOIN_SECRET")
	}

	stderrIsTTY := term.IsTerminal(int(os.Stderr.Fd()))
	jsonEmitter := output.NewEmitter(nil)
	if outMode == output.ModeJSON {
		jsonEmitter = output.NewEmitter(os.Stdout)
	}

	// --- Open session ---
	openCtx, openCancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer openCancel()

	startedAt := time.Now()
	sess, err := embed.Open(openCtx, embed.Config{
		ServerURL:  serverURL,
		JoinSecret: joinSecret,
		MaxViewers: flags.maxViewers,
		Label:      flags.label,
		SDKID:      "cli",
		SDKVersion: version,
	})
	if err != nil {
		if flags.noFailOnBridge {
			fmt.Fprintf(os.Stderr,
				"[fernsicht] warn: bridge open failed (%v); running wrapped command without monitoring.\n", err)
			return runWithoutBridge(flags, cmdArgs, customParsers)
		}
		fmt.Fprintf(os.Stderr, "[fernsicht] error: bridge open failed: %v\n", err)
		return 255
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = sess.Close(ctx)
	}()

	// --- URL banner / QR / first-run tip / URL file / clipboard ---
	urlPath := flags.urlFile
	if urlPath == "" {
		urlPath = urlfile.Default(os.Getpid())
	}
	if err := urlfile.Write(urlPath, sess.ViewerURL()); err != nil {
		if !flags.quiet {
			fmt.Fprintf(os.Stderr,
				"[fernsicht] warn: could not write URL file: %v\n", err)
		}
	}
	defer func() { _ = urlfile.Remove(urlPath) }()

	if flags.copyURL {
		copyURLToClipboard(sess.ViewerURL(), flags.quiet)
	}

	printSessionBanner(flags, sess.ViewerURL(), stderrIsTTY)
	jsonEmitter.SessionOpen(sess.RoomID(), sess.ViewerURL())

	// --- Bridge failure detection (for --strict) ---
	var bridgeFailedFlag atomic.Int32
	sess.SetEventHook(func(name string, raw json.RawMessage) {
		if name == "closed" && flags.strict {
			var p struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(raw, &p)
			if p.Reason == "fatal_error" || p.Reason == "stdin_eof" {
				if bridgeFailedFlag.CompareAndSwap(0, 1) {
					fmt.Fprintf(os.Stderr,
						"[fernsicht] warn: bridge died mid-run (--strict on); terminating wrapped command.\n")
					// Self-signal SIGTERM → wrap forwards to wrapped
					// cmd's process group → wrap.Run returns. Uses
					// os.Process.Signal for cross-platform support.
					if proc, err := os.FindProcess(os.Getpid()); err == nil {
						_ = proc.Signal(os.Interrupt)
					}
				}
			}
		}
	})

	// --- Track viewer-count peak for webhook payload ---
	var viewerMaxMu sync.Mutex
	viewerMax := 0
	updateViewerMax := func() {
		c := sess.ViewerCount()
		viewerMaxMu.Lock()
		if c > viewerMax {
			viewerMax = c
		}
		viewerMaxMu.Unlock()
	}

	// --- Wire wrap callbacks to embed ---
	router := newTickRouter(sess, defaultLabel(flags.label, cmdArgs), flags.unit, jsonEmitter)

	wrapOpts := wrap.Options{
		Command:     cmdArgs[0],
		Args:        cmdArgs[1:],
		NoPty:       flags.noPty,
		NoUnbuffer:  flags.noUnbuffer,
		Debug:       flags.debug,
		NoDetect:    flags.noDetect,
		NoMagic:     flags.noMagic,
		StrictMagic: flags.strictMagic,
		OnTick:      router.handleTick,
		OnLifecycle: router.handleLifecycle,
		OnSIGUSR1: func() {
			if !flags.quiet && !flags.share {
				fmt.Fprintln(os.Stderr) // visual separator
				printSessionBanner(flags, sess.ViewerURL(), stderrIsTTY)
			}
			updateViewerMax()
		},
	}

	// Plumb custom parsers in via the registry. wrap.Run reads them
	// from a per-call construction in buildParserState — which we
	// can't easily extend without a wrap-layer change. Workaround:
	// have the router add them BEFORE the wrap goroutine starts
	// using the registry. Cleanest path: extend Options.
	wrapOpts.CustomParsers = customParsers

	result, err := wrap.Run(context.Background(), wrapOpts)

	// --- Exit + cleanup ---
	durationSec := time.Since(startedAt).Seconds()
	jsonEmitter.SessionClose(result.ExitCode, durationSec, viewerMax)

	// Webhook POST (best-effort; doesn't block exit code).
	if flags.webhook != "" {
		fireWebhook(flags.webhook, sess, cmdArgs, result, startedAt, viewerMax)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		if result.ExitCode == 0 {
			return 254
		}
		return result.ExitCode
	}

	// --strict: bridge failure overrides exit code (regardless of
	// what the wrapped command returned).
	if bridgeFailedFlag.Load() == 1 {
		return 200
	}
	return result.ExitCode
}

// runFlags is the parsed flag bundle for `run`.
type runFlags struct {
	// Phase 1
	noPty       bool
	noUnbuffer  bool
	debug       bool

	// Phase 2
	noDetect    bool
	noMagic     bool
	strictMagic bool

	// Phase 3
	serverURL      string
	joinSecret     string
	maxViewers     int
	label          string
	share          bool
	quiet          bool
	qrOn           bool
	qrOff          bool
	noFailOnBridge bool

	// Phase 4
	unit       string
	patterns   stringSlice // repeated --pattern
	copyURL    bool
	strict     bool
	outputMode string
	config     string
	urlFile    string
	webhook    string
}

// stringSlice satisfies flag.Value so --pattern can repeat.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, "; ") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func parseRunFlags(args []string) (runFlags, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	var f runFlags

	// Phase 1.
	fs.BoolVar(&f.noPty, "no-pty", false, "Don't allocate a pty.")
	fs.BoolVar(&f.noUnbuffer, "no-unbuffer", false, "Don't set PYTHONUNBUFFERED=1 etc.")
	fs.BoolVar(&f.debug, "debug", false, "Verbose internal logging.")

	// Phase 2.
	fs.BoolVar(&f.noDetect, "no-detect", false, "Disable Tier-1 auto-detection.")
	fs.BoolVar(&f.noMagic, "no-magic", false, "Don't intercept __fernsicht__ prefix lines.")
	fs.BoolVar(&f.strictMagic, "strict-magic", false, "Exit 250 on invalid magic-prefix lines.")

	// Phase 3.
	fs.StringVar(&f.serverURL, "server-url", "", "Signaling server URL.")
	fs.StringVar(&f.joinSecret, "join-secret", "", "Server auth (or $FERNSICHT_JOIN_SECRET).")
	fs.IntVar(&f.maxViewers, "max-viewers", 8, "Cap on concurrent viewers.")
	fs.StringVar(&f.label, "label", "", "Task label shown to viewers.")
	fs.BoolVar(&f.share, "share", false, "Print only the URL on stdout; suppress chrome.")
	fs.BoolVar(&f.quiet, "quiet", false, "Suppress fernsicht's own output (still prints URL).")
	fs.BoolVar(&f.qrOn, "qr", false, "Force QR code rendering.")
	fs.BoolVar(&f.qrOff, "no-qr", false, "Suppress QR code rendering.")
	fs.BoolVar(&f.noFailOnBridge, "no-fail-on-bridge", false, "Run wrapped command even if bridge open fails.")

	// Phase 4.
	fs.StringVar(&f.unit, "unit", "", "Progress unit shown to viewers (default: it).")
	fs.Var(&f.patterns, "pattern", "Custom regex pattern (repeatable). See `fernsicht magic` for syntax.")
	fs.BoolVar(&f.copyURL, "copy-url", false, "Copy viewer URL to system clipboard.")
	fs.BoolVar(&f.strict, "strict", false, "Bridge failure mid-run → kill wrapped command, exit 200.")
	fs.StringVar(&f.outputMode, "output", "text", "Output mode: text|json.")
	fs.StringVar(&f.config, "config", "", "Explicit .fernsicht.toml path.")
	fs.StringVar(&f.urlFile, "url-file", "", "Override URL file path (default: $XDG_RUNTIME_DIR/fernsicht/<pid>.url).")
	fs.StringVar(&f.webhook, "webhook", "", "POST JSON to URL on session end.")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	return f, nil
}

// mergeConfigDefaults overlays config-file defaults onto unset flag
// values. Flag > env > config > default.
func mergeConfigDefaults(f *runFlags, c *cfg.File) {
	if c == nil {
		return
	}
	if f.unit == "" {
		f.unit = c.Run.DefaultUnit
	}
	if !f.strict {
		f.strict = c.Run.Strict
	}
	switch c.Run.QR {
	case "always":
		f.qrOn = true
	case "never":
		f.qrOff = true
	}
}

// compileCustomPatterns folds --pattern flag values + config-file
// patterns into a slice of compiled parse.Parser. Errors out at
// startup so users see typos immediately.
func compileCustomPatterns(flagPatterns []string, configFile *cfg.File) ([]parse.Parser, error) {
	var out []parse.Parser

	// --pattern (anonymous regex from CLI). Auto-name them
	// "flag-1", "flag-2" so users can identify them in --debug logs.
	for i, regex := range flagPatterns {
		cp := parse.CustomPattern{
			Name:         fmt.Sprintf("flag-%d", i+1),
			Regex:        regex,
			ValueCapture: 1, // assume the FIRST capture is the value (most common)
		}
		p, err := cp.Compile()
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}

	if configFile != nil {
		for _, e := range configFile.Detection.Patterns {
			cp := parse.CustomPattern{
				Name:         e.Name,
				Regex:        e.Regex,
				ValueCapture: e.ValueCapture,
				NCapture:     e.NCapture,
				TotalCapture: e.TotalCapture,
			}
			p, err := cp.Compile()
			if err != nil {
				return nil, err
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// defaultLabel computes the task label when the user didn't pass one.
func defaultLabel(explicit string, cmdArgs []string) string {
	if explicit != "" {
		return explicit
	}
	full := strings.Join(cmdArgs, " ")
	if len(full) > 60 {
		return full[:57] + "..."
	}
	return full
}

// printSessionBanner emits the URL banner, QR, and (on first run)
// the onboarding tips. --share / --quiet suppress the chrome.
func printSessionBanner(f runFlags, url string, stderrTTY bool) {
	if f.share {
		fmt.Println(url)
		return
	}
	bannerW := os.Stderr
	if !f.quiet {
		termui.PrintViewerURLBanner(bannerW, url)
	}
	if !f.quiet && termui.QREnabled(f.qrOn, f.qrOff, stderrTTY) {
		_ = termui.QR(bannerW, url)
	}
	if !f.quiet && cfg.IsFirstRun() {
		fmt.Fprintln(bannerW)
		termui.PrintFirstRunTips(bannerW)
		_ = cfg.MarkFirstRunDone()
	}
}

// copyURLToClipboard runs the platform clipboard tool. Failures are
// non-fatal — print a warn line if not --quiet.
func copyURLToClipboard(url string, quiet bool) {
	if err := clipboard.Copy(url); err != nil {
		if !quiet {
			fmt.Fprintf(os.Stderr,
				"[fernsicht] warn: --copy-url: %v\n", err)
		}
		return
	}
	if !quiet {
		fmt.Fprintln(os.Stderr,
			"[fernsicht] viewer URL copied to clipboard.")
	}
}

// fireWebhook POSTs the session-end payload. Best-effort; failures
// log a warn line but don't change the exit code.
func fireWebhook(url string, sess *embed.Session, cmdArgs []string,
	result wrap.Result, startedAt time.Time, viewerMax int) {
	ctx, cancel := context.WithTimeout(context.Background(), webhook.DefaultTimeout)
	defer cancel()
	endedAt := time.Now()
	payload := webhook.Payload{
		Event: "session_end",
		Session: webhook.SessionInfo{
			RoomID:      sess.RoomID(),
			ViewerURL:   sess.ViewerURL(),
			StartedAt:   startedAt,
			EndedAt:     endedAt,
			DurationSec: endedAt.Sub(startedAt).Seconds(),
			MaxViewers:  viewerMax,
		},
		Wrapped: webhook.WrappedInfo{
			Command:  strings.Join(cmdArgs, " "),
			ExitCode: result.ExitCode,
		},
	}
	if err := webhook.New().Send(ctx, url, payload); err != nil {
		fmt.Fprintf(os.Stderr,
			"[fernsicht] warn: webhook POST failed: %v\n", err)
	}
}

// runWithoutBridge handles --no-fail-on-bridge: wrap normally, no
// session, no monitoring. Custom patterns are still parsed (they may
// be useful for debug logging) but ticks go nowhere.
func runWithoutBridge(f runFlags, cmdArgs []string, customParsers []parse.Parser) int {
	wrapOpts := wrap.Options{
		Command:       cmdArgs[0],
		Args:          cmdArgs[1:],
		NoPty:         f.noPty,
		NoUnbuffer:    f.noUnbuffer,
		Debug:         f.debug,
		NoDetect:      true,
		NoMagic:       f.noMagic,
		StrictMagic:   f.strictMagic,
		CustomParsers: customParsers,
	}
	result, err := wrap.Run(context.Background(), wrapOpts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[fernsicht] error: %v\n", err)
		if result.ExitCode == 0 {
			return 254
		}
	}
	return result.ExitCode
}

// --- tickRouter --------------------------------------------------------

type tickRouter struct {
	sess         *embed.Session
	defaultLabel string
	defaultUnit  string
	emitter      *output.Emitter

	mu           sync.Mutex
	activeTaskID string
}

func newTickRouter(sess *embed.Session, defaultLabel, defaultUnit string,
	emitter *output.Emitter) *tickRouter {
	if defaultUnit == "" {
		defaultUnit = "it"
	}
	return &tickRouter{
		sess:         sess,
		defaultLabel: defaultLabel,
		defaultUnit:  defaultUnit,
		emitter:      emitter,
	}
}

func (r *tickRouter) handleTick(t parse.Tick) {
	r.mu.Lock()
	defer r.mu.Unlock()

	taskID := t.TaskID
	if taskID == "" {
		if r.activeTaskID == "" {
			r.activeTaskID = "task-1"
			_ = r.sess.StartTask(r.activeTaskID, r.defaultLabel)
		}
		taskID = r.activeTaskID
	}
	unit := t.Unit
	if unit == "" {
		unit = r.defaultUnit
	}
	_ = r.sess.Tick(embed.Tick{
		TaskID: taskID,
		Value:  t.Value,
		N:      t.N,
		Total:  t.Total,
		Unit:   unit,
	})
	r.emitter.Tick(taskID, t.Value, t.N, t.Total)
}

func (r *tickRouter) handleLifecycle(mp parse.MagicLine) {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch mp.Event {
	case parse.MagicStart:
		taskID := mp.TaskID
		if taskID == "" {
			taskID = mp.Label
		}
		if taskID == "" {
			taskID = "task"
		}
		_ = r.sess.StartTask(taskID, mp.Label)
		r.activeTaskID = taskID

	case parse.MagicEnd:
		taskID := mp.TaskID
		if taskID == "" {
			taskID = r.activeTaskID
		}
		if taskID != "" {
			_ = r.sess.EndTask(taskID)
			if taskID == r.activeTaskID {
				r.activeTaskID = ""
			}
		}

	case parse.MagicLabel, parse.MagicURL:
		// MagicLabel: bridge has no per-task label-update wire frame.
		// MagicURL: handled by SIGUSR1 path now. Phase 5+ may add
		// programmatic re-print via this lifecycle event too.
	}
}

// --- separator parsing helpers ---------------------------------------

func splitAtSeparator(args []string) (before, after []string, found bool) {
	for i, a := range args {
		if a == "--" {
			return args[:i], args[i+1:], true
		}
	}
	return args, nil, false
}

func printRunMissingSeparator(args []string, w io.Writer) {
	if len(args) == 0 {
		fmt.Fprintln(w,
			"[fernsicht] error: missing wrapped command.")
		fmt.Fprintln(w,
			"Usage: fernsicht run [flags] -- <command> [args...]")
		return
	}
	if !strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(w,
			"[fernsicht] error: missing `--` separator before the wrapped command.")
		fmt.Fprintln(w,
			"            Did you mean: fernsicht run -- "+strings.Join(args, " "))
		fmt.Fprintln(w,
			"            (use `--` to separate fernsicht flags from the wrapped command)")
		return
	}
	fmt.Fprintln(w,
		"[fernsicht] error: missing `--` separator before the wrapped command.")
	fmt.Fprintln(w,
		"Usage: fernsicht run [flags] -- <command> [args...]")
}
