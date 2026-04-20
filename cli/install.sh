#!/bin/sh
# fernsicht CLI installer.
#
# Usage:
#   curl -fsSL https://github.com/MuteJester/Fernsicht/releases/latest/download/install.sh | sh
#   VERSION=v0.1.0 sh -c "$(curl -fsSL ...)"  # pin a specific version
#   INSTALL_DIR=$HOME/bin sh -c "$(curl -fsSL ...)"  # custom install path
#
# Environment variables:
#   VERSION         — release tag to install (default: latest stable).
#   INSTALL_DIR     — where to put the binary (default: /usr/local/bin,
#                     falls back to ~/.local/bin if not writable).
#   SKIP_VERIFY     — if "1", skip ALL verification (NOT recommended).
#   NO_COLOR        — if set, suppress ANSI colors.
#   FERNSICHT_DEBUG — if set, print every step.
#
# The script:
#   1. Detects OS + arch.
#   2. Resolves install dir (writable, fallback to ~/.local/bin).
#   3. Downloads SHA256SUMS for the chosen release.
#   4. If cosign is on PATH, verifies the SHA256SUMS signature.
#   5. Downloads the platform binary.
#   6. Verifies the binary's SHA256 against SHA256SUMS.
#   7. Smoke-tests the binary (`--version` returns 0).
#   8. Atomically installs into INSTALL_DIR.
#   9. Prints next-steps.
#
# Exit codes:
#   0 — installed.
#   1 — generic failure (network, disk, etc.). Trap prints details.
#   2 — environment problem (unsupported platform, missing curl, etc.).

set -eu

# --- Configuration --------------------------------------------------

REPO="MuteJester/Fernsicht"
BIN_NAME="fernsicht"
DEFAULT_INSTALL_DIR="/usr/local/bin"
USER_INSTALL_DIR="$HOME/.local/bin"

# BASE_URL overrides the GitHub release host. Used by tests + corp
# mirrors. Defaults to public GitHub. End users should never need to
# touch this.
BASE_URL="${BASE_URL:-https://github.com/${REPO}/releases}"

VERSION="${VERSION:-}"
INSTALL_DIR="${INSTALL_DIR:-}"
SKIP_VERIFY="${SKIP_VERIFY:-}"
DEBUG="${FERNSICHT_DEBUG:-}"

# --- Colors ---------------------------------------------------------

if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  C_DIM=$(printf '\033[2m')
  C_GREEN=$(printf '\033[32m')
  C_YELLOW=$(printf '\033[33m')
  C_RED=$(printf '\033[31m')
  C_BOLD=$(printf '\033[1m')
  C_RESET=$(printf '\033[0m')
else
  C_DIM=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_BOLD=''; C_RESET=''
fi

step()    { printf '%s→%s %s\n' "$C_GREEN" "$C_RESET" "$1"; }
warn()    { printf '%s!%s %s\n' "$C_YELLOW" "$C_RESET" "$1" >&2; }
err()     { printf '%s✘%s %s\n' "$C_RED"    "$C_RESET" "$1" >&2; }
debug()   { [ -n "$DEBUG" ] && printf '%s  %s%s\n' "$C_DIM" "$1" "$C_RESET" >&2 || true; }
ok()      { printf '%s✓%s %s\n' "$C_GREEN" "$C_RESET" "$1"; }

# --- Cleanup trap ---------------------------------------------------

TMPDIR=""
cleanup() {
  exit_code=$?
  if [ -n "$TMPDIR" ] && [ -d "$TMPDIR" ]; then
    rm -rf "$TMPDIR"
  fi
  if [ "$exit_code" -ne 0 ]; then
    err "installation failed (exit $exit_code)"
    err "for help: open https://github.com/${REPO}/issues with the output above"
  fi
  exit "$exit_code"
}
trap cleanup EXIT INT TERM

# --- Pre-flight checks ----------------------------------------------

require_cmd() {
  for c in "$@"; do
    if ! command -v "$c" >/dev/null 2>&1; then
      err "missing required tool: $c"
      exit 2
    fi
  done
}

require_cmd curl uname mkdir mv chmod sha256sum 2>/dev/null \
  || require_cmd curl uname mkdir mv chmod shasum

# --- Detect OS + arch -----------------------------------------------

