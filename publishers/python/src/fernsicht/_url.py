"""Shareable URL construction for Fernsicht WebRTC rooms."""

from __future__ import annotations

DEFAULT_BASE_URL = "https://fernsicht.github.io/fernsicht/"


def build_url(
    room_id: str,
    base_url: str = DEFAULT_BASE_URL,
) -> str:
    """Build the shareable viewer URL for a room."""
    # Strip trailing slash from base for clean join
    base = base_url.rstrip("/")
    return f"{base}/#room={room_id}&role=viewer"
