// Package wrap spawns a wrapped command and pumps its stdio
// transparently through the parent process. This is the minimum
// surface needed for `fernsicht run -- <command>` to behave like the
// command alone: forwarded input/output, mirrored exit code, signals
// delivered to the wrapped command's process group.
//
// Phase 1: no progress detection, no bridge integration. Phase 2 hooks
// the parser into the stdout/stderr stream; Phase 3 wires the bridge.
package wrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"runtime/debug"
	"sync"
	"syscall"
	"time"

	"github.com/MuteJester/fernsicht/cli/internal/parse"
	"golang.org/x/term"
)

// Options configures a single wrap.Run call. Fields default to
// "match the parent process" — in particular, Stdin/Stdout/Stderr
// default to os.Stdin/Stdout/Stderr if left nil.
type Options struct {
	// Command is the executable to run (looked up via PATH).
	Command string

	// Args are passed to the executable (excluding argv[0]).
	Args []string

	// Env, if non-nil, is the wrapped command's environment.
	// nil means inherit os.Environ().
	Env []string

	// NoPty forces pipe mode even when stdout is a tty.
	NoPty bool

	// NoUnbuffer disables setting PYTHONUNBUFFERED=1 etc. on the
	// wrapped command's environment. Default is to set them.
	NoUnbuffer bool

	// Debug enables verbose internal logging to ErrLog.
	Debug bool

	// Phase-2 parser flags.
	NoDetect    bool // disable Tier-1 auto-detection
	NoMagic     bool // disable __fernsicht__ prefix interception
	StrictMagic bool // exit 250 on invalid magic-prefix lines

	// OnTick is called for every parsed Tick (Tier-1 or magic).
	// Phase 3 wires this to bridge.Tick. nil → fall back to printing
	// `[parse]` lines via ErrLog.
	OnTick func(parse.Tick)

	// OnLifecycle is called for magic start/end/label/url events.
	// Phase 3 wires this to bridge lifecycle calls.
	OnLifecycle func(parse.MagicLine)

	// CustomParsers (Phase 4) are user-supplied Tier-3 patterns from
	// --pattern flags + .fernsicht.toml. Appended to the registry
	// after the built-in Tier-1 parsers.
	CustomParsers []parse.Parser

	// Stdin/Stdout/Stderr default to the process's own stdio when nil.
	// Tests inject fakes here.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// ErrLog is where internal warnings / debug lines go. nil → stderr.
	ErrLog io.Writer

	// SignalSource, if non-nil, is the signal channel the wrap
	// listens to. Defaults to os/signal.Notify on the standard set.
	// Tests inject a synthetic channel here.
	SignalSource <-chan os.Signal

	// OnSIGUSR1 is fired on each SIGUSR1 the wrap layer receives.
	// Phase 4: the run subcommand uses this to re-print the viewer
	// URL banner on demand. Nil ≡ ignore the signal.
	OnSIGUSR1 func()

	// SecondInterruptWindow / TermDeadline override the defaults from
	// §9.2 / §9.3. Zero ≡ use defaults. Tests use these to make the
	// signal-escalation paths fast.
	SecondInterruptWindow time.Duration
	TermDeadline          time.Duration
}

// Result captures everything a caller might need after Run returns.
type Result struct {
	// ExitCode is what the CLI should exit with: the wrapped command's
	// exit status, or 128+N if killed by signal N. 0 if Run failed
	// before the command started.
	ExitCode int

	// Killed is true if we sent SIGKILL because the command didn't
	// exit gracefully within the SIGTERM deadline.
	Killed bool

	// Duration is the wall-clock time the wrapped command ran.
	Duration time.Duration

	// strictMagicErr is set by the pump when --strict-magic is on and
	// an invalid magic line was seen. Causes ExitCode override to 250.
	strictMagicErr error
}

// Errors returned by Run. Wrap-level failures (ErrSpawnFailed) bubble
// up with a non-zero ExitCode of 254 so callers can distinguish them
// from the wrapped command's own exit codes.
var (
	ErrSpawnFailed = errors.New("wrap: failed to spawn wrapped command")
	ErrPanic       = errors.New("wrap: recovered from panic")
	ErrStrictMagic = errors.New("wrap: invalid magic-prefix line (--strict-magic)")
)

// Tunable defaults per CLI plan §9.2 / §9.3. Production code uses
// these unless the caller (typically a test) overrides via Options.
const (
	DefaultSecondInterruptWindow = 2 * time.Second
	DefaultTermDeadline          = 10 * time.Second
)

