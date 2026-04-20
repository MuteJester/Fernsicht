"""Tests for the blick wrapper (WebRTC transport)."""

import pytest

import fernsicht._blick as blick_module
from fernsicht._blick import FernsichtBar, blick, manual
from fernsicht._session import SessionBootstrapError


def test_blick_iterates_all_items():
    items = list(range(10))
    result = list(blick(items, disable=True))
    assert result == items


def test_blick_tracks_count():
    bar = blick(range(5), disable=True)
    result = []
    for item in bar:
        result.append(item)
    assert bar.n == 5
    assert result == [0, 1, 2, 3, 4]


def test_blick_infers_total_from_list():
    bar = FernsichtBar([1, 2, 3], disable=True)
    assert bar.total == 3


def test_blick_explicit_total():
    bar = FernsichtBar(total=42, disable=True)
    assert bar.total == 42


def test_blick_no_total_for_generator():
    def gen():
        yield from range(3)
    bar = FernsichtBar(gen(), disable=True)
    assert bar.total is None


def test_manual_update():
    bar = manual(total=100, disable=True)
    assert bar.n == 0
    bar.update(25)
    assert bar.n == 25
    bar.update(25)
    assert bar.n == 50
    bar.close()


def test_context_manager():
    with blick(total=10, disable=True) as bar:
        for _ in range(10):
            bar.update(1)
    assert bar.n == 10


def test_set_description():
    bar = manual(total=10, disable=True)
    assert bar.desc is None
    bar.set_description("hello")
    assert bar.desc == "hello"
    bar.close()


def test_len_with_total():
    bar = FernsichtBar(range(42), disable=True)
    assert len(bar) == 42


def test_url_empty_when_disabled():
    bar = FernsichtBar(disable=True)
    assert bar.url == ""
    bar.close()


def test_iter_exception_reraises():
    """When the user's loop throws, blick re-raises the exception."""
    def bad_iterable():
        yield 1
        yield 2
        raise ValueError("boom")

    with pytest.raises(ValueError, match="boom"):
        for _ in blick(bad_iterable(), disable=True):
            pass


def test_iter_exception_closes_bar():
    """Bar is closed after an exception in the iterable."""
    def bad_iterable():
        yield 1
        raise RuntimeError("fail")

    bar = blick(bad_iterable(), disable=True)
    with pytest.raises(RuntimeError):
        for _ in bar:
            pass
    assert bar._closed is True


def test_context_manager_exception():
    """Context manager closes the bar even on exception."""
    with pytest.raises(ValueError):
        with blick(total=10, disable=True) as bar:
            bar.update(5)
            raise ValueError("test error")
    assert bar._closed is True


def test_bootstrap_failure_raises_by_default(monkeypatch: pytest.MonkeyPatch):
    def _raise_session_error(
        *,
        session_url: str,
        timeout_sec: float = 5.0,
        api_key: str | None = None,
        max_viewers: int | None = None,
    ):
        raise SessionBootstrapError("boom")

    monkeypatch.setattr(blick_module, "create_session", _raise_session_error)

    with pytest.raises(RuntimeError, match="session bootstrap failed"):
        FernsichtBar(total=1, disable=False)


def test_max_viewers_validation() -> None:
    with pytest.raises(ValueError, match="max_viewers must be >= 1"):
        FernsichtBar(total=1, disable=False, max_viewers=0)
