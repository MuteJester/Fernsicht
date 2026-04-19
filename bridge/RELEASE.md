# Releasing fernsicht-bridge

This document describes how to cut a new release of the bridge binary
and how to respond to security issues in pion (or any transitive
dependency).

The protocol contract lives in [`BRIDGE_PROTOCOL.md`](../BRIDGE_PROTOCOL.md);
breaking changes there require a protocol-version bump (`SupportedProtocolVersion`
in `internal/proto/proto.go`) AND a coordinated SDK release.

---

## Tagging convention

The bridge lives in a Go-module sub-directory of the Fernsicht
monorepo (`bridge/go.mod`). Per Go's nested-module conventions, tag
releases with a path-prefixed semver tag:

```
bridge/v0.1.0
bridge/v0.1.1
bridge/v0.2.0
```

This keeps bridge releases independent of frontend / Python SDK
releases that live in the same repo.

When `git describe --tags` is run from `bridge/`, it returns the
matching `bridge/v*.*.*` tag, which the Makefile embeds into the
binary via `-ldflags="-X main.version=…"`.

---

## Versioning policy

We follow [semver](https://semver.org/):

- **Patch** (`v0.1.0 → v0.1.1`): bug fixes, performance improvements,
  documentation. No protocol or behavior changes that would surprise
  an existing SDK.
- **Minor** (`v0.1.0 → v0.2.0`): backwards-compatible feature
  additions. New optional command fields, new event types, new
  optional `Options` fields. Existing SDKs continue to work.
- **Major** (`v0.x.y → v1.0.0` and beyond): breaking changes to the
  JSON protocol or the wire-frame contract. **Bumps
  `SupportedProtocolVersion`** so the bridge rejects mismatched SDKs
  with a clear error rather than silently misbehaving.

Pre-1.0 (`v0.x.y`): treat minor bumps as potentially breaking. We'll
freeze the protocol at `v1.0.0`.

---

## Release process

### 1. Verify clean state

```bash
cd bridge
make test                          # unit + integration; must pass
make vet                           # static analysis; must pass
git status                         # working tree clean; no uncommitted changes
```

### 2. Pick a version

Decide on the next version based on the changes since the last tag.

```bash
git log --oneline $(git describe --tags --abbrev=0 --match 'bridge/v*' 2>/dev/null || echo HEAD)..HEAD -- bridge/
```

### 3. Tag

```bash
git tag -a bridge/v0.1.0 -m "bridge: v0.1.0 — first release"
git push origin bridge/v0.1.0
```

### 4. Build the matrix

```bash
cd bridge
make release
```

This produces `dist/fernsicht-bridge-{linux,darwin,windows}-{amd64,arm64}{,.exe}`
plus `dist/SHA256SUMS`. All binaries:

- Built with `CGO_ENABLED=0` (statically linked, no libc dependency
  except on Darwin where Mach-O always links libSystem).
- Built with `-trimpath -ldflags="-s -w"` (reproducible; no source
  paths, no debug info).
- Embed the version from `git describe --tags --always --dirty`, so
  cutting from a clean tagged commit gives e.g. `bridge/v0.1.0`.

### 5. Verify

Spot-check at least the binaries you can run on your dev machine:

```bash
./dist/fernsicht-bridge-linux-amd64 --version
echo '{"op":"hello","sdk":"smoke","sdk_version":"0.0.0","protocol":1}' | \
    ./dist/fernsicht-bridge-linux-amd64
```

Confirm static linking on Linux:

```bash
file dist/fernsicht-bridge-linux-amd64    # should say "statically linked"
ldd  dist/fernsicht-bridge-linux-amd64    # should say "not a dynamic executable"
```

For platforms you can't run locally, at minimum check `file` output
identifies the right architecture and format (Mach-O for Darwin, PE
for Windows).

### 6. Create the GitHub release

Using the `gh` CLI (recommended):

```bash
cd bridge
gh release create bridge/v0.1.0 \
    --title "bridge v0.1.0" \
    --notes-file RELEASE_NOTES.md \
    dist/fernsicht-bridge-* \
    dist/SHA256SUMS
```

Or upload manually via the GitHub web UI: go to
**Releases → Draft a new release**, choose the `bridge/v0.1.0` tag,
upload all five binaries plus `SHA256SUMS`.

Release notes should cover:

- What's new (features, bug fixes, breaking changes)
- Protocol version (and whether it bumped)
- pion version embedded
- Verification snippet (download + sha256 check + smoke command)

### 7. Bump downstream SDKs (if applicable)

Each language SDK (publishers/python, future publishers/r, etc.)
ships a copy of the platform-matched binary. Open a PR in each
affected SDK package to bump the bundled binary, run their test
suite, and cut a coordinated SDK release.

---

## CVE response process

If a security issue is discovered in pion, in a transitive dependency,
or in the bridge itself:

### Immediate (within 24h of disclosure)

1. **Triage severity.** Does the issue affect the DataChannel-only
   path the bridge uses? Many WebRTC CVEs only affect media or
   server-side use.
2. **Confirm the fix is available** in upstream pion (or the
   relevant dep). If not, file an issue / PR upstream.

### Patch release (within 48h of upstream fix)

1. Bump the dependency:

   ```bash
   cd bridge
   go get -u github.com/pion/webrtc/v4@latest
   go mod tidy
   make test
   ```

2. **Patch release only** — bump the patch number (e.g.
   `bridge/v0.1.0 → bridge/v0.1.1`) and ship *only* the security fix.
   No other changes. This keeps the diff reviewable and minimizes
   regression risk.

3. Tag, build, release per the standard process above.

4. **Notify SDKs.** Coordinate with each language SDK to bump the
   bundled binary. Communicate via the Fernsicht repo issue tracker
   and any active SDK release channels.

5. Reference the upstream advisory (CVE ID, GHSA, etc.) in the
   release notes.

### Audit cadence

Without an active CVE, audit dependencies quarterly:

```bash
cd bridge
go list -u -m all                  # check for newer versions
govulncheck ./...                  # if installed
```

---

## Reproducible builds

Releases are reproducible: building from the same source on different
machines produces byte-identical binaries (modulo the embedded
version string).

Key flags ensuring this:

- `CGO_ENABLED=0` — no per-system C compiler involvement
- `-trimpath` — strips filesystem paths from the binary
- `-ldflags="-s -w"` — strips symbol and debug tables
- `-ldflags="-X main.version=..."` — only the version string varies,
  and it comes from `git describe`

To reproduce a specific release locally:

```bash
git checkout bridge/v0.1.0
cd bridge
make release
sha256sum dist/fernsicht-bridge-linux-amd64
# Should match the published SHA256SUMS line.
```

---

## License

The release artifacts are AGPL-3.0 licensed. Embedded dependencies
(pion/webrtc and transitives) are MIT or Apache-2.0; their notices
should ship as `THIRD_PARTY_LICENSES` in future releases (currently
listed in this `RELEASE.md` and accessible via `go mod download`).
