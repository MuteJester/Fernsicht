# Tests for R/bridge.R — processx wrapper around fernsicht-bridge.
#
# Uses tests/testthat/fake_bridge.R as the subprocess. The fake speaks
# the BRIDGE_PROTOCOL.md wire format but does no networking, so these
# tests are hermetic (no internet, no real bridge binary needed).

# spawn_fake() and fake_bridge_path() live in helper-bridge.R so they
# can be reused by other test files (notably test-session.R).

# --- bridge_spawn ----------------------------------------------------

test_that("bridge_spawn errors when binary path doesn't exist", {
  expect_error(
    bridge_spawn("/no/such/bridge/binary"),
    class = "fernsicht_internal_error",
    regexp = "bridge binary not found"
  )
})

test_that("bridge_spawn returns a live processx handle for a real binary", {
  # Use the R interpreter as a stand-in 'binary' that exists. We won't
  # send commands to it — just verify the spawn machinery itself.
  r_bin <- file.path(R.home("bin"), "R")
  proc <- bridge_spawn(r_bin, args = c("--vanilla", "--quiet", "-e", "Sys.sleep(60)"))
  withr::defer(try(proc$kill_tree(), silent = TRUE))
  expect_true(inherits(proc, "process"))
  expect_true(proc$is_alive())
})

# --- bridge_send -----------------------------------------------------

test_that("bridge_send rejects payloads without an 'op' field", {
  r_bin <- file.path(R.home("bin"), "R")
  proc <- bridge_spawn(r_bin, args = c("--vanilla", "--quiet", "-e", "Sys.sleep(60)"))
  withr::defer(try(proc$kill_tree(), silent = TRUE))

  expect_error(bridge_send(proc, list(foo = 1)),
    class = "fernsicht_internal_error",
    regexp = "'op' field")
  expect_error(bridge_send(proc, "not a list"),
    class = "fernsicht_internal_error")
})

test_that("bridge_send raises if the process is dead", {
  proc <- spawn_fake()
  bridge_send(proc, list(op = "close"))
  proc$wait(timeout = 2000)
  expect_false(proc$is_alive())

  expect_error(bridge_send(proc, list(op = "ping", id = "x")),
    class = "fernsicht_internal_error",
    regexp = "not running")
})

# --- bridge_drain ----------------------------------------------------

test_that("bridge_drain returns empty list when nothing's available", {
  proc <- spawn_fake()
  Sys.sleep(0.1)  # let process start
  out <- bridge_drain(proc)
  expect_type(out, "list")
  expect_length(out, 0L)
})

test_that("bridge_drain parses JSON event lines into R lists", {
  proc <- spawn_fake()
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  # Drain with poll loop until we see something or timeout.
  out <- list()
  deadline <- Sys.time() + 2
  while (Sys.time() < deadline && length(out) == 0L) {
    proc$poll_io(50L)
    out <- bridge_drain(proc)
  }
  expect_gt(length(out), 0L)
  expect_equal(out[[1]]$event, "hello_ack")
  expect_equal(out[[1]]$bridge_version, "fake-0.0.0")
  expect_equal(out[[1]]$protocol, 1L)
})

test_that("bridge_drain wraps unparseable lines in _parse_error placeholder", {
  # Use a bare R one-liner that prints an invalid JSON line then exits.
  r_bin <- file.path(R.home("bin"), "Rscript")
  proc <- processx::process$new(
    r_bin,
    c("--vanilla", "-e",
      "cat('not-valid-json\\n'); flush(stdout()); Sys.sleep(0.5)"),
    stdin = "|", stdout = "|", stderr = "|", cleanup = TRUE
  )
  withr::defer(try(proc$kill_tree(), silent = TRUE))

  out <- list()
  deadline <- Sys.time() + 2
  while (Sys.time() < deadline && length(out) == 0L) {
    proc$poll_io(50L)
    out <- bridge_drain(proc)
  }
  expect_gt(length(out), 0L)
  expect_equal(out[[1]]$event, "_parse_error")
  expect_match(out[[1]]$raw, "not-valid-json")
})

# --- bridge_wait_for -------------------------------------------------

test_that("bridge_wait_for returns the matching event", {
  proc <- spawn_fake()
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  res <- bridge_wait_for(proc, "hello_ack", timeout = 3)
  expect_equal(res$event$event, "hello_ack")
  expect_equal(res$event$bridge_version, "fake-0.0.0")
  expect_length(res$buffered, 0L)
})

