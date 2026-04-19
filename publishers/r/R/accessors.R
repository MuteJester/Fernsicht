# Public accessors — programmatic access to session metadata.
#
# Plan §4.4: critical for non-interactive contexts (Rscript, knitr,
# CI) where the message() output from session() isn't visible. All
# three default to the ambient session so users in interactive use
# can call them with no arguments.

#' Get the viewer URL for a session.
#'
#' Defaults to the ambient session — the same one `blick()`,
#' `with_session()`, and [session()] all share. Pass a `Session`
#' explicitly to query a specific one.
#'
#' @param session A `Session` from [session()], or `NULL` (default)
#'   for the ambient session.
#' @param copy If `TRUE`, also copy the URL to the system clipboard
#'   via the `clipr` package (a `Suggests` dependency). If `clipr`
#'   isn't installed or the clipboard isn't available, a warning is
#'   issued and the URL is still returned.
#' @return The viewer URL as a single character string. Errors if no
#'   session is open.
#' @export
viewer_url <- function(session = NULL, copy = FALSE) {
  s <- ensure_active_session(session, "viewer_url")
  url <- s$viewer_url()
  if (isTRUE(copy)) try_copy_to_clipboard(url)
  url
}

#' Current viewer roster.
#'
#' Returns the names of viewers currently connected to the session.
#' The list is updated as `viewer_joined` / `viewer_left` events
#' arrive from the bridge; calling this function drains pending events
#' first so the result reflects the latest state.
#'
#' @param session A `Session` from [session()], or `NULL` (default)
#'   for the ambient session.
#' @return Character vector of viewer names (length 0 if nobody's connected).
#' @export
viewers <- function(session = NULL) {
  s <- ensure_active_session(session, "viewers")
  s$pump()
  s$viewers()
}

#' Diagnostic snapshot of the current session.
#'
#' Useful for logging, bug reports, and `traceback`-style debugging.
#' **Never includes `sender_secret`** — a regression test enforces this.
#'
#' @param session A `Session` from [session()], or `NULL` (default)
#'   for the ambient session.
#' @return A named list with: `bridge_version`, `label`, `room_id`,
#'   `viewer_url`, `viewer_count`, `names`, `bridge_pid`, `expires_at`,
#'   `expires_in_min`. The `sender_secret` field is **deliberately
#'   excluded**.
#' @export
diagnostics <- function(session = NULL) {
  s <- ensure_active_session(session, "diagnostics")
  s$pump()
  s$diagnostics()
}

# --- internal --------------------------------------------------------

ensure_active_session <- function(s, what) {
  if (is.null(s)) s <- pkgenv$session
  if (is.null(s)) {
    abort_internal(what, "(): no session is open. Call session() or ",
                   "blick() first to start one.")
  }
  if (!inherits(s, "Session")) {
    abort_internal(what, "(): expected a Session object")
  }
  s
}

try_copy_to_clipboard <- function(text) {
  if (!requireNamespace("clipr", quietly = TRUE)) {
    warning("clipr is not installed; cannot copy viewer URL to clipboard.\n",
            "  install.packages('clipr')",
            call. = FALSE)
    return(invisible(NULL))
  }
  ok <- tryCatch(clipr::clipr_available(), error = function(e) FALSE)
  if (!ok) {
    warning("system clipboard is not available in this environment.",
            call. = FALSE)
    return(invisible(NULL))
  }
  tryCatch({
    clipr::write_clip(text)
    message("Viewer URL copied to clipboard.")
  }, error = function(e) {
    warning("clipr::write_clip failed: ", conditionMessage(e), call. = FALSE)
  })
  invisible(NULL)
}
