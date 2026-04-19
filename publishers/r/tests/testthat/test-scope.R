# Tests for R/scope.R — with_session() and the active-task stack.
#
# All tests inject the fake bridge as the ambient session so we never
# touch the network.

# Wire the fake bridge as the ambient session for the duration of the
# calling test. Resets task_stack + task_counter so each test starts
# from a clean slate.
register_fake <- function(.local_envir = parent.frame()) {
  cleanup_ambient(.local_envir = .local_envir)
  pkgenv$task_stack   <- list()
  pkgenv$task_counter <- 0L
  proc <- spawn_fake(.local_envir = .local_envir)
  s <- new_session(server_url = "https://signal.example",
                   max_viewers = 8L, .proc = proc)
  pkgenv$session <- s
  s
}

# --- generate_task_id ------------------------------------------------

test_that("generate_task_id returns monotonically increasing IDs", {
  pkgenv$task_counter <- 0L
  expect_equal(generate_task_id(), "task-1")
  expect_equal(generate_task_id(), "task-2")
  expect_equal(generate_task_id(), "task-3")
})

# --- with_session: basic --------------------------------------------

test_that("with_session() pushes a task scope, tick(value=) targets it", {
  s <- register_fake()

  # Inside with_session the stack has one entry; tick(value=) reads it.
  result <- with_session({
    expect_length(pkgenv$task_stack, 1L)
    top <- pkgenv$task_stack[[1L]]
    expect_match(top$task_id, "^task-")
    tick(value = 0.5, n = 5L, total = 10L)
    "ok"
  }, label = "Manual loop")

  # Stack popped after with_session returns.
  expect_length(pkgenv$task_stack, 0L)
  expect_equal(result, "ok")
})

test_that("with_session() evaluates expr in the caller's environment", {
  s <- register_fake()
  local_var <- "hello"
  with_session({
    # Reference a binding from the calling frame to prove caller env is used.
    expect_equal(local_var, "hello")
  })
})

test_that("with_session() returns the value of expr", {
  s <- register_fake()
  out <- with_session(42L)
  expect_equal(out, 42L)
})

test_that("default label falls back to the auto task_id", {
  s <- register_fake()
  with_session({
    top <- pkgenv$task_stack[[1L]]
    expect_equal(top$label, top$task_id)
  })
})

# --- error path ------------------------------------------------------

test_that("with_session() pops the stack and ends the task even if expr errors", {
  s <- register_fake()
  expect_error(
    with_session({
      stop("boom")
    }),
    "boom"
  )
  expect_length(pkgenv$task_stack, 0L)
})

# --- nested scopes --------------------------------------------------

test_that("nested with_session() — tick targets inner, outer resumes after", {
  s <- register_fake()

  # Capture into an env so cross-scope assignments work regardless of
  # how `eval(expr, envir = caller)` interacts with <<-.
  captured <- new.env()

  with_session({
    captured$outer <- pkgenv$task_stack[[length(pkgenv$task_stack)]]$task_id
    expect_length(pkgenv$task_stack, 1L)
    tick(value = 0.1)

    with_session({
      captured$inner <- pkgenv$task_stack[[length(pkgenv$task_stack)]]$task_id
      expect_length(pkgenv$task_stack, 2L)
      expect_false(identical(captured$outer, captured$inner))
      tick(value = 0.5)
    })

    # After inner exits, stack is back to depth 1 with outer on top.
    expect_length(pkgenv$task_stack, 1L)
    expect_equal(pkgenv$task_stack[[1L]]$task_id, captured$outer)
    tick(value = 0.9)
  })

  expect_length(pkgenv$task_stack, 0L)
  expect_false(is.null(captured$outer))
  expect_false(is.null(captured$inner))
})

# --- tick outside scope ---------------------------------------------

test_that("tick(value=...) outside any with_session errors with 'no active task scope'", {
  s <- register_fake()
  expect_length(pkgenv$task_stack, 0L)
  expect_error(tick(value = 0.5),
    class = "fernsicht_internal_error",
    regexp = "no active task scope")
})

test_that("tick(value=...) with no ambient session at all errors with 'no session is open'", {
  cleanup_ambient()
  pkgenv$session <- NULL
  pkgenv$task_stack <- list()
  expect_error(tick(value = 0.5),
    class = "fernsicht_internal_error",
    regexp = "no session is open")
})

# --- §4.2 example: full walkthrough ---------------------------------

test_that("§4.2 manual-loop example runs end-to-end", {
  s <- register_fake()
  with_session(
    {
      for (i in seq_len(10)) {
        # No Sys.sleep() in tests — just emit ticks.
        tick(value = i / 10, n = i, total = 10L)
      }
    },
    label = "Manual loop"
  )
  # Session still open after with_session exits — only the task ended.
  expect_true(s$is_open())
  expect_length(pkgenv$task_stack, 0L)
})
