# Security Policy

Fernsicht ships software that runs on user machines (the publisher
SDKs, the bridge, the CLI) and in browsers (the viewer page). We
take security reports seriously and aim to acknowledge within 48
hours.

## Reporting a vulnerability

Please **do not** open a public GitHub issue.

Email: **thomaskon90@gmail.com**

If you'd like to use encrypted email, ask first and we'll exchange
keys. Plain email is acceptable — most reports we've received haven't
needed encryption.

Include in the report:

- Component (bridge / CLI / signaling server / publisher SDK / viewer)
- Version (`fernsicht --version`, package version, or commit SHA)
- Steps to reproduce
- Impact assessment (your view of the worst-case)
- Whether you've published the finding anywhere

## Disclosure timeline

| Timeline | What happens |
|---|---|
| **0 days** | You email us. |
| **48 hours** | We acknowledge receipt and ask follow-up questions. |
| **7 days** | We confirm reproducibility and severity, share initial fix plan. |
| **30 days** | Fix shipped in a patch release (typical case; complex issues may take longer — we'll communicate). |
| **30–90 days** | Coordinated public disclosure. We credit you in the release notes unless you prefer to stay anonymous. |

If you haven't heard back within 48 hours, please email again — we
might have missed it.

## Scope

In scope:

- The bridge (`bridge/`) — Go subprocess that ships in CLI + future SDKs.
- The CLI (`cli/`) — `fernsicht run` and friends.
- The signaling server (`fernsicht-server*`) — the rendezvous service
  at `signal.fernsicht.space`.
- The viewer page (`frontend/`) — the browser-side WebRTC client.
- Publisher SDKs (`publishers/python/`, `publishers/r/`).
- Distribution paths: `install.sh`, GitHub releases, package-manager
  artifacts.

Out of scope (please report to the upstream project instead):

- Bugs in `pion/webrtc/v4` or other bundled dependencies (report to
  the relevant maintainer; we'll bump our pin once a fix exists).
- Issues in user code that wraps Fernsicht.

## What we consider a vulnerability

- Remote code execution.
- Privilege escalation.
- Auth bypass on the signaling server.
- Information disclosure: leaks of the room's `sender_secret`,
  unauthorized viewer access, log files containing credentials.
- DoS that breaks all sessions on the signaling server (capacity
  attacks against a single room are a known limitation, not a vuln).
- Supply-chain integrity: tampering with release binaries, install.sh,
  or GitHub release artifacts.

## What we DON'T consider a vulnerability

- Theoretical attacks requiring an attacker who already controls the
  user's terminal / wrapped command's stdout (they own the room
  anyway).
- Magic-prefix injection from a wrapped command. By design the
  wrapped command's stdout drives the bridge; if a tool prints
  `__fernsicht__` lines, the CLI honours them. The user already
  trusts the command they invoked.
- Best-effort warnings (`[fernsicht] warn:`) being displayed to the
  user when something unexpected happens. That's by design.
- Cosmetic ANSI rendering issues.
- Browser-extension interactions with the viewer page.

## Verifying release artifacts

All CLI release binaries are signed with cosign keyless via GitHub
Actions OIDC + Sigstore. See [`cli/RELEASE.md`](cli/RELEASE.md) for
verification commands.

If you have evidence a published release artifact has been tampered
with (e.g., the cosign cert subject doesn't match this repository),
treat that as a P0 vulnerability and report it via the email above
immediately.

## Rotating signing identity

Cosign keyless ties signatures to GitHub workflow identities. If our
signing identity rotates (e.g., we move to a new GH org), we'll:

1. Publish a `SECURITY-NOTICE.md` in the repo root for at least 90
   days indicating the change.
2. Update `cli/RELEASE.md`'s verification commands.
3. Pin the change in our next release notes.

## Hall of fame

Credits for past vulnerability reports go in the release notes for
the patched version. If you'd like to be listed, mention it in your
report.

(Currently empty — we'd love to add your name if you find something.)
