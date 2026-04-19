# Tests for R/session.R — Session R6 class, ambient slot, finalizer,
# event handling, expiry warning, sender_secret redaction.
#
# Most tests construct a Session directly from an injected processx
# handle (the fake bridge) so we can assert on private state without
# real binary discovery / download.

# A canned session_ready event used by Session$new() in tests that
# don't go through the full handshake.
fake_ready <- function(expires_in = 86400L,
                        sender_secret = "REDACTED-FAKE-SECRET",
                        room_id = "fake1234",
                        viewer_url = "https://app.fernsicht.space/#room=fake1234",
                        max_viewers = 8L) {
  list(
    room_id        = room_id,
    sender_secret  = sender_secret,
    viewer_url     = viewer_url,
    expires_at     = format(Sys.time() + expires_in, "%Y-%m-%dT%H:%M:%SZ",
                             tz = "UTC"),
    expires_in     = expires_in,
    max_viewers    = max_viewers,
    poll_interval_hint = 25L
  )
}

# Construct a Session against a fresh fake bridge. Returns the Session.
# The fake's processx handle is auto-cleaned by spawn_fake's defer.
build_session <- function(env = character(), expires_in = 86400L,
                          .local_envir = parent.frame()) {
  proc <- spawn_fake(env = env, .local_envir = .local_envir)
  ready <- fake_ready(expires_in = expires_in)
  sess <- Session$new(proc, ready, bridge_version = "fake-0.0.0")
  withr::defer(try(sess$close(), silent = TRUE), envir = .local_envir)
  sess
}

# --- Construction + accessors ----------------------------------------

test_that("Session$new exposes room_id, viewer_url, viewers", {
  sess <- build_session()
  expect_equal(sess$room_id(), "fake1234")
  expect_match(sess$viewer_url(), "fake1234")
  expect_equal(sess$viewers(), character())
  expect_true(sess$is_open())
})

test_that("diagnostics() returns expected fields and EXCLUDES sender_secret", {
  sess <- build_session()
  d <- sess$diagnostics()
  expect_setequal(names(d), c("bridge_version", "label", "room_id",
                              "viewer_url", "viewer_count", "names",
                              "bridge_pid", "expires_at", "expires_in_min"))
  expect_false("sender_secret" %in% names(d))
  # Spot-check values.
  expect_equal(d$room_id, "fake1234")
  expect_equal(d$viewer_count, 0L)
  expect_match(d$expires_at, "^\\d{4}-\\d{2}-\\d{2}T")
  expect_true(is.numeric(d$expires_in_min) || is.integer(d$expires_in_min))
})

test_that("print(Session) does not leak sender_secret", {
  sess <- build_session()
  out <- capture.output(print(sess))
  expect_false(any(grepl("REDACTED-FAKE-SECRET", out)))
  expect_false(any(grepl("sender_secret", out, ignore.case = TRUE)))
  expect_true(any(grepl("fake1234", out)))
  expect_true(any(grepl("Session", out)))
})

# --- Event handling --------------------------------------------------

test_that("viewer_joined / viewer_left mutate the roster", {
  sess <- build_session()
  # Drive events via process_event() directly (private, accessed via
  # sess$.__enclos_env__$private). Cleaner than driving the fake.
  priv <- sess$.__enclos_env__$private
  priv$process_event(list(event = "viewer_joined", name = "vega"))
  priv$process_event(list(event = "viewer_joined", name = "orion"))
  expect_equal(sess$viewers(), c("vega", "orion"))

  priv$process_event(list(event = "viewer_left", name = "vega"))
  expect_equal(sess$viewers(), "orion")
})

test_that("viewer_count replaces the roster from the bridge's authoritative list", {
  sess <- build_session()
  priv <- sess$.__enclos_env__$private
  priv$process_event(list(event = "viewer_count", count = 2L,
                          names = list("vega", "orion")))
  expect_equal(sess$viewers(), c("vega", "orion"))

  priv$process_event(list(event = "viewer_count", count = 0L,
                          names = list()))
  expect_equal(sess$viewers(), character())
})

