# Changelog — Fernsicht Python SDK

All notable changes to the `fernsicht` package on PyPI.

Format: [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
Versioning: [SemVer](https://semver.org/) + [PEP 440](https://peps.python.org/pep-0440/).

## [Unreleased]

### Added / Changed / Deprecated / Removed / Fixed / Security
- —

---

## [0.1.2] — 2026-04-23

First public release on PyPI.

Version starts at 0.1.2 to align with the Fernsicht CLI's `cli/v0.1.2`
branding for the initial public launch — these artifacts share a brand
but version independently from here on.

### Added

- `blick(iterable, desc=...)` — wrap any iterable, get a viewer URL.
- `manual()` — explicit progress updates outside an iterable.
- `aiortc`-backed WebRTC DataChannel transport (peer-to-peer after
  the signalling handshake).
- Pipe-delimited wire protocol (`P|`, `START|`, `END|`, presence,
  keepalive) compatible with the CLI bridge and the browser viewer.

### Notes

- Default signalling server: `https://signal.fernsicht.space`.
- Override with `FERNSICHT_SERVER_URL` for self-hosting.

---

<!--
Per-release template:

## [X.Y.Z] — YYYY-MM-DD

### Added

- One-line bullet.

### Changed

- —

### Fixed

- —
-->
