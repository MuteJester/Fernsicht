// Package errcatalog provides stable error codes (E001, E002, …)
// for everything the CLI surfaces to users, plus per-code lookup
// for `fernsicht doctor --explain Exxx`.
//
// Codes are stable across CLI versions: once published, a code's
// meaning never changes. New errors get new codes; deprecated codes
// stay in the catalog with a "deprecated" note.
//
// Code ranges (per CLI plan §10.2):
//
//   E001–E009   network
//   E010–E019   auth
//   E020–E029   wrap (subprocess management)
//   E030–E039   parse (regex / magic prefix)
//   E040–E049   bridge
//   E050–E059   config
//   E090–E098   install / update
//   E099        internal
package errcatalog

import (
	"fmt"
	"sort"
	"strings"
)

// Class groups codes for `fernsicht doctor` to print them by section.
type Class string

const (
	ClassNetwork  Class = "network"
	ClassAuth     Class = "auth"
	ClassWrap     Class = "wrap"
	ClassParse    Class = "parse"
	ClassBridge   Class = "bridge"
	ClassConfig   Class = "config"
	ClassInstall  Class = "install"
	ClassInternal Class = "internal"
)

// Entry is one cataloged error.
type Entry struct {
	Code    string // "E001", "E010", ...
	Class   Class
	Summary string // one-line user-facing summary
	Cause   string // longer "what's actually happening"
	Hint    string // actionable suggestion
}

