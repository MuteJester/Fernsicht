# Tests for R/binary.R — platform detection, cache resolution,
# download orchestration, smoke testing, error paths.
#
# All external side effects (download.file, processx::run, digest)
# are dependency-injected via the .runner / .downloader / .hasher
# arguments, so these tests don't need a mocking library and don't
# touch the network.

# Helper: capture+cleanup an env var around a block.
local_env <- function(name, value, .local_envir = parent.frame()) {
  old <- Sys.getenv(name, unset = NA)
  do.call(Sys.setenv, setNames(list(value), name))
  withr::defer({
    if (is.na(old)) {
      Sys.unsetenv(name)
    } else {
      do.call(Sys.setenv, setNames(list(old), name))
    }
  }, envir = .local_envir)
}

# Helper: temporary cache_dir for a test.
local_temp_cache <- function(.local_envir = parent.frame()) {
  tmp <- file.path(tempdir(), paste0("fernsicht-cache-", as.integer(Sys.time())))
  dir.create(tmp, showWarnings = FALSE, recursive = TRUE)
  withr::local_options(list(fernsicht.cache_dir = tmp), .local_envir = .local_envir)
  withr::defer(unlink(tmp, recursive = TRUE), envir = .local_envir)
  tmp
}

# --- os_arch / ext / cache_dir ---------------------------------------

test_that("os_arch returns one of the supported platform strings", {
  result <- os_arch()
  expect_match(result, "^(linux|darwin|windows)-(amd64|arm64)$")
})

test_that("ext returns '.exe' on Windows, empty otherwise", {
  if (.Platform$OS.type == "windows") {
    expect_equal(ext(), ".exe")
  } else {
    expect_equal(ext(), "")
  }
})

test_that("cache_dir creates and returns the configured directory", {
  tmp <- local_temp_cache()
  expect_equal(cache_dir(), tmp)
  expect_true(dir.exists(tmp))
})

test_that("cache_dir falls back to tools::R_user_dir without override", {
  withr::local_options(list(fernsicht.cache_dir = NULL))
  d <- cache_dir()
  expect_true(dir.exists(d))
  expect_match(d, "fernsicht")
})

test_that("cache_binary_path uses platform + extension", {
  local_temp_cache()
  expect_equal(
    basename(cache_binary_path("linux-amd64", "")),
    "fernsicht-bridge-linux-amd64"
  )
  expect_equal(
    basename(cache_binary_path("windows-amd64", ".exe")),
    "fernsicht-bridge-windows-amd64.exe"
  )
})

test_that("download_url builds the GitHub release path", {
  expect_equal(
    download_url("0.1.0", "linux-amd64", ""),
    "https://github.com/MuteJester/Fernsicht/releases/download/bridge/v0.1.0/fernsicht-bridge-linux-amd64"
  )
  expect_equal(
    download_url("0.2.5", "darwin-arm64", ""),
    "https://github.com/MuteJester/Fernsicht/releases/download/bridge/v0.2.5/fernsicht-bridge-darwin-arm64"
  )
})

# --- smoke_test_binary -----------------------------------------------

test_that("smoke_test_binary recognizes valid bridge output", {
  fake_runner <- function(...) {
    list(status = 0L, stdout = "fernsicht-bridge 0.1.0\n", stderr = "")
  }
  expect_true(smoke_test_binary("/fake", .runner = fake_runner))
})

test_that("smoke_test_binary rejects non-bridge output", {
  fake_runner <- function(...) {
    list(status = 0L, stdout = "some-other-tool 1.0\n", stderr = "")
  }
  expect_false(smoke_test_binary("/fake", .runner = fake_runner))
})

test_that("smoke_test_binary rejects non-zero exit codes", {
  fake_runner <- function(...) {
    list(status = 1L, stdout = "", stderr = "boom")
  }
  expect_false(smoke_test_binary("/fake", .runner = fake_runner))
})

test_that("smoke_test_binary handles runner errors gracefully", {
  fake_runner <- function(...) stop("not found")
  expect_false(smoke_test_binary("/fake", .runner = fake_runner))
})

