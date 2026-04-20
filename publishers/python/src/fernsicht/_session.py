"""Session bootstrap helpers for one-liner Fernsicht UX."""

from __future__ import annotations

import json
from dataclasses import dataclass
from urllib.request import Request, urlopen

DEFAULT_TIMEOUT_SEC = 5.0


class SessionBootstrapError(RuntimeError):
    """Raised when session bootstrap fails."""


@dataclass(frozen=True, slots=True)
class SessionInfo:
    """Response from ``POST /session``.

    ``sender_secret`` is the authentication credential the SDK sends as
    ``Authorization: Bearer`` on ``GET /poll/{room}`` and as a body field
    on ``POST /ticket/{id}/answer``.
    """

    room_id: str
    sender_secret: str
    viewer_url: str
    signaling_url: str
    expires_at: str | None
    expires_in: int | None
    max_viewers: int | None
    poll_interval_hint: int | None


def create_session(
    *,
    session_url: str,
    timeout_sec: float = DEFAULT_TIMEOUT_SEC,
    api_key: str | None = None,
    max_viewers: int | None = None,
) -> SessionInfo:
    """Create a session via the signaling bootstrap endpoint."""
    headers = {"Accept": "application/json"}
    if api_key:
        headers["X-Fernsicht-Api-Key"] = api_key

    body: bytes | None = None
    if max_viewers is not None:
        if isinstance(max_viewers, bool) or not isinstance(max_viewers, int):
            raise SessionBootstrapError("max_viewers must be an integer")
        if max_viewers < 1:
            raise SessionBootstrapError("max_viewers must be >= 1")
        body = json.dumps({"max_viewers": max_viewers}).encode("utf-8")
        headers["Content-Type"] = "application/json"

    req = Request(session_url, data=body, method="POST", headers=headers)
    try:
        with urlopen(req, timeout=timeout_sec) as response:  # nosec B310
            payload = response.read().decode("utf-8")
    except Exception as exc:
        raise SessionBootstrapError(f"failed to request session: {exc}") from exc

    try:
        data = json.loads(payload)
    except json.JSONDecodeError as exc:
        raise SessionBootstrapError("session endpoint returned invalid JSON") from exc

    room_id = data.get("room_id")
    sender_secret = data.get("sender_secret")
    viewer_url = data.get("viewer_url")
    signaling_url = data.get("signaling_url")
    expires_at = data.get("expires_at")
    expires_in = data.get("expires_in")
    max_viewers_value = data.get("max_viewers")
    poll_interval_hint = data.get("poll_interval_hint")

    if not isinstance(room_id, str) or not room_id:
        raise SessionBootstrapError("session response missing room_id")
    if not isinstance(sender_secret, str) or not sender_secret:
        raise SessionBootstrapError("session response missing sender_secret")
    if not isinstance(viewer_url, str) or not viewer_url:
        raise SessionBootstrapError("session response missing viewer_url")
    if not isinstance(signaling_url, str) or not signaling_url:
        raise SessionBootstrapError("session response missing signaling_url")
    if expires_at is not None and not isinstance(expires_at, str):
        raise SessionBootstrapError("session response contains invalid expires_at")
    if expires_in is not None and not isinstance(expires_in, int):
        raise SessionBootstrapError("session response contains invalid expires_in")
    if max_viewers_value is not None and not isinstance(max_viewers_value, int):
        raise SessionBootstrapError("session response contains invalid max_viewers")
    if poll_interval_hint is not None and not isinstance(poll_interval_hint, int):
        raise SessionBootstrapError("session response contains invalid poll_interval_hint")

    return SessionInfo(
        room_id=room_id,
        sender_secret=sender_secret,
        viewer_url=viewer_url,
        signaling_url=signaling_url,
        expires_at=expires_at,
        expires_in=expires_in,
        max_viewers=max_viewers_value,
        poll_interval_hint=poll_interval_hint,
    )
