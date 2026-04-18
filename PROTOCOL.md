# Fernsicht Wire Protocol V2

This document describes the Fernsicht protocol as implemented by the V2
connectionless signaling server, the Python SDK, and the browser viewer.

## 1. Architecture

1. Sender (Python SDK) calls `POST /session` on the signaling server. The
   server registers a room and returns a `sender_secret`.
2. Sender polls `GET /poll/{room_id}?secret=…` on a fixed interval.
3. Viewer (browser) creates a WebRTC offer locally, then `POST /watch` with
   the offer. The server returns a ticket ID.
4. Sender's next poll returns the pending ticket with the viewer's offer.
5. Sender creates an answer, `POST /ticket/{id}/answer` with the answer SDP.
6. Viewer polls `GET /ticket/{id}/answer` until it receives the answer.
7. Both sides exchange ICE candidates via `POST|GET /ticket/{id}/ice/sender`
   and `POST|GET /ticket/{id}/ice/viewer`.
8. WebRTC DataChannel opens. Progress data flows P2P, bypassing the server.

There is no persistent socket on either side. The server holds only the room
registry and short-lived tickets (default TTL 25s).

## 2. HTTP Endpoints

All endpoints are on the signaling server (default `https://signal.fernsicht.space`).

### `POST /session`

Create a new room. Optional JSON body:

```json
{"max_viewers": 4}
```

Optional header: `X-Fernsicht-Api-Key` if the server requires it.

Response:

```json
{
  "room_id": "abc...",
  "sender_token": "v2.<exp>.<max_viewers>.<hmac>",
  "sender_secret": "<base64url 16 bytes>",
  "viewer_url": "https://app.fernsicht.space/#room=abc...&role=viewer",
  "signaling_url": "https://signal.fernsicht.space",
  "expires_at": "2026-04-18T12:00:00Z",
  "expires_in": 43200,
  "max_viewers": 1,
  "poll_interval_hint": 25
}
```

### `GET /poll/{room_id}?secret=<b64>`

Sender polls for pending viewer tickets. Requires the `sender_secret` from
session creation.

Response:

```json
{
  "tickets": [
    {"ticket_id": "abc...", "offer": {"type": "offer", "sdp": "…"}}
  ]
}
```

### `POST /watch`

Viewer submits an offer to join a room.

Body:

```json
{"room_id": "abc...", "offer": {"type": "offer", "sdp": "…"}}
```

Response: `{"ticket_id": "…", "status": "queued", "ttl": 25}`.
Returns 429 with `Retry-After` header when room or server is at capacity.

### Ticket exchange

| Method | Path                          | Caller | Purpose                                     |
|--------|-------------------------------|--------|---------------------------------------------|
| POST   | `/ticket/{id}/answer`         | Sender | Submit SDP answer (requires `secret`)       |
| GET    | `/ticket/{id}/answer`         | Viewer | Poll for SDP answer                         |
| POST   | `/ticket/{id}/ice/sender`     | Sender | Submit ICE candidates (requires `secret`)   |
| GET    | `/ticket/{id}/ice/sender`     | Viewer | Fetch sender's ICE candidates               |
| POST   | `/ticket/{id}/ice/viewer`     | Viewer | Submit ICE candidates                       |
| GET    | `/ticket/{id}/ice/viewer`     | Sender | Fetch viewer's ICE candidates               |

ICE GET endpoints accept `?since=N` for incremental polling.

## 3. Room IDs

- Character set: `[A-Za-z0-9_-]`
- Length: server-configurable (`ROOM_ID_MIN_LEN` … `ROOM_ID_MAX_LEN`)

## 4. WebRTC Role Reversal

In V2 the **viewer creates the offer** and the DataChannel, and the sender
creates the answer. This allows the sender to remain connectionless — it only
reaches out to the server on its poll schedule.

DataChannel label: `fernsicht`. Ordered delivery.

## 5. DataChannel Messages (Pipe-delimited UTF-8)

### Identity

```
ID|<peer_id>
```

Emitted by the sender once the DataChannel opens.

### Task start

```
START|<task_id>|<label>
```

### Progress

```
P|<task_id>|<value>|<elapsed>|<eta>|<n>|<total>|<rate>|<unit>
```

| Field     | Format                        | Notes                                     |
|-----------|-------------------------------|-------------------------------------------|
| `value`   | Float `0.0000` – `1.0000`     | 4-decimal fraction                        |
| `elapsed` | Float seconds (1 decimal) or `-` | Time since task start                   |
| `eta`     | Float seconds (1 decimal) or `-` | Estimated seconds remaining             |
| `n`       | Integer or `-`                 | Items completed                          |
| `total`   | Integer or `-`                 | Total items (if known)                   |
| `rate`    | Float (2 decimals) or `-`      | Items per second                         |
| `unit`    | String                         | `it`, `epochs`, `files`, etc.            |

Fields after `value` are optional. Parsers must treat `-` as "unknown".

### Task end

```
END|<task_id>
```

### Keepalive

```
K
```

Sent periodically (every ~20 seconds) to keep the DataChannel warm.

## 6. Sender Sequence

```
ID → START → P* → END
```

Senders should send `K` while idle. Viewers must tolerate duplicate or
malformed frames defensively and ignore anything they don't recognise.
