# Releasing the fernsicht CLI

This document covers everything maintainers need to cut a release and
everything users need to verify one. Stable across versions; deviations
get noted at the top.

## TL;DR (cut a release)

```bash
# 1. Make sure tests pass on main.
cd cli && make test

# 2. Tag + push. Naming: cli/v<MAJOR>.<MINOR>.<PATCH> (semver).
git tag cli/v0.1.0
git push origin cli/v0.1.0

# 3. The GitHub Actions workflow at .github/workflows/cli-release.yml
#    cross-compiles + signs + publishes the release.
#    Watch it at: https://github.com/MuteJester/Fernsicht/actions
```

## Versioning

Semver. Pre-`1.0.0` we may make breaking changes between minors;
post-`1.0.0` we follow strict semver:

- **MAJOR** — backward-incompatible CLI flag / config-file changes.
- **MINOR** — new flags, new subcommands, new built-in parsers.
- **PATCH** — bug fixes, parser tweaks, performance, docs.

Pre-release identifiers (`cli/v0.2.0-rc1`) automatically mark the
GitHub release as pre-release.

## What gets built

Five static binaries (no CGO, no runtime dependencies):

| Platform | Filename |
|---|---|
| Linux x86-64 | `fernsicht-linux-amd64` |
| Linux ARM64 | `fernsicht-linux-arm64` |
| macOS Intel | `fernsicht-darwin-amd64` |
| macOS Apple Silicon | `fernsicht-darwin-arm64` |
| Windows x86-64 | `fernsicht-windows-amd64.exe` |

Plus per-binary signature artifacts (`.sig`) + Sigstore certificates
(`.cert`), and a single `SHA256SUMS` file covering all binaries.

## Reproducible builds

Every release is reproducible: anyone can rebuild from the tagged
commit and produce **bit-identical** binaries. We document this so
users / auditors can verify a binary matches the published source.

### How reproducibility is achieved

The Makefile (`cli/Makefile`) uses these flags:

- `-trimpath` — strips local filesystem paths from binaries + panic
  traces.
- `-ldflags="-s -w -buildid="` — drops symbol table, DWARF debug info,
  and the per-build random buildID.
- `-buildvcs=false` — prevents Go from embedding `git status`-style
  metadata (which varies between checkouts).
- `CGO_ENABLED=0` — pure Go, no host-libc linkage variation.
- `SOURCE_DATE_EPOCH` — when set, the `BUILD_DATE` ldflag value is
  derived from it instead of `date -u`. The release workflow sets it
  to the tagged commit's committer timestamp.

### Verifying reproducibility yourself

```bash
git clone https://github.com/MuteJester/Fernsicht
cd Fernsicht/cli

# Use the same Go version the release was built with (currently 1.26.x).
go version  # check, install if needed

# Get the exact commit timestamp from the release tag.
TAG=cli/v0.1.0
SOURCE_DATE_EPOCH=$(git log -1 --format=%ct "$TAG")
export SOURCE_DATE_EPOCH

# Build all platforms.
git checkout "$TAG"
make dist

# Compare against the published SHA256SUMS.
diff dist/SHA256SUMS <(curl -sL https://github.com/MuteJester/Fernsicht/releases/download/$TAG/SHA256SUMS)
# (no output → identical → reproducibility verified)
```

The `make verify-repro` target builds the same artifacts twice in one
shell and compares their checksums — useful as a local sanity check
before tagging.

## Verifying a downloaded binary

Two layers of verification: SHA256 matches the published checksum,
and the cosign signature proves the binary came from our GH Actions
workflow.

### SHA256

```bash
cd ~/Downloads
curl -sLO https://github.com/MuteJester/Fernsicht/releases/download/cli/v0.1.0/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
```

### Cosign signature

We sign with cosign keyless (Sigstore) — there's no static public key
to manage; verification chains to GitHub's OIDC + Sigstore's transparency
log. Install cosign once: https://docs.sigstore.dev/cosign/installation.

```bash
TAG=cli/v0.1.0
F=fernsicht-linux-amd64
BASE=https://github.com/MuteJester/Fernsicht/releases/download/$TAG

curl -sLO "$BASE/$F"
curl -sLO "$BASE/$F.sig"
curl -sLO "$BASE/$F.cert"

cosign verify-blob \
  --certificate "$F.cert" \
  --signature  "$F.sig" \
  --certificate-identity-regexp "https://github.com/MuteJester/Fernsicht/.+" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "$F"
# → "Verified OK"
```

