# Layer 2 — with_session(expr, label) and the active-task stack.
#
# `with_session()` does NOT create a new bridge session. It scopes a
# *task* (start/end pair) within the ambient session, and pushes onto
# `pkgenv$task_stack` so that bare `tick(value=...)` calls inside `expr`
# know which task to update.
#
# R is single-threaded, so a global stack is safe. Parallel/futures
# support is explicitly out-of-scope for v1 (plan §16).

#' Scope a task around an expression and stream progress.
#'
#' Evaluates `expr` in the caller's environment with an active task
#' scope. Inside `expr`, calls to `tick(value = ..., ...)` (without
#' an explicit session/task_id) automatically target this scope.
#'
#' Reuses the per-process ambient session — no new bridge subprocess
#' is spawned. The first `with_session()` (or [session()] / `blick()`)
#' call in an R process spawns the bridge; subsequent calls share it.
#'
#' Nested `with_session()` calls work: the inner scope replaces the
#' outer task on the wire (the bridge implicitly ends the previous
#' task on each new START), and on exit the outer task is restarted
#' so subsequent ticks update it again.
#'
#' @param expr Code to run with the task scope active. Evaluated in
#'   the caller's environment; capture-by-reference, not deep-copied.
#' @param label Human-readable label shown to viewers. Defaults to
#'   the auto-generated task ID.
#' @return The value returned by `expr`, invisibly when `expr` returns
#'   invisibly.
#' @export
with_session <- function(expr, label = NULL) {
  expr_q <- substitute(expr)
  caller <- parent.frame()

  s <- ambient_session()

  task_id <- generate_task_id()
  if (is.null(label)) label <- task_id

  # Snapshot the task immediately above ours so we can restart it on
  # exit. The bridge implicitly ends the previous task whenever a new
  # START arrives (BRIDGE_PROTOCOL §6), so once `expr` finishes the
  # outer task is no longer active on the wire — we restart it so
  # ticks in the outer scope continue to land on the right task.
  prev_task <- if (length(pkgenv$task_stack) > 0L) {
    pkgenv$task_stack[[length(pkgenv$task_stack)]]
  } else NULL

  pkgenv$task_stack[[length(pkgenv$task_stack) + 1L]] <- list(
    task_id = task_id, label = label
  )

  on.exit({
    # Pop our entry first so a later error in end_task doesn't leave a
    # stale scope on the stack.
    n <- length(pkgenv$task_stack)
    if (n > 0L) pkgenv$task_stack[[n]] <- NULL

    # Tear down our task on the bridge. Tolerate a dead/closed session
    # — we're tearing down anyway, no point surfacing noise.
    if (!is.null(s) && s$is_open()) {
      try(s$end_task(task_id), silent = TRUE)
      # Restart the previous task so ticks in the outer scope continue
      # to land on it (the bridge implicitly ended it when we started).
      if (!is.null(prev_task)) {
        try(s$start_task(prev_task$task_id, label = prev_task$label),
            silent = TRUE)
      }
    }
  }, add = TRUE)

  s$start_task(task_id, label = label)
  eval(expr_q, envir = caller)
}

# --- internal --------------------------------------------------------

# Auto-incrementing task ID generator. Counter lives in pkgenv so it's
# stable across the R process but resets on package reload (devtools).
generate_task_id <- function() {
  if (is.null(pkgenv$task_counter)) pkgenv$task_counter <- 0L
  pkgenv$task_counter <- pkgenv$task_counter + 1L
  paste0("task-", pkgenv$task_counter)
}
