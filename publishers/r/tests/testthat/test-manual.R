# Tests for R/manual.R — Layer 3 free-function API.
#
# Uses the fake bridge fixture; injects via new_session(.proc=...) and
# stashes the result in pkgenv$session so the public free functions
# resolve to it.

# Build a Session against the fake and register it as the ambient one.
# Auto-cleans on test exit.
register_fake_session <- function(env = character(),
                                   label = NULL,
                                   .local_envir = parent.frame()) {
  cleanup_ambient(.local_envir = .local_envir)
  proc <- spawn_fake(env = env, .local_envir = .local_envir)
  s <- new_session(server_url = "https://signal.example",
                   max_viewers = 8L, label = label, .proc = proc)
  pkgenv$session <- s
  s
}

# --- session() -------------------------------------------------------

test_that("session() returns the ambient session if one is already open", {
  s <- register_fake_session()
  s2 <- session()  # no spawn — reuses ambient
  expect_identical(s, s2)
})

test_that("session() stores the label and surfaces it via diagnostics()", {
  s <- register_fake_session(label = "Training")
  d <- s$diagnostics()
  expect_equal(d$label, "Training")
})

# --- start_task / tick / end_task ------------------------------------

test_that("start_task validates inputs and forwards to the Session", {
  s <- register_fake_session()
  expect_error(start_task("not a session", "t1", "x"),
    class = "fernsicht_internal_error",
    regexp = "must be a Session")
  expect_error(start_task(s, "", "x"),
    class = "fernsicht_internal_error",
    regexp = "non-empty string")
  expect_error(start_task(s, c("t1", "t2"), "x"),
    class = "fernsicht_internal_error")

  expect_invisible(start_task(s, "t1", "Training"))
})

test_that("tick(session, task_id, value) dispatches to Session$tick", {
  s <- register_fake_session()
  start_task(s, "t1", "Training")
  expect_silent(tick(s, "t1", value = 0.25, n = 25L, total = 100L))
  expect_silent(tick(s, "t1", value = 1.0, n = 100L, total = 100L))
  end_task(s, "t1")
})

test_that("tick() rejects positional progress fields", {
  s <- register_fake_session()
  start_task(s, "t1", "x")
  # value MUST be named in this call form.
  expect_error(tick(s, "t1", 0.5),
    class = "fernsicht_internal_error",
    regexp = "by name")
})

test_that("tick() requires task_id when given a Session as the first arg", {
  s <- register_fake_session()
  expect_error(tick(s),
    class = "fernsicht_internal_error",
    regexp = "task_id as the second argument")
})

test_that("tick() with no Session and empty task stack errors clearly", {
  cleanup_ambient()
  pkgenv$session <- NULL
  pkgenv$task_stack <- list()
  expect_error(tick(value = 0.5),
    class = "fernsicht_internal_error",
    regexp = "no session is open")
})

test_that("tick() with ambient session but empty stack tells user to with_session()", {
  s <- register_fake_session()
  pkgenv$task_stack <- list()
  expect_error(tick(value = 0.5),
    class = "fernsicht_internal_error",
    regexp = "no active task scope")
})

# --- close_session ---------------------------------------------------

test_that("close_session() closes the ambient session when called with no arg", {
  s <- register_fake_session()
  close_session()
  expect_false(s$is_open())
  expect_null(pkgenv$session)
})

test_that("close_session(session) closes the explicit one", {
  s <- register_fake_session()
  close_session(s)
  expect_false(s$is_open())
})

test_that("close_session() with no session is a no-op", {
  cleanup_ambient()
  pkgenv$session <- NULL
  expect_silent(close_session())
  expect_silent(close_session(NULL))
})

# --- end-to-end: §4.3 example walkthrough ----------------------------

test_that("§4.3 manual lifecycle example runs end-to-end against the fake bridge", {
  cleanup_ambient()
  proc <- spawn_fake()
  withr::defer(try(proc$kill_tree(), silent = TRUE))

  # Stand in for session() — uses .proc seam since fake isn't a real binary.
  s <- new_session(server_url = "https://signal.example",
                   max_viewers = 8L, .proc = proc)
  pkgenv$session <- s
  withr::defer(try(close_session(s), silent = TRUE))

  for (epoch in 1:3) {
    start_task(s, "epoch", paste("Epoch", epoch))
    for (batch in 1:5) {
      tick(s, "epoch", value = batch / 5, n = batch, total = 5L)
    }
    end_task(s, "epoch")
  }

  expect_true(s$is_open())
  close_session(s)
  expect_false(s$is_open())
})
