"""Session bootstrap helpers for one-liner Fernsicht UX."""

from __future__ import annotations

import json
from dataclasses import dataclass
from urllib.parse import urlsplit, urlunsplit
from urllib.request import Request, urlopen

DEFAULT_TIMEOUT_SEC = 5.0


class SessionBootstrapError(RuntimeError):
    """Raised when session bootstrap fails."""


@dataclass(frozen=True, slots=True)
class SessionInfo:
    room_id: str
    sender_token: str
    viewer_url: str
    signaling_url: str
    expires_at: str | None
    expires_in: int | None


def derive_session_url(signaling_url: str) -> str:
    """Derive HTTP(S) session endpoint from ws(s) signaling URL."""
    split = urlsplit(signaling_url.strip())
    if not split.scheme or not split.netloc:
        raise SessionBootstrapError("signaling_url must be an absolute URL")

    if split.scheme == "wss":
        scheme = "https"
    elif split.scheme == "ws":
        scheme = "http"
    elif split.scheme in {"https", "http"}:
        scheme = split.scheme
    else:
        raise SessionBootstrapError(
            f"unsupported signaling URL scheme '{split.scheme}'"
        )

    path = split.path or "/"
    if path.endswith("/ws"):
        session_path = f"{path[:-3]}/session" or "/session"
    elif path == "/":
        session_path = "/session"
    else:
        session_path = f"{path.rstrip('/')}/session"

    return urlunsplit((scheme, split.netloc, session_path, "", ""))


def create_session(
    *,
    session_url: str,
    timeout_sec: float = DEFAULT_TIMEOUT_SEC,
    api_key: str | None = None,
) -> SessionInfo:
    """Create a session via the signaling bootstrap endpoint."""
    headers = {"Accept": "application/json"}
    if api_key:
        headers["X-Fernsicht-Api-Key"] = api_key

    req = Request(session_url, method="POST", headers=headers)
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
    sender_token = data.get("sender_token")
    viewer_url = data.get("viewer_url")
    signaling_url = data.get("signaling_url")
    expires_at = data.get("expires_at")
    expires_in = data.get("expires_in")

    if not isinstance(room_id, str) or not room_id:
        raise SessionBootstrapError("session response missing room_id")
    if not isinstance(sender_token, str) or not sender_token:
        raise SessionBootstrapError("session response missing sender_token")
    if not isinstance(viewer_url, str) or not viewer_url:
        raise SessionBootstrapError("session response missing viewer_url")
    if not isinstance(signaling_url, str) or not signaling_url:
        raise SessionBootstrapError("session response missing signaling_url")
    if expires_at is not None and not isinstance(expires_at, str):
        raise SessionBootstrapError("session response contains invalid expires_at")
    if expires_in is not None and not isinstance(expires_in, int):
        raise SessionBootstrapError("session response contains invalid expires_in")

    return SessionInfo(
        room_id=room_id,
        sender_token=sender_token,
        viewer_url=viewer_url,
        signaling_url=signaling_url,
        expires_at=expires_at,
        expires_in=expires_in,
    )
