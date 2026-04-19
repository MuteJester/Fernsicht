# processx wrapper around the fernsicht-bridge subprocess.
#
# Phase 2 surface (plan §5):
#   bridge_spawn(binary_path, args)        — start the subprocess
#   bridge_send(proc, cmd)                 — write one JSON command line
#   bridge_drain(proc)                     — non-blocking read of pending events
#   bridge_wait_for(proc, name, timeout)   — block until matching event or timeout
#
# These are the only seams the rest of the package needs. Higher
# layers (Session R6, blick, …) build on top of these four.

#' Spawn the fernsicht-bridge subprocess.
#'
#' @param binary_path Absolute path to the bridge binary.
#' @param args Character vector of CLI args to pass (default none).
#' @return A `processx::process` handle with stdin/stdout/stderr piped.
#' @noRd
bridge_spawn <- function(binary_path, args = character()) {
  if (!file.exists(binary_path)) {
    abort_internal("bridge binary not found at: ", binary_path)
  }
  tryCatch(
    processx::process$new(
      binary_path, args,
      stdin = "|", stdout = "|", stderr = "|",
      cleanup = TRUE,
      cleanup_tree = TRUE
    ),
    error = function(e) {
      abort_internal("failed to spawn bridge: ", conditionMessage(e))
    }
  )
}

#' Serialize and send one command to the bridge.
#'
#' Commands are JSON objects with an `op` field; `auto_unbox = TRUE`
#' so single-element R vectors serialize as JSON scalars (R has no
#' scalar type — `1` is `c(1)`).
#'
#' @param proc A live processx handle from `bridge_spawn()`.
#' @param cmd Named list with at least an `op` field.
#' @noRd
bridge_send <- function(proc, cmd) {
  if (!is.list(cmd) || is.null(cmd$op)) {
    abort_internal("bridge_send requires a list with an 'op' field")
  }
  if (!proc$is_alive()) {
    abort_internal(bridge_dead_message(proc, "send command"))
  }
  line <- jsonlite::toJSON(cmd, auto_unbox = TRUE, null = "null", na = "null")
  tryCatch(
    proc$write_input(paste0(line, "\n")),
    error = function(e) {
      abort_internal("failed to write to bridge stdin: ", conditionMessage(e))
    }
  )
  invisible(NULL)
}

#' Drain all currently-buffered event lines from the bridge's stdout.
#'
#' Non-blocking: returns immediately with whatever's available (possibly
#' an empty list). Each event is parsed from JSON to an R list with
#' `simplifyVector = FALSE` so arrays stay as lists (consistent shape).
#'
#' Lines that fail to parse are returned as a placeholder
#' `list(event = "_parse_error", message = ..., raw = ...)` so callers
#' can decide how to react instead of silently dropping bytes.
#' @noRd
bridge_drain <- function(proc) {
  raw <- tryCatch(
    proc$read_output_lines(),
    error = function(e) character()
  )
  raw <- raw[nzchar(raw)]
  if (length(raw) == 0L) return(list())
  lapply(raw, function(line) {
    tryCatch(
      jsonlite::fromJSON(line, simplifyVector = FALSE),
      error = function(e) {
        list(event = "_parse_error",
             message = conditionMessage(e),
             raw = line)
      }
    )
  })
}

#' Block until a specific event arrives on the bridge stdout.
#'
#' Polls with `proc$poll_io()` so we don't busy-wait. Events that
#' arrive while waiting (but don't match `event_name`) are passed to
#' `on_event` (if non-NULL) and otherwise buffered into the return
#' value's `buffered` slot for the caller to handle.
#'
#' @param proc Live processx handle.
#' @param event_name String; the `event` value to wait for.
#' @param timeout Seconds to wait (numeric).
#' @param on_event Optional function called with each non-matching
#'   event before the wait continues. Useful in Phase 3 where the
#'   Session R6 wants to react to viewer_joined / error events as
#'   they arrive rather than after the fact.
#' @return A list with `event` (the matching event) and `buffered`
#'   (list of intervening events; empty if `on_event` was provided).
#' @noRd
bridge_wait_for <- function(proc, event_name, timeout = 5,
                            on_event = NULL) {
  deadline <- Sys.time() + timeout
  buffered <- list()

  repeat {
    evs <- bridge_drain(proc)
    for (ev in evs) {
      if (!is.null(ev$event) && identical(ev$event, event_name)) {
        return(list(event = ev, buffered = buffered))
      }
      if (!is.null(on_event)) {
        on_event(ev)
      } else {
        buffered[[length(buffered) + 1L]] <- ev
      }
    }

    if (!proc$is_alive()) {
      # One more drain — process may have written final lines just before
      # exiting, and read_output_lines() can return them post-exit.
      evs <- bridge_drain(proc)
      for (ev in evs) {
        if (!is.null(ev$event) && identical(ev$event, event_name)) {
          return(list(event = ev, buffered = buffered))
        }
        if (!is.null(on_event)) on_event(ev)
        else buffered[[length(buffered) + 1L]] <- ev
      }
      abort_internal(bridge_dead_message(
        proc, paste0("wait for '", event_name, "' event")
      ))
    }

    remaining <- as.numeric(deadline - Sys.time(), units = "secs")
    if (remaining <= 0) {
      abort_internal(
        "timed out (", timeout, "s) waiting for bridge event '", event_name, "'"
      )
    }
    # Cap individual poll at 200ms so we re-check is_alive often.
    poll_ms <- as.integer(min(remaining, 0.2) * 1000)
    if (poll_ms < 1L) poll_ms <- 1L
    proc$poll_io(poll_ms)
  }
}

# --- internal helpers -------------------------------------------------

# Build a useful diagnostic message when the bridge has unexpectedly
# died. Includes exit status (if known) and the tail of stderr.
bridge_dead_message <- function(proc, what) {
  status <- tryCatch(proc$get_exit_status(), error = function(e) NA_integer_)
  stderr_tail <- tryCatch(
    {
      lines <- proc$read_error_lines()
      if (length(lines) == 0L) "" else paste(utils::tail(lines, 20), collapse = "\n")
    },
    error = function(e) ""
  )
  paste0(
    "bridge process is not running (cannot ", what, ").",
    if (!is.na(status)) paste0(" exit_status=", status) else "",
    if (nzchar(stderr_tail)) paste0("\nstderr tail:\n", stderr_tail) else ""
  )
}