The `--certificate-identity-regexp` constrains accepted certs to
those issued for our repository's GH Actions workflows. If someone
sneaks a fake binary into the GH release manually (without going
through the workflow), their cosign cert won't match this regex and
verification fails.

## Manual release flow (if Actions is broken)

If the workflow is broken and you need to ship anyway:

```bash
cd cli

# 1. Set version and reproducible timestamp.
export VERSION=0.1.0
export SOURCE_DATE_EPOCH=$(git log -1 --format=%ct HEAD)

# 2. Build everything.
make dist

# 3. Sign each artifact (you need cosign installed locally).
cd dist
for f in fernsicht-* SHA256SUMS; do
  cosign sign-blob "$f" --output-signature "$f.sig" --output-certificate "$f.cert"
done

# 4. Create the GH release manually.
gh release create cli/v$VERSION \
  --title "fernsicht CLI $VERSION" \
  --notes "Manual release — see CHANGELOG.md" \
  fernsicht-* SHA256SUMS *.sig *.cert
```

## Hot-fix releases

Semver patch bumps for security or correctness fixes:

```bash
# branch from the tag you're patching
git checkout -b hotfix/cli-v0.1.1 cli/v0.1.0
# ... make fix, commit ...
git tag cli/v0.1.1
git push origin cli/v0.1.1
```

The workflow handles re-publishing the release notes against the
patched commit.

## Yanking a release

GitHub doesn't have first-class "yank" support like crates.io.
What we do instead:

1. Mark the release as pre-release in GH UI (visual signal).
2. Edit the release description to add a prominent warning at the top.
3. Cut a fixed `cli/v<X.Y.Z+1>` ASAP.

Don't delete tags or release artifacts — that breaks anyone who has
the old SHA256 cached and tries to verify.

## Package-manager onramps

Each release also publishes:

- **Docker image** at `ghcr.io/mutejester/fernsicht:vX.Y.Z` (and
  `:latest` for stable releases). Multi-arch (linux/amd64 +
  linux/arm64), built from [`cli/Dockerfile`](Dockerfile) by
  [`.github/workflows/cli-docker.yml`](../.github/workflows/cli-docker.yml).
  Signed with cosign keyless.

  ```bash
  docker run --rm ghcr.io/mutejester/fernsicht:latest --version
  ```

- **Homebrew formula** at `MuteJester/homebrew-fernsicht/Formula/fernsicht.rb`.
  Rendered from [`dist-templates/fernsicht.rb.tmpl`](dist-templates/fernsicht.rb.tmpl)
  per release.

  ```bash
  brew tap MuteJester/fernsicht
  brew install MuteJester/fernsicht/fernsicht
  ```

- **Scoop manifest** at `MuteJester/scoop-fernsicht/fernsicht.json`.
  Rendered from [`dist-templates/fernsicht.json.tmpl`](dist-templates/fernsicht.json.tmpl).

  ```powershell
  scoop bucket add fernsicht https://github.com/MuteJester/scoop-fernsicht
  scoop install fernsicht
  ```

### One-time tap setup (maintainer)

The release workflow renders `fernsicht.rb` + `fernsicht.json` and
attaches them as release assets. Maintainers copy them into the tap
repos manually per release; an auto-PR workflow may replace this
step in a future release.

See [`dist-templates/README.md`](dist-templates/README.md) for the
first-ever tap repo creation steps.

### Verifying the Docker image

Same cosign keyless pattern as the binary releases:

```bash
cosign verify ghcr.io/mutejester/fernsicht:latest \
  --certificate-identity-regexp 'https://github.com/MuteJester/Fernsicht/.+' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## Bridge version coupling

The CLI binary statically links the bridge code (per CLI plan §6).
This means every CLI release is pinned to a specific bridge version.
`fernsicht --version` reports both:

```
fernsicht 0.1.0
  commit:   abc1234
  built:    2026-04-19T18:32:00Z
  go:       go1.26.2
  os/arch:  linux/amd64
```

When the bridge releases a fix, the CLI re-releases with the bumped
`bridge` dependency in `cli/go.mod`. We don't ship "CLI updates the
bridge in place" — the bridge IS the CLI, distribution-wise.
