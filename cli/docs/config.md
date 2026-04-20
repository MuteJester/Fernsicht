# `.fernsicht.toml` reference

Per-project / per-user configuration. All settings are optional;
the file is searched once per `fernsicht run` invocation.

## Search order

1. `--config <path>` flag (explicit override).
2. `./.fernsicht.toml` and walk up directories until `$HOME` (or `/`).
3. `$XDG_CONFIG_HOME/fernsicht/config.toml`
   (defaults to `~/.config/fernsicht/config.toml`).
4. None — defaults are used silently.

The first match wins. A missing config file is **never an error**
unless you specified one explicitly via `--config`.

## Schema

```toml
# Per-run defaults applied when the corresponding flag isn't set.
[run]
default_label             = "{command}"   # task label fallback
default_unit              = "it"           # progress unit
rate_limit_ticks_per_sec  = 10             # cap tick frequency to bridge
strict                    = false          # default for --strict
qr                        = "auto"         # "auto" | "always" | "never"

# Detection tuning + custom regex patterns.
[detection]
disable_builtin            = false  # if true, only [[detection.patterns]] runs
confidence_threshold_matches = 2    # matches needed before parser locks in
confidence_window_sec      = 5      # rolling window for the threshold

# Repeatable: each pattern adds a custom regex to the registry,
# tried after the built-in Tier-1 detectors.
[[detection.patterns]]
name           = "myorch"
regex          = '\[ETA: (\d+):(\d+)\] (\d+)% complete'
value_capture  = 3   # group 3 is the percent (1-indexed)

[[detection.patterns]]
name           = "thingfile"
regex          = 'Wrote (\d+) of (\d+) thing\.bin'
n_capture      = 1
total_capture  = 2
```

## Field reference

### `[run]`

| Key | Type | Default | Notes |
|---|---|---|---|
| `default_label` | string | (deparsed cmd) | Task label when `--label` isn't passed. |
| `default_unit` | string | `"it"` | Progress unit when `--unit` isn't passed. |
| `rate_limit_ticks_per_sec` | int | 10 | Cap on bridge ticks per second (informational; the bridge enforces a hard cap of ~10 ticks/sec internally). |
| `strict` | bool | false | Default value for `--strict` (CI users frequently want strict on). |
| `qr` | string | `"auto"` | `"always"` forces QR even when stderr isn't a tty; `"never"` suppresses. |

### `[detection]`

| Key | Type | Default | Notes |
|---|---|---|---|
| `disable_builtin` | bool | false | If `true`, ONLY `[[detection.patterns]]` entries run — built-in Tier-1 (tqdm / fraction-bracket / step-keyword / bare-percent) is suppressed. Useful when your tool's output collides with a built-in regex and produces false positives. |
| `confidence_threshold_matches` | int | 2 | Matches needed within `confidence_window_sec` before a parser locks in (per-parser overrides not surfaced via TOML; bare-percent gets +1 internally). |
| `confidence_window_sec` | int | 5 | Sliding window for the threshold count. |

### `[[detection.patterns]]`

| Key | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Identifier shown in `--debug` parse logs (`[parse] custom:<name>`). |
| `regex` | string | yes | Go RE2 syntax. Backreferences (`\1`) are NOT supported by RE2; use named groups if you need them. |
| `value_capture` | int | one of these | 1-indexed group number; parsed as float64; auto-detects 0..1 fraction vs percent (>1.0 → divided by 100). |
| `n_capture` | int | three required | 1-indexed group number; parsed as int. |
| `total_capture` | int | as a set | 1-indexed group number; parsed as int (>0 required). |

At least one of `value_capture` / `n_capture` / `total_capture` must
be specified. When both `n_capture` and `total_capture` are set,
`value` is computed as `n/total` automatically.

## Examples

### Snakemake workflow (project-local)

`Snakefile` and `.fernsicht.toml` in the same directory:

```toml
# .fernsicht.toml — pin defaults for this workflow only
[run]
default_label = "snakemake"
default_unit  = "step"

[[detection.patterns]]
name  = "snakemake-of"
regex = '\[(\d+) of (\d+) steps \((\d+)%\) done\]'
n_capture     = 1
total_capture = 2
```

Now any `fernsicht run -- snakemake ...` from that directory uses
the snakemake-aware pattern + project label. No flags needed.

### Personal default (CI strict mode globally)

`~/.config/fernsicht/config.toml`:

```toml
[run]
strict = true   # every fernsicht run in this account treats bridge failure as fatal
qr     = "never"  # I don't use QR; suppress the visual noise
```

### Disable Tier-1 entirely (use only magic prefix)

```toml
[detection]
disable_builtin = true
```

Equivalent to passing `--no-detect` on every invocation.

## Override priority

For settings that exist on both the command line and in config:

```
flag > env var > .fernsicht.toml > built-in default
```

Examples:

- `--strict` (or absent): wins over `[run].strict = true`.
- `FERNSICHT_SERVER_URL`: env var wins over the built-in default
  `https://signal.fernsicht.space`. The server URL is intentionally
  flag/env-only (not in `.fernsicht.toml`) — checking a config file
  into a repo shouldn't silently route a colleague's traffic to a
  different signaling server.

## Validation

A config file with a syntax error or invalid value type fails the
run with exit code 2 + `[fernsicht] error: config load: ...`. Catch
issues early by validating your TOML at <https://www.toml-lint.com/>
or in any TOML-aware editor.

Invalid regex in a `[[detection.patterns]]` entry also fails at
startup: you see `[fernsicht] error: custom pattern "myorch":
invalid regex: ...` before the wrapped command spawns.
