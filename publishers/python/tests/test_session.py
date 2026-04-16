"""Tests for session bootstrap helpers."""

from __future__ import annotations

import pytest

from fernsicht._session import SessionBootstrapError, create_session, derive_session_url


def test_derive_session_url_from_wss() -> None:
    assert (
        derive_session_url("wss://signal.fernsicht.space/ws")
        == "https://signal.fernsicht.space/session"
    )


def test_derive_session_url_from_ws_subpath() -> None:
    assert derive_session_url("ws://localhost:8080/api/ws") == "http://localhost:8080/api/session"


def test_derive_session_url_rejects_invalid_scheme() -> None:
    with pytest.raises(SessionBootstrapError):
        derive_session_url("ftp://example.com/ws")


def test_create_session_parses_payload(monkeypatch: pytest.MonkeyPatch) -> None:
    class _FakeResponse:
        def __enter__(self) -> _FakeResponse:
            return self

        def __exit__(self, exc_type, exc, tb) -> None:
            return None

        def read(self) -> bytes:
            return (
                b'{"room_id":"abc123","sender_token":"tok","viewer_url":"https://viewer/#room=abc123&role=viewer",'
                b'"signaling_url":"wss://signal.fernsicht.space/ws","expires_at":"2026-01-01T00:00:00Z","expires_in":60}'
            )

    monkeypatch.setattr("fernsicht._session.urlopen", lambda *_args, **_kwargs: _FakeResponse())

    info = create_session(session_url="https://signal.fernsicht.space/session")
    assert info.room_id == "abc123"
    assert info.sender_token == "tok"
    assert info.signaling_url == "wss://signal.fernsicht.space/ws"


def test_create_session_rejects_missing_fields(monkeypatch: pytest.MonkeyPatch) -> None:
    class _FakeResponse:
        def __enter__(self) -> _FakeResponse:
            return self

        def __exit__(self, exc_type, exc, tb) -> None:
            return None

        def read(self) -> bytes:
            return b'{"room_id":"abc123"}'

    monkeypatch.setattr("fernsicht._session.urlopen", lambda *_args, **_kwargs: _FakeResponse())

    with pytest.raises(SessionBootstrapError):
        create_session(session_url="https://signal.fernsicht.space/session")
