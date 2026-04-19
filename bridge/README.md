# fernsicht-bridge

A language-agnostic WebRTC publishing daemon for [Fernsicht](https://app.fernsicht.space/).

`fernsicht-bridge` is a single, statically-linked Go binary that handles
all WebRTC signaling and DataChannel work for Fernsicht publishers.
Language SDKs (R, Julia, Ruby, MATLAB, …) ship the binary alongside
their package, spawn it as a subprocess, and exchange newline-delimited
JSON over stdin/stdout. The bridge takes care of:

- Opening a session against the Fernsicht signaling server
- Accepting viewer DataChannel handshakes via [pion/webrtc](https://github.com/pion/webrtc)
- Broadcasting your progress frames to every connected viewer
- Maintaining viewer presence (HELLO names + `viewer_joined` / `viewer_left` events)
- Graceful shutdown on stdin EOF, SIGINT/SIGTERM, or `{"op":"close"}`

The full JSON protocol is specified in
[`BRIDGE_PROTOCOL.md`](../BRIDGE_PROTOCOL.md) at the repo root.

---

## Why a bridge?

Reimplementing WebRTC per language is infeasible — Google's
`libwebrtc` is hundreds of thousands of lines, and even the
DataChannel-only subset (SDP + ICE + DTLS + SCTP) is months of work.
Most languages don't have a maintained native WebRTC binding (R,
Julia, Ruby, MATLAB, Lua, shell, …). Centralizing the WebRTC code
path in one Go binary unblocks all of them at once and means bugs and
security fixes happen in one place.

Each language SDK becomes a ~150-line wrapper that spawns the bridge
and translates the language's progress idioms into JSON commands.

---

## Install

### Pre-built binaries (recommended)

Download the binary for your platform from the
[GitHub releases page](https://github.com/MuteJester/Fernsicht/releases).
Five platforms are supported:

| Platform | Binary |
|---|---|
| Linux x86_64 | `fernsicht-bridge-linux-amd64` |
| Linux ARM64 | `fernsicht-bridge-linux-arm64` |
| macOS Intel | `fernsicht-bridge-darwin-amd64` |
| macOS Apple Silicon | `fernsicht-bridge-darwin-arm64` |
| Windows x86_64 | `fernsicht-bridge-windows-amd64.exe` |

Verify the SHA256 checksum against `SHA256SUMS` from the same release,
mark the file executable on Unix, and place it on your `$PATH`:

```bash
chmod +x fernsicht-bridge-linux-amd64
sudo mv fernsicht-bridge-linux-amd64 /usr/local/bin/fernsicht-bridge
fernsicht-bridge --version
```

### macOS Gatekeeper workaround

The macOS binaries are not yet code-signed by Apple. The first time
you run one, macOS will refuse with: *"fernsicht-bridge cannot be
opened because Apple cannot check it for malicious software."*

To allow it once:

1. Open **System Settings → Privacy & Security**.
2. Scroll down. You should see *"fernsicht-bridge was blocked from
   use because it is not from an identified developer."*
3. Click **Allow Anyway**.
4. Run the binary again. macOS will ask one more time and then
   remember your choice.

Alternative one-liner (skips the dialog):

```bash
xattr -d com.apple.quarantine /path/to/fernsicht-bridge-darwin-arm64
```

We may ship Apple-notarized binaries in a future release if there's
demand.

### Build from source

Requires Go 1.22+:

```bash
git clone https://github.com/MuteJester/Fernsicht.git
cd Fernsicht/bridge
make build
./dist/fernsicht-bridge --version
```

Cross-compile all five platform binaries with `make release` (output
in `dist/`).

---

## Quick start

The bridge speaks newline-delimited JSON. Every command starts with
`{"op":"…"}`; every event from the bridge starts with `{"event":"…"}`.
Each message is one line.

A minimal session looks like:

```bash
fernsicht-bridge << 'EOF'
{"op":"hello","sdk":"smoke","sdk_version":"0.0.0","protocol":1}
{"op":"session","base_url":"https://signal.fernsicht.space"}
{"op":"start","task_id":"t1","label":"Demo run"}
{"op":"progress","task_id":"t1","value":0.5,"n":50,"total":100}
{"op":"end","task_id":"t1"}
{"op":"close"}
EOF
```

Expected stdout:

```
{"event":"hello_ack","bridge_version":"v0.1.0","protocol":1}
{"event":"session_ready","room_id":"…","sender_secret":"…","viewer_url":"https://app.fernsicht.space/#room=…",…}
{"event":"viewer_joined","name":"vega"}
{"event":"viewer_count","count":1,"names":["vega"]}
{"event":"closed","reason":"sdk_close"}
```

(Viewer events arrive only when someone actually opens the viewer URL
in a browser.)

For the full protocol — every command, event, error code, and edge
case — see [`BRIDGE_PROTOCOL.md`](../BRIDGE_PROTOCOL.md).

---

## Driving the bridge from a language SDK

Pseudocode for an SDK wrapping the bridge:

```python
import json, subprocess

proc = subprocess.Popen(
    ["fernsicht-bridge"],
    stdin=subprocess.PIPE, stdout=subprocess.PIPE,
    text=True, bufsize=1,
)

def send(op, **kwargs):
    proc.stdin.write(json.dumps({"op": op, **kwargs}) + "\n")
    proc.stdin.flush()

def recv():
    return json.loads(proc.stdout.readline())

send("hello", sdk="example", sdk_version="0.1.0", protocol=1)
assert recv()["event"] == "hello_ack"

send("session", base_url="https://signal.fernsicht.space")
ready = recv()
print(f"Viewer at {ready['viewer_url']}")

send("start", task_id="t1", label="Training")
for n in range(100):
    send("progress", task_id="t1", value=n/100, n=n, total=100)
send("end", task_id="t1")
send("close")
```

Real SDKs run a separate event-reader thread/coroutine and dispatch
events to user callbacks. See `publishers/` in the Fernsicht repo for
language-specific implementations.

---

## Operational notes

### Logging

The bridge writes diagnostic logs to **stderr**. The level is
controllable:

```bash
FERNSICHT_BRIDGE_LOG=debug fernsicht-bridge
```

Levels: `debug`, `info`, `warn` (default), `error`, `silent`.
**stdout is reserved for the JSON event stream** — never write
diagnostic output there.

### Diagnostic dump

On Unix, sending `SIGUSR1` to a running bridge prints a runtime
snapshot to stderr:

```bash
kill -USR1 $(pgrep fernsicht-bridge)
```

Includes goroutine count, heap usage, and (in a future release)
active session, viewer roster, and pending queue depths.

### Exit codes

| Code | Meaning |
|---|---|
| 0 | Clean shutdown via `{"op":"close"}`, stdin EOF, or SIGINT/SIGTERM |
| 1 | Generic fatal error |
| 2 | Bad invocation (invalid CLI flags) |
| 3 | Could not open session at startup (bad URL, bad join_secret) |
| 4 | Protocol version mismatch in `hello` |

---

## Development

### Run tests

```bash
make test       # full unit + integration suite under -race
make vet        # go vet
```

### Build & verify static linking

```bash
make verify-cgo   # builds local platform binary, asserts no shared library deps
```

### Project layout

```
bridge/
  cmd/fernsicht-bridge/  — main.go entry point + signal helpers
  internal/
    proto/                — JSON command/event types + ordering validator
    wire/                 — pipe-delimited wire-frame serializers
    transport/            — HTTP signaling client + backoff
    peer/                 — pion peer-connection manager
    bridge/               — orchestrator (Run, dispatcher, poll loop)
  test/
    integration/          — end-to-end test (subprocess + fake server + pion viewer)
```

The architecture is documented in detail in
`.private/BRIDGE_IMPLEMENTATION_PLAN.md` (gitignored — internal
engineering notes).

---

## License

AGPL-3.0 with dual-licensing for commercial use. See the repo root
[`LICENSE`](../LICENSE) and Fernsicht's main README for details.

The bridge embeds [pion/webrtc](https://github.com/pion/webrtc) (MIT
license). All transitive Go dependencies are MIT or Apache-2.0
licensed; full acknowledgements are bundled in releases as
`THIRD_PARTY_LICENSES`.
