# Lazy-download + cache management for the fernsicht-bridge binary.
#
# Plan §6:
#   - One binary file per platform in tools::R_user_dir("fernsicht", "cache")
#   - Resolution order:
#       1. Explicit override (function arg / option / FERNSICHT_BRIDGE_PATH env)
#       2. Cache hit AND `binary --version` matches BRIDGE_VERSION
#       3. Download from GitHub releases, verify SHA256, smoke test, cache
#
# All side-effecting external calls (download.file, digest, processx::run)
# are dependency-injected via .runner / .downloader / .hasher arguments
# so tests can substitute fakes without mocking-framework gymnastics.

#' Platform string used for cache filenames and download URLs.
#' Returns "<os>-<arch>" — one of:
#'   linux-amd64, linux-arm64, darwin-amd64, darwin-arm64, windows-amd64
#' @noRd
os_arch <- function() {
  os <- switch(Sys.info()[["sysname"]],
    Linux = "linux", Darwin = "darwin", Windows = "windows",
    abort_binary("unsupported OS: ", Sys.info()[["sysname"]]))
  arch <- switch(R.version$arch,
    "x86_64" = "amd64",
    "aarch64" = "arm64",
    "arm64"   = "arm64",
    abort_binary("unsupported arch: ", R.version$arch))
  paste0(os, "-", arch)
}

#' Executable file extension on the current platform.
#' @noRd
ext <- function() if (.Platform$OS.type == "windows") ".exe" else ""

#' Cache directory for the bridge binary.
#'
#' Honors the `fernsicht.cache_dir` option (used by tests) and falls
#' back to `tools::R_user_dir("fernsicht", "cache")`. Creates the
#' directory if it doesn't exist.
#' @noRd
cache_dir <- function() {
  d <- getOption("fernsicht.cache_dir",
    tools::R_user_dir("fernsicht", which = "cache"))
  if (!dir.exists(d)) dir.create(d, recursive = TRUE, showWarnings = FALSE)
  d
}

#' Path the cached binary for the current platform should live at.
#' @noRd
cache_binary_path <- function(platform = os_arch(), extension = ext()) {
  file.path(cache_dir(), paste0("fernsicht-bridge-", platform, extension))
}

#' GitHub release URL for the bridge binary.
#' @noRd
download_url <- function(version, platform = os_arch(), extension = ext()) {
  paste0(
    "https://github.com/MuteJester/Fernsicht/releases/download/",
    "bridge/v", version,
    "/fernsicht-bridge-", platform, extension
  )
}

#' Run `binary --version` and decide if it looks like a real bridge.
#'
#' @param path Path to the binary to test.
#' @param expected_version If non-NULL, the --version output must
#'   contain this string (exact substring match). Use NULL to accept
#'   any version (e.g. for FERNSICHT_BRIDGE_PATH dev builds).
#' @param .runner Function with the same signature as
#'   `processx::run(path, args, timeout, error_on_status)`. Mockable
#'   in tests.
#' @return TRUE if the binary appears valid; FALSE otherwise.
#' @noRd
smoke_test_binary <- function(path, expected_version = NULL,
                              .runner = processx::run) {
  out <- tryCatch(
    .runner(path, "--version", timeout = 5, error_on_status = FALSE),
    error = function(e) list(status = -1L, stdout = "",
                              stderr = conditionMessage(e))
  )
  if (!isTRUE(out$status == 0L)) return(FALSE)
  stdout <- if (is.null(out$stdout)) "" else out$stdout
  if (!startsWith(stdout, "fernsicht-bridge")) return(FALSE)
  if (!is.null(expected_version)) {
    if (!grepl(expected_version, stdout, fixed = TRUE)) return(FALSE)
  }
  TRUE
}