// buildCmd constructs a fresh *exec.Cmd from opts. Used both for the
// initial spawn in Run() and for the pty→pipe fallback path inside
// runPty (we cannot reuse a cmd object whose Process is non-nil, even
// if that process is already dead — Go's exec.Cmd refuses Start()
// twice with "exec: already started").
func buildCmd(opts Options) *exec.Cmd {
	cmd := exec.Command(opts.Command, opts.Args...)
	cmd.Env = opts.Env
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}
	if !opts.NoUnbuffer {
		cmd.Env = applyUnbufferEnv(cmd.Env)
	}
	configureProcessGroup(cmd)
	return cmd
}

// Run spawns and supervises the wrapped command per opts. Returns a
// Result describing how the wrapping ended; the returned error is
// non-nil only for wrap-level failures (couldn't spawn, panic
// recovered). The wrapped command's own exit code is on Result —
// even an exit-1 wrapped command produces err == nil.
func Run(ctx context.Context, opts Options) (result Result, err error) {
	// Top-level panic recovery — one of the Phase 1 acceptance items.
	// If anything in the pump loop panics, we want to:
	//   1. Log the panic with stack to stderr.
	//   2. Best-effort kill the wrapped process group so the panic
	//      doesn't orphan whatever the user asked us to wrap.
	//   3. Return a synthetic Result that surfaces the failure.
	//   4. Re-raise so the OS sees a crash exit, not a clean one.
	var cmd *exec.Cmd
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(errLog(opts), "[fernsicht] FATAL panic: %v\n%s",
				r, debug.Stack())
			if cmd != nil && cmd.Process != nil {
				_ = killGroup(cmd)
			}
			err = fmt.Errorf("%w: %v", ErrPanic, r)
			result.ExitCode = 255
			panic(r)
		}
	}()

	// Defaults.
	if opts.Stdin == nil {
		opts.Stdin = os.Stdin
	}
	if opts.Stdout == nil {
		opts.Stdout = os.Stdout
	}
	if opts.Stderr == nil {
		opts.Stderr = os.Stderr
	}

	cmd = buildCmd(opts)

	// Decide pty vs pipe. We only allocate a pty when:
	//   - Caller didn't opt out via --no-pty
	//   - The caller's stdout is itself a tty (otherwise pty's CR/LF
	//     translation corrupts bytes piped to a file or other tool)
	usePty := !opts.NoPty && stdoutIsTerminal(opts.Stdout)

	start := time.Now()
	if usePty {
		err = runPty(ctx, &cmd, opts, &result)
	} else {
		err = runPipe(ctx, cmd, opts, &result)
	}
	result.Duration = time.Since(start)

	if err != nil {
		return result, err
	}
	result.ExitCode = exitCodeFromCmd(cmd)
	if result.ExitCode == int(syscall.SIGKILL)+128 {
		result.Killed = true
	}
	// Strict-magic: even if the wrapped command exited 0, surface the
	// magic-prefix violation. The wrap-level exit code overrides per
	// CLI plan §4.4 (250).
	if opts.StrictMagic && result.strictMagicErr != nil {
		result.ExitCode = 250
		return result, fmt.Errorf("%w: %v", ErrStrictMagic, result.strictMagicErr)
	}
	return result, nil
}

// runPipe handles the pipe-mode case: stdout/stderr remain separated;
// each gets its own goroutine that pumps bytes through the parser
// (Phase 2) and forwards survivors to the caller's stream.
func runPipe(ctx context.Context, cmd *exec.Cmd, opts Options, result *Result) error {
	cmd.Stdin = opts.Stdin

	// Build OS pipes ourselves so we can interpose the Pump between
	// the wrapped command's writer and the caller's reader.
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		return fmt.Errorf("%w: stdout pipe: %v", ErrSpawnFailed, err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		stdoutR.Close()
		stdoutW.Close()
		return fmt.Errorf("%w: stderr pipe: %v", ErrSpawnFailed, err)
	}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	if err := cmd.Start(); err != nil {
		stdoutR.Close()
		stdoutW.Close()
		stderrR.Close()
		stderrW.Close()
		return fmt.Errorf("%w: %v", ErrSpawnFailed, err)
	}

	// Close the write ends in the parent so the reader pumps EOF
	// after the child exits.
	stdoutW.Close()
	stderrW.Close()

	stopSig := installSignalHandler(ctx, cmd, opts)
	defer stopSig()

	// Shared parser state across both stream pumps.
	registry, confidence, tui := buildParserState(opts)
	killCmd := func() { _ = signalGroup(cmd, syscall.SIGTERM) }
	pumpOut := newPump(opts, opts.Stdout, registry, confidence, tui, killCmd)
	pumpErr := newPump(opts, opts.Stderr, registry, confidence, tui, killCmd)

	var pumpWg sync.WaitGroup
	pumpWg.Add(2)
	go func() {
		defer pumpWg.Done()
		_, _ = io.Copy(pumpOut, stdoutR)
		pumpOut.Flush()
	}()
	go func() {
		defer pumpWg.Done()
		_, _ = io.Copy(pumpErr, stderrR)
		pumpErr.Flush()
	}()

	waitErr := waitFor(cmd)
	pumpWg.Wait()

	// Strict-magic: the pump records the first violation. Surface it
	// so Run can convert to exit 250.
	if e := pumpOut.FatalErr(); e != nil {
		result.strictMagicErr = e
	} else if e := pumpErr.FatalErr(); e != nil {
		result.strictMagicErr = e
	}
	return waitErr
}

