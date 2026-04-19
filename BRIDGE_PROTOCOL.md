# Fernsicht Bridge Protocol

The wire contract between language SDKs and `fernsicht-bridge`.

This document is the **public, normative spec** for SDK authors. The
bridge implementation is in [`bridge/`](bridge/); the matching wire
format the bridge speaks to viewers is in [`PROTOCOL.md`](PROTOCOL.md).

**Current version: 1**

---

## 1. Overview

`fernsicht-bridge` is a single Go binary that handles all WebRTC
signaling and DataChannel work for Fernsicht publishers. Language
SDKs spawn it as a subprocess and communicate over stdin/stdout
using newline-delimited JSON.

```
┌─────────────────────────────────────────────────────────┐
│  Publisher process (R script, Julia, Ruby, …)           │
│                                                         │
│  ┌──────────────────┐                                   │
│  │   Language SDK   │   spawns + IPC                    │
│  │  (~150 lines)    │ ────────────────┐                 │
│  └──────────────────┘                 │                 │
└─────────────────────────────────────────┼────────────────┘
                                          │
                                          ▼
┌─────────────────────────────────────────────────────────┐
│  fernsicht-bridge (Go subprocess)                       │
│    - parses JSON commands from stdin                    │
│    - emits JSON events on stdout                        │
│    - emits diagnostic logs to stderr                    │
│    - opens HTTP signaling + WebRTC peers internally     │
└─────────────────────────────────────────────────────────┘
```

The SDK never has to know about WebRTC, ICE, SCTP, DataChannels, or
any other protocol detail. It only has to read and write JSON lines.

---

## 2. Format

| Property | Value |
|---|---|
| Encoding | UTF-8 |
| Line delimiter | `\n` (the bridge also accepts `\r\n` for Windows SDKs) |
| Max line length | 64 KB; longer lines are rejected with `LINE_TOO_LONG` (parser stays alive) |
| Direction | SDK → bridge (commands) on stdin; bridge → SDK (events) on stdout |
| Diagnostics | Bridge writes only to **stderr** for diagnostics; **stdout is reserved for the event stream** |

Every command is a JSON object with an `op` field. Every event is a
JSON object with an `event` field. Both are one line each.

---

## 3. Required ordering

The first command MUST be `hello`. The bridge replies with
`hello_ack` (or a fatal `error`). After that, the SDK SHOULD send
`session` to open a publishing session, after which task commands
(`start`, `progress`, `end`) are accepted.

```
hello → hello_ack → session → session_ready → start → progress* → end → close
                                          ↘ viewer_joined / viewer_left (anytime after session)
                                          ↘ error (anytime; fatal=true → bridge exits)
```

`ping` is always allowed (even before `hello`, even after `close`).

Sending out-of-order commands gets you a non-fatal
`{"event":"error","code":"INVALID_COMMAND",…}` event.

---

## 4. Commands (SDK → bridge)

### `hello` — handshake (must be first)

