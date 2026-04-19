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

# Up to 4 concurrent viewers:
for _ in blick(range(100), desc="Training", max_viewers=4):
    time.sleep(0.1)
```

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

The Python SDK uses these defaults out of the box:

- Session/signaling API: `https://signal.fernsicht.space`
- Viewer app: `https://app.fernsicht.space`

Override for self-hosting:

```bash
export FERNSICHT_SESSION_URL="https://your-signal-domain/session"
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
frontend/        Viewer web app (Vite + TypeScript)
publishers/
  python/        Python SDK (pip install fernsicht)
PROTOCOL.md      Wire protocol and message format
```

The signaling server lives in a separate repo: [`fernsicht-server`](https://github.com/MuteJester/fernsicht-server).

## Support Fernsicht

Fernsicht is free and open source. If it saves you time, help keep the infrastructure running:

[Support on Ko-fi](https://ko-fi.com/fernsicht)

## License

Fernsicht is dual-licensed:

- **Open source**: [AGPL-3.0](./LICENSE) — free for open-source projects, research, and personal use. Any project using Fernsicht must also be open-sourced under AGPL-3.0.
- **Commercial**: companies and individuals who want to use Fernsicht in closed-source or proprietary products must purchase a commercial license.

For commercial licensing inquiries, contact **thomas.konstat@gmail.com**.