test_that("bridge_wait_for buffers intervening events when on_event is NULL", {
  proc <- spawn_fake(env = c(FAKE_BRIDGE_EMIT_PRESENCE = "1"))
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  bridge_wait_for(proc, "hello_ack", timeout = 3)

  bridge_send(proc, list(op = "session", base_url = "x", max_viewers = 8L))
  res <- bridge_wait_for(proc, "viewer_count", timeout = 3)
  expect_equal(res$event$event, "viewer_count")
  expect_equal(res$event$count, 1L)

  # Two events come before viewer_count: session_ready + viewer_joined.
  buffered_names <- vapply(res$buffered, function(e) e$event, character(1))
  expect_equal(buffered_names, c("session_ready", "viewer_joined"))
})

test_that("bridge_wait_for invokes on_event for non-matching events", {
  proc <- spawn_fake(env = c(FAKE_BRIDGE_EMIT_PRESENCE = "1"))
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  bridge_wait_for(proc, "hello_ack", timeout = 3)

  seen <- list()
  bridge_send(proc, list(op = "session", base_url = "x", max_viewers = 8L))
  res <- bridge_wait_for(proc, "viewer_count", timeout = 3,
    on_event = function(ev) seen[[length(seen) + 1L]] <<- ev$event)
  expect_equal(res$event$event, "viewer_count")
  # buffered is empty when on_event handles the others.
  expect_length(res$buffered, 0L)
  expect_equal(seen, list("session_ready", "viewer_joined"))
})

test_that("bridge_wait_for times out cleanly on silent bridge", {
  proc <- spawn_fake(env = c(FAKE_BRIDGE_NEVER_RESPOND = "1"))
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  expect_error(
    bridge_wait_for(proc, "hello_ack", timeout = 0.5),
    class = "fernsicht_internal_error",
    regexp = "timed out"
  )
})

test_that("bridge_wait_for reports bridge death with stderr tail", {
  proc <- spawn_fake(env = c(FAKE_BRIDGE_CRASH_AFTER = "1"))
  # First command processes normally (count==1), second triggers crash.
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  bridge_wait_for(proc, "hello_ack", timeout = 3)
  bridge_send(proc, list(op = "ping", id = "boom"))
  expect_error(
    bridge_wait_for(proc, "pong", timeout = 3),
    class = "fernsicht_internal_error",
    regexp = "not running|crash_after"
  )
})

# --- end-to-end Phase 2 acceptance -----------------------------------
# "spawn bridge (or fake), send hello, receive hello_ack, send close,
#  see clean exit."

test_that("end-to-end: hello → hello_ack → close → closed → exit 0", {
  proc <- spawn_fake()

  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  res1 <- bridge_wait_for(proc, "hello_ack", timeout = 3)
  expect_equal(res1$event$event, "hello_ack")

  bridge_send(proc, list(op = "close"))
  res2 <- bridge_wait_for(proc, "closed", timeout = 3)
  expect_equal(res2$event$event, "closed")
  expect_equal(res2$event$reason, "sdk_close")

  proc$wait(timeout = 2000)
  expect_false(proc$is_alive())
  expect_equal(proc$get_exit_status(), 0L)
})

test_that("end-to-end: ping echoes the same id (correlation works)", {
  proc <- spawn_fake()
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  bridge_wait_for(proc, "hello_ack", timeout = 3)

  bridge_send(proc, list(op = "ping", id = "corr-42"))
  res <- bridge_wait_for(proc, "pong", timeout = 3)
  expect_equal(res$event$id, "corr-42")

  bridge_send(proc, list(op = "close"))
  proc$wait(timeout = 2000)
})

test_that("end-to-end: session emits session_ready with a viewer_url", {
  proc <- spawn_fake()
  bridge_send(proc, list(op = "hello", sdk = "r",
                         sdk_version = "0.0.0", protocol = 1L))
  bridge_wait_for(proc, "hello_ack", timeout = 3)

  bridge_send(proc, list(op = "session",
                         base_url = "https://signal.example",
                         max_viewers = 4L))
  res <- bridge_wait_for(proc, "session_ready", timeout = 3)
  expect_equal(res$event$room_id, "fake1234")
  expect_match(res$event$viewer_url, "fake1234")
  expect_equal(res$event$max_viewers, 8L)  # fake hardcodes 8

  bridge_send(proc, list(op = "close"))
  proc$wait(timeout = 2000)
})