detect_platform() {
  os_raw=$(uname -s)
  arch_raw=$(uname -m)

  case "$os_raw" in
    Linux)  os=linux ;;
    Darwin) os=darwin ;;
    *)
      err "unsupported OS: $os_raw"
      err "supported: Linux, macOS. Windows: use install.ps1 (PowerShell)."
      exit 2
      ;;
  esac

  case "$arch_raw" in
    x86_64|amd64)   arch=amd64 ;;
    aarch64|arm64)  arch=arm64 ;;
    *)
      err "unsupported architecture: $arch_raw"
      err "supported: amd64 (x86_64), arm64 (aarch64)."
      exit 2
      ;;
  esac

  PLATFORM="${os}-${arch}"
  ASSET="${BIN_NAME}-${PLATFORM}"
  debug "platform: $PLATFORM"
}

# --- Detect glibc on Linux (warn musl users) ------------------------
#
# Our binaries are CGO-free, so they run on both glibc and musl.
# But this hasn't always been true for every Go program — warn musl
# users so they know what to expect, and tell them where to report
# issues if compatibility breaks down the road.

check_libc() {
  if [ "$os" != "linux" ]; then
    return
  fi
  if [ -f /etc/alpine-release ] || ldd --version 2>&1 | grep -qi musl; then
    debug "musl libc detected (Alpine?); CGO-free binary should work but report issues if not"
  fi
}

# --- Resolve install dir --------------------------------------------

resolve_install_dir() {
  if [ -n "$INSTALL_DIR" ]; then
    debug "using user-specified INSTALL_DIR=$INSTALL_DIR"
    return
  fi

  if [ -w "$DEFAULT_INSTALL_DIR" ] 2>/dev/null; then
    INSTALL_DIR="$DEFAULT_INSTALL_DIR"
    return
  fi

  # /usr/local/bin not writable. Fall back to ~/.local/bin and warn
  # if it isn't on PATH.
  INSTALL_DIR="$USER_INSTALL_DIR"
  mkdir -p "$INSTALL_DIR"
  debug "falling back to user install dir: $INSTALL_DIR"

  if ! echo ":$PATH:" | grep -q ":$INSTALL_DIR:"; then
    PATH_HINT=1
  fi
}

# --- Resolve version (latest unless VERSION pinned) -----------------

resolve_version() {
  if [ -n "$VERSION" ]; then
    # Normalize: accept "v0.1.0", "0.1.0", "cli/v0.1.0".
    case "$VERSION" in
      cli/*) TAG="$VERSION" ;;
      v*)    TAG="cli/$VERSION" ;;
      *)     TAG="cli/v$VERSION" ;;
    esac
    debug "using pinned version: $TAG"
    return
  fi

  # No version specified — resolve "latest" via the GH redirect
  # endpoint. This avoids API rate-limiting and skips pre-releases.
  step "Resolving latest release..."
  redirect_target=$(
    curl -fsSL -o /dev/null -w '%{url_effective}' \
      "${BASE_URL}/latest/download/SHA256SUMS"
  ) || {
    err "could not resolve latest release."
    err "set VERSION=cli/vX.Y.Z manually."
    exit 1
  }
  # Redirect URL looks like:
  #   https://github.com/.../releases/download/cli/v0.1.0/SHA256SUMS
  TAG=$(echo "$redirect_target" | sed -n 's|.*/releases/download/\(cli/v[^/]*\)/.*|\1|p')
  if [ -z "$TAG" ]; then
    err "could not parse release tag from redirect: $redirect_target"
    exit 1
  fi
  ok "Latest release: $TAG"
}

# --- Download + verify ----------------------------------------------

download_and_verify() {
  TMPDIR=$(mktemp -d)
  BASE="${BASE_URL}/download/${TAG}"

  step "Downloading SHA256SUMS..."
  curl -fsSL -o "$TMPDIR/SHA256SUMS" "$BASE/SHA256SUMS"

  if [ -z "$SKIP_VERIFY" ]; then
    verify_signature
  else
    warn "SKIP_VERIFY=1 — bypassing all signature/checksum verification"
  fi

  step "Downloading binary ($ASSET)..."
  curl -fsSL -o "$TMPDIR/$ASSET" "$BASE/$ASSET"

  if [ -z "$SKIP_VERIFY" ]; then
    step "Verifying SHA256..."
    verify_sha256
  fi

  step "Smoke-testing binary..."
  chmod +x "$TMPDIR/$ASSET"
  if ! "$TMPDIR/$ASSET" --version >/dev/null 2>&1; then
    err "downloaded binary failed --version smoke test"
    err "this might mean: incompatible glibc, AppArmor/SELinux, or noexec /tmp"
    exit 1
  fi
}

