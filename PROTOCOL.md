# Fernsicht Wire Protocol v2 (WebRTC)

This document defines the live progress protocol used by Fernsicht publishers and the browser viewer.

## 1) Architecture

1. Sender (Python wrapper) and Viewer (browser) connect to the signaling server over WebSocket.
2. Signaling server matches peers by room and relays SDP/ICE signaling messages.
3. Sender and Viewer establish a WebRTC DataChannel.
4. Progress messages flow over DataChannel (P2P). The signaling server is not in the data path after handshake.

## Session Bootstrap (Recommended UX)

Before joining `/ws`, a publisher can create a session via:

```
POST /session
```

Response JSON includes:
- `room_id`
- `sender_token`
- `viewer_url`
- `signaling_url`
- `expires_at` / `expires_in`

## 2) Signaling (WebSocket)

Endpoint:

```text
GET /ws
```

First frame must be JOIN:

```text
JOIN|<room_id>|SENDER
JOIN|<room_id>|SENDER|<token>   # when server auth is enabled
JOIN|<room_id>|VIEWER
```

Rules:

- `room_id` character set: `[A-Za-z0-9_-]`
- `room_id` length: server-configurable (`ROOM_ID_MIN_LEN`..`ROOM_ID_MAX_LEN`)
- server may send:
  - `ERROR|ROLE_TAKEN`
  - `ERROR|SERVER_BUSY`
  - policy close on invalid joins

Server handshake helper:

- server sends `READY` to `SENDER` when both roles are present

SDP/ICE envelope format (JSON text frame):

```json
{"type":"offer","payload":{"type":"offer","sdp":"..."}}
{"type":"answer","payload":{"type":"answer","sdp":"..."}}
{"type":"ice","payload":{"candidate":"candidate:...","sdpMid":"0","sdpMLineIndex":0}}
```

## 3) DataChannel Messages (Pipe-delimited)

Transport:

- WebRTC DataChannel text frames (UTF-8)
- Channel label: `fernsicht`
- Ordered delivery

### Message Types

1. Identity:

```text
ID|<peer_id>
```

2. Task start:

```text
START|<task_id>|<label>
```

3. Progress fraction:

```text
P|<task_id>|<value>
```

Constraints:

- `value` must be in `[0, 1]`
- sender should format with 2 decimals (`0.00` to `1.00`)

4. Task end:

```text
END|<task_id>
```

5. Keepalive:

```text
K
```

6. Signaling helper (not rendered in UI):

```text
READY
```

## 4) Sender Sequence (recommended)

```text
ID -> START -> P* -> END
```

- send `K` periodically while idle
- support repeated/duplicate signaling frames defensively
- ignore malformed unknown data channel frames
