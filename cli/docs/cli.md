# fernsicht CLI reference

Full reference for every subcommand and flag. For a friendlier
walkthrough start at [examples.md](examples.md).

## Subcommand index

| Subcommand | What it does |
|---|---|
| [`run`](#fernsicht-run) | Wrap a command; broadcast its progress. |
| [`url`](#fernsicht-url) | Print the viewer URL of running session(s). |
| [`doctor`](#fernsicht-doctor) | Diagnose installation / network. |
| [`magic`](#fernsicht-magic) | Show the magic-prefix protocol reference. |
| [`completion`](#fernsicht-completion) | Generate shell completion scripts. |
| [`update`](#fernsicht-update) | Check for / install a newer version. |
| [`version`](#fernsicht-version) | Print version + build info. |

## `fernsicht run`

```
fernsicht run [flags] -- <command> [args...]
```

Wraps a command, broadcasts its progress to a viewer URL.
The `--` is required to separate fernsicht flags from the wrapped
command's args (otherwise `fernsicht run python -V` is ambiguous —
is `-V` ours or python's?).

### Examples

```bash
# Auto-detect tqdm/pip-style progress:
fernsicht run -- pip install pandas

# Manual markers (any program / language):
fernsicht run -- bash my_script.sh
# ... where my_script.sh emits:
#     for i in {1..100}; do
#         echo "__fernsicht__ progress $i/100"
#         sleep 1
#     done

# Custom regex for a tool we don't know about:
fernsicht run --pattern '\[(\d+)/(\d+)\]' -- ./mytool

# Strict mode for CI (fails the run if monitoring breaks):
fernsicht run --strict -- ./long_test.sh
```

### Core flags

| Flag | Default | Notes |
|---|---|---|
| `--label STRING` | (deparsed cmd) | Task label shown to viewers. |
| `--unit STRING` | `it` | Progress unit, e.g. `epoch`, `batch`, `row`. |
| `--max-viewers INT` | `8` | Cap on concurrent viewers. |
| `--webhook URL` | — | POST JSON to URL on session end. |

### Detection flags

| Flag | Default | Notes |
|---|---|---|
| `--no-detect` | off | Disable Tier-1 auto-detection; only the magic prefix runs. |
| `--no-magic` | off | Don't intercept `__fernsicht__` lines. |
| `--strict-magic` | off | Exit 250 on invalid magic-prefix line. |
| `--pattern REGEX` | (none) | Add a custom regex (repeatable). See [magic-prefix.md](magic-prefix.md#custom-patterns). |

### Output flags

| Flag | Default | Notes |
|---|---|---|
| `--share` | off | Print URL on stdout, suppress chrome. Useful for `URL=$(fernsicht run --share -- ./script.sh &)`. |
| `--copy-url` | off | Copy URL to system clipboard (uses xclip/wl-copy/pbcopy/clip.exe). |
| `--qr` / `--no-qr` | auto | QR code in terminal (default: on when stderr is a tty). |
| `--quiet` | off | Suppress fernsicht's own output (URL still printed). |
| `--output {text,json}` | text | Machine-readable mode. |

### Environment flags

| Flag | Default | Notes |
|---|---|---|
| `--no-pty` | off | Don't allocate a pty (for restricted CI / no-tty environments). |
| `--no-unbuffer` | off | Don't set `PYTHONUNBUFFERED=1` etc. |
| `--strict` | off | Bridge failure mid-run → exit 200, kill wrapped command. |
| `--no-fail-on-bridge` | off | Bridge failure at startup → run wrapped command anyway, no monitoring. |

### Config / network flags

| Flag | Default | Notes |
|---|---|---|
| `--server-url URL` | `https://signal.fernsicht.space` | Signaling server URL. |
| `--join-secret STR` | `$FERNSICHT_JOIN_SECRET` | Server auth (when configured). |
| `--config PATH` | (auto) | Explicit `.fernsicht.toml` path; otherwise searched cwd → home → XDG. |
| `--url-file PATH` | (auto) | Where to write the per-PID URL file. |

### Debug

| Flag | Notes |
|---|---|
| `--debug` | Verbose internal logging (parser hits, bridge state). Equivalent to `FERNSICHT_DEBUG=1`. |

### Exit codes

| Code | Meaning |
|---|---|
| 0..127 | Wrapped command's exit code (mirrored). |
| 128+N | Wrapped command killed by signal N. |
| 200 | `--strict` mode: bridge failed mid-run. |
| 250 | `--strict-magic`: invalid magic-prefix line encountered. |
| 254 | Couldn't spawn wrapped command (not found, permission). |
| 255 | Bridge / signaling failure that prevented wrapping at all. |

The default behavior **never overrides** on mid-run failure: if the
bridge dies but the wrapped command keeps going, you get the wrapped
command's exit code. CI users opt into stricter semantics with
`--strict` / `--strict-magic`.

## `fernsicht url`

```
fernsicht url [--all] [--pid PID]
```

Prints the viewer URL of currently-running fernsicht sessions on
this host. Useful for headless workflows where the original
`viewer:` line scrolled out of your terminal hours ago.

### Examples

```bash
# Single session running → just the URL.
fernsicht url

# Multiple sessions → table.
fernsicht url --all

# A specific PID:
fernsicht url --pid 12345

# Pipe to slack-cli or similar:
slack-cli post "$(fernsicht url --pid $!)"
```

Sessions are discovered by reading URL files at
`$XDG_RUNTIME_DIR/fernsicht/<pid>.url` (or `/tmp/fernsicht-<pid>.url`).
Stale entries (PID gone) are silently filtered.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Found a session and printed its URL. |
| 1 | No sessions found, or `--pid N` didn't match. |

## `fernsicht doctor`

```
fernsicht doctor [--server-url URL] [--no-color]
fernsicht doctor --explain Exxx
```

Runs the diagnostic suite. 11 checks covering: binary integrity,
platform support, libc compatibility, DNS resolution, TCP
connectivity, TLS handshake, signaling `/healthz`, proxy
environment, pty allocation, magic-prefix parser smoke, telemetry-
free declaration. Each check shows PASS / WARN / FAIL with an
actionable hint when not PASS.

`--explain Exxx` looks up an error code in the catalog and prints
the four-line summary / cause / hint / docs block. Use this when a
log line says "[fernsicht] error: E001 ..." and you want context.

### Examples

```bash
fernsicht doctor
# → PASS  binary integrity        /usr/local/bin/fernsicht (10 MB)
# → PASS  platform support        linux/amd64
# → PASS  DNS resolution          signal.fernsicht.space → 172.86.93.90
# → ...

fernsicht doctor --explain E001
# E001 — Could not reach signaling server.
#   cause: DNS resolved but TCP / TLS failed within 30s.
#   hint:  Check internet; behind a proxy? Set HTTPS_PROXY=...
#   docs:  https://github.com/MuteJester/Fernsicht/blob/main/SECURITY.md
```

### Exit codes

| Code | Meaning |
|---|---|
| 0 | All checks PASS or only WARN. |
| 1 | At least one check FAILed (or unknown error code with `--explain`). |

## `fernsicht magic`

```
fernsicht magic
```

Prints the magic-prefix protocol reference (mirrors
[`magic-prefix.md`](magic-prefix.md)). No flags. Output is intended
for offline lookup.

## `fernsicht completion`

```
fernsicht completion {bash|zsh|fish|powershell}
```

Generates a shell completion script. Install per shell:

```bash
# bash
source <(fernsicht completion bash)
# or system-wide:
fernsicht completion bash | sudo tee /etc/bash_completion.d/fernsicht > /dev/null

# zsh
fernsicht completion zsh > "${fpath[1]}/_fernsicht"
# (then re-run compinit, or open a new shell)

# fish
fernsicht completion fish > ~/.config/fish/completions/fernsicht.fish

# powershell
fernsicht completion powershell >> $PROFILE
# (open a new shell)
```

After install, `fernsicht <Tab>` completes subcommands;
`fernsicht run -<Tab>` completes flags; `fernsicht completion <Tab>`
completes shell names.

## `fernsicht update`

```
fernsicht update [--check]
```

Checks GitHub for a newer release. Filters out pre-releases. With
`--check`: prints whether you're on the latest. Without `--check`:
same behavior + prints the install one-liner you should re-run.

We don't auto-install on `fernsicht update` — auto-update mechanisms
have a long history of bugs (partial downloads, locked binaries on
Windows, signing-cert rotations). We'd rather have you re-run
`install.sh` explicitly.

## `fernsicht version`

```
fernsicht version
fernsicht --version
fernsicht -V
```

Prints version + commit + build date + Go version + os/arch.

## Global flags

`--help` / `-h` works on every subcommand. `--version` / `-V` is a
top-level alias for `version`. Other flags are subcommand-scoped.

## See also

- [`magic-prefix.md`](magic-prefix.md) — explicit progress markers
- [`config.md`](config.md) — `.fernsicht.toml` schema
- [`examples.md`](examples.md) — recipes for common workflows
- [`troubleshooting.md`](troubleshooting.md) — top issues + fixes
- [`../RELEASE.md`](../RELEASE.md) — versioning, release process,
  reproducible builds, signature verification
