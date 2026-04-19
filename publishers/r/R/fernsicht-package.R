#' fernsicht: Watch Your R Code's Progress From Anywhere
#'
#' Wrap any long-running loop with `blick()` (or use the lower-level
#' `with_session()` / [session()] APIs) to stream live progress to a
#' shareable URL. Progress flows peer-to-peer via WebRTC; the package
#' wraps the `fernsicht-bridge` Go binary which is downloaded lazily
#' from GitHub releases on first use.
#'
#' See `vignette("fernsicht")` for a walkthrough.
#'
#' @keywords internal
#' @importFrom R6 R6Class
"_PACKAGE"
