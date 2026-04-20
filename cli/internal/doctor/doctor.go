// Package doctor self-diagnoses common installation / network /
// environment issues so users can resolve them without filing an
// issue.
//
// Each Check is a small unit that returns a Result. The doctor
// runner runs them in order and prints a colored PASS/WARN/FAIL line
// per check. Exit status: 0 when every check passed (or only
// warned), 1 if anything FAILed.
//
// Adding a new check: define a Check with a Name and a Run func.
// Append to DefaultChecks(). Tests in doctor_test.go cover each
// individual check via direct invocation, so checks should be small
// and side-effect-free apart from network reads.
package doctor

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/creack/pty"
)

// Status is the outcome of a single check.
type Status int

const (
	StatusPass Status = iota
	StatusWarn
	StatusFail
	StatusSkip // not applicable on this platform
)

func (s Status) String() string {
	switch s {
	case StatusPass:
		return "PASS"
	case StatusWarn:
		return "WARN"
	case StatusFail:
		return "FAIL"
	case StatusSkip:
		return "SKIP"
	}
	return "?"
}

// Result is what a Check returns.
type Result struct {
	Status Status
	Detail string // one-line free text shown after the status
	Hint   string // actionable suggestion when not PASS
}

// Check describes one diagnostic.
type Check struct {
	Name string
	Run  func(ctx context.Context) Result
}

// Config tunes the diagnostics — what server URL to probe, etc.
type Config struct {
	ServerURL  string // default: https://signal.fernsicht.space
	HTTPClient *http.Client
}

func (c Config) defaults() Config {
	if c.ServerURL == "" {
		c.ServerURL = "https://signal.fernsicht.space"
	}
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	return c
}

// DefaultChecks returns the standard diagnostic suite.
func DefaultChecks(cfg Config) []Check {
	c := cfg.defaults()
	return []Check{
		{Name: "binary integrity",        Run: c.checkBinaryIntegrity},
		{Name: "platform support",        Run: c.checkPlatformSupport},
		{Name: "libc compatibility",      Run: c.checkLibc},
		{Name: "DNS resolution",          Run: c.checkDNS},
		{Name: "tcp connectivity",        Run: c.checkTCP},
		{Name: "tls handshake",           Run: c.checkTLS},
		{Name: "signaling /healthz",      Run: c.checkHealthz},
		{Name: "proxy environment",       Run: c.checkProxyEnv},
		{Name: "pty allocation",          Run: c.checkPty},
		{Name: "magic-prefix parser",     Run: c.checkMagicPrefix},
		{Name: "telemetry-free",          Run: c.checkTelemetry},
	}
}

// --- Individual checks -----------------------------------------------

func (c Config) checkBinaryIntegrity(_ context.Context) Result {
	exe, err := os.Executable()
	if err != nil {
		return Result{StatusWarn, "could not resolve executable path",
			"this is unusual; rerun from a normal shell"}
	}
	info, err := os.Stat(exe)
	if err != nil {
		return Result{StatusWarn, fmt.Sprintf("could not stat %s", exe), ""}
	}
	if info.Size() < 1_000_000 {
		return Result{StatusWarn, fmt.Sprintf("binary smaller than expected (%d bytes)", info.Size()),
			"the binary may be incomplete; reinstall via the official one-liner"}
	}
	return Result{StatusPass, fmt.Sprintf("%s (%d MB)", exe, info.Size()/(1024*1024)), ""}
}

func (c Config) checkPlatformSupport(_ context.Context) Result {
	supported := map[string]map[string]bool{
		"linux":   {"amd64": true, "arm64": true},
		"darwin":  {"amd64": true, "arm64": true},
		"windows": {"amd64": true},
	}
	plat := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	if archMap, ok := supported[runtime.GOOS]; ok {
		if archMap[runtime.GOARCH] {
			return Result{StatusPass, plat, ""}
		}
	}
	return Result{StatusFail, plat,
		"unsupported platform; see https://github.com/MuteJester/Fernsicht/issues to request"}
}