test_that("smoke_test_binary checks expected_version when provided", {
  fake_runner <- function(...) {
    list(status = 0L, stdout = "fernsicht-bridge 0.1.0\n", stderr = "")
  }
  expect_true(smoke_test_binary("/fake", expected_version = "0.1.0", .runner = fake_runner))
  expect_false(smoke_test_binary("/fake", expected_version = "0.2.0", .runner = fake_runner))
})

# --- find_or_download_binary: explicit override path -----------------

test_that("FERNSICHT_BRIDGE_PATH override is honored", {
  tmp <- tempfile()
  writeLines("fake", tmp)
  withr::defer(unlink(tmp))

  fake_runner <- function(...) {
    list(status = 0L, stdout = "fernsicht-bridge dev\n", stderr = "")
  }
  local_env("FERNSICHT_BRIDGE_PATH", tmp)

  result <- find_or_download_binary(.runner = fake_runner)
  expect_equal(result, tmp)
})

test_that("FERNSICHT_BRIDGE_PATH override errors when path missing", {
  local_env("FERNSICHT_BRIDGE_PATH", "/nonexistent/path/to/bridge")
  expect_error(find_or_download_binary(),
    class = "fernsicht_binary_error",
    regexp = "non-existent")
})

test_that("FERNSICHT_BRIDGE_PATH override errors when binary fails smoke test", {
  tmp <- tempfile()
  writeLines("fake", tmp)
  withr::defer(unlink(tmp))

  fake_runner <- function(...) list(status = 1L, stdout = "", stderr = "")
  local_env("FERNSICHT_BRIDGE_PATH", tmp)
  expect_error(find_or_download_binary(.runner = fake_runner),
    class = "fernsicht_binary_error",
    regexp = "did not respond")
})

test_that("fernsicht.bridge_path option works as override", {
  tmp <- tempfile()
  writeLines("fake", tmp)
  withr::defer(unlink(tmp))

  fake_runner <- function(...) {
    list(status = 0L, stdout = "fernsicht-bridge dev\n", stderr = "")
  }
  withr::local_options(list(fernsicht.bridge_path = tmp))

  expect_equal(find_or_download_binary(.runner = fake_runner), tmp)
})

# --- find_or_download_binary: cache hit path -------------------------

test_that("cached binary with matching version is reused", {
  withr::local_envvar(c(FERNSICHT_BRIDGE_PATH = NA))
  cache <- local_temp_cache()
  cached <- cache_binary_path()
  writeLines("real-binary-bytes", cached)

  download_called <- FALSE
  fake_runner <- function(path, ...) {
    list(status = 0L, stdout = "fernsicht-bridge 0.1.0\n", stderr = "")
  }
  fake_downloader <- function(...) {
    download_called <<- TRUE
    0
  }

  result <- find_or_download_binary(version = "0.1.0",
    .runner = fake_runner, .downloader = fake_downloader)
  expect_equal(result, cached)
  expect_false(download_called)
})

test_that("stale cached binary (version mismatch) is removed and redownloaded", {
  skip_if_not(BUNDLED_SHA256[[paste0(os_arch(), ext())]] != "PHASE0_PLACEHOLDER",
    "needs real BUNDLED_SHA256 (Phase 8 — bridge release ready)")

  # If we ever get here (post-Phase-8), pre-populate cache with stale
  # binary, mock runner so the cache version differs, mock downloader
  # to write a "new" binary, mock hasher to match expected SHA.
  # Not implemented in Phase 1 — skipping.
})

test_that("cached binary failing smoke test triggers redownload", {
  withr::local_envvar(c(FERNSICHT_BRIDGE_PATH = NA))
  cache <- local_temp_cache()
  cached <- cache_binary_path()
  writeLines("corrupt-binary", cached)

  # First runner call (smoke test of cached): fails.
  # download_binary will short-circuit on PHASE0_PLACEHOLDER, so we
  # expect the redownload attempt to surface that error, NOT the
  # original cache-hit path.
  fake_runner <- function(...) list(status = 1L, stdout = "", stderr = "")
  fake_downloader <- function(...) 0

  suppressMessages(expect_error(
    find_or_download_binary(.runner = fake_runner,
      .downloader = fake_downloader),
    class = "fernsicht_binary_error",
    regexp = "PHASE0_PLACEHOLDER|placeholder"
  ))
  # Stale cache should have been cleaned up before the download attempt.
  expect_false(file.exists(cached))
})

