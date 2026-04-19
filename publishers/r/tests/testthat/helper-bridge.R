# Shared test fixtures for spawning the fake bridge subprocess.
# Loaded before any test-*.R file (testthat helper-* convention).

# Resolve the path to fake_bridge.R. testthat usually runs tests with
# cwd at tests/testthat/, but devtools / R CMD check paths can differ.
fake_bridge_path <- function() {
  p <- "fake_bridge.R"
  if (file.exists(p)) return(normalizePath(p))
  p2 <- testthat::test_path("fake_bridge.R")
  if (file.exists(p2)) return(normalizePath(p2))
  stop("fake_bridge.R not found (cwd=", getwd(), ")")
}

# Spawn the fake bridge under Rscript. Returns the live processx
# handle; auto-cleans up at end of the calling test via withr::defer.
spawn_fake <- function(env = character(),
                       .local_envir = parent.frame()) {
  rscript <- file.path(R.home("bin"), "Rscript")
  proc <- processx::process$new(
    rscript,
    c("--vanilla", fake_bridge_path()),
    stdin = "|", stdout = "|", stderr = "|",
    env = c("current", env),
    cleanup = TRUE,
    cleanup_tree = TRUE
  )
  withr::defer({
    if (proc$is_alive()) {
      try(proc$kill_tree(), silent = TRUE)
    }
  }, envir = .local_envir)
  proc
}

# Reset the package-level ambient session at end of test so cross-test
# state can't leak. Tests that intentionally use the ambient slot
# should call this in their setup.
cleanup_ambient <- function(.local_envir = parent.frame()) {
  withr::defer({
    if (!is.null(pkgenv$session)) {
      try(pkgenv$session$close(), silent = TRUE)
      pkgenv$session <- NULL
    }
  }, envir = .local_envir)
}
