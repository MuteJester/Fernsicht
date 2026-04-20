# Troubleshooting

Top issues users hit and how to fix them. Run `fernsicht doctor`
first — it catches most environment problems automatically.

## "Could not reach signaling server" (E001)

**Symptoms:** session-open fails; you see `E001` or "POST /session:
Post ...: dial tcp ...".

Most common causes:

- **Behind a corporate proxy** — set `HTTPS_PROXY=http://proxy.corp:8080`
  (Go's HTTP client honors this automatically). If you also need
  basic auth: `HTTPS_PROXY=http://user:pw@proxy.corp:8080`.
- **Firewall blocking outbound 443** — fernsicht only needs HTTPS to
  the signaling server (default `signal.fernsicht.space`). Check
  with `curl -v https://signal.fernsicht.space/healthz`.
- **DNS resolution failed** — `nslookup signal.fernsicht.space`
  should resolve. If your network has a captive portal or filtered
  DNS, you'll see this fail.

```bash
# Use the doctor to find which check fails:
fernsicht doctor
```

If you want to run the wrapped command anyway when the bridge is
unreachable (e.g., on a flaky connection), pass `--no-fail-on-bridge`:

```bash
fernsicht run --no-fail-on-bridge -- ./script.sh
```

## "TLS handshake failed" (E002)

You're behind a TLS-intercepting corporate proxy. Most enterprise
firewalls re-sign HTTPS traffic with a corporate CA installed in the
OS trust store; if fernsicht doesn't trust that CA, the handshake
fails.

Workaround: **add the corporate CA to the system trust store**.
Go's `crypto/x509` reads the OS trust store automatically, so once
the CA is installed system-wide, fernsicht picks it up with no flag.

Verify with: `openssl s_client -connect signal.fernsicht.space:443`.
If openssl's chain validation fails, fernsicht's will too.

## "Wrapped command not found" (E020)

`fernsicht run -- mytool` couldn't find `mytool` on your PATH.

- Check `which mytool`.
- Use the full path: `fernsicht run -- /usr/local/bin/mytool`.
- If you're in a virtualenv: activate it first, or pass the venv's
  python directly: `fernsicht run -- ~/.venv/bin/python script.py`.

## macOS: "fernsicht cannot be opened because the developer cannot be verified"

macOS Gatekeeper rejects binaries that aren't signed with an Apple
Developer ID. Fernsicht's CLI is currently not Apple-notarized; the
binaries ARE cosign-signed (Sigstore), which Gatekeeper doesn't
recognize.

**Preferred fix: use Homebrew.** Homebrew bypasses Gatekeeper entirely
because Formula bottles are trusted by Homebrew itself:

```bash
brew tap MuteJester/fernsicht
brew install MuteJester/fernsicht/fernsicht
fernsicht --version
```

**If you must use the `curl | sh` installer** on macOS, remove the
quarantine attribute after installation:

```bash
curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
xattr -d com.apple.quarantine /usr/local/bin/fernsicht
fernsicht --version
```

`xattr -d com.apple.quarantine` tells macOS "I trust this file" for
the specific binary. It's the same escape hatch used by tools like
`nvm`, Rustup's `rustup-init`, etc.

**Verify the binary's cosign signature before running it** (belt +
suspenders if you're skipping Gatekeeper):

```bash
cosign verify-blob \
  --certificate fernsicht-darwin-arm64.cert \
  --signature   fernsicht-darwin-arm64.sig \
  --certificate-identity-regexp 'https://github.com/MuteJester/Fernsicht/.+' \
  --certificate-oidc-issuer     https://token.actions.githubusercontent.com \
  /usr/local/bin/fernsicht
```

## "Could not allocate pty" (E022)

Some sandboxed environments (certain containers, restricted
SELinux/AppArmor profiles, `noexec` mount on `/dev/pts`) refuse pty
allocation. Workaround: pass `--no-pty`. The wrapped command runs in
pipe mode — your terminal won't see the colored output the tool
would normally produce, but progress detection still works.

## Magic-prefix lines aren't being detected

```bash
echo "__fernsicht__ progress 50/100"
# ... but no [parse] line in --debug output
```

Common causes:

- **Output buffering** — Python and many other languages
  block-buffer stdout when not interactive. fernsicht sets
  `PYTHONUNBUFFERED=1` by default; check it isn't being clobbered:

  ```bash
  fernsicht run --no-pty -- python -c \
    "import os; print(os.environ.get('PYTHONUNBUFFERED'))"
  # → 1
  ```

  If you've passed `--no-unbuffer`, undo that.

- **Trailing space** — the prefix is `__fernsicht__ ` (with one
  space). `__fernsicht__progress` (no space) doesn't match.

- **Pre-magic content** — the prefix must be at the START of the
  line. `prefix: __fernsicht__ progress 5` won't match.

- **`--no-magic` was passed** — `fernsicht run --debug -- ...` will
  show the resolved flags.

## Tier-1 detection isn't ticking

- **Confidence locking** — auto-detection requires 2 matches in a
  5-second window before locking. A single matching line doesn't
  trigger anything (defensive against false positives).
- **TUI mode** — if your tool emits the alt-screen escape `\e[?1049h`
  (rich, textual, ncurses, vim, htop, etc.), fernsicht disables
  Tier-1 to avoid parsing terminal-redraw garbage. You'll see one
  warn line: "detected fullscreen TUI; auto-detection disabled."
  Use the [magic prefix](magic-prefix.md) instead.
- **`--no-detect` was passed** — disables Tier-1 entirely.

## Viewers see nothing in the browser

The CLI says `viewer:` and prints a URL, but the browser stays on
"Awaiting signal" forever.

- **Wait** — if no progress events have been emitted yet, the bar
  has nothing to show. The handshake completes immediately; ticks
  arrive when the wrapped command produces parseable output.
- **Check the wrapped command's output** — does it actually print
  anything matching a Tier-1 pattern or magic prefix?
- **Run with `--debug`** to see the parser's view of stdout:

  ```bash
  fernsicht run --debug --no-pty -- ./your-script.sh 2>&1 | grep '\[parse\]'
  ```

## "Browser refused to connect"

The viewer URL points at `app.fernsicht.space` (the GH Pages-hosted
viewer). If the browser can't reach it:

- Check your network / firewall — `app.fernsicht.space` is on
  GitHub Pages CDN.
- The URL fragment (`#room=abc12345&role=viewer`) MUST be preserved.
  If you're sharing the URL via tools that strip fragments (rare
  but possible), wrap it.