```json
{"op":"hello","sdk":"r","sdk_version":"0.1.0","protocol":1}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `sdk` | string | yes | Short SDK identifier (`r`, `julia`, `ruby`, …). |
| `sdk_version` | string | yes | SDK semver. |
| `protocol` | integer | yes | Protocol version the SDK speaks. Bridge replies with `hello_ack` if it matches; otherwise fatal `PROTOCOL_VERSION_MISMATCH`. |

### `session` — open a publishing session

```json
{
  "op":"session",
  "base_url":"https://signal.fernsicht.space",
  "join_secret":"…optional…",
  "max_viewers":8,
  "session_token_ttl_sec":43200
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `base_url` | string | yes | Fernsicht signaling server URL. |
| `join_secret` | string | no | Required only if the server has `SENDER_JOIN_SECRET` configured. Sent as `X-Fernsicht-Api-Key` header. |
| `max_viewers` | integer | no | Cap; server enforces an absolute max. |
| `session_token_ttl_sec` | integer | no | Server may honor or ignore. |

The bridge POSTs to `/session`, parses the response, and starts polling
for viewer tickets in the background.

### `start` — emit a START frame

```json
{"op":"start","task_id":"t1","label":"Training"}
```

| Field | Type | Required |
|---|---|---|
| `task_id` | string | yes |
| `label` | string | yes (human-readable, shown to viewers) |

If a `start` arrives while another task is active, the bridge
**implicitly emits `END|<old>` then `START|<new>`** — the new task
takes over (mirrors the Python SDK).

### `progress` — emit a P frame

```json
{
  "op":"progress",
  "task_id":"t1",
  "value":0.42,
  "n":420,
  "total":1000,
  "rate":18.5,
  "elapsed":22.7,
  "eta":31.3,
  "unit":"ep"
}
```

| Field | Type | Required | Notes |
|---|---|---|---|
| `task_id` | string | yes | Must match the active task. |
| `value` | number | **yes** | Progress fraction 0..1; clamped if out of range. |
| `n` | integer | no | Items completed. |
| `total` | integer | no | Total items. |
| `rate` | number | no | Items/second. |
| `elapsed` | number | no | Seconds since start. |
| `eta` | number | no | Seconds remaining. |
| `unit` | string | no | Default `"it"`. |

`value=0` is valid (start of progress) and is distinct from a missing
field — the bridge requires `value` to be present.

### `end` — emit an END frame

```json
{"op":"end","task_id":"t1"}
```

Clears the active task. The bridge stays alive (publishers may start
a new task afterwards).

### `close` — graceful shutdown

```json
{"op":"close"}
```

Bridge sends `END|<active>` if a task is in flight, drains viewer
queues for up to ~2s, closes peer connections, emits `closed`, and
exits with code 0.

### `ping` — heartbeat

```json
{"op":"ping","id":"abc"}
```

Bridge immediately echoes `{"event":"pong","id":"abc"}`. The `id` is
opaque; SDKs use it to correlate concurrent pings if they need to.

---

## 5. Events (bridge → SDK)

### `hello_ack`

```json
{"event":"hello_ack","bridge_version":"v0.1.0","protocol":1}
```

### `session_ready`

```json
{
  "event":"session_ready",
  "room_id":"abc12345",
  "sender_secret":"…",
  "viewer_url":"https://app.fernsicht.space/#room=abc12345",
  "expires_at":"2026-04-19T12:00:00Z",
  "expires_in":43200,
  "max_viewers":8,
  "poll_interval_hint":25
}
```

The viewer_url is what the SDK should hand to the user. The
sender_secret is included for completeness; SDKs typically don't need
it (the bridge handles authentication internally).

### `viewer_joined` / `viewer_left`

```json
{"event":"viewer_joined","name":"vega"}
{"event":"viewer_left","name":"orion"}
```

Fires when a viewer's HELLO frame arrives or when their connection
ends (closed or culled after 20s of disconnect).

### `viewer_count`

```json
{"event":"viewer_count","count":2,"names":["vega","orion"]}
```

Sent on every presence change. Convenience event so SDKs don't have
to maintain their own roster from join/leave deltas. `names` is
always a JSON array (never `null`).

### `pong`

```json
{"event":"pong","id":"abc"}
```

Echo of the matching `ping`'s `id`.

### `error`

```json
{"event":"error","code":"NO_ACTIVE_TASK","message":"…","fatal":false}
```

| `code` | Meaning | Fatal? |
|---|---|---|
| `PROTOCOL_VERSION_MISMATCH` | SDK speaks an unsupported protocol version | yes |
| `INVALID_COMMAND` | Malformed JSON, unknown `op`, or wrong ordering | no |
| `LINE_TOO_LONG` | Stdin line exceeded 64 KB | no |
| `NO_ACTIVE_TASK` | `progress` or `end` arrived with no matching active task | no |
| `SESSION_FAILED` | Couldn't open session (bad URL, bad secret, etc.) | yes |
| `SESSION_EXPIRED` | Server returned token expired | yes |
| `SIGNALING_UNREACHABLE` | HTTP signaling failed for >5 min sustained | yes |
| `TICKET_HANDLING_FAILED` | Couldn't handle a viewer's offer | no |
| `INTERNAL` | Unexpected bridge bug (panic recovered) | yes |

`fatal:true` means the bridge will emit `closed` and exit shortly
after with a non-zero exit code. `fatal:false` is informational —
the bridge keeps running.

### `closed`

```json
{"event":"closed","reason":"sdk_close"}
```

Always the last event before exit.

| `reason` | Meaning |
|---|---|
| `sdk_close` | SDK sent `{"op":"close"}` |
| `stdin_eof` | SDK closed stdin (e.g., process exited) |
| `signal` | Bridge received SIGINT or SIGTERM |
| `fatal_error` | Unrecoverable error (preceded by `error{fatal:true}`) |

---

## 6. Edge case behaviors

The bridge enforces these explicitly so SDK authors don't have to
guess:

| Scenario | Bridge behavior |
|---|---|
| `progress` arrives before any `start` | Drop. Emit non-fatal `error` with code `NO_ACTIVE_TASK`. |
| `progress` for a `task_id` other than the active one | Drop. Emit non-fatal `error` with code `NO_ACTIVE_TASK`. |
| `start` arrives while a task is active | **Implicit END**: bridge broadcasts `END|<previous>` then `START|<new>`. No error. |
| `end` for an unknown / non-active `task_id` | Drop. Emit non-fatal `error`. |
| Concurrent / overlapping `task_id`s | Not supported. Each `start` replaces the previous via implicit END. |
| `session` while a session is already open | Reject with `INVALID_COMMAND`. (Multi-session per process is not supported.) |
| Any command other than `hello`/`ping` before `hello_ack` | Reject with `INVALID_COMMAND`. |
| `hello` arrives twice | Reject the second with `INVALID_COMMAND`. |
| `close` arrives more than once | First triggers shutdown; subsequent are silently ignored. |
| `progress` arrives during graceful close | Silently dropped (no error noise during teardown). |

---

## 7. Exit codes

| Code | Meaning |
|---|---|
| 0 | Clean shutdown via `close`, stdin EOF, or SIGINT/SIGTERM |
| 1 | Generic fatal error |
| 2 | Bad invocation (invalid CLI flags) |
| 3 | Could not open session at startup |
| 4 | Protocol version mismatch in `hello` |

---

## 8. Underlying wire format (passthrough — reference only)

The bridge speaks the existing pipe-delimited Fernsicht wire format
unchanged to viewers. SDK authors never see this — it's the bridge's
job to translate between the JSON command stream and these wire
frames. Documented for completeness:

| Frame | Direction | Purpose |
|---|---|---|
| `ID\|<peer>` | sender → viewers | sender identifies itself |
| `START\|<task>\|<label>` | sender → viewers | new task |
| `P\|<task>\|<value>\|<elapsed>\|<eta>\|<n>\|<total>\|<rate>\|<unit>` | sender → viewers | progress update |
| `END\|<task>` | sender → viewers | task complete |
| `V\|<name1>\|<name2>\|...` | sender → viewers | viewer presence list |
| `K` | both directions | keepalive |
| `HELLO\|<name>` | viewer → sender | viewer identifies itself |

See [`PROTOCOL.md`](PROTOCOL.md) for the canonical wire-format spec.

---

## 9. Versioning policy

The protocol version is an integer in the `hello` / `hello_ack`
exchange (currently `1`). The bridge bumps this on **breaking JSON
shape changes** (renamed/removed fields, semantically incompatible
behavior).

- **Backwards-compatible additions** (new optional fields, new event
  types) do not bump the protocol version. Old SDKs ignore unknown
  fields and unknown events; new SDKs ignore old bridges that don't
  emit new fields.
- **Breaking changes** bump the protocol version. The bridge rejects
  mismatched SDKs with a fatal `PROTOCOL_VERSION_MISMATCH` error.

The protocol will freeze at `1` upon the bridge's `v1.0.0` release.
Until then, treat minor bridge versions as potentially-breaking.

---

## 10. License

The bridge implementation is AGPL-3.0 with dual-licensing for
commercial use. The protocol itself (this document) is documented
informally; SDK implementations may use the protocol freely under
the same dual-license terms.
