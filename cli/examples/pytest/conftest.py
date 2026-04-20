"""Fernsicht magic-prefix integration for pytest.

Drop this file alongside your tests (pytest auto-discovers it).
When run under `fernsicht run -- pytest ...`, viewers see one
progress tick per completed test, with the suite size as the total.

Without fernsicht wrapping, the magic-prefix lines just look like
ordinary stdout — they're harmless and don't affect pytest's own
output. (You can grep them out with `2>&1 | grep -v __fernsicht__`
if you really want.)
"""

from __future__ import annotations

import sys


def _emit(line: str) -> None:
    """Write to stdout, flush — pytest captures stdout, but the
    magic prefix has to land on the file descriptor fernsicht reads.

    pytest captures by default; pass `-s` to disable, or `--capture=no`,
    or set `pytestArgs = ['-s']` in your config. Without that, the
    magic-prefix lines stay inside pytest's capture buffer and never
    reach fernsicht. With `-s`, everything passes through.
    """
    sys.stdout.write(line + "\n")
    sys.stdout.flush()


def pytest_collection_modifyitems(config, items):
    """Stash the total test count for later progress reporting."""
    config._fernsicht_total = len(items)
    config._fernsicht_done = 0
    _emit(f'__fernsicht__ start "pytest ({len(items)} tests)"')


def pytest_runtest_logreport(report):
    """Tick after each test's `call` phase (skip setup/teardown for
    cleaner accounting)."""
    if report.when != "call":
        return
    # Pytest's report doesn't expose the session config directly here;
    # walk the .item ref instead, which has a .session.config.
    config = getattr(report, "config", None)
    if config is None and hasattr(report, "session"):
        config = report.session.config
    if config is None:
        # Older pytest versions — fall back to per-test ticks without
        # totals.
        _emit(f'__fernsicht__ progress 1')
        return

    config._fernsicht_done += 1
    n = config._fernsicht_done
    total = config._fernsicht_total
    _emit(f"__fernsicht__ progress {n}/{total}")


def pytest_sessionfinish(session, exitstatus):
    _emit('__fernsicht__ end')
