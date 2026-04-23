# Contributing to Fernsicht

Thanks for helping make Fernsicht better. This repo is a monorepo
with several independently-released components (CLI, bridge, Python
SDK, R SDK) plus the viewer web app. Each has its own README — this
file is a high-level orchestrator.

## Before you start

- **Bug report or feature request?** Open an
  [issue](https://github.com/MuteJester/Fernsicht/issues/new/choose) —
  the templates prompt for the info we need.
- **Security issue?** Don't open a public issue. Follow
  [`SECURITY.md`](SECURITY.md) instead.
- **Small fix or obvious improvement?** PRs welcome against `main`.
- **Larger change?** Open an issue first so we can align on scope
  before you invest time.

## Repo layout

| Path | Stack | Tests |
|---|---|---|
| [`cli/`](cli/) | Go 1.26+ | `cd cli && make test` |
| [`bridge/`](bridge/) | Go 1.26+, CGO | `cd bridge && make test` |
| [`frontend/`](frontend/) | Vite + TypeScript | `cd frontend && npm run build` |
| [`publishers/python/`](publishers/python/) | Python ≥ 3.9 | `cd publishers/python && pytest` |
| [`publishers/r/`](publishers/r/) | R | `R CMD check publishers/r` |

Each component's README has detailed dev setup, build targets, and
release notes.

## Running the full test suite locally

```bash
# Go components
(cd cli && make test)
(cd bridge && make test)

# Python SDK
(cd publishers/python && pip install -e ".[dev]" && pytest)

# Frontend (typechecks as part of build)
(cd frontend && npm ci && npm run build)

# R package (optional, slower — requires R toolchain)
R CMD check publishers/r
```

CI runs the Go, Python, and frontend checks on every pull request —
see [`.github/workflows/ci.yml`](.github/workflows/ci.yml).

## Commit messages

We use [Conventional Commits](https://www.conventionalcommits.org/)
with a component scope:

```
feat(cli): add --json-output flag
fix(bridge): recover from DataChannel close during handshake
docs(readme): clarify install one-liner on Windows
release(python): cut v0.1.3
```

Common types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`,
`release`. Scope is usually the component directory or a short tag.

## Branches and releases

- `main` is the trunk. CI must pass before merge.
- Releases are driven by tags of the form `<scope>/vX.Y.Z` —
  e.g., `cli/v0.1.2`, `py/v0.1.2`, `bridge/v0.1.0`. Each scope has
  its own release workflow in [`.github/workflows/`](.github/workflows/).
- See [`cli/RELEASE.md`](cli/RELEASE.md) and
  [`bridge/RELEASE.md`](bridge/RELEASE.md) for the release playbook.

## Licensing of contributions

Fernsicht is dual-licensed (AGPL-3.0 + commercial — see
[`LICENSE`](LICENSE)). By submitting a contribution you agree it
may be distributed under both. If you're contributing something
substantial and want to discuss this explicitly, raise it in your PR.

## Questions

For "how do I…" questions, open an issue for now. If we enable
GitHub Discussions later, we'll update this doc.
