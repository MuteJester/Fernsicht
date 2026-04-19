# Tests for R/accessors.R — viewer_url(), viewers(), diagnostics().
#
# Accessors all default to the ambient session, so we wire that up
# from the fake bridge fixture.

setup_ambient <- function(env = character(),
                          .local_envir = parent.frame()) {
  cleanup_ambient(.local_envir = .local_envir)
  proc <- spawn_fake(env = env, .local_envir = .local_envir)
  s <- new_session(server_url = "https://signal.example",
                   max_viewers = 8L, .proc = proc)
  pkgenv$session <- s
  s
}

# --- viewer_url -----------------------------------------------------

test_that("viewer_url() with no args returns the ambient session URL", {
  s <- setup_ambient()
  expect_equal(viewer_url(), s$viewer_url())
  expect_match(viewer_url(), "fake1234")
})

test_that("viewer_url(session) accepts an explicit Session", {
  s <- setup_ambient()
  expect_equal(viewer_url(s), s$viewer_url())
})

test_that("viewer_url() errors when no session is open", {
  cleanup_ambient()
  pkgenv$session <- NULL
  expect_error(viewer_url(),
    class = "fernsicht_internal_error",
    regexp = "no session is open")
})

test_that("viewer_url(copy = TRUE) warns gracefully when clipr unavailable", {
  s <- setup_ambient()
  # In a non-interactive test session clipr may or may not work, but
  # the contract is: the URL is still returned regardless.
  url <- suppressWarnings(suppressMessages(viewer_url(copy = TRUE)))
  expect_equal(url, s$viewer_url())
})

# --- viewers --------------------------------------------------------

test_that("viewers() returns the current roster (defaults to ambient)", {
  s <- setup_ambient()
  expect_equal(viewers(), character())  # nobody connected to fake.

  # Inject roster events through the private hook to simulate bridge events.
  priv <- s$.__enclos_env__$private
  priv$process_event(list(event = "viewer_joined", name = "vega"))
  priv$process_event(list(event = "viewer_joined", name = "orion"))
  expect_equal(viewers(), c("vega", "orion"))
})

# --- diagnostics ----------------------------------------------------

test_that("diagnostics() returns the expected fields and EXCLUDES sender_secret", {
  s <- setup_ambient()
  d <- diagnostics()
  expect_setequal(names(d), c("bridge_version", "label", "room_id",
                              "viewer_url", "viewer_count", "names",
                              "bridge_pid", "expires_at", "expires_in_min"))
  expect_false("sender_secret" %in% names(d))
  expect_equal(d$room_id, "fake1234")
})

test_that("diagnostics(session) accepts an explicit Session", {
  s <- setup_ambient()
  d <- diagnostics(s)
  expect_equal(d$room_id, "fake1234")
  expect_false("sender_secret" %in% names(d))
})

# --- regression: nothing in any accessor leaks the secret ----------

test_that("no accessor or printable form leaks the sender_secret string", {
  s <- setup_ambient()
  # The fake bridge sends sender_secret = "REDACTED-FAKE-SECRET".
  # No public surface should ever surface it.
  expect_false(any(grepl("REDACTED-FAKE-SECRET", viewer_url())))
  expect_false(any(grepl("REDACTED-FAKE-SECRET", viewers())))
  d <- diagnostics()
  expect_false(any(grepl("REDACTED-FAKE-SECRET",
                         unlist(lapply(d, as.character)))))
  out <- capture.output(print(s))
  expect_false(any(grepl("REDACTED-FAKE-SECRET", out)))
})