test_that("fatal error event raises the matching typed condition", {
  sess <- build_session()
  priv <- sess$.__enclos_env__$private
  expect_error(
    priv$process_event(list(event = "error", code = "SESSION_FAILED",
                             message = "bad secret", fatal = TRUE)),
    class = "fernsicht_session_error",
    regexp = "bad secret"
  )
  expect_error(
    priv$process_event(list(event = "error", code = "SESSION_EXPIRED",
                             message = "token expired", fatal = TRUE)),
    class = "fernsicht_session_expired",
    regexp = "token expired"
  )
  expect_error(
    priv$process_event(list(event = "error", code = "PROTOCOL_VERSION_MISMATCH",
                             message = "v999", fatal = TRUE)),
    class = "fernsicht_protocol_error"
  )
  expect_error(
    priv$process_event(list(event = "error", code = "SIGNALING_UNREACHABLE",
                             message = "down", fatal = TRUE)),
    class = "fernsicht_signaling_error"
  )
})

test_that("non-fatal error event raises a warning, not a stop", {
  sess <- build_session()
  priv <- sess$.__enclos_env__$private
  expect_warning(
    priv$process_event(list(event = "error", code = "NO_ACTIVE_TASK",
                             message = "stale tick", fatal = FALSE)),
    class = "fernsicht_command_warning"
  )
  expect_warning(
    priv$process_event(list(event = "error", code = "TICKET_HANDLING_FAILED",
                             message = "bad offer", fatal = FALSE)),
    class = "fernsicht_ticket_warning"
  )
})

# --- Expiry warning --------------------------------------------------

test_that("expiry warning fires once when past 80% of TTL", {
  # expires_in = 100s, but expires_at is 10s in the future → 90%
  # elapsed → past the 80% threshold.
  sess <- build_session(expires_in = 100L)
  priv <- sess$.__enclos_env__$private
  # Force expires_at to be 10s from now, expires_in stays 100s.
  priv$.expires_at <- Sys.time() + 10
  priv$.expires_in <- 100L

  expect_warning(priv$check_expiry(), class = "fernsicht_expiry_warning")
  # Second call doesn't re-warn.
  expect_silent(priv$check_expiry())
  expect_true(priv$.expiry_warned)
})

test_that("expiry warning does NOT fire when more than 20% of TTL remains", {
  sess <- build_session(expires_in = 100L)
  priv <- sess$.__enclos_env__$private
  priv$.expires_at <- Sys.time() + 80
  priv$.expires_in <- 100L
  expect_silent(priv$check_expiry())
  expect_false(priv$.expiry_warned)
})

# --- Lifecycle (start/tick/end/close) --------------------------------

test_that("start_task → tick → end_task → close runs against the fake bridge", {
  sess <- build_session()

  expect_silent(sess$start_task("t1", label = "Training"))
  expect_silent(sess$tick("t1", value = 0.5, n = 5L, total = 10L))
  expect_silent(sess$end_task("t1"))
  expect_silent(sess$close())

  expect_false(sess$is_open())
})

test_that("close() is idempotent and tolerates a dead bridge", {
  sess <- build_session()
  sess$close()
  expect_false(sess$is_open())
  # Second close is a no-op (no error).
  expect_silent(sess$close())
})

test_that("operations on a closed session error with fernsicht_internal_error", {
  sess <- build_session()
  sess$close()
  expect_error(sess$start_task("t1", "x"),
    class = "fernsicht_internal_error",
    regexp = "session is closed")
  expect_error(sess$tick("t1", 0.5),
    class = "fernsicht_internal_error",
    regexp = "session is closed")
})

# --- Factory: new_session() with injected processx -------------------

