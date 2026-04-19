# Phase 0 scaffold sanity check.
#
# Verifies that the package builds, installs, loads, and that the
# minimal pieces of state we DO have are wired correctly. Real test
# coverage lands phase-by-phase.

test_that("BRIDGE_VERSION constant is defined", {
  expect_true(exists("BRIDGE_VERSION", envir = asNamespace("fernsicht")))
  ver <- get("BRIDGE_VERSION", envir = asNamespace("fernsicht"))
  expect_type(ver, "character")
  expect_length(ver, 1L)
})

test_that("BUNDLED_SHA256 has entries for all five supported platforms", {
  sha <- get("BUNDLED_SHA256", envir = asNamespace("fernsicht"))
  expect_type(sha, "list")
  expect_named(sha,
    c("linux-amd64", "linux-arm64",
      "darwin-amd64", "darwin-arm64",
      "windows-amd64.exe"),
    ignore.order = TRUE
  )
})

test_that("ambient state environment exists and starts empty", {
  pkgenv <- get("pkgenv", envir = asNamespace("fernsicht"))
  expect_true(is.environment(pkgenv))
  expect_null(pkgenv$session)
  expect_length(pkgenv$task_stack, 0L)
})
