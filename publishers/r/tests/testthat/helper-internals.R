# Aliases for internal functions so tests can call them unqualified.
# testthat auto-loads helper-*.R files before any test-*.R runs.

os_arch              <- fernsicht:::os_arch
ext                  <- fernsicht:::ext
cache_dir            <- fernsicht:::cache_dir
cache_binary_path    <- fernsicht:::cache_binary_path
download_url         <- fernsicht:::download_url
smoke_test_binary    <- fernsicht:::smoke_test_binary
download_binary      <- fernsicht:::download_binary
find_or_download_binary <- fernsicht:::find_or_download_binary

BUNDLED_SHA256       <- fernsicht:::BUNDLED_SHA256
BRIDGE_VERSION       <- fernsicht:::BRIDGE_VERSION

bridge_spawn         <- fernsicht:::bridge_spawn
bridge_send          <- fernsicht:::bridge_send
bridge_drain         <- fernsicht:::bridge_drain
bridge_wait_for      <- fernsicht:::bridge_wait_for

abort_internal       <- fernsicht:::abort_internal
abort_session        <- fernsicht:::abort_session
abort_binary         <- fernsicht:::abort_binary

resolve_config       <- fernsicht:::resolve_config
sdk_name             <- fernsicht:::sdk_name
sdk_version          <- fernsicht:::sdk_version
PROTOCOL_VERSION     <- fernsicht:::PROTOCOL_VERSION

Session              <- fernsicht:::Session
new_session          <- fernsicht:::new_session
ambient_session      <- fernsicht:::ambient_session
parse_expires_at     <- fernsicht:::parse_expires_at
pkgenv               <- fernsicht:::pkgenv

ensure_active_session <- fernsicht:::ensure_active_session
ensure_session        <- fernsicht:::ensure_session
generate_task_id      <- fernsicht:::generate_task_id
