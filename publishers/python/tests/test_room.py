"""Tests for room-id helpers."""

import pytest

from fernsicht._room import generate_room_id


def test_generate_room_id_default_shape() -> None:
    room_id = generate_room_id()
    assert len(room_id) == 24
    assert all(ch.isalnum() or ch in "-_" for ch in room_id)


def test_generate_room_id_custom_length() -> None:
    room_id = generate_room_id(32)
    assert len(room_id) == 32


def test_generate_room_id_rejects_short_length() -> None:
    with pytest.raises(ValueError):
        generate_room_id(7)
