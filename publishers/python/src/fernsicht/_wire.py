"""Pipe-delimited Fernsicht wire message helpers (WebRTC DataChannel)."""

from __future__ import annotations


def _required(value: str, name: str) -> str:
    if not value:
        raise ValueError(f"{name} must not be empty")
    return value


def serialize_identity(peer_id: str) -> str:
    return f"ID|{_required(peer_id, 'peer_id')}"


def serialize_start(task_id: str, label: str) -> str:
    return f"START|{_required(task_id, 'task_id')}|{_required(label, 'label')}"


def serialize_progress(
    task_id: str,
    value: float,
    *,
    elapsed: float | None = None,
    eta: float | None = None,
    n: int | None = None,
    total: int | None = None,
    rate: float | None = None,
    unit: str = "it",
) -> str:
    clamped = max(0.0, min(1.0, value))
    parts = [
        "P",
        _required(task_id, "task_id"),
        f"{clamped:.4f}",
        f"{elapsed:.1f}" if elapsed is not None else "-",
        f"{eta:.1f}" if eta is not None else "-",
        str(n) if n is not None else "-",
        str(total) if total is not None else "-",
        f"{rate:.2f}" if rate is not None else "-",
        unit,
    ]
    return "|".join(parts)


def serialize_end(task_id: str) -> str:
    return f"END|{_required(task_id, 'task_id')}"


def serialize_keepalive() -> str:
    return "K"