# --- download_binary: SHA256 verification + smoke test ---------------

test_that("download_binary refuses to use Phase-0 placeholder SHAs", {
  expect_error(
    download_binary(version = "0.1.0", dest = tempfile()),
    class = "fernsicht_binary_error",
    regexp = "PHASE0_PLACEHOLDER|placeholder"
  )
})

test_that("download_binary detects SHA256 mismatch and aborts", {
  # Patch the namespace's BUNDLED_SHA256 to a non-placeholder value.
  ns <- asNamespace("fernsicht")
  orig <- ns$BUNDLED_SHA256
  withr::defer(assignInNamespace("BUNDLED_SHA256", orig, ns))
  faked <- orig
  for (k in names(faked)) faked[[k]] <- "EXPECTED_HASH"
  assignInNamespace("BUNDLED_SHA256", faked, ns)

  fake_downloader <- function(url, dest, ...) {
    writeBin(charToRaw("downloaded bytes"), dest)
    0
  }
  fake_hasher <- function(p) "WRONG_HASH"

  expect_error(
    download_binary("0.1.0", tempfile(),
      .downloader = fake_downloader, .hasher = fake_hasher),
    class = "fernsicht_binary_error",
    regexp = "SHA256 mismatch"
  )
})

test_that("download_binary smoke-tests the downloaded file and aborts on failure", {
  ns <- asNamespace("fernsicht")
  orig <- ns$BUNDLED_SHA256
  withr::defer(assignInNamespace("BUNDLED_SHA256", orig, ns))
  faked <- orig
  for (k in names(faked)) faked[[k]] <- "MATCHING_HASH"
  assignInNamespace("BUNDLED_SHA256", faked, ns)

  fake_downloader <- function(url, dest, ...) {
    writeBin(charToRaw("would-be binary"), dest)
    0
  }
  fake_hasher <- function(p) "MATCHING_HASH"
  fake_runner <- function(...) list(status = 1L, stdout = "", stderr = "")

  expect_error(
    download_binary("0.1.0", tempfile(),
      .downloader = fake_downloader, .hasher = fake_hasher,
      .runner = fake_runner),
    class = "fernsicht_binary_error",
    regexp = "failed to run"
  )
})

test_that("download_binary success path: hash matches, smoke passes, file moves into place", {
  ns <- asNamespace("fernsicht")
  orig <- ns$BUNDLED_SHA256
  withr::defer(assignInNamespace("BUNDLED_SHA256", orig, ns))
  faked <- orig
  for (k in names(faked)) faked[[k]] <- "MATCHING_HASH"
  assignInNamespace("BUNDLED_SHA256", faked, ns)

  dest <- tempfile()
  withr::defer(unlink(dest))

  fake_downloader <- function(url, dest, ...) {
    writeBin(charToRaw("would-be binary bytes"), dest)
    0
  }
  fake_hasher <- function(p) "MATCHING_HASH"
  fake_runner <- function(path, ...) {
    list(status = 0L, stdout = "fernsicht-bridge 0.1.0\n", stderr = "")
  }

  result <- download_binary("0.1.0", dest,
    .downloader = fake_downloader, .hasher = fake_hasher,
    .runner = fake_runner)
  expect_equal(result, dest)
  expect_true(file.exists(dest))
})

test_that("download_binary aborts on missing platform SHA256 entry", {
  ns <- asNamespace("fernsicht")
  orig <- ns$BUNDLED_SHA256
  withr::defer(assignInNamespace("BUNDLED_SHA256", orig, ns))
  assignInNamespace("BUNDLED_SHA256", list(), ns)  # empty — no platform matches

  expect_error(
    download_binary("0.1.0", tempfile()),
    class = "fernsicht_binary_error",
    regexp = "no bundled SHA256"
  )
})
