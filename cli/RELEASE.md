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

## R SDK bridge-binary coupling

The R SDK doesn't bundle the bridge binary — it downloads it lazily
from the `bridge/v<VER>` GitHub release on first use. For that to
work, THREE things must stay in sync:

1. **A `bridge/v<VER>` release exists** in this repo with
   `fernsicht-bridge-<os>-<arch>` assets (produced by
   `.github/workflows/bridge-release.yml`).
2. **`publishers/r/R/bundled_sha256.R` → `BRIDGE_VERSION`** matches
   the release tag's `<VER>`.
3. **`publishers/r/R/bundled_sha256.R` → `BUNDLED_SHA256`** has the
   real SHA256 for each platform's `fernsicht-bridge-*` binary. If
   any entry is `"PHASE0_PLACEHOLDER"` the package aborts with an
   explicit error on first bridge invocation — no silent failures.

### Ship order for any release that touches R

```
1. git tag bridge/v<VER> && git push --tags
   → bridge-release.yml cross-builds + signs + publishes

2. After workflow green, locally:
   curl -fsSL \
     "https://github.com/MuteJester/Fernsicht/releases/download/bridge/v<VER>/SHA256SUMS" \
     -o bridge/dist/SHA256SUMS
   cd publishers/r
   Rscript tools/update_sha256.R <VER>
   # Regenerates R/bundled_sha256.R in place.
   git add R/bundled_sha256.R
   git commit -m "chore(r): bump BRIDGE_VERSION + SHAs to <VER>"
   git push

3. Optional (not wired yet; see RELEASE_PIPELINE_PLAN Phase 2):
   git tag r/v<VER> && git push --tags
   → r-release.yml (future) publishes R SDK on GitHub / CRAN.
```

**If you ship a CLI release without doing this:** existing R SDK
users who try to `fernsicht::blick()` get a Phase-0-placeholder
abort. They can work around by setting `FERNSICHT_BRIDGE_VERSION`
env var to the last known-good `<VER>`, but that's a bad UX.

**For `cli/v0.1.0-rc1` rehearsal:** no R SDK release is planned,
but anyone who clones `main` at that commit has a broken R SDK.
This is pre-existing state, not a regression introduced by the
rehearsal. Fix during the first STABLE v0.1.0 ship.

## Partial-release failure recovery

A release touches N independent channels: GH Release, GHCR, Docker
Hub, Homebrew tap, Scoop bucket, PyPI (for `py/v*` tags), CRAN (for
`r/v*` tags). Some channels are **immutable** — PyPI never lets a
version be re-uploaded, and Docker image SHAs are permanent. If the
pipeline publishes to one and fails on the next, we need a
deterministic path forward rather than ad-hoc scrambling.

### Channel order + reversibility

The release workflows publish in this order, least-reversible last:

```
1. Build + sign + SBOM + SLSA provenance   (local, freely retryable)
2. Green-CI preflight                       (local, retryable)
3. Supply-chain scan (vuln + license)       (retryable)
4. macOS binary workaround (Gatekeeper doc) (no action at release time)
5. Publish GH Release (draft-then-published) (DELETABLE + retryable)
6. Publish Docker image (GHCR)              (retryable by tag bump)
7. Publish Docker image (Docker Hub mirror) (retryable by tag bump)
8. Publish to PyPI                          ⚠️  IMMUTABLE — commit point
9. Open tap PRs (Brew + Scoop)              (revertable PR)
```

Steps 1–7 are safely idempotent: a failure anywhere leaves the
release in a recoverable state. **PyPI publish (step 8) is the commit
point** — once it succeeds, the version number is permanently claimed
and you're forward-only.

### Recovery playbook by failure point

| Failed at | State | Action |
|---|---|---|
| Preflight (tests / version assertion) | Nothing published | Fix the code / bump the manifest / retag. Safe to reuse the same version number. |
| Cross-compile / sign | Nothing published | Re-run workflow; repro builds are deterministic so the second run produces identical hashes. |
| GH Release creation | Draft release may exist | Delete draft from GH UI; re-run workflow. |
| GHCR / Docker Hub push | Image tag may exist but unsigned | Re-run workflow; cosign will sign on the second pass. |
| PyPI publish (Python only) | Version claimed; SBOM/SLSA may be missing | **Cut `X.Y.Z+1` ASAP.** Don't try to re-publish the same version — PyPI will refuse. |
| Tap PR (Brew/Scoop) | Binaries are out; users via `curl \| sh` can install. `brew`/`scoop` users temporarily can't get the new version. | Manually re-run `brew-scoop-pr.yml` via workflow_dispatch, OR copy rendered manifests into tap repos by hand. |

### Stale draft cleanup

Failed runs may leave GH Release drafts or unreferenced GHCR tags.
No automated reaper today; check the Releases tab quarterly and
delete drafts older than 7 days that never went public.

## Release failure notifications

`release-alert.yml` listens to every tag-triggered release workflow
and opens a GH issue in this repo (labeled `release-failure`) if any
concludes with a non-success status. If an issue is already open for
the same ref, it comments rather than opening a duplicate. The
triage checklist in the issue body points back to this playbook.

No Slack/email integration yet — the label is watchable via GH
notifications; a webhook can be wired in post-v0.1.0 if needed.

## Secret-management policy

Every long-lived secret used by the release pipeline has a documented
rotation schedule + break-glass procedure:

| Secret | Used by | Scope | Rotation | Break-glass |
|---|---|---|---|---|
| `RELEASE_APP_ID` | `brew-scoop-pr.yml` | Public app ID (not a secret, but stored for convenience) | — | — |
| `RELEASE_APP_PRIVATE_KEY` | `brew-scoop-pr.yml` | GitHub App private key (`.pem`); mints installation tokens scoped to tap repos only | **Every 12 months.** New key via Settings → Developer settings → GitHub Apps → Fernsicht Release Bot → "Generate a private key". | Uninstall app from `homebrew-fernsicht` + `scoop-fernsicht` immediately. |
| `DOCKERHUB_USERNAME` + `DOCKERHUB_TOKEN` | `cli-docker.yml` | Docker Hub push access (username public; token is a `dckr_pat_*`) | **Every 90 days.** New token via Docker Hub → Account Settings → Security. | Revoke token in Docker Hub UI; push to GHCR still works (separate auth via `GITHUB_TOKEN`). |
| `GITHUB_TOKEN` | All workflows | Ephemeral per-job token minted by Actions | N/A (per-run) | N/A |

OIDC tokens (PyPI trusted publisher, cosign keyless, SLSA
provenance) are short-lived and minted on demand; nothing to rotate.

**If a secret is ever pasted in chat, a commit, a bug report, or a
screenshot — treat it as compromised and rotate immediately**, even
if the exposure was brief. Log scrapers are fast.

**Secret scanning** — enable GitHub's secret scanning (Settings →
Code security and analysis) + add a `gitleaks` CI step if we want
defense-in-depth against accidental commits.

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
