"""Room identifier helpers for Fernsicht sessions."""

from __future__ import annotations

import secrets

ROOM_ID_ALPHABET = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-"
DEFAULT_ROOM_ID_LEN = 24


def generate_room_id(length: int = DEFAULT_ROOM_ID_LEN) -> str:
    """Generate a random room ID compatible with server validation."""
    if length < 8:
        raise ValueError("length must be at least 8")
    return "".join(secrets.choice(ROOM_ID_ALPHABET) for _ in range(length))