// runPty allocates a pty, attaches the wrapped command to its slave
// end, and pumps bytes between the master end and our parent stdio.
// The wrapped command sees a real terminal — important for tools that
// disable color / progress when isatty(stdout) is false.
//
// In pty mode stdout AND stderr from the wrapped command share the
// pty stream — we can't separate them. Most tools that print progress
// write it to one of them, and users who need separation can pass
// --no-pty.
func runPty(ctx context.Context, cmdPtr **exec.Cmd, opts Options, result *Result) error {
	cmd := *cmdPtr
	master, err := startPty(cmd)
	if err != nil {
		// Auto-fall-back to pipe mode rather than fail. This handles
		// CI envs and sandboxed environments (seccomp / AppArmor /
		// containers without /dev/ptmx access) where pty allocation
		// refuses.
		//
		// We MUST build a fresh *exec.Cmd here. pty.Start may have
		// already called cmd.Start() under the hood (the EPERM can
		// arrive after fork succeeds but before exec settles), in
		// which case cmd.Process != nil and a second Start() fails
		// with "exec: already started". Rebuild from opts and swap
		// the caller's cmd reference so the panic handler + exit
		// code reader in Run() see the live cmd, not the dead one.
		fmt.Fprintf(errLog(opts),
			"[fernsicht] warn: pty allocation failed (%v); falling back to pipe mode.\n"+
				"            Pass --no-pty to suppress this warning if your environment never grants ptys.\n",
			err)
		fresh := buildCmd(opts)
		*cmdPtr = fresh
		return runPipe(ctx, fresh, opts, result)
	}
	defer master.Close()

	stopSig := installSignalHandler(ctx, cmd, opts)
	defer stopSig()

	registry, confidence, tui := buildParserState(opts)
	killCmd := func() { _ = signalGroup(cmd, syscall.SIGTERM) }
	pump := newPump(opts, opts.Stdout, registry, confidence, tui, killCmd)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(pump, master)
		pump.Flush()
	}()

	// Caller stdin → master. We don't WaitGroup this one — if stdin
	// is a tty we want the goroutine to outlive the cmd; the deferred
	// master.Close() above unblocks it.
	go func() {
		_, _ = io.Copy(master, opts.Stdin)
	}()

	if err := waitFor(cmd); err != nil {
		return err
	}

	// Closing the master ensures the pump goroutine returns.
	_ = master.Close()
	wg.Wait()

	if e := pump.FatalErr(); e != nil {
		result.strictMagicErr = e
	}
	return nil
}

// buildParserState constructs the per-session shared parser state:
// registry (Tier-1 + custom), confidence (locking), TUI (alt-screen).
//
// Disabled detection (`--no-detect`) returns an empty registry — the
// magic prefix still flows through.
func buildParserState(opts Options) (*parse.Registry, *parse.Confidence, *parse.TUI) {
	r := parse.NewRegistry()
	if opts.NoDetect {
		r.Disable()
	}
	for _, p := range opts.CustomParsers {
		r.AddCustom(p)
	}
	c := parse.NewConfidence(parse.ConfidenceConfig{})
	t := &parse.TUI{}
	return r, c, t
}

// newPump assembles a Pump with options-derived policy. Each stream
// gets its own Pump but they share registry/confidence/tui.
//
// killCmd is the strict-magic escalation hook: wrap.Run wires it to
// signalGroup(SIGTERM) so a violation kills the wrapped command
// immediately instead of waiting for it to exit naturally.
func newPump(opts Options, forward io.Writer,
	r *parse.Registry, c *parse.Confidence, t *parse.TUI,
	killCmd func()) *Pump {
	return &Pump{
		Forward:           forward,
		Registry:          r,
		Confidence:        c,
		TUI:               t,
		EventLog:          errLog(opts),
		OnTick:            opts.OnTick,
		OnLifecycle:       opts.OnLifecycle,
		MagicEnabled:      !opts.NoMagic,
		StrictMagic:       opts.StrictMagic,
		Debug:             opts.Debug,
		OnStrictViolation: killCmd,
	}
}

