#!/usr/bin/env Rscript
# Fake fernsicht-bridge for unit-style tests.
#
# Speaks the BRIDGE_PROTOCOL.md wire format on stdin/stdout (newline-
# delimited JSON), but does no networking and no WebRTC. Just enough
# behavior for tests to exercise the R SDK's processx wrapper.
#
# Behavior:
#   hello   → emit hello_ack
#   session → emit session_ready (with redacted sender_secret)
#   start   → silently accept
#   progress → silently accept
#   end     → silently accept
#   ping    → emit pong with same id
#   close   → emit closed{reason: sdk_close}, exit 0
#   <other> → emit error{code: INVALID_COMMAND, fatal: false}
#
# Optional env vars (set by tests to drive specific paths):
#   FAKE_BRIDGE_PROTOCOL_MISMATCH=1   → emit fatal PROTOCOL_VERSION_MISMATCH
#                                        on hello, then closed, then exit 1
#   FAKE_BRIDGE_DELAY_HELLO_MS=N      → sleep N ms before answering hello
#   FAKE_BRIDGE_NEVER_RESPOND=1       → read commands silently, never reply
#   FAKE_BRIDGE_CRASH_AFTER=N         → after N commands, write "boom"
#                                        to stderr and exit 99
#   FAKE_BRIDGE_EMIT_PRESENCE=1       → on session, also emit a
#                                        viewer_joined + viewer_count event
#   FAKE_BRIDGE_SESSION_FAILED=1      → on session, emit fatal
#                                        SESSION_FAILED, then closed
#   FAKE_BRIDGE_EXPIRES_IN_SEC=N      → override session_ready expires_in
#                                        (and recompute expires_at)

suppressPackageStartupMessages(library(jsonlite))

emit <- function(obj) {
  cat(jsonlite::toJSON(obj, auto_unbox = TRUE, null = "null"), "\n", sep = "")
  flush(stdout())
}

protocol_mismatch <- nzchar(Sys.getenv("FAKE_BRIDGE_PROTOCOL_MISMATCH"))
delay_hello_ms   <- as.integer(Sys.getenv("FAKE_BRIDGE_DELAY_HELLO_MS", "0"))
never_respond    <- nzchar(Sys.getenv("FAKE_BRIDGE_NEVER_RESPOND"))
crash_after      <- suppressWarnings(as.integer(Sys.getenv("FAKE_BRIDGE_CRASH_AFTER", "")))
emit_presence    <- nzchar(Sys.getenv("FAKE_BRIDGE_EMIT_PRESENCE"))
session_failed   <- nzchar(Sys.getenv("FAKE_BRIDGE_SESSION_FAILED"))
expires_in_sec   <- suppressWarnings(as.integer(Sys.getenv("FAKE_BRIDGE_EXPIRES_IN_SEC", "")))
if (is.na(expires_in_sec)) expires_in_sec <- 86400L

if (is.na(crash_after)) crash_after <- -1L
cmd_count <- 0L

con <- file("stdin", open = "r", blocking = TRUE)
on.exit(try(close(con), silent = TRUE), add = TRUE)

repeat {
  line <- tryCatch(
    readLines(con, n = 1L, warn = FALSE),
    error = function(e) character()
  )
  if (length(line) == 0L) break  # EOF
  if (!nzchar(line)) next

  cmd_count <- cmd_count + 1L
  if (crash_after > 0L && cmd_count > crash_after) {
    cat("fake_bridge: crash_after triggered\n", file = stderr())
    flush(stderr())
    quit(save = "no", status = 99L)
  }

  cmd <- tryCatch(
    jsonlite::fromJSON(line, simplifyVector = FALSE),
    error = function(e) NULL
  )
  if (is.null(cmd) || is.null(cmd$op)) {
    if (!never_respond) {
      emit(list(event = "error", code = "INVALID_COMMAND",
                message = "could not parse command", fatal = FALSE))
    }
    next
  }

  if (never_respond) next

  switch(cmd$op,
    "hello" = {
      if (delay_hello_ms > 0L) Sys.sleep(delay_hello_ms / 1000)
      if (protocol_mismatch) {
        emit(list(event = "error", code = "PROTOCOL_VERSION_MISMATCH",
                  message = "fake bridge speaks protocol 999", fatal = TRUE))
        emit(list(event = "closed", reason = "fatal_error"))
        quit(save = "no", status = 1L)
      }
      emit(list(event = "hello_ack",
                bridge_version = "fake-0.0.0",
                protocol = 1L))
    },
    "session" = {
      if (session_failed) {
        emit(list(event = "error", code = "SESSION_FAILED",
                  message = "fake bridge refusing session", fatal = TRUE))
        emit(list(event = "closed", reason = "fatal_error"))
        quit(save = "no", status = 2L)
      }
      expires_at <- format(Sys.time() + expires_in_sec, "%Y-%m-%dT%H:%M:%SZ",
                           tz = "UTC")
      emit(list(event = "session_ready",
                room_id = "fake1234",
                sender_secret = "REDACTED-FAKE-SECRET",
                viewer_url = "https://app.fernsicht.space/#room=fake1234",
                expires_at = expires_at,
                expires_in = expires_in_sec,
                max_viewers = 8L,
                poll_interval_hint = 25L))
      if (emit_presence) {
        emit(list(event = "viewer_joined", name = "vega"))
        emit(list(event = "viewer_count", count = 1L,
                  names = list("vega")))
      }
    },
    "start"    = invisible(NULL),
    "progress" = invisible(NULL),
    "end"      = invisible(NULL),
    "ping" = {
      emit(list(event = "pong", id = cmd$id))
    },
    "close" = {
      emit(list(event = "closed", reason = "sdk_close"))
      break
    },
    {
      emit(list(event = "error", code = "INVALID_COMMAND",
                message = paste0("unknown op: ", cmd$op), fatal = FALSE))
    }
  )
}

quit(save = "no", status = 0L)
