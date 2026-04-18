# Fernsicht (Python)

Remote progress tracking over WebRTC. Wrap your loop, get a shareable URL, watch from anywhere.

## Install

```bash
pip install fernsicht
```

## Quick Start

```python
from fernsicht import blick

# Wrap any iterable
for item in blick(range(10000), desc="Training"):
    process(item)

# Allow up to 4 concurrent viewers
for item in blick(range(10000), desc="Training", max_viewers=4):
    process(item)

# Manual updates
from fernsicht import manual

bar = manual(total=100, desc="Uploading")
for chunk in stream:
    upload(chunk)
    bar.update(len(chunk))
bar.close()

# Context manager
with blick(total=500, desc="Epochs") as bar:
    for batch in data_loader:
        train(batch)
        bar.update(len(batch))
```

A URL like `https://.../#room=<id>&role=viewer` is printed when the bar starts.
Open it on your phone or another machine to watch live progress including
elapsed time, ETA, rate, and item count.

## Configuration

No configuration is required for the default hosted node — the SDK points at
`https://signal.fernsicht.space` out of the box.

### Self-hosting

To use your own signaling server, set one of:

```bash
export FERNSICHT_SERVER_URL="https://your-signal-domain"
# or supply a direct session URL:
export FERNSICHT_SESSION_URL="https://your-signal-domain/session"
```

### Authenticated session endpoint

If your server requires an API key on `POST /session`:

```bash
export FERNSICHT_SESSION_API_KEY="your-api-key"
```

### Legacy env var

`FERNSICHT_SIGNALING_URL` is still accepted (for backwards compatibility with
old configs). A `wss://…/ws` value is automatically converted to `https://…`.

## Viewer limits

- Default is `max_viewers=1` (single concurrent viewer).
- Set `max_viewers > 1` for multi-viewer rooms.
- The server enforces an upper bound via `MAX_VIEWERS_PER_ROOM`.

## How it works

1. `blick()` calls `POST /session` on the signaling server, which creates a
   new room and returns a `sender_secret`.
2. A background thread polls `GET /poll/{room}?secret=…` every ~25 seconds
   to check for viewer connections.
3. When a viewer opens the link, it posts a WebRTC offer; the sender thread
   picks it up, creates an answer, and completes the handshake.
4. Progress data flows directly over the WebRTC DataChannel — the server is
   never in the data path.

No persistent connection is held by the SDK. Between polls, the background
thread is idle.
