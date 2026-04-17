# Fernsicht (Python)

Remote progress tracking over WebRTC. Wrap your loop, get a shareable URL, watch from anywhere.

## Install

```bash
pip install fernsicht
```

## Required Configuration

No configuration is required for the default hosted node.

Optional overrides for self-hosting:

```bash
export FERNSICHT_SIGNALING_URL="wss://your-signaling-domain/ws"
export FERNSICHT_SESSION_URL="https://your-signaling-domain/session"
```

Optional (only for legacy mode when session bootstrap is disabled):

```bash
export FERNSICHT_SENDER_TOKEN="your-sender-secret"
```

Optional (if server requires session endpoint API key):

```bash
export FERNSICHT_SESSION_API_KEY="your-api-key"
```

Optional (fallback only; disabled by default):

```bash
export FERNSICHT_ALLOW_LOCAL_FALLBACK=true
```

When enabled, failed session bootstrap falls back to local room generation.

## Usage

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
Open it on your phone or another machine to watch live progress.

Viewer cap notes:
- Default is `max_viewers=1` (single concurrent viewer).
- Set `max_viewers > 1` for multi-viewer rooms.
- Server enforces an upper bound via `MAX_VIEWERS_PER_ROOM`.
