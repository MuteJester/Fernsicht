# In-process spawn of the standalone fake signaling server (Rscript).
#
# httpuv's event loop only services requests when the host R session
# pumps it via service(). During a synchronous integration test the
# parent R session is busy waiting on the bridge subprocess, so the
# fake server has to live in its OWN R subprocess — spawned via
# processx so we can read its READY line and tear it down cleanly.

fake_signaling_path <- function() {
  p <- "fake_signaling.R"
  if (file.exists(p)) return(normalizePath(p))
  p2 <- testthat::test_path("fake_signaling.R")
  if (file.exists(p2)) return(normalizePath(p2))
  stop("fake_signaling.R not found (cwd=", getwd(), ")")
}

# Spawn the fake signaling subprocess. Returns:
#   url        — base URL the bridge should be pointed at
#   stop()     — kill it
spawn_fake_signaling <- function(.local_envir = parent.frame(),
                                  room_id = "intg-room-01",
                                  sender_secret = "FAKE-INTEGRATION-SECRET",
                                  api_key_required = NULL,
                                  expires_in = 3600L) {
  if (!requireNamespace("httpuv", quietly = TRUE)) {
    skip("httpuv not installed (Suggests dependency)")
  }

  # Pick a port that's currently free. Race-free enough for tests.
  port <- pick_free_port()

  rscript <- file.path(R.home("bin"), "Rscript")
  env_vars <- c(
    FAKE_SIG_PORT       = as.character(port),
    FAKE_SIG_ROOM_ID    = room_id,
    FAKE_SIG_SECRET     = sender_secret,
    FAKE_SIG_EXPIRES_IN = as.character(as.integer(expires_in))
  )
  if (!is.null(api_key_required)) {
    env_vars["FAKE_SIG_API_KEY_REQUIRED"] <- api_key_required
  }

  proc <- processx::process$new(
    rscript,
    c("--vanilla", fake_signaling_path()),
    stdin = "|", stdout = "|", stderr = "|",
    env = c("current", env_vars),
    cleanup = TRUE,
    cleanup_tree = TRUE
  )
  withr::defer({
    if (proc$is_alive()) try(proc$kill_tree(), silent = TRUE)
  }, envir = .local_envir)

  # Wait for "READY <port>" so we know the server is listening.
  deadline <- Sys.time() + 5
  ready <- FALSE
  while (Sys.time() < deadline) {
    proc$poll_io(100L)
    line <- proc$read_output_lines()
    if (length(line) > 0 && any(grepl("^READY ", line))) {
      ready <- TRUE
      break
    }
    if (!proc$is_alive()) {
      stderr_tail <- paste(proc$read_error_lines(), collapse = "\n")
      stop("fake signaling subprocess died before READY: ", stderr_tail)
    }
  }
  if (!ready) {
    try(proc$kill_tree(), silent = TRUE)
    stop("fake signaling subprocess did not become ready within 5s")
  }

  list(
    url   = paste0("http://127.0.0.1:", port),
    proc  = proc,
    port  = port
  )
}

# Bind a free local port. Try a few until one is available.
pick_free_port <- function(tries = 20L) {
  for (i in seq_len(tries)) {
    port <- as.integer(sample(20000:60000, 1L))
    # Use socketConnection to test if the port is in use; we want
    # the connect to FAIL, meaning nothing is listening.
    in_use <- tryCatch({
      con <- suppressWarnings(socketConnection(
        "127.0.0.1", port, blocking = TRUE,
        timeout = 0.2, open = "r"
      ))
      close(con)
      TRUE  # something accepted — port is taken
    }, error = function(e) FALSE)
    if (!in_use) return(port)
  }
  stop("could not find a free local port for the fake signaling server")
}