test_that("new_session(.proc=fake) drives a full hello → session handshake", {
  cleanup_ambient()
  proc <- spawn_fake()
  sess <- new_session(server_url = "https://signal.example",
                      max_viewers = 8L, .proc = proc)
  withr::defer(try(sess$close(), silent = TRUE))

  expect_true(sess$is_open())
  expect_equal(sess$room_id(), "fake1234")
  expect_match(sess$viewer_url(), "fake1234")
})

test_that("pump() drives drain_and_handle through real queued events", {
  # Regression: the drain_and_handle → process_event path was previously
  # broken by a renaming typo (private$.process_event vs process_event)
  # that the unit tests missed because they called process_event
  # directly. This test exercises the FULL pipeline: bridge emits
  # events → pump drains them → process_event mutates state.
  cleanup_ambient()
  proc <- spawn_fake(env = c(FAKE_BRIDGE_EMIT_PRESENCE = "1"))
  sess <- new_session(server_url = "https://signal.example",
                      max_viewers = 8L, .proc = proc)
  withr::defer(try(sess$close(), silent = TRUE))

  # The fake bridge emitted viewer_joined + viewer_count after
  # session_ready; new_session's bridge_wait_for consumed them via
  # on_event (which no-ops). So the roster is still empty here.
  # We need events that arrive AFTER new_session returned. Send a
  # ping — fake replies with pong (consumed-but-ignored) plus the
  # already-emitted presence stays in the buffer if not yet read.
  #
  # Easiest: emit a viewer_count via the priv hook AND verify pump
  # actually invokes process_event by making sess$tick run an event
  # the fake re-emits. Since the fake's session emission is one-shot,
  # we instead trigger pump() with a freshly-arriving event by sending
  # a command that the fake echoes. Use ping/pong as the carrier.
  bridge_send(proc, list(op = "ping", id = "drain-test"))
  Sys.sleep(0.2)  # give the fake time to write pong
  expect_silent(sess$pump())  # this is the line that errored before
})

test_that("new_session() raises fernsicht_session_error on SESSION_FAILED", {
  cleanup_ambient()
  proc <- spawn_fake(env = c(FAKE_BRIDGE_SESSION_FAILED = "1"))
  expect_error(
    new_session(server_url = "https://signal.example",
                max_viewers = 8L, .proc = proc),
    class = "fernsicht_session_error",
    regexp = "fake bridge refusing session"
  )
})

# --- Ambient session -------------------------------------------------

test_that("ambient_session() returns the same instance on repeat calls", {
  cleanup_ambient()
  proc <- spawn_fake()
  s1 <- new_session(server_url = "https://x", max_viewers = 8L, .proc = proc)
  pkgenv$session <- s1
  withr::defer(try(s1$close(), silent = TRUE))

  s2 <- ambient_session()  # should NOT spawn a new one — reuses s1.
  expect_identical(s1, s2)
})

test_that("ambient_session() recreates after close", {
  cleanup_ambient()
  proc1 <- spawn_fake()
  s1 <- new_session(server_url = "https://x", max_viewers = 8L, .proc = proc1)
  pkgenv$session <- s1
  s1$close()
  expect_null(pkgenv$session)  # close() vacated the slot.

  # Next call would spawn a real bridge — we can't easily test that here
  # without a real binary. We just verify the slot is empty post-close.
})

# --- Finalizer -------------------------------------------------------

test_that("finalizer closes bridge if Session R6 is GC'd while open", {
  proc <- spawn_fake()
  pid <- proc$get_pid()
  ready <- fake_ready()
  sess <- Session$new(proc, ready, bridge_version = "fake-0.0.0")

  # Drop the only reference and force GC. R will run finalizers.
  sess <- NULL
  for (i in 1:3) gc(verbose = FALSE)

  # Give the finalizer + processx time to actually exit.
  deadline <- Sys.time() + 2
  while (Sys.time() < deadline && proc$is_alive()) Sys.sleep(0.05)

  expect_false(proc$is_alive())
})
