# Integration tests using the REAL fernsicht-bridge binary against an
# in-process httpuv fake signaling server.
#
# Skipped by default. To run locally:
#
#   cd bridge && CGO_ENABLED=0 go build -o /tmp/fernsicht-bridge \
#     ./cmd/fernsicht-bridge/
#   FERNSICHT_BRIDGE_PATH=/tmp/fernsicht-bridge \
#     R_LIBS_USER=$HOME/R/library Rscript -e \
#     "library(fernsicht); testthat::test_dir('tests/testthat')"

skip_if_no_bridge <- function() {
  bp <- Sys.getenv("FERNSICHT_BRIDGE_PATH", unset = "")
  if (!nzchar(bp)) {
    skip("FERNSICHT_BRIDGE_PATH not set — skipping real-bridge integration tests")
  }
  if (!file.exists(bp)) {
    skip(sprintf("FERNSICHT_BRIDGE_PATH=%s does not exist", bp))
  }
  if (!requireNamespace("httpuv", quietly = TRUE)) {
    skip("httpuv not installed")
  }
  invisible(NULL)
}

# Each integration test gets its own fresh ambient session + fake server.
setup_real_bridge <- function(api_key = NULL,
                              api_key_required = NULL,
                              .local_envir = parent.frame()) {
  cleanup_ambient(.local_envir = .local_envir)
  pkgenv$task_stack   <- list()
  pkgenv$task_counter <- 0L

  signaling <- spawn_fake_signaling(api_key_required = api_key_required,
                                     .local_envir = .local_envir)
  s <- new_session(server_url   = signaling$url,
                   join_secret  = api_key,
                   max_viewers  = 4L)
  pkgenv$session <- s
  withr::defer(try(close_session(s), silent = TRUE),
               envir = .local_envir)
  list(session = s, signaling = signaling)
}

# --- Lifecycle: full end-to-end ---

test_that("real bridge: hello → session → start → progress × N → end → close", {
  skip_if_no_bridge()
  ctx <- setup_real_bridge()

  expect_true(ctx$session$is_open())
  expect_match(ctx$session$viewer_url(), "intg-room-01")
  expect_equal(ctx$session$diagnostics()$room_id, "intg-room-01")

  # Drive a small blick — exercises start/progress×N/end on the real bridge.
  result <- blick(1:20, function(i) i * 2)
  expect_length(result, 20L)
  expect_equal(unlist(result), seq(2, 40, by = 2))

  close_session(ctx$session)
  expect_false(ctx$session$is_open())
})

# --- Error path: bad join_secret ---

test_that("real bridge: bad join_secret → fernsicht_session_error", {
  skip_if_no_bridge()
  cleanup_ambient()
  pkgenv$session <- NULL
  pkgenv$task_stack <- list()
  pkgenv$task_counter <- 0L

  signaling <- spawn_fake_signaling(api_key_required = "expected-secret")
  expect_error(
    new_session(server_url  = signaling$url,
                join_secret = "wrong-secret",
                max_viewers = 4L),
    class = "fernsicht_session_error"
  )
})

# --- Session reuse ---

test_that("real bridge: two consecutive blick() calls share one session + URL", {
  skip_if_no_bridge()
  ctx <- setup_real_bridge()

  blick(1:5, function(x) x)
  url1 <- viewer_url()
  blick(1:5, function(x) x * 2)
  url2 <- viewer_url()

  expect_equal(url1, url2)
  expect_equal(url1, ctx$session$viewer_url())
})

# --- Bridge crash detection ---

test_that("real bridge: killing the subprocess surfaces fernsicht_internal_error", {
  skip_if_no_bridge()
  ctx <- setup_real_bridge()

  proc <- ctx$session$.__enclos_env__$private$.proc
  expect_true(proc$is_alive())
  proc$kill_tree()
  proc$wait(timeout = 2000)
  expect_false(proc$is_alive())

  expect_error(
    ctx$session$start_task("t-after-crash", "post-crash"),
    class = "fernsicht_internal_error"
  )
})

# --- Layer 2 (with_session) end-to-end ---

test_that("real bridge: with_session() drives a real task + ticks", {
  skip_if_no_bridge()
  ctx <- setup_real_bridge()

  with_session({
    for (i in 1:10) {
      tick(value = i / 10, n = i, total = 10L)
    }
  }, label = "Integration loop")

  expect_true(ctx$session$is_open())
  expect_length(pkgenv$task_stack, 0L)
})