#' Download the bridge binary, verify its SHA256, smoke-test it, and
#' atomically move it into place at `dest`.
#' @noRd
download_binary <- function(version, dest,
                             platform = os_arch(),
                             extension = ext(),
                             .downloader = utils::download.file,
                             .hasher = function(p) digest::digest(p, algo = "sha256", file = TRUE),
                             .runner = processx::run,
                             .quiet = !interactive()) {
  url <- download_url(version, platform, extension)
  expected <- BUNDLED_SHA256[[paste0(platform, extension)]]
  if (is.null(expected)) {
    abort_binary("no bundled SHA256 for platform '", platform, extension, "'")
  }
  if (identical(expected, "PHASE0_PLACEHOLDER")) {
    abort_binary(
      "BUNDLED_SHA256 contains a Phase-0 placeholder for ", platform,
      extension, ".\n",
      "Run tools/update_sha256.R against a real bridge release before installing."
    )
  }

  tmp <- tempfile()
  on.exit(if (file.exists(tmp)) file.remove(tmp), add = TRUE)

  tryCatch(
    .downloader(url, tmp, mode = "wb", method = "libcurl", quiet = .quiet),
    error = function(e) abort_binary("download failed: ", conditionMessage(e))
  )

  actual <- .hasher(tmp)
  if (!identical(expected, actual)) {
    abort_binary(
      "SHA256 mismatch downloading bridge\n",
      "  expected: ", expected, "\n",
      "  got:      ", actual
    )
  }

  Sys.chmod(tmp, "0755")

  if (!smoke_test_binary(tmp, expected_version = version, .runner = .runner)) {
    abort_binary(
      "downloaded bridge binary failed to run.\n",
      "Possible causes: incompatible glibc, SELinux/AppArmor, ",
      "or noexec mount on the cache directory."
    )
  }

  # Atomic move; fall back to copy if file.rename can't cross filesystems.
  if (file.exists(dest)) file.remove(dest)
  if (!file.rename(tmp, dest)) {
    file.copy(tmp, dest, overwrite = TRUE)
    file.remove(tmp)
  }
  Sys.chmod(dest, "0755")
  dest
}

#' Resolve the path to a usable bridge binary, downloading if needed.
#'
#' Resolution order (plan §6.1):
#'   1. Explicit `bridge_path` (function arg > `fernsicht.bridge_path`
#'      option > `FERNSICHT_BRIDGE_PATH` env var)
#'   2. Cache hit with matching version
#'   3. Download fresh
#'
#' All external calls are dependency-injected for testability.
#' @noRd
find_or_download_binary <- function(version = BRIDGE_VERSION,
                                    bridge_path = NULL,
                                    .runner = processx::run,
                                    .downloader = utils::download.file,
                                    .hasher = function(p) digest::digest(p, algo = "sha256", file = TRUE),
                                    .quiet = !interactive()) {
  # 1. Explicit override.
  if (is.null(bridge_path)) {
    bridge_path <- getOption("fernsicht.bridge_path",
      Sys.getenv("FERNSICHT_BRIDGE_PATH", unset = ""))
    if (identical(bridge_path, "")) bridge_path <- NULL
  }
  if (!is.null(bridge_path)) {
    if (!file.exists(bridge_path)) {
      abort_binary(
        "FERNSICHT_BRIDGE_PATH points to a non-existent file: ", bridge_path
      )
    }
    if (!smoke_test_binary(bridge_path, .runner = .runner)) {
      abort_binary(
        "binary at ", bridge_path, " did not respond to --version ",
        "with 'fernsicht-bridge ...' output. Is it really the bridge?"
      )
    }
    return(bridge_path)
  }

  # 2. Cache hit?
  cached <- cache_binary_path()
  if (file.exists(cached) &&
      smoke_test_binary(cached, expected_version = version, .runner = .runner)) {
    return(cached)
  }
  # Stale (wrong version, corrupt, or a previous Phase-0 placeholder):
  # remove it before redownloading.
  if (file.exists(cached)) file.remove(cached)

  # 3. Download.
  message("Downloading fernsicht-bridge v", version, " ...")
  download_binary(version, cached,
    .downloader = .downloader, .hasher = .hasher,
    .runner = .runner, .quiet = .quiet)
  message("Bridge cached at ", cached)
  cached
}
