"""Message serialization for the Fernsicht wire protocol v1."""

from __future__ import annotations

import json
import time
from typing import Any

PROTOCOL_VERSION = 1
TOPIC_PREFIX = "fernsicht/runs"

ERROR_MESSAGE_MAX_LEN = 512


def build_topic(topic_id: str) -> str:
    """Build the full MQTT topic for a given topic ID."""
    return f"{TOPIC_PREFIX}/{topic_id}"


def _build_envelope(msg_type: str) -> dict[str, Any]:
    """Common envelope shared by all message types."""
    return {
        "type": msg_type,
        "v": PROTOCOL_VERSION,
        "ts": round(time.time(), 3),
    }


def _to_bytes(msg: dict[str, Any]) -> bytes:
    """Compact JSON serialization to UTF-8 bytes."""
    return json.dumps(msg, separators=(",", ":")).encode("utf-8")


def serialize_state(
    *,
    n: int,
    total: int | None,
    desc: str | None = None,
    unit: str = "it",
    rate: float | None = None,
    elapsed: float,
    done: bool = False,
) -> bytes:
    """Serialize a progress state to JSON bytes (protocol v1)."""
    msg = _build_envelope("progress")
    msg["n"] = n
    msg["total"] = total
    msg["elapsed"] = round(elapsed, 3)
    if desc is not None:
        msg["desc"] = desc
    if unit != "it":
        msg["unit"] = unit
    if rate is not None:
        msg["rate"] = round(rate, 2)
    if done:
        msg["done"] = True
    return _to_bytes(msg)


def serialize_heartbeat(*, elapsed: float) -> bytes:
    """Serialize a heartbeat (liveness signal)."""
    msg = _build_envelope("heartbeat")
    msg["elapsed"] = round(elapsed, 3)
    return _to_bytes(msg)


def serialize_error(*, error: str, message: str, fatal: bool) -> bytes:
    """Serialize an error message."""
    msg = _build_envelope("error")
    msg["error"] = error
    msg["message"] = message[:ERROR_MESSAGE_MAX_LEN]
    msg["fatal"] = fatal
    return _to_bytes(msg)


def serialize_session(
    *,
    desc: str | None = None,
    total: int | None,
    unit: str = "it",
    pub_lang: str = "python",
    pub_version: str,
) -> bytes:
    """Serialize a session init message (sent once at start)."""
    msg = _build_envelope("session")
    msg["total"] = total
    if desc is not None:
        msg["desc"] = desc
    if unit != "it":
        msg["unit"] = unit
    msg["pub"] = {"lang": pub_lang, "pkg_version": pub_version}
    return _to_bytes(msg)
