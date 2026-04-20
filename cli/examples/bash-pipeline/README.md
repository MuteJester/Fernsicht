# bash-pipeline example

A 20-step shell pipeline that emits magic-prefix progress so viewers
see a live bar without any code in fernsicht knowing about your
specific tool.

## Run

```bash
chmod +x pipeline.sh
fernsicht run -- ./pipeline.sh
```

The viewer URL is printed once. Open it in any browser; the bar
fills 0% → 100% over the run, labeled "data pipeline".

## What's happening

`pipeline.sh` emits three flavors of stdout lines:

1. `__fernsicht__ start "data pipeline"` — magic-prefix lifecycle:
   begins a labeled task. Stripped from the user's terminal.
2. `__fernsicht__ progress $i/$TOTAL chunk` — magic-prefix tick.
   Stripped.
3. `processing chunk $i/$TOTAL ...` — normal log line. Passes
   through unchanged so you can grep it later.

The CLI sees both magic and non-magic lines, intercepts the magic
ones, and forwards everything else.

## Variations

- `--label "Phase 1"` to set the task label from a flag rather
  than via the magic-prefix `start` event.
- `--unit seconds` if your iteration represents wall time, not
  items.
- For richer info per tick, switch to JSON form:

  ```sh
  echo "__fernsicht__ {\"n\":$i,\"total\":$TOTAL,\"label\":\"chunk $i\"}"
  ```
