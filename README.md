<p align="center">
  <img src="./.github/assets/fernsight_icon.png" alt="Fernsicht logo" width="140" />
</p>

<h1 align="center">Fernsicht</h1>

<p align="center">
  Real-time remote progress monitoring over peer-to-peer WebRTC.
</p>

<p align="center">
  <a href="https://app.fernsicht.space">Live App</a>
  ·
  <a href="https://signal.fernsicht.space/healthz">Signal Health</a>
  ·
  <a href="https://ko-fi.com/fernsicht">Support on Ko-fi</a>
</p>

---

## What Fernsicht Does

Fernsicht lets you wrap any long-running loop and share live progress with anyone, on any device.

- **Sender**: your local script, training job, or data pipeline
- **Viewer**: any browser — phone, laptop, tablet
- **Transport**: WebRTC DataChannel (fully peer-to-peer after handshake)
- **Server role**: lightweight HTTP handshake only — no WebSockets, no persistent connections

## Quick Start

<details open>
<summary><b>CLI — wraps any command, in any language</b></summary>

### 1. Install
```bash
curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
```

### 2. Wrap any long-running command
```bash
fernsicht run -- python train.py
fernsicht run -- snakemake --cores 4
fernsicht run -- pip install pandas
```

No SDK, no code change. Auto-detects tqdm / pip / snakemake-style
progress; explicit progress via the `__fernsicht__` magic prefix from
any program.

See [`cli/README.md`](cli/README.md) and [`cli/docs/`](cli/docs/) for
flags, recipes, troubleshooting.
</details>

<details>
<summary><b>Python SDK</b></summary>

### 1. Install
```bash
pip install fernsicht
```

### 2. Wrap your loop
```python
import time
from fernsicht import blick

for _ in blick(range(100), desc="Training"):
    time.sleep(0.1)
```
</details>

<details>
<summary><b>R SDK</b></summary>

### 1. Install
```r
remotes::install_github("MuteJester/Fernsicht", subdir = "publishers/r")
```

### 2. Wrap your loop
```r
library(fernsicht)

result <- blick(1:100, function(i) {
  Sys.sleep(0.1)
  i * 2
}, label = "Training")
```
</details>

A shareable viewer URL is printed to your terminal:

```
Fernsicht: https://app.fernsicht.space/#room=<room_id>&role=viewer
```

Open that link on any device to watch progress live — elapsed time, ETA, items/sec, and count all update in real time.

## How It Works

1. **Sender** calls `POST /session` to register a room and get a secret.
2. **Sender** polls `GET /poll/{room}` every ~25 seconds (no persistent connection).
3. **Viewer** opens the link, creates a WebRTC offer, and posts it to `POST /watch`.
4. **Sender** picks up the offer on its next poll, creates an answer, and posts it back.
5. **ICE candidates** are exchanged via short-lived ticket endpoints.
6. **DataChannel opens** — progress streams directly P2P. The server forgets the ticket.

No WebSockets. No long-lived connections. The viewer creates the offer (viewer-offer-first), so the sender never needs to hold a socket open.

## Hosted Defaults

All publishers (CLI, Python SDK, R SDK) ship with these hosted
defaults — no signup, no config required:

- Session/signaling API: `https://signal.fernsicht.space`
- Viewer app: `https://app.fernsicht.space`

Override for self-hosting (or pointing at staging):

```bash
export FERNSICHT_SERVER_URL="https://your-signal-domain"
```

## Progress Data

Each progress update includes:

| Field | Example | Description |
|-------|---------|-------------|
| Fraction | `0.4523` | 0.0 to 1.0 |
| Elapsed | `12.3s` | Time since start |
| ETA | `~15s left` | Estimated time remaining |
| Count | `452 / 1,000` | Items completed / total |
| Rate | `36.7 it/s` | Items per second |
| Unit | `it`, `epochs`, `files` | Customizable label |

## Repository Layout

```text
cli/             Go CLI — `fernsicht run -- <command>` (no SDK, no code change)
bridge/          Shared Go bridge embedded by the CLI + future SDKs
frontend/        Viewer web app (Vite + TypeScript) → app.fernsicht.space
publishers/
  python/        Python SDK (pip install fernsicht)
  r/             R SDK (remotes::install_github("MuteJester/Fernsicht", subdir="publishers/r"))
PROTOCOL.md      DataChannel wire protocol
BRIDGE_PROTOCOL.md  Bridge ↔ host process protocol
SECURITY.md      Vulnerability disclosure policy
```

The signaling server lives in a separate repo:
[`fernsicht-server`](https://github.com/MuteJester/fernsicht-server).

## Support Fernsicht

Fernsicht is free and open source. If it saves you time, help keep the infrastructure running:

[Support on Ko-fi](https://ko-fi.com/fernsicht)

## License

Fernsicht is dual-licensed:

- **Open source**: [AGPL-3.0](./LICENSE) — free for open-source projects, research, and personal use. Any project using Fernsicht must also be open-sourced under AGPL-3.0.
- **Commercial**: companies and individuals who want to use Fernsicht in closed-source or proprietary products must purchase a commercial license.

For commercial licensing inquiries, contact **thomas.konstat@gmail.com**.

