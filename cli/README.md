# fernsicht CLI

Wrap any shell command and broadcast its progress to a sharable
viewer URL — no SDK, no code change, no account.

```bash
fernsicht run -- python train.py
# → viewer URL printed; phone scans the QR; bar fills live.
```

## Install

Pick whichever fits your platform.

**Linux / macOS** (one-liner — installer handles platform detection,
SHA256 verification, PATH guidance):

```bash
curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
```

**Windows** (PowerShell):

```powershell
irm https://github.com/MuteJester/Fernsicht/releases/latest/download/install.ps1 | iex
```

**Homebrew** (macOS / Linux):

```bash
brew tap MuteJester/fernsicht
brew install MuteJester/fernsicht/fernsicht
```

**Scoop** (Windows):

```powershell
scoop bucket add fernsicht https://github.com/MuteJester/scoop-fernsicht
scoop install fernsicht
```

**Docker** (try without installing, or use in CI):

```bash
docker run --rm ghcr.io/mutejester/fernsicht:latest --version
```

Multi-arch (linux/amd64 + linux/arm64), built on Alpine with `tini`
for clean signal forwarding to wrapped commands. Cosign-signed.

### What the curl/irm installer does

The installer:

- Downloads the binary for your platform from the latest stable
  GitHub release.
- Verifies SHA256 against the published `SHA256SUMS`.
- Verifies the cosign signature when cosign is installed (otherwise
  warns and proceeds with SHA256 only).
- Smoke-tests the binary (`--version` returns 0) before installing.
- Installs to `/usr/local/bin/fernsicht` (Linux/macOS) or
  `%LOCALAPPDATA%\fernsicht\bin\fernsicht.exe` (Windows). Falls back
  to `~/.local/bin` if `/usr/local/bin` isn't writable.

Pin a specific version:

```bash
VERSION=v0.1.0 sh -c "$(curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh)"
```

Or download the binary directly from the
[releases page](https://github.com/MuteJester/Fernsicht/releases) and
drop it on your PATH.

See [`RELEASE.md`](RELEASE.md) for full verification + reproducible-
build instructions.

## Subcommands

| Command | What it does |
|---|---|
| `fernsicht run -- <cmd>` | Wrap a command and broadcast its progress. |
| `fernsicht url` | Print the viewer URL of a running session. |
| `fernsicht doctor` | Diagnose installation + signaling-server reach. |
| `fernsicht magic` | Print the magic-prefix protocol reference. |
| `fernsicht completion <shell>` | Generate bash / zsh / fish / PowerShell completion. |
| `fernsicht update` | Check for or install a newer version. |
| `fernsicht version` | Print version + build info. |

Run any subcommand with `--help` for full flags.

## How progress is detected

Three tiers, each more explicit than the last:

1. **Auto-detection (Tier-1).** Built-in regex parsers recognise
   common shapes: `tqdm`-style bars, `[N/M]` brackets, `N of M`,
   `N% complete`, `Step N`, etc. Confidence locks after two matches
   in a 5s window so a single noisy line never paints a fake bar.
2. **Custom regex.** Pass `--pattern '...'` (repeatable) or define
   `[[detection.patterns]]` in `.fernsicht.toml` to teach fernsicht
   tool-specific output.
3. **Magic prefix.** Print `__fernsicht__ progress 42/100` from any
   language for unambiguous, structured progress + lifecycle events.
   See [`docs/magic-prefix.md`](docs/magic-prefix.md).

Auto-detection silently disables itself inside fullscreen TUIs
(rich, textual, ncurses) to avoid parsing cursor-redraw garbage —
use the magic prefix in those cases.

## Documentation

- [`docs/cli.md`](docs/cli.md) — full subcommand + flag reference.
- [`docs/magic-prefix.md`](docs/magic-prefix.md) — explicit progress protocol.
- [`docs/config.md`](docs/config.md) — `.fernsicht.toml` schema.
- [`docs/examples.md`](docs/examples.md) — recipes for common tools.
- [`docs/troubleshooting.md`](docs/troubleshooting.md) — top issues + fixes.
- [`examples/`](examples/) — runnable demos (bash pipeline, snakemake,
  pytest, docker build).

## Other ways to use Fernsicht

If you'd rather call from inside your code than wrap a command:

- **Python**: `pip install fernsicht` — see [`publishers/python/`](../publishers/python/).
- **R**: `remotes::install_github("MuteJester/Fernsicht", subdir="publishers/r")` — see [`publishers/r/`](../publishers/r/).

All three speak the same wire protocol; pick whichever fits your
workflow.

## Building locally

```bash
make build           # → dist/fernsicht (unstripped, dev)
make build-stripped  # → dist/fernsicht (production-style)
make size            # build + report binary size (release-budget gate)
make test            # go test ./...
make cross-compile   # 5-platform release-style binaries
```
