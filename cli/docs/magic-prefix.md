# Magic-prefix protocol

When the wrapped command emits a stdout line beginning with the
literal prefix `__fernsicht__ ` (one trailing space), fernsicht:

1. **Strips** the line from forwarded output (it doesn't appear in
   logs, pipes, or the user's terminal).
2. **Parses** it as a progress / lifecycle event.
3. **Forwards** the result to the bridge → viewer browser.

This is the universal escape hatch: any program in any language can
emit progress without an SDK, just by `print`ing one line.

The reference is also bundled in the binary — run `fernsicht magic`
for an offline copy.

## Why use the magic prefix?

Tier-1 auto-detection (regexes for tqdm, pip, fraction patterns,
bare percent) handles the common cases without code changes. But:

- Your tool's progress format isn't recognized.
- You need explicit task lifecycle (start / end with custom labels).
- You want monotonic, non-fuzzy progress (no false positives).
- Auto-detection's confidence-locking delay would be visible.

The magic prefix wins in all of these.

## Quick reference

```
PREFIX
    __fernsicht__

JSON FORM
    __fernsicht__ {"value":0.5}
    __fernsicht__ {"n":50,"total":100}
    __fernsicht__ {"value":0.5,"n":50,"total":100,"label":"Training","unit":"batch"}
    __fernsicht__ {"event":"start","label":"Phase 1"}
    __fernsicht__ {"event":"end"}
    __fernsicht__ {"event":"label","label":"New label"}
    __fernsicht__ {"event":"url"}

COMPACT FORM
    __fernsicht__ progress N[/TOTAL] [UNIT]
    __fernsicht__ progress NN%
    __fernsicht__ start "label with spaces"
    __fernsicht__ end [TASK_ID]
    __fernsicht__ label "New label"
    __fernsicht__ url
```

## Field reference (JSON form)

| Field | Type | Notes |
|---|---|---|
| `value` | float, 0..1 | Progress fraction. If absent and `n`+`total` set, computed as `n/total`. Out-of-range rejected. |
| `n` | int, ≥0 | Items completed. |
| `total` | int, >0 | Total items. (0 rejected — division by zero.) |
| `label` | string | Human-readable task label. |
| `unit` | string | Default `"it"`; e.g., `"epoch"`, `"batch"`, `"row"`. |
| `task_id` | string | Stable identifier for explicit lifecycle. |
| `event` | string | One of: `progress` (default), `start`, `end`, `label`, `url`. |

### `event` semantics

- `progress` (default if omitted): one progress observation. Use
  `value` OR `n`+`total`.
- `start`: begin a new task. Provide `label` (and optionally `task_id`).
  Bridge implicitly ends any previous task.
- `end`: end a task. Empty body ends the active one; pass `task_id`
  to be specific.
- `label`: rename the current task without ending it. (Note: the
  bridge has no per-task label-update wire frame in this release;
  the CLI records the change locally but viewers see the label set
  at `start` time.)
- `url`: signal the CLI to re-print the viewer URL banner. Same
  effect as sending SIGUSR1 to the fernsicht process.

## Examples by language

### Bash

```bash
for i in {1..100}; do
    echo "__fernsicht__ progress $i/100"
    sleep 1
done
```

### Python

```python
import time
for i in range(100):
    print(f"__fernsicht__ progress {i+1}/100", flush=True)
    time.sleep(1)
```

Note `flush=True` — without it, Python block-buffers stdout when not
interactive and your progress arrives in 4 KB chunks. The CLI's
default `--unbuffer` mode already sets `PYTHONUNBUFFERED=1` so most
scripts don't need this.

### Perl

```perl
$| = 1;  # autoflush
for my $i (1..100) {
    print "__fernsicht__ progress $i/100\n";
    sleep 1;
}
```

### Awk (one-liner)

```bash
seq 1 100 | awk '{print "__fernsicht__ progress " $1 "/100"; system("sleep 1")}'
```

### Custom JSON for richer data

```python
import json, time
for epoch in range(10):
    for batch in range(100):
        # Per-batch progress within the current epoch.
        msg = {
            "value": (epoch * 100 + batch) / 1000,
            "n": batch,
            "total": 100,
            "label": f"Epoch {epoch+1}/10",
            "unit": "batch",
        }
        print(f"__fernsicht__ {json.dumps(msg)}", flush=True)
        time.sleep(0.05)
```

### Multi-task lifecycle

```bash
for phase in download preprocess train evaluate; do
    echo "__fernsicht__ start \"$phase\""
    # ... do the work, optionally with progress lines ...
    for i in {1..50}; do
        echo "__fernsicht__ progress $i/50"
        sleep 0.1
    done
    echo "__fernsicht__ end"
done
```

Viewers see each phase as a discrete bar, advancing 0→100% then
ending and starting fresh on the next phase.

## Validation behavior

- **Invalid JSON** → `[fernsicht] warn: invalid magic prefix: ...`
  printed on stderr. Line is still stripped from forwarded output
  (so the typo doesn't leak downstream). Wrapped command continues.
- **Unknown verb / event** → same as above.
- **`value` out of [0,1]** → rejected with the warn message.
- **`total` ≤ 0** → rejected.

With `--strict-magic`, any invalid magic line is fatal: exit code
250, wrapped command terminated. Use this in CI when explicit
correctness matters.

With `--no-magic`, the prefix is **not** stripped — lines pass
through to the user's terminal verbatim, no parsing.

## Custom patterns

If your tool emits a progress format the built-in detectors don't
recognize, two options:

1. **Magic prefix** — modify your script to emit `__fernsicht__`
   lines (recommended; explicit and unambiguous).
2. **Custom regex** via `--pattern` — non-invasive, regex-only.

```bash
fernsicht run --pattern '\[ETA: \d+:\d+\] (\d+)% complete' -- ./oddtool
```

The first capture group is taken as `value` (auto-detected as
percentage if > 1.0, otherwise as a 0..1 fraction). For more control:
use a `.fernsicht.toml` file with explicit `n_capture` /
`total_capture` / `value_capture`. See [config.md](config.md).

## Why is the prefix `__fernsicht__`?

- Distinctive: vanishingly rare to occur naturally in stdout.
- Recognizable: includes the project name, so a user looking at log
  output knows what the line means.
- Easy to type / grep / search.

If you need to disable interception entirely, pass `--no-magic`.
