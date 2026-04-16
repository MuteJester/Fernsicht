"""blick — remote progress bar wrapper for Fernsicht."""

from __future__ import annotations

import os
import sys
import time
from typing import Generic, Iterable, Iterator, TypeVar

from fernsicht._crypto import generate_topic_id
from fernsicht._session import SessionBootstrapError, create_session, derive_session_url
from fernsicht._transport import Transport
from fernsicht._url import build_url

T = TypeVar("T")

SIGNALING_URL_ENV = "FERNSICHT_SIGNALING_URL"
SESSION_URL_ENV = "FERNSICHT_SESSION_URL"
SESSION_API_KEY_ENV = "FERNSICHT_SESSION_API_KEY"
SENDER_TOKEN_ENV = "FERNSICHT_SENDER_TOKEN"
DEFAULT_SIGNALING_URL = "wss://signal.fernsicht.space/ws"


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
        file: object = None,
        base_url: str | None = None,
        signaling_url: str | None = None,
        session_url: str | None = None,
        sender_token: str | None = None,
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

        if disable:
            self._transport = None
            self._room_id = ""
            self._url = ""
            return

        resolved_signaling_url = (
            signaling_url or os.getenv(SIGNALING_URL_ENV) or DEFAULT_SIGNALING_URL
        )
        resolved_sender_token = sender_token or os.getenv(SENDER_TOKEN_ENV)
        resolved_api_key = os.getenv(SESSION_API_KEY_ENV)

        self._room_id = ""
        self._url = ""

        if resolved_sender_token is None:
            chosen_session_url = (
                session_url
                or os.getenv(SESSION_URL_ENV)
                or derive_session_url(resolved_signaling_url)
            )
            try:
                session = create_session(
                    session_url=chosen_session_url,
                    api_key=resolved_api_key,
                )
                self._room_id = session.room_id
                resolved_sender_token = session.sender_token
                self._url = session.viewer_url
                if signaling_url is None:
                    resolved_signaling_url = session.signaling_url
            except SessionBootstrapError as exc:
                print(
                    (
                        f"\n  Fernsicht session bootstrap failed ({exc}). "
                        "Falling back to local room generation.\n"
                    ),
                    file=self._file,
                    flush=True,
                )

        if not self._room_id:
            self._room_id = generate_topic_id()

        if base_url:
            self._url = build_url(self._room_id, base_url=base_url)
        elif not self._url:
            self._url = build_url(self._room_id)

        # Start the background WebRTC sender.
        self._transport = Transport(
            self._room_id,
            start_time=self._start_time,
            desc=self._desc,
            total=self._total,
            unit=self._unit,
            signaling_url=resolved_signaling_url,
            sender_token=resolved_sender_token,
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
    **kwargs: object,
) -> FernsichtBar[T]:
    """Create a remote progress bar. Wrap any iterable to track it live."""
    return FernsichtBar(
        iterable=iterable, desc=desc, total=total, unit=unit,
        disable=disable, **kwargs,  # type: ignore[arg-type]
    )


def manual(
    total: int | None = None,
    desc: str | None = None,
    unit: str = "it",
    **kwargs: object,
) -> FernsichtBar:
    """Create a manual-update progress bar (no iterable)."""
    return FernsichtBar(
        iterable=None, desc=desc, total=total, unit=unit, **kwargs,  # type: ignore[arg-type]
    )
