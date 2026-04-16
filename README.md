# Fernsicht

![Fernsight Icon](./.github/assets/fernsight_icon.png)

Remote progress bar tracking. Wrap your loop, get a shareable URL, watch progress from anywhere.

```python
from fernsicht import blick

for item in blick(range(10000), desc="Training"):
    process(item)
# Prints: View progress at https://yoursite.github.io/fernsicht/#room=...&role=viewer
```

Open the link on your phone, second monitor, or share it with a colleague. The progress bar updates in real time.

## Hosted Defaults

By default, the Python wrapper bootstraps a session from:
- signaling: `wss://signal.fernsicht.space/ws`
- session bootstrap: `https://signal.fernsicht.space/session`

Override for self-hosting:

```bash
export FERNSICHT_SIGNALING_URL="wss://your-signal-domain/ws"
export FERNSICHT_SESSION_URL="https://your-signal-domain/session"
```

## How It Works

1. The Python wrapper joins a signaling room on your Fernsicht signaling server
2. The viewer (browser) joins the same room and performs a WebRTC handshake
3. Progress updates flow over a direct WebRTC DataChannel (P2P), bypassing the signaling server after handshake

No accounts. Minimal backend load (signaling only).

## Install

### Python
```bash
pip install fernsicht
```

## Architecture

```
publishers/     Per-language libraries (Python, JS, Rust, C, C++)
frontend/       Static web dashboard (shared by all publishers)
PROTOCOL.md     Wire protocol specification
```

All publishers implement the same protocol. See [PROTOCOL.md](PROTOCOL.md) for the full specification.

## License

MIT