verify_signature() {
  if ! command -v cosign >/dev/null 2>&1; then
    warn "cosign not installed; skipping signature verification (SHA256 still verified)"
    warn "to enable: install from https://docs.sigstore.dev/cosign/installation"
    return
  fi
  step "Verifying cosign signature on SHA256SUMS..."
  curl -fsSL -o "$TMPDIR/SHA256SUMS.sig"  "$BASE/SHA256SUMS.sig"
  curl -fsSL -o "$TMPDIR/SHA256SUMS.cert" "$BASE/SHA256SUMS.cert"
  cosign verify-blob \
    --certificate "$TMPDIR/SHA256SUMS.cert" \
    --signature   "$TMPDIR/SHA256SUMS.sig"  \
    --certificate-identity-regexp "https://github.com/${REPO}/.+" \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    "$TMPDIR/SHA256SUMS" >/dev/null 2>&1 || {
    err "cosign verification failed — DO NOT USE THIS BINARY"
    err "see SECURITY.md for what to do (someone may have tampered with the release)"
    exit 1
  }
  ok "cosign verified"
}

verify_sha256() {
  expected=$(grep " $ASSET\$" "$TMPDIR/SHA256SUMS" | awk '{print $1}')
  if [ -z "$expected" ]; then
    err "no checksum entry for $ASSET in SHA256SUMS"
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual=$(sha256sum "$TMPDIR/$ASSET" | awk '{print $1}')
  else
    actual=$(shasum -a 256 "$TMPDIR/$ASSET" | awk '{print $1}')
  fi
  if [ "$expected" != "$actual" ]; then
    err "SHA256 MISMATCH for $ASSET"
    err "  expected: $expected"
    err "  actual:   $actual"
    err "DO NOT USE THIS BINARY — re-download or report at github.com/${REPO}/issues"
    exit 1
  fi
  debug "SHA256 verified: $actual"
}

# --- Atomic install -------------------------------------------------

install_binary() {
  step "Installing to $INSTALL_DIR/$BIN_NAME..."

  # Detect existing install for upgrade messaging.
  if [ -f "$INSTALL_DIR/$BIN_NAME" ]; then
    existing=$("$INSTALL_DIR/$BIN_NAME" --version 2>/dev/null \
      | head -n1 | awk '{print $2}' || echo "?")
    debug "existing install: $existing"
  fi

  # Atomic via mv. mktemp dir is on the same FS as INSTALL_DIR (both
  # under /). If they're on different filesystems, mv falls back to
  # copy+delete which isn't atomic but still safe enough here (the
  # smoke test already passed; a partial copy results in a broken
  # binary that fails its OWN smoke test the user runs next).
  mv "$TMPDIR/$ASSET" "$INSTALL_DIR/$BIN_NAME"
  chmod +x "$INSTALL_DIR/$BIN_NAME"
}

# --- Post-install message -------------------------------------------

post_install() {
  printf '\n'
  ok "${C_BOLD}fernsicht installed at $INSTALL_DIR/$BIN_NAME${C_RESET}"
  printf '\n'

  if [ -n "${PATH_HINT:-}" ]; then
    warn "$INSTALL_DIR is not on your PATH"
    warn "add this to your shell profile:"
    printf '  %s%sexport PATH="$PATH:%s"%s\n\n' "$C_BOLD" "$C_GREEN" "$INSTALL_DIR" "$C_RESET"
  fi

  printf "%sQuick start:%s\n" "$C_BOLD" "$C_RESET"
  printf "  %sfernsicht run -- echo hello%s\n" "$C_GREEN" "$C_RESET"
  printf "\n"
  printf "%sMore:%s\n" "$C_BOLD" "$C_RESET"
  printf "  fernsicht --help    %s# top-level help%s\n"        "$C_DIM" "$C_RESET"
  printf "  fernsicht run --help %s# all run-mode flags%s\n"   "$C_DIM" "$C_RESET"
  printf "  fernsicht doctor    %s# diagnose installation (Phase 6)%s\n" "$C_DIM" "$C_RESET"
  printf "\n"
  printf "%sPrivacy: no telemetry, no accounts. Progress flows P2P via WebRTC.%s\n" \
    "$C_DIM" "$C_RESET"
}

# --- Main -----------------------------------------------------------

main() {
  printf '%sFernsicht CLI installer%s\n' "$C_BOLD" "$C_RESET"
  printf '  repo: github.com/%s\n\n' "$REPO"

  detect_platform
  check_libc
  resolve_install_dir
  resolve_version
  download_and_verify
  install_binary
  post_install
}

main