// installSignalHandler subscribes to the standard interrupt signals
// and forwards them to the wrapped command's process group. Returns
// a stop function the caller MUST defer.
//
// Behavior per CLI plan §9.2:
//   - First SIGINT  → forward SIGINT to group; arm a 2s window.
//   - Second SIGINT within 2s → SIGKILL the group.
//   - SIGTERM       → forward, then SIGKILL after TermDeadline.
//   - SIGQUIT/SIGHUP → forward as-is.
//
// Test seam: Options.SignalSource overrides the os/signal subscription.
func installSignalHandler(ctx context.Context, cmd *exec.Cmd, opts Options) func() {
	sigCh := opts.SignalSource
	var stopFn func()
	if sigCh == nil {
		ch := make(chan os.Signal, 4)
		signal.Notify(ch, defaultSignalSet()...)
		sigCh = ch
		stopFn = func() { signal.Stop(ch) }
	} else {
		stopFn = func() {}
	}

	done := make(chan struct{})
	go pumpSignals(ctx, cmd, sigCh, done, opts)

	return func() {
		close(done)
		stopFn()
	}
}

func pumpSignals(ctx context.Context, cmd *exec.Cmd,
	sigCh <-chan os.Signal, done <-chan struct{}, opts Options) {
	secondInterruptWindow := opts.SecondInterruptWindow
	if secondInterruptWindow == 0 {
		secondInterruptWindow = DefaultSecondInterruptWindow
	}
	termDeadline := opts.TermDeadline
	if termDeadline == 0 {
		termDeadline = DefaultTermDeadline
	}

	var lastInterrupt time.Time

	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case sig, ok := <-sigCh:
			if !ok {
				return
			}
			ssig, isSyscallSig := sig.(syscall.Signal)
			if !isSyscallSig {
				continue
			}
			if isReprintSignal(ssig) {
				// Caller-defined hook; doesn't propagate to the
				// wrapped command. Used by the run subcommand to
				// re-print the viewer URL on demand.
				if opts.OnSIGUSR1 != nil {
					opts.OnSIGUSR1()
				}
				continue
			}
			switch ssig {
			case syscall.SIGINT:
				if time.Since(lastInterrupt) < secondInterruptWindow {
					if opts.Debug {
						fmt.Fprintln(errLog(opts),
							"[fernsicht] debug: second SIGINT, escalating to SIGKILL")
					}
					_ = killGroup(cmd)
					return
				}
				lastInterrupt = time.Now()
				_ = signalGroup(cmd, syscall.SIGINT)
			case syscall.SIGTERM:
				_ = signalGroup(cmd, syscall.SIGTERM)
				// Escalate after termDeadline if still alive.
				go func() {
					t := time.NewTimer(termDeadline)
					defer t.Stop()
					select {
					case <-t.C:
						_ = killGroup(cmd)
					case <-done:
					}
				}()
			default:
				_ = signalGroup(cmd, ssig)
			}
		}
	}
}

// waitFor calls cmd.Wait and reduces "command exited non-zero" from a
// Go-level error into a result we surface via Cmd.ProcessState. We
// only return an error when the WAIT itself failed (not when the
// command exited non-zero).
func waitFor(cmd *exec.Cmd) error {
	err := cmd.Wait()
	if err == nil {
		return nil
	}
	// *exec.ExitError is the "command exited non-zero" case. That's
	// not a wrap-level error; the exit code is mirrored separately.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return nil
	}
	return err
}

// exitCodeFromCmd extracts the right exit code from cmd.ProcessState:
//   - Normal exit: ExitCode()
//   - Killed by signal: 128 + signal number (POSIX convention)
//   - Not yet exited: 0 (shouldn't happen if Wait returned)
func exitCodeFromCmd(cmd *exec.Cmd) int {
	if cmd == nil || cmd.ProcessState == nil {
		return 0
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return 128 + int(ws.Signal())
		}
	}
	return cmd.ProcessState.ExitCode()
}

// stdoutIsTerminal reports whether w is (or wraps) a tty. Used to
// decide whether to allocate a pty: if the caller's stdout is a pipe
// or file, allocating a pty would just corrupt the byte stream with
// CR/LF translation.
func stdoutIsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// errLog returns the writer to send our own diagnostic output to.
// Defaults to os.Stderr when opts.ErrLog is nil.
func errLog(opts Options) io.Writer {
	if opts.ErrLog != nil {
		return opts.ErrLog
	}
	return os.Stderr
}
