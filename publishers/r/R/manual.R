# Layer 3 — manual lifecycle, exposed as free functions.
#
# Plan §4.3 example:
#
#   sess <- fernsicht::session(label = "Training")
#   for (epoch in 1:10) {
#     fernsicht::start_task(sess, "epoch", paste("Epoch", epoch))
#     for (batch in 1:100) {
#       train_batch()
#       fernsicht::tick(sess, "epoch",
#                       value = batch / 100, n = batch, total = 100)
#     }
#     fernsicht::end_task(sess, "epoch")
#   }
#   fernsicht::close_session(sess)
#
# Internally `Session` is an R6 class, but only free functions are
# exposed. Two ways to do the same thing (R6 method vs free function)
# is documentation/discoverability burden — pick one.

#' Open or reuse the ambient Fernsicht session.
#'
#' Returns the per-process ambient session, lazy-spawning the bridge
#' subprocess on first use. Subsequent calls in the same R process
#' return the same Session object — the viewer URL stays stable.
#'
#' @param label Optional human-readable annotation for this session,
#'   surfaced in `print()` and [diagnostics()]. Not transmitted to the
#'   bridge or viewers.
#' @param server_url,join_secret,max_viewers,bridge_path Configuration
#'   overrides. See plan §4.5 for the resolution order
#'   (arg > option > env > default).
#' @return A `Session` R6 object. Typically you'll just hand it to
#'   [start_task()] / [tick()] / [end_task()] without inspecting it.
#' @export
session <- function(label = NULL,
                    server_url = NULL,
                    join_secret = NULL,
                    max_viewers = NULL,
                    bridge_path = NULL) {
  if (!is.null(pkgenv$session) && pkgenv$session$is_open()) {
    return(pkgenv$session)
  }
  s <- new_session(server_url  = server_url,
                   join_secret = join_secret,
                   max_viewers = max_viewers,
                   bridge_path = bridge_path,
                   label       = label)
  pkgenv$session <- s
  message("Connected. Viewer: ", s$viewer_url())
  s
}

#' Begin a task within a session.
#'
#' Equivalent to a START frame in the wire protocol. If a task is
#' already active when `start_task()` is called, the bridge implicitly
#' ends the previous one — no error.
#'
#' @param session A `Session` from [session()].
#' @param task_id Stable identifier for this task (any non-empty string).
#' @param label Human-readable label shown to viewers.
#' @return The `session`, invisibly (so calls can be chained).
#' @export
start_task <- function(session, task_id, label) {
  ensure_session(session, "start_task")
  if (!is.character(task_id) || length(task_id) != 1L || !nzchar(task_id)) {
    abort_internal("start_task: task_id must be a single non-empty string")
  }
  if (missing(label) || is.null(label)) label <- task_id
  session$start_task(task_id, label = label)
  invisible(session)
}

#' Emit a progress update for an active task.
#'
#' Two call forms:
#'
#' \describe{
#'   \item{Layer 3 (explicit)}{`tick(session, task_id, value, ...)` —
#'     supply the session and task_id directly.}
#'   \item{Layer 2 (implicit)}{`tick(value, ...)` — uses the ambient
#'     session and the task on top of the `with_session()` stack
#'     (added in a later phase). Errors clearly if no scope is active.}
#' }
#'
#' @param ... See above. Named args (`value`, `n`, `total`, `rate`,
#'   `elapsed`, `eta`, `unit`) match the bridge's wire fields.
#' @return The `session`, invisibly.
#' @export
tick <- function(...) {
  args <- list(...)
  nm <- names(args)
  if (is.null(nm)) nm <- rep("", length(args))

  is_layer3 <- length(args) >= 1L &&
    (nm[[1]] == "" || nm[[1]] == "session") &&
    inherits(args[[1]], "Session")

  if (is_layer3) {
    session <- args[[1]]
    rest <- args[-1L]
    rest_nm <- nm[-1L]
    # Next positional (or named "task_id") is the task identifier.
    if (length(rest) == 0L ||
        !(rest_nm[[1]] == "" || rest_nm[[1]] == "task_id")) {
      abort_internal(
        "tick(session, task_id, ...) requires task_id as the second argument"
      )
    }
    task_id <- rest[[1]]
    progress_args <- rest[-1L]
    if (is.null(names(progress_args))) {
      abort_internal(
        "tick(session, task_id, ...): progress fields ",
        "(value, n, total, ...) must be passed by name"
      )
    }
    do.call(session$tick, c(list(task_id = task_id), progress_args))
    return(invisible(session))
  }

  # Layer 2 path: pull from ambient session + task stack.
  session <- pkgenv$session
  if (is.null(session) || !session$is_open()) {
    abort_internal(
      "tick(): no session is open. Call session(), with_session(), ",
      "or pass a Session explicitly."
    )
  }
  if (length(pkgenv$task_stack) == 0L) {
    abort_internal(
      "tick(): no active task scope. Wrap your loop in with_session(), ",
      "or pass an explicit Session and task_id."
    )
  }
  if (is.null(names(args))) {
    abort_internal(
      "tick(): progress fields (value, n, total, ...) must be passed by name"
    )
  }
  task <- pkgenv$task_stack[[length(pkgenv$task_stack)]]
  do.call(session$tick, c(list(task_id = task$task_id), args))
  invisible(session)
}

#' End an active task.
#'
#' @param session A `Session` from [session()].
#' @param task_id The task to end. If it's not the active one, the
#'   bridge raises a non-fatal `NO_ACTIVE_TASK` warning.
#' @return The `session`, invisibly.
#' @export
end_task <- function(session, task_id) {
  ensure_session(session, "end_task")
  session$end_task(task_id)
  invisible(session)
}

#' Close a session and shut down the bridge subprocess.
#'
#' Idempotent. The bridge is also closed automatically when the R
#' process exits, so explicit close is optional in interactive use.
#'
#' @param session A `Session` from [session()], or `NULL` (default)
#'   to close the ambient session.
#' @return `NULL`, invisibly.
#' @export
close_session <- function(session = NULL) {
  if (is.null(session)) session <- pkgenv$session
  if (is.null(session)) return(invisible(NULL))
  session$close()
  invisible(NULL)
}

# --- internal --------------------------------------------------------

ensure_session <- function(s, what) {
  if (!inherits(s, "Session")) {
    abort_internal(what, ": first argument must be a Session ",
                   "(returned by session())")
  }
}