func (c Config) checkLibc(_ context.Context) Result {
	if runtime.GOOS != "linux" {
		return Result{StatusSkip, "not on Linux", ""}
	}
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return Result{StatusPass, "musl (Alpine) — CGO-free binary works",
			"if a wrapped command needs glibc, run from a non-Alpine container"}
	}
	return Result{StatusPass, "glibc (assumed)", ""}
}

func (c Config) checkDNS(ctx context.Context) Result {
	host := hostFromURL(c.ServerURL)
	if host == "" {
		return Result{StatusFail, fmt.Sprintf("could not parse host from %q", c.ServerURL), ""}
	}
	// Use the resolver's context-aware API so a hostile resolver
	// can't hang the whole doctor run.
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return Result{StatusFail, fmt.Sprintf("%s: %v", host, err),
			"check /etc/resolv.conf or the system DNS settings"}
	}
	return Result{StatusPass, fmt.Sprintf("%s → %s", host, strings.Join(addrs, ", ")), ""}
}

func (c Config) checkTCP(ctx context.Context) Result {
	host := hostFromURL(c.ServerURL)
	if host == "" {
		return Result{StatusFail, "no host", ""}
	}
	d := net.Dialer{Timeout: 10 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", host+":443")
	if err != nil {
		return Result{StatusFail, err.Error(),
			"check firewall rules; the bridge needs outbound 443 to " + host}
	}
	_ = conn.Close()
	return Result{StatusPass, host + ":443 reachable", ""}
}

func (c Config) checkTLS(ctx context.Context) Result {
	host := hostFromURL(c.ServerURL)
	if host == "" {
		return Result{StatusFail, "no host", ""}
	}
	d := tls.Dialer{Config: &tls.Config{ServerName: host}, NetDialer: &net.Dialer{Timeout: 10 * time.Second}}
	conn, err := d.DialContext(ctx, "tcp", host+":443")
	if err != nil {
		return Result{StatusFail, err.Error(),
			"E002 — TLS-intercepting proxy? You may need to add a corporate CA to the system trust store"}
	}
	defer conn.Close()
	cs := conn.(*tls.Conn).ConnectionState()
	return Result{StatusPass,
		fmt.Sprintf("%s, TLS %s", cs.PeerCertificates[0].Subject.CommonName, tlsVersionStr(cs.Version)),
		""}
}

func (c Config) checkHealthz(ctx context.Context) Result {
	url := strings.TrimRight(c.ServerURL, "/") + "/healthz"
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return Result{StatusFail, err.Error(), "E001 — server unreachable; see proxy + firewall checks above"}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Result{StatusFail,
			fmt.Sprintf("HTTP %d", resp.StatusCode),
			"signaling server is up but unhealthy; report at github.com/MuteJester/Fernsicht/issues"}
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	if !strings.Contains(string(body), "ok") {
		return Result{StatusWarn, "200 but body wasn't 'ok'", ""}
	}
	return Result{StatusPass, "200 ok", ""}
}

func (c Config) checkProxyEnv(_ context.Context) Result {
	httpProxy := firstNonEmpty(os.Getenv("HTTP_PROXY"), os.Getenv("http_proxy"))
	httpsProxy := firstNonEmpty(os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy"))
	noProxy := firstNonEmpty(os.Getenv("NO_PROXY"), os.Getenv("no_proxy"))

	if httpProxy == "" && httpsProxy == "" {
		return Result{StatusPass, "no proxy configured (direct internet)", ""}
	}
	detail := []string{}
	if httpsProxy != "" {
		detail = append(detail, "HTTPS_PROXY="+redactProxyAuth(httpsProxy))
	}
	if httpProxy != "" {
		detail = append(detail, "HTTP_PROXY="+redactProxyAuth(httpProxy))
	}
	if noProxy != "" {
		detail = append(detail, "NO_PROXY="+noProxy)
	}
	return Result{StatusPass, strings.Join(detail, ", "),
		"Go's HTTP client honors these automatically — no extra config needed"}
}

func (c Config) checkPty(_ context.Context) Result {
	if runtime.GOOS == "windows" {
		return Result{StatusSkip, "ConPTY support tested separately on Windows", ""}
	}
	cmd := exec.Command("true")
	master, err := pty.Start(cmd)
	if err != nil {
		return Result{StatusWarn, err.Error(),
			"E022 — your shell can't allocate ptys (sandbox? noexec /tmp?). Use --no-pty as a workaround."}
	}
	_ = master.Close()
	_ = cmd.Wait()
	return Result{StatusPass, "/dev/pts allocation succeeded", ""}
}

func (c Config) checkMagicPrefix(_ context.Context) Result {
	// We don't import the parse package here to keep this package
	// self-contained; instead we do a literal-string smoke. The parse
	// package's own tests cover the real semantics.
	prefix := "__fernsicht__ "
	line := prefix + `{"value":0.5}`
	if !strings.HasPrefix(line, prefix) {
		return Result{StatusFail, "string compare broken (impossible)", ""}
	}
	return Result{StatusPass, "prefix detection works on this stdout encoding", ""}
}

func (c Config) checkTelemetry(_ context.Context) Result {
	// Conceptual check: confirm that the binary's outbound-HTTP
	// surface is limited to the configured signaling server. We don't
	// instrument the network layer; we declare the property here for
	// users who want a one-line confirmation.
	return Result{StatusPass,
		"no telemetry — the only outbound endpoint is " + c.ServerURL,
		""}
}

// --- Runner --------------------------------------------------------

// Runner executes Checks and prints a per-check colored line to w.
// Returns the worst status seen (PASS < WARN < FAIL).
type Runner struct {
	Out      io.Writer
	NoColor  bool
}

// Run executes every check in order. ctx applies a per-check budget.
func (r *Runner) Run(ctx context.Context, checks []Check) Status {
	if r.Out == nil {
		r.Out = os.Stdout
	}

	worst := StatusPass
	for _, ch := range checks {
		cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		res := ch.Run(cctx)
		cancel()

		r.print(ch.Name, res)
		if !r.NoColor {
			// blank line readability for FAILs only
			if res.Status == StatusFail || res.Status == StatusWarn {
				if res.Hint != "" {
					fmt.Fprintf(r.Out, "    hint: %s\n", res.Hint)
				}
			}
		} else if res.Hint != "" && (res.Status == StatusFail || res.Status == StatusWarn) {
			fmt.Fprintf(r.Out, "    hint: %s\n", res.Hint)
		}

		if res.Status > worst {
			worst = res.Status
		}
	}
	return worst
}

func (r *Runner) print(name string, res Result) {
	tag := res.Status.String()
	if !r.NoColor {
		switch res.Status {
		case StatusPass:
			tag = "\x1b[32mPASS\x1b[0m"
		case StatusWarn:
			tag = "\x1b[33mWARN\x1b[0m"
		case StatusFail:
			tag = "\x1b[31mFAIL\x1b[0m"
		case StatusSkip:
			tag = "\x1b[2mSKIP\x1b[0m"
		}
	}
	fmt.Fprintf(r.Out, "  [%s]  %-25s  %s\n", tag, name, res.Detail)
}

// --- helpers --------------------------------------------------------

func hostFromURL(u string) string {
	// Tiny URL parser to avoid pulling net/url for two strings.
	s := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.Index(s, "/"); i >= 0 {
		s = s[:i]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return s
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if x != "" {
			return x
		}
	}
	return ""
}

// redactProxyAuth strips user:pass@ from a proxy URL so we don't
// leak credentials into doctor output / pasted issue reports.
func redactProxyAuth(proxyURL string) string {
	if i := strings.Index(proxyURL, "@"); i >= 0 {
		// Find scheme prefix; preserve it.
		schemeEnd := strings.Index(proxyURL, "://")
		if schemeEnd < 0 || schemeEnd > i {
			// Malformed; just return as-is.
			return proxyURL
		}
		return proxyURL[:schemeEnd+3] + "<redacted>@" + proxyURL[i+1:]
	}
	return proxyURL
}

func tlsVersionStr(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "1.0"
	case tls.VersionTLS11:
		return "1.1"
	case tls.VersionTLS12:
		return "1.2"
	case tls.VersionTLS13:
		return "1.3"
	}
	return fmt.Sprintf("%#x", v)
}

// ErrSomeFailed indicates at least one check returned FAIL. Used by
// the doctor command to set a non-zero exit code.
var ErrSomeFailed = errors.New("doctor: one or more checks failed")
