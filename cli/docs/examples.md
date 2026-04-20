# fernsicht recipe book

Worked examples for the most common workflows. Each links to a
runnable example in [`../examples/`](../examples/) when applicable.

## Quick reference

| Use case | Recipe |
|---|---|
| Watch a Python script | [Auto-detect tqdm](#auto-detect-tqdm-pip-mlx-most-python-tools) |
| Watch a snakemake pipeline | [Snakemake](#snakemake) |
| Watch a long pytest run | [pytest](#pytest) |
| Watch a Docker build | [Docker build](#docker-build) |
| Watch a custom shell pipeline | [Bash with magic prefix](#bash-with-magic-prefix) |
| Watch from a CI job | [GitHub Actions / GitLab CI](#cicd) |
| Get the URL into Slack | [Webhook on completion](#webhook-on-completion) |
| Use in scripts | [`--share` URL capture](#capture-the-url) |

## Auto-detect: tqdm, pip, MLX, most Python tools

Tier-1 detection handles tqdm and tqdm-derivative formats out of
the box — no flags needed.

```bash
fernsicht run -- pip install pandas
fernsicht run -- python train.py     # if train.py uses tqdm
```

What you see:

```
[fernsicht] viewer: https://app.fernsicht.space#room=abc12345
[QR code rendered when stdout is a tty]

Collecting pandas
  Downloading pandas-2.3.0-cp312-cp312-manylinux_2_17_x86_64.whl (12 MB)
     ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━ 4.2/12.0 MB 18.3 MB/s
... (bar fills both in your terminal AND in the browser)
```

## Snakemake

Snakemake's "[N of M steps (NN%) done]" line gets matched by
Tier-1's `fraction-of` parser, but for cleaner labels add a
project-local `.fernsicht.toml`:

See [`../examples/snakemake/`](../examples/snakemake/).

```bash
cd examples/snakemake
fernsicht run -- snakemake --cores 4
```

The included `.fernsicht.toml`:

```toml
[run]
default_label = "snakemake"
default_unit  = "step"

[[detection.patterns]]
name  = "snakemake"
regex = '\[(\d+) of (\d+) steps \((\d+)%\) done\]'
n_capture     = 1
total_capture = 2
```

## pytest

pytest's progress (`xx/yy [25%]`) matches Tier-1's `fraction-of`
parser. For richer integration use the `pytest-tqdm` plugin or a
conftest hook that emits magic prefix per test.

See [`../examples/pytest/`](../examples/pytest/) for a `conftest.py`
that emits per-test magic-prefix progress.

```bash
cd examples/pytest
fernsicht run -- pytest tests/
```

## Docker build

Modern `docker build --progress=plain` emits per-step lines like
`#5 ... [3/8] RUN ...`. Tier-1 catches these:

```bash
fernsicht run -- docker build --progress=plain .
```

For the BuildKit JSON format (`--progress=rawjson`), use a custom
pattern. See [`../examples/docker-build/`](../examples/docker-build/).

## Bash with magic prefix

When your shell pipeline does its own work (no tool with auto-
detectable progress), emit explicit markers:

```bash
fernsicht run -- bash -c '
  files=( /data/*.csv )
  total=${#files[@]}
  for i in "${!files[@]}"; do
      n=$((i + 1))
      echo "__fernsicht__ progress $n/$total file"
      process "${files[$i]}"
  done
'
```

See [`../examples/bash-pipeline/`](../examples/bash-pipeline/) for a
runnable demo.

## CI/CD

### GitHub Actions

Wrap your test step:

```yaml
- name: Run integration tests with watchable progress
  run: |
    curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
    fernsicht run --share -- pytest tests/integration/ > viewer-url.txt
    echo "::notice::Viewer URL: $(cat viewer-url.txt)"
```

The `--share` mode prints just the URL on stdout (so you can capture
it cleanly), wrapped command's output continues on stderr.

### GitLab CI

Same pattern. Use `before_script` to install fernsicht once per job:

```yaml
before_script:
  - curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
script:
  - fernsicht run --strict -- ./long-build.sh
```

`--strict` flips bridge-failure to a non-zero exit code, so CI fails
the job if monitoring breaks (your call whether you want this).

## Webhook on completion

Get a Slack ping when your 6-hour job finishes:

```bash
fernsicht run \
    --webhook https://hooks.slack.com/services/YOUR/WEBHOOK/URL \
    -- \
    python train.py
```

The webhook gets POSTed when the wrapped command exits, with a
JSON payload like:

```json
{
  "event": "session_end",
  "session": {
    "room_id": "abc12345",
    "viewer_url": "https://app.fernsicht.space#room=abc12345",
    "started_at": "2026-04-19T18:32:00Z",
    "ended_at": "2026-04-20T00:32:00Z",
    "duration_sec": 21600,
    "max_viewers": 3
  },
  "wrapped": {
    "command": "python train.py",
    "exit_code": 0
  }
}
```

Slack incoming-webhooks expect a `text` field, not arbitrary JSON
— pipe through a small adapter (`jq`, a serverless function, etc.)
if you're posting to Slack directly.

## Capture the URL

For background / detached runs:

```bash
url=$(fernsicht run --share -- ./long-script.sh > /tmp/wrapped.log 2>&1 &)
echo "Watching at: $url"
# ... do other work ...
wait
```

In another terminal, you can also discover running sessions:

```bash
fernsicht url           # if exactly one running, prints just the URL
fernsicht url --all     # table of all running sessions
fernsicht url --pid 12345
```

## Pinning a label / unit

Long runs benefit from a meaningful label (otherwise viewers see
"python long-training-with-many-args.py --foo --bar"):

```bash
fernsicht run --label "Training run #42" --unit batch -- python train.py
```

## Config-file convenience

After you've used the same flags twice, move them into
`.fernsicht.toml`:

```toml
[run]
default_label = "Snakemake"
default_unit  = "step"
qr            = "never"  # I'm always running over SSH; QR is noise

[[detection.patterns]]
name  = "snakemake"
regex = '\[(\d+) of (\d+) steps'
n_capture     = 1
total_capture = 2
```

See [config.md](config.md) for the full schema.

## Want a recipe for a specific tool?

Open an issue at
<https://github.com/MuteJester/Fernsicht/issues> with the tool's
output format and we'll add a recipe (or a Tier-1 parser if it's a
common one).
