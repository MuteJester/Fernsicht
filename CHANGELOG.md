# Changelog

All notable changes to **Fernsicht** are recorded here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project follows [Semantic Versioning](https://semver.org/).

This file covers the repo as a whole — the CLI, bridge, viewer, and
anything else not tied to a specific SDK. Per-SDK changelogs live in
their own files because they release on independent tag namespaces:

- **CLI + bridge + viewer** — this file
- **Python SDK** — [`publishers/python/CHANGELOG.md`](publishers/python/CHANGELOG.md)
- **R SDK** — [`publishers/r/NEWS.md`](publishers/r/NEWS.md)

## [Unreleased]

Landing in the next release.

---

## [cli/v0.1.0] — 2026-04-20

First public release of the Fernsicht CLI. Wraps any command and
broadcasts progress to a shareable viewer URL over WebRTC.

### Added

- **CLI** (`fernsicht run -- <cmd>`) with auto-detection for tqdm-
  style bars, `[N/M]` brackets, `N of M` phrasing, percent progress,
  and step/epoch keywords — no code change required for most tools.
- **Magic-prefix protocol** (`__fernsicht__ …`) for unambiguous
  progress + lifecycle events from any language.
- **Custom regex patterns** via `--pattern` flag or
  `.fernsicht.toml` config file.
- **Subcommands**: `run`, `url`, `doctor`, `magic`, `completion`,
  `update`, `version`.
- **Multi-platform binaries**: linux/macOS/windows × amd64/arm64
  (windows arm64 deferred to v0.2.0), all cosign keyless-signed.
- **Docker image** (`ghcr.io/mutejester/fernsicht` primary,
  `thomask90/fernsicht` mirror; multi-arch linux/amd64 + linux/arm64).
- **Homebrew tap** + **Scoop bucket** auto-published per release.
- **CycloneDX SBOM** + **SLSA v1.0 level-3 provenance** attached to
  every GH release for supply-chain verification.
- **Reproducible builds** via SOURCE_DATE_EPOCH + `-trimpath` +
  `-buildvcs=false` + `-buildid=`. Verified in CI on every release.

### Security

- Supply-chain scanning CI: `govulncheck`, `pip-audit`, `npm audit`,
  `trivy` on Dockerfile, `osv-scanner` aggregate, `go-licenses` +
  `pip-licenses` for AGPL-compat audit.
- All third-party GitHub Actions SHA-pinned; Dependabot auto-bumps
  weekly.
- GitHub App-based cross-repo auth (no long-lived PATs) for
  Homebrew/Scoop tap PRs.
- Concurrency-grouped publish workflows prevent double-release
  races.

### Known limitations

- macOS binaries are **not Apple-notarized** in v0.1.0 — installing
  via `curl install.sh | sh` on macOS will hit Gatekeeper. Use
  `brew install MuteJester/fernsicht/fernsicht` instead (bypasses
  Gatekeeper), or run `xattr -d com.apple.quarantine
  /usr/local/bin/fernsicht` after install. See
  `cli/docs/troubleshooting.md`.
- Windows binaries are **not Authenticode-signed** — SmartScreen
  will warn on first run. Users can click "Run anyway." Revisit
  in v0.2.0 pending download metrics.
- R SDK (`publishers/r/`) is shipped from GitHub (not CRAN yet)
  and requires a separate `bridge/v0.1.0` release for its lazy
  bridge-binary download. See `cli/RELEASE.md` §"R SDK bridge-
  binary coupling" for the ship order.

---

<!--
Release-note template — copy this stanza above, replace the version,
date, and fill in whichever subsections apply. Don't leave empty
subsections; just delete them.

## [X.Y.Z] — YYYY-MM-DD

### Added / Changed / Deprecated / Removed / Fixed / Security
-->

## Prior to v0.1.0

See the [GitHub Releases](https://github.com/MuteJester/Fernsicht/releases)
page for pre-launch history. The first structured entry in this file
will be v0.1.0.