var entries = []Entry{
	// --- Network ---
	{
		Code: "E001", Class: ClassNetwork,
		Summary: "Could not reach signaling server.",
		Cause: "DNS resolution succeeded but the TCP connection or TLS handshake failed within the bridge's timeout (30s).",
		Hint:  "Check internet connectivity. Behind a corporate proxy? Set HTTPS_PROXY=http://proxy.corp:8080. To diagnose: curl -v https://signal.fernsicht.space/healthz",
	},
	{
		Code: "E002", Class: ClassNetwork,
		Summary: "TLS handshake failed.",
		Cause: "The server's certificate could not be validated. Common in environments with TLS-intercepting corporate proxies that re-sign traffic with their own CA.",
		Hint:  "Add your corporate CA to the system trust store (or wait for Phase 6 polish: --ca-cert flag). To check: openssl s_client -connect signal.fernsicht.space:443",
	},
	{
		Code: "E003", Class: ClassNetwork,
		Summary: "HTTP_PROXY unreachable.",
		Cause: "HTTP_PROXY or HTTPS_PROXY env var is set, but the proxy itself rejected the connection or timed out.",
		Hint:  "Verify proxy URL is correct and reachable. Try: curl -x $HTTPS_PROXY https://signal.fernsicht.space/healthz",
	},

	// --- Auth ---
	{
		Code: "E010", Class: ClassAuth,
		Summary: "Server rejected join secret.",
		Cause: "The signaling server has a SENDER_JOIN_SECRET configured and the value passed via --join-secret / FERNSICHT_JOIN_SECRET doesn't match.",
		Hint:  "Check the value of FERNSICHT_JOIN_SECRET. If you self-host the signaling server, verify the env var matches the one in /etc/fernsicht/fernsicht.env.",
	},
	{
		Code: "E011", Class: ClassAuth,
		Summary: "Session token expired (12-hour limit).",
		Cause: "Sessions are capped at 12 hours by the server. Long-running jobs need to restart fernsicht periodically.",
		Hint:  "Restart your fernsicht run before hour 12. The R/Python SDKs auto-warn at the 80% mark; the CLI gets the same warning in Phase 7.",
	},

	// --- Wrap (subprocess) ---
	{
		Code: "E020", Class: ClassWrap,
		Summary: "Wrapped command not found.",
		Cause: "The command you passed after `--` couldn't be found on PATH.",
		Hint:  "Verify the command exists: `which <cmd>`. Use the full path if it's not on PATH.",
	},
	{
		Code: "E021", Class: ClassWrap,
		Summary: "Wrapped command permission denied.",
		Cause: "The file exists but isn't executable, or the current user lacks permission to exec it.",
		Hint:  "Check permissions: ls -la <cmd>. May need: chmod +x <cmd>.",
	},
	{
		Code: "E022", Class: ClassWrap,
		Summary: "Could not allocate pty.",
		Cause: "On Linux/macOS, pty allocation failed (sandboxed environment, /dev/ptmx missing, AppArmor / SELinux block, etc.). On Windows, ConPTY isn't supported on this build.",
		Hint:  "Run with --no-pty to fall back to pipe-mode (loses some color/progress in tools that detect tty).",
	},

	// --- Parse ---
	{
		Code: "E030", Class: ClassParse,
		Summary: "Invalid magic-prefix line (--strict-magic).",
		Cause: "A wrapped command emitted a `__fernsicht__ ...` line with malformed JSON or unknown verb, and --strict-magic was set so this is fatal.",
		Hint:  "Run `fernsicht magic` for the valid syntax. Drop --strict-magic to downgrade to a warning.",
	},
	{
		Code: "E031", Class: ClassParse,
		Summary: "Invalid custom regex pattern.",
		Cause: "A --pattern flag value or .fernsicht.toml entry contains a regex that doesn't compile.",
		Hint:  "Test your regex with: echo \"sample\" | grep -E 'YOUR_REGEX'. Fernsicht uses Go's RE2 syntax, which doesn't support backreferences (\\1).",
	},

	// --- Bridge ---
	{
		Code: "E040", Class: ClassBridge,
		Summary: "Bridge protocol mismatch with server.",
		Cause: "The bridge speaks an older protocol version than the signaling server expects.",
		Hint:  "Update fernsicht: `fernsicht update --check` then re-run the install one-liner.",
	},
	{
		Code: "E041", Class: ClassBridge,
		Summary: "Bridge subprocess died.",
		Cause: "The external bridge binary (--bridge-path) crashed unexpectedly.",
		Hint:  "Run with --debug to see bridge stderr. Most CLI users don't need --bridge-path; remove it to use the in-process bridge.",
	},
	{
		Code: "E042", Class: ClassBridge,
		Summary: "Bridge in-process panic recovered.",
		Cause: "A bug in the bridge's WebRTC stack triggered a panic. The wrapped command's exit code was preserved; viewers may have lost their connection.",
		Hint:  "Please report at github.com/MuteJester/Fernsicht/issues with the panic stack trace from stderr.",
	},

	// --- Config ---
	{
		Code: "E050", Class: ClassConfig,
		Summary: ".fernsicht.toml parse error.",
		Cause: "TOML syntax error in the config file. The error message includes the line number.",
		Hint:  "Validate your TOML at https://www.toml-lint.com/ (or any TOML-aware editor). Common issue: forgetting to quote a string with special characters.",
	},
	{
		Code: "E051", Class: ClassConfig,
		Summary: "Invalid value for setting.",
		Cause: "A config key has a value of the wrong type or out of range (e.g., qr = \"sometimes\" instead of \"auto\"|\"always\"|\"never\").",
		Hint:  "Check the schema: see the [run] / [detection] sections in dist-templates / docs.",
	},

	// --- Install ---
	{
		Code: "E090", Class: ClassInstall,
		Summary: "Binary corrupted (SHA mismatch).",
		Cause: "An update or install download passed HTTPS but the SHA256 didn't match the published SHA256SUMS. Network corruption, MITM, or a tampered release.",
		Hint:  "Re-download. If the mismatch persists after multiple retries, report at github.com/MuteJester/Fernsicht/issues — could be a release-asset compromise.",
	},

	// --- Internal ---
	{
		Code: "E099", Class: ClassInternal,
		Summary: "Unrecognized internal error.",
		Cause: "Something we didn't anticipate. Run with --debug for full context.",
		Hint:  "Please report at github.com/MuteJester/Fernsicht/issues with the --debug output.",
	},
}

// Lookup returns the Entry for code (case-insensitive), or false if
// no such code exists.
func Lookup(code string) (Entry, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	for _, e := range entries {
		if e.Code == code {
			return e, true
		}
	}
	return Entry{}, false
}

// All returns every cataloged entry, sorted by code.
func All() []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// Format renders an entry as the standard four-line block:
//
//   error: <summary>
//   cause: <cause>
//   hint:  <hint>
//   docs:  https://fernsicht.space/docs/errors/<code>
//
// Returns the formatted string (no trailing newline).
func Format(e Entry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %s\n", e.Code, e.Summary)
	fmt.Fprintf(&sb, "  cause: %s\n", e.Cause)
	fmt.Fprintf(&sb, "  hint:  %s\n", e.Hint)
	fmt.Fprintf(&sb, "  docs:  https://github.com/MuteJester/Fernsicht/blob/main/SECURITY.md (full catalog)")
	return sb.String()
}
