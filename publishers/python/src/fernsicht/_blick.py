"""blick — remote progress bar wrapper for Fernsicht."""

from __future__ import annotations

import os
import sys
import time
from typing import Any, Generic, Iterable, Iterator, TypeVar

from fernsicht._session import SessionBootstrapError, create_session
from fernsicht._transport import Transport

T = TypeVar("T")

SERVER_URL_ENV = "FERNSICHT_SERVER_URL"
SIGNALING_URL_ENV = "FERNSICHT_SIGNALING_URL"  # legacy alias
SESSION_URL_ENV = "FERNSICHT_SESSION_URL"
SESSION_API_KEY_ENV = "FERNSICHT_SESSION_API_KEY"
DEFAULT_SERVER_URL = "https://signal.fernsicht.space"


def _normalize_legacy_ws_url(url: str | None) -> str | None:
    """Convert legacy wss:// signaling URLs to https:// server URLs."""
    if not url:
        return None
    u = url.strip()
    if not u:
        return None
    if u.startswith("wss://"):
        u = "https://" + u[len("wss://"):]
    elif u.startswith("ws://"):
        u = "http://" + u[len("ws://"):]
    # Strip trailing /ws if present
    if u.endswith("/ws"):
        u = u[:-3]
    return u


def _normalize_max_viewers(
    *,
    max_viewers: int,
    multi_viewer: int | None,
) -> int:
    resolved = max_viewers
    if multi_viewer is not None:
        if max_viewers != 1 and multi_viewer != max_viewers:
            raise ValueError("max_viewers and multi_viewer must match when both are set")
        resolved = multi_viewer

    if isinstance(resolved, bool) or not isinstance(resolved, int):
        raise ValueError("max_viewers must be an integer")
    if resolved < 1:
        raise ValueError("max_viewers must be >= 1")
    return resolved


