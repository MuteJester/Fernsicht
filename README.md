<p align="center">
  <img src="./.github/assets/fernsight_icon.png" alt="Fernsicht logo" width="140" />
</p>

<h1 align="center">Fernsicht</h1>

<p align="center">
  Real-time remote progress monitoring over peer-to-peer WebRTC.
</p>

<p align="center">
  <a href="https://app.fernsicht.space">Viewer</a>
  ·
  <a href="https://signal.fernsicht.space/healthz">Signal Health</a>
  ·
  <a href="./PROTOCOL.md">Protocol</a>
  ·
  <a href="https://ko-fi.com/fernsicht">Support on Ko-fi</a>
</p>

---

## What Fernsicht Does

Fernsicht lets you wrap long-running loops and share live progress from any device.

- Sender: your local script or job
- Viewer: browser on phone, laptop, or another network
- Transport: WebRTC DataChannel (P2P after handshake)
- Backend role: signaling/bootstrap only

This keeps latency low and server costs minimal.

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

The sender prints a shareable viewer URL, for example:

`https://app.fernsicht.space/#room=<room_id>&role=viewer`

Open that link on any device to watch progress live.

## Hosted Defaults

The Python wrapper uses these defaults out of the box:

- Signaling WebSocket: `wss://signal.fernsicht.space/ws`
- Session bootstrap API: `https://signal.fernsicht.space/session`
- Viewer base URL: `https://app.fernsicht.space/`

Override for self-hosting:

```bash
export FERNSICHT_SIGNALING_URL="wss://your-signal-domain/ws"
export FERNSICHT_SESSION_URL="https://your-signal-domain/session"
```

## How It Works

1. Sender requests a session from the signaling server.
2. Sender and viewer join the same room.
3. WebRTC handshake is exchanged via signaling.
4. Progress messages flow directly P2P over DataChannel.

After handshake, progress traffic bypasses the signaling server.

## Repository Layout

```text
publishers/      Language SDKs (Python, JS, Rust, C, C++)
frontend/        Static viewer application
PROTOCOL.md      Wire protocol and message format
```

## Support Fernsicht

Fernsicht is community-first and open source.

If it saves you time, you can help keep the domain, signaling node, and uptime running:

- `https://ko-fi.com/fernsicht`

## License

MIT
