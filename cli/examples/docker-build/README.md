# docker-build example

Watch a Docker build's progress remotely. Three approaches depending
on what's available to you:

## 1. `--progress=plain` (recommended)

```bash
fernsicht run -- docker build --progress=plain -t myapp .
```

Plain progress emits per-step lines like:

```
#5 [3/8] RUN apt-get update
#5 sha256:abc...
#5 DONE 4.2s
```

Tier-1's `fraction-bracket` parser catches `[3/8]`. Confidence
locks after the second bracketed step; bar fills as the build
progresses through stages.

## 2. `--progress=tty` (default, on a tty)

The default `tty` mode redraws lines using ANSI cursor moves —
fernsicht detects this as a TUI and disables auto-detection (to
avoid parsing redrawn-line garbage). You'll see one warn line:

```
[fernsicht] warn: detected fullscreen TUI; auto-detection disabled.
            Use magic prefix (__fernsicht__) for explicit progress.
```

Solution: switch to `--progress=plain` (above), or wrap with the
magic prefix:

```bash
fernsicht run -- bash -c '
  docker build -t myapp . 2>&1 | while read -r line; do
    echo "$line"
    # Detect "[N/M]" and emit explicit ticks.
    if [[ "$line" =~ \[([0-9]+)/([0-9]+)\] ]]; then
      echo "__fernsicht__ progress ${BASH_REMATCH[1]}/${BASH_REMATCH[2]}"
    fi
  done
'
```

## 3. BuildKit `--progress=rawjson`

For richer integrations (CI dashboards, custom monitoring), parse
the raw JSON event stream:

```bash
fernsicht run -- bash -c '
  docker build --progress=rawjson . 2>&1 | while read -r line; do
    echo "$line"
    n=$(echo "$line" | jq -r .vertexes[0].step.completedSteps 2>/dev/null)
    t=$(echo "$line" | jq -r .vertexes[0].step.totalSteps 2>/dev/null)
    if [[ -n "$n" && -n "$t" && "$n" != "null" ]]; then
      echo "__fernsicht__ progress $n/$t"
    fi
  done
'
```

(Requires `jq`. The exact JSON path varies across BuildKit versions
— inspect a sample with `--progress=rawjson` first.)

## CI integration

In CI, prefer `--progress=plain` so logs are readable in the build
output AND fernsicht's parser gets clean lines:

```yaml
# GitHub Actions
- name: Watchable Docker build
  run: |
    curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
    fernsicht run --share -- docker build --progress=plain -t myapp . > viewer-url.txt
    echo "::notice::Build viewer: $(cat viewer-url.txt)"
```