class FernsichtBar(Generic[T]):
    """A progress bar that publishes updates remotely.

    Usage::

        from fernsicht import blick
        for item in blick(range(1000), desc="Processing"):
            process(item)

        # Or with context manager:
        with blick(total=500) as bar:
            for batch in loader:
                train(batch)
                bar.update(len(batch))
    """

    def __init__(
        self,
        iterable: Iterable[T] | None = None,
        desc: str | None = None,
        total: int | None = None,
        unit: str = "it",
        disable: bool = False,
        file: Any = None,
        server_url: str | None = None,
        session_url: str | None = None,
        max_viewers: int = 1,
        multi_viewer: int | None = None,
    ) -> None:
        self._iterable = iterable
        self._desc = desc
        self._unit = unit
        self._disable = disable
        self._file = file or sys.stderr

        # Infer total from iterable if possible
        if total is not None:
            self._total = total
        elif iterable is not None and hasattr(iterable, "__len__"):
            self._total = len(iterable)  # type: ignore[arg-type]
        else:
            self._total = None

        # Progress state
        self._n = 0
        self._start_time = time.monotonic()
        self._closed = False
        self._transport = None

        if disable:
            self._room_id = ""
            self._url = ""
            return

        resolved_max_viewers = _normalize_max_viewers(
            max_viewers=max_viewers,
            multi_viewer=multi_viewer,
        )

        resolved_server_url = (
            server_url
            or os.getenv(SERVER_URL_ENV)
            or _normalize_legacy_ws_url(os.getenv(SIGNALING_URL_ENV))
            or DEFAULT_SERVER_URL
        )
        resolved_api_key = os.getenv(SESSION_API_KEY_ENV)

        chosen_session_url = (
            session_url
            or os.getenv(SESSION_URL_ENV)
            or f"{resolved_server_url.rstrip('/')}/session"
        )

        try:
            session = create_session(
                session_url=chosen_session_url,
                api_key=resolved_api_key,
                max_viewers=resolved_max_viewers,
            )
        except SessionBootstrapError as exc:
            raise RuntimeError(
                f"Fernsicht session bootstrap failed: {exc}. "
                "Check FERNSICHT_SERVER_URL and network connectivity."
            ) from exc

        self._room_id = session.room_id
        self._url = session.viewer_url

        poll_interval = session.poll_interval_hint or 25

        # Start the background WebRTC sender.
        self._transport = Transport(
            self._room_id,
            start_time=self._start_time,
            desc=self._desc,
            total=self._total,
            unit=self._unit,
            base_url=session.signaling_url or resolved_server_url,
            sender_secret=session.sender_secret,
            poll_interval_sec=float(poll_interval),
        )

        # Print the tracking URL
        print(f"\n  Fernsicht: {self._url}\n", file=self._file, flush=True)

    @property
    def n(self) -> int:
        return self._n

    @property
    def total(self) -> int | None:
        return self._total

    @property
    def url(self) -> str:
        return self._url

    @property
    def desc(self) -> str | None:
        return self._desc

    def _elapsed(self) -> float:
        return time.monotonic() - self._start_time

    def _rate(self) -> float | None:
        elapsed = self._elapsed()
        if elapsed <= 0:
            return None
        return self._n / elapsed

    def _post_update(self) -> None:
        """Send current state to the transport."""
        if self._transport is None:
            return
        self._transport.post(
            n=self._n,
            total=self._total,
            desc=self._desc,
            unit=self._unit,
            rate=self._rate(),
            elapsed=self._elapsed(),
        )

    def update(self, n: int = 1) -> None:
        """Increment the progress counter by n."""
        if self._closed:
            return
        self._n += n
        self._post_update()

    def set_description(self, desc: str | None = None) -> None:
        """Update the description."""
        self._desc = desc

    def close(self) -> None:
        """Send the final message and shut down the transport."""
        if self._closed:
            return
        self._closed = True
        if self._transport is not None:
            self._transport.close(
                n=self._n,
                total=self._total,
                desc=self._desc,
                unit=self._unit,
                rate=self._rate(),
                elapsed=self._elapsed(),
            )

    def __iter__(self) -> Iterator[T]:
        if self._iterable is None:
            raise TypeError("Cannot iterate: no iterable was provided")
        try:
            for item in self._iterable:
                yield item
                self.update(1)
        except Exception as exc:
            if self._transport is not None:
                self._transport.send_error(
                    error=type(exc).__name__,
                    message=str(exc),
                    fatal=True,
                )
            self.close()
            raise
        else:
            self.close()

    def __enter__(self) -> FernsichtBar[T]:
        return self

    def __exit__(
        self,
        exc_type: type[BaseException] | None,
        exc_val: BaseException | None,
        exc_tb: object,
    ) -> None:
        if exc_val is not None and self._transport is not None:
            self._transport.send_error(
                error=type(exc_val).__name__,
                message=str(exc_val),
                fatal=True,
            )
        self.close()

    def __del__(self) -> None:
        self.close()

    def __len__(self) -> int:
        if self._total is not None:
            return self._total
        raise TypeError("Length unknown: total was not set")


def blick(
    iterable: Iterable[T] | None = None,
    desc: str | None = None,
    total: int | None = None,
    unit: str = "it",
    disable: bool = False,
    max_viewers: int = 1,
    multi_viewer: int | None = None,
    **kwargs: object,
) -> FernsichtBar[T]:
    """Create a remote progress bar. Wrap any iterable to track it live."""
    return FernsichtBar(
        iterable=iterable, desc=desc, total=total, unit=unit,
        disable=disable,
        max_viewers=max_viewers,
        multi_viewer=multi_viewer,
        **kwargs,  # type: ignore[arg-type]
    )


def manual(
    total: int | None = None,
    desc: str | None = None,
    unit: str = "it",
    max_viewers: int = 1,
    multi_viewer: int | None = None,
    **kwargs: object,
) -> FernsichtBar:
    """Create a manual-update progress bar (no iterable)."""
    return FernsichtBar(
        iterable=None,
        desc=desc,
        total=total,
        unit=unit,
        max_viewers=max_viewers,
        multi_viewer=multi_viewer,
        **kwargs,  # type: ignore[arg-type]
    )