## Session disconnected mid-run (heartbeat lost)

Sessions have a 12-hour ceiling enforced by the signaling server
(E011). The CLI will warn at ~80% of the budget. For longer jobs,
restart fernsicht periodically:

```bash
# Naive periodic restart wrapper:
while true; do
    fernsicht run -- ./long-job.sh
    sleep 5
done
```

Or split work into 8-hour chunks.

## Performance: bar lags way behind wrapped command's actual progress

- **Buffering** — see "magic-prefix lines aren't being detected"
  above; same root cause.
- **Tick volume** — fernsicht caps tick rate to ~10/sec by design
  (avoids saturating the bridge). If your tool emits 1000 progress
  lines/second, you'll see ~1% of them ticked.

## "fernsicht run: unknown command"

You meant `fernsicht run -- <command>` but typed `fernsicht run
<command>` (forgot the `--`). The CLI catches this with a hint:

```
[fernsicht] error: missing `--` separator before the wrapped command.
            Did you mean: fernsicht run -- python train.py
```

## Self-hosted signaling server: misc errors

If you run your own signaling server, set:

```bash
export FERNSICHT_SERVER_URL=https://your-signal.example.com
export FERNSICHT_JOIN_SECRET=<your-secret-from-fernsicht.env>
```

Or pass via flags. Common gotchas:

- The server's `SENDER_JOIN_SECRET` must match `FERNSICHT_JOIN_SECRET`
  exactly. A trailing newline or whitespace in the env file breaks
  this silently — check `cat -A /etc/fernsicht/fernsicht.env`.
- The `MAX_VIEWERS_PER_ROOM` server cap can shadow your `--max-viewers`
  flag. Check `journalctl -u fernsicht -n 100`.

## Reporting a bug

If `fernsicht doctor` doesn't help and you've checked the items
above, open an issue at
<https://github.com/MuteJester/Fernsicht/issues> with:

- Output of `fernsicht --version`
- Output of `fernsicht doctor` (redact any proxy credentials —
  the doctor does this for you)
- Output of `fernsicht run --debug -- <your-command> 2>&1 | head -40`
- Your OS + arch (`uname -a`, or System Settings on macOS)
