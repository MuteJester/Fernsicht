# Layer 1 — blick(): the headline 90% case.
#
# A drop-in for lapply() that streams progress to viewers connected
# via the ambient Fernsicht session.

#' Iterate with live progress streamed to viewers.
#'
#' Drop-in replacement for [lapply()] that emits a progress update
#' after each element. The first call in an R process spawns the
#' bridge subprocess and prints the viewer URL; subsequent `blick()`
#' calls in the same process **reuse the same session** — viewers
#' keep watching the same URL across runs.
#'
#' @param items A vector or list. Names are preserved on the result.
#' @param fun A function applied to each element of `items`.
#' @param label Optional human-readable label shown to viewers. If
#'   `NULL` (default), the deparsed call is used (truncated).
#' @param unit Short label for "what one iteration represents" (e.g.
#'   `"row"`, `"epoch"`, `"sim"`). Defaults to `"it"` to match the
#'   Python SDK.
#' @return A list of the same length as `items`, with the same names
#'   if any (matches `lapply` semantics).
#' @examples
#' \dontrun{
#' result <- blick(1:100, function(i) {
#'   Sys.sleep(0.05)
#'   i * 2
#' })
#' }
#' @export
blick <- function(items, fun, label = NULL, unit = "it") {
  if (!is.function(fun)) {
    abort_internal("blick(): `fun` must be a function")
  }
  total <- length(items)

  # Empty input: no session spawn, no task, just return an empty list
  # with the same shape lapply would.
  if (total == 0L) {
    out <- list()
    if (!is.null(names(items))) names(out) <- character()
    return(out)
  }

  # Default label is the deparsed call, truncated so progress UIs
  # don't get a wall of text.
  if (is.null(label)) {
    call_text <- paste(deparse(sys.call(), width.cutoff = 80L),
                       collapse = " ")
    label <- if (nchar(call_text) > 60L) {
      paste0(substr(call_text, 1, 57), "...")
    } else {
      call_text
    }
  }

  # Detect whether this call will spawn a fresh session — used to
  # decide if we should print the viewer URL via message().
  fresh_session <- is.null(pkgenv$session) || !pkgenv$session$is_open()
  s <- ambient_session()
  if (fresh_session) {
    message("Connected. Viewer: ", s$viewer_url())
  }

  task_id <- generate_task_id()
  out <- vector("list", total)
  if (!is.null(names(items))) names(out) <- names(items)

  s$start_task(task_id, label = label)
  on.exit({
    if (s$is_open()) try(s$end_task(task_id), silent = TRUE)
  }, add = TRUE)

  start_time <- Sys.time()
  for (i in seq_len(total)) {
    out[[i]] <- fun(items[[i]])

    elapsed <- as.numeric(difftime(Sys.time(), start_time, units = "secs"))
    rate    <- if (elapsed > 0) i / elapsed else 0
    eta     <- if (rate > 0) (total - i) / rate else 0

    s$tick(task_id,
           value   = i / total,
           n       = i,
           total   = total,
           rate    = rate,
           elapsed = elapsed,
           eta     = eta,
           unit    = unit)
  }

  out
}
