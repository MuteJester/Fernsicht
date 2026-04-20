# fernsicht

> Watch your R code's progress from anywhere.

`fernsicht` wraps any long-running R loop with a one-line call and
streams live progress to a sharable URL. Progress flows
**peer-to-peer over WebRTC DataChannels** — viewers connect directly
to your R process, so the data stays between you and them.

```r
remotes::install_github("MuteJester/Fernsicht", subdir = "publishers/r")

library(fernsicht)

result <- blick(1:100, function(i) {
  Sys.sleep(0.05)
  i * 2
})
# Connecting to https://signal.fernsicht.space ...
# Connected. Viewer: https://app.fernsicht.space/#room=abc12345
```

Open the printed URL in any browser — yours or someone else's — and
watch the bar fill in live.

## What it's for

Long-running R workloads where you want to know "is it still going?"
without re-attaching a terminal:

- MCMC / Bayesian sampling (`Stan`, `brms`, `rstanarm`)
- Bootstrap, cross-validation, hyperparameter search
- Large `lme4` / `glmmTMB` fits
- Simulation studies, Monte Carlo runs
- Any `lapply()`-style loop you'd otherwise wrap in `pbapply` or `progressr`

The viewer runs in any browser (mobile too) — no app to install, no
account to create, no data uploaded to a third party.

## How it works

```
R session                   signal server                 viewer browser
   │                              │                             │
   │── publish ─────────────────▶ │                             │
   │                              │ ◀── connect ────────────────│
   │ ◀──────── WebRTC handshake (a few seconds, via signal) ──▶│
   │                              │                             │
   │ ━━━━━━━━━━━━━━━━━━━━━━━━━━━━ DataChannel ━━━━━━━━━━━━━━━━ │
   │                       (peer-to-peer; signal not involved) │
```

The R package spawns a small Go binary (`fernsicht-bridge`) that
handles all the WebRTC / ICE / SCTP plumbing. The bridge is downloaded
on first use from GitHub releases (~10 MB, cached locally).

## Three API layers

Pick the one that matches your loop style. **All three reuse the
same per-process session** — the viewer URL is stable across calls.

### 1. `blick()` — drop-in for `lapply`

```r
result <- blick(items, fun, label = NULL, unit = "it")
```

```r
# Headline usage. Returns the same shape lapply() would.
results <- blick(1:1000, function(i) my_expensive_step(i))
```

### 2. `with_session()` — for manual loops

When your loop doesn't fit `lapply()`:

```r
with_session({
  for (i in seq_len(100)) {
    do_one_step(i)
    tick(value = i / 100, n = i, total = 100)
  }
}, label = "Manual loop")
```

### 3. Manual lifecycle — for nested phases

```r
sess <- session(label = "Training")

for (epoch in 1:10) {
  start_task(sess, "epoch", paste("Epoch", epoch))
  for (batch in 1:100) {
    train_batch()
    tick(sess, "epoch", value = batch / 100, n = batch, total = 100)
  }
  end_task(sess, "epoch")
}

close_session(sess)   # optional — exits anyway when R exits
```

## Accessors

```r
viewer_url()                  # current ambient session's viewer URL
viewer_url(copy = TRUE)       # also copy to clipboard (needs `clipr`)
viewers()                     # current roster, e.g. c("vega", "orion")
diagnostics()                 # full snapshot for logs / bug reports
```

`diagnostics()` is safe to log — it never includes the sender secret.

## Configuration

Function arg > R option > env var > default.

| Setting | Function arg | R option | Env var | Default |
|---|---|---|---|---|
| Server URL | `server_url=` | `fernsicht.server_url` | `FERNSICHT_SERVER_URL` | `https://signal.fernsicht.space` |
| Join secret | `join_secret=` | `fernsicht.join_secret` | `FERNSICHT_JOIN_SECRET` | `NULL` |
| Max viewers | `max_viewers=` | `fernsicht.max_viewers` | `FERNSICHT_MAX_VIEWERS` | `8` |
| Bridge binary path | `bridge_path=` | `fernsicht.bridge_path` | `FERNSICHT_BRIDGE_PATH` | (use cache) |

Set `FERNSICHT_BRIDGE_PATH` to point at a locally-built bridge for
development (skips download + cache lookup).

## Limits and caveats

- **Sessions live up to 12 hours.** Long enough for most jobs;
  ~80% in, the SDK warns once via `warning()` so you can plan a
  restart for week-long runs.
- **One session per R process.** Use multiple R processes for
  multiple sessions. Parallel/futures support is on the roadmap.
- **No account, no telemetry, no analytics.** The package only
  contacts the configured signaling server and GitHub releases (for
  the one-time bridge download).

## Other ways to use Fernsicht

Don't want a code change at all? Two alternatives:

- **CLI** — `fernsicht run -- Rscript train.R` wraps any command
  and auto-detects common progress patterns. See
  [`cli/README.md`](https://github.com/MuteJester/Fernsicht/tree/main/cli).
- **Python** — `pip install fernsicht` for the sibling SDK.

All three speak the same wire protocol; rooms / viewer URLs /
hosted defaults are identical.

## Status

Pre-release. The headline `blick()` API is stable; lower-level
internals may move before v1.0.

## License

AGPL-3.0 — see [LICENSE](LICENSE).
