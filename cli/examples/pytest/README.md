# pytest example

Two ways to watch a pytest run with fernsicht:

1. **Auto-detect**: pytest emits `[ N%]` style progress that
   Tier-1's `bare-percent` parser catches. Zero setup, but with
   confidence locking it takes a few percentage updates before the
   bar starts moving.

2. **Magic prefix via `conftest.py`**: emits explicit per-test
   progress immediately. Recommended for long suites.

## Auto-detect run

Just wrap pytest:

```bash
fernsicht run -- pytest tests/
```

The bar starts moving once auto-detection locks in (~2 percentage
prints).

## Magic-prefix integration via conftest.py

Drop the included `conftest.py` next to your tests:

```bash
cp conftest.py /path/to/your/tests/
fernsicht run -- pytest tests/
```

The conftest hooks pytest's `pytest_runtest_logreport` to emit a
magic-prefix line per completed test. Viewers see N/M ticks with
the test name as the label.

## How `conftest.py` works

```python
import sys

def pytest_collection_modifyitems(config, items):
    config._fernsicht_total = len(items)
    config._fernsicht_done = 0

def pytest_runtest_logreport(report):
    if report.when != "call":
        return
    config = report.session.config if hasattr(report, "session") else None
    # ... emit __fernsicht__ progress per completed test
```

(Full file: [`conftest.py`](conftest.py).)

The result: bar fills smoothly, one tick per test, labeled with the
suite size.
