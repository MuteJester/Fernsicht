# Configuration resolution: function arg > R option > env var > default.
#
# Plan §4.5. Each setting has the same shape: pick the first non-NULL/
# non-empty value across the four sources. Centralizing here means
# new settings only need one entry, not four code paths.

# Value-or-default helper. NULL is "unset" everywhere.
`%||%` <- function(a, b) if (is.null(a) || (is.character(a) && !nzchar(a))) b else a

# Read env var, returning NULL when unset (empty string).
env_or_null <- function(name) {
  v <- Sys.getenv(name, unset = "")
  if (nzchar(v)) v else NULL
}

resolve_config <- function(server_url = NULL,
                           join_secret = NULL,
                           max_viewers = NULL,
                           bridge_path = NULL,
                           bridge_version = NULL) {
  list(
    server_url = server_url
      %||% getOption("fernsicht.server_url")
      %||% env_or_null("FERNSICHT_SERVER_URL")
      %||% "https://signal.fernsicht.space",
    join_secret = join_secret
      %||% getOption("fernsicht.join_secret")
      %||% env_or_null("FERNSICHT_JOIN_SECRET"),
    max_viewers = as.integer(
      max_viewers
      %||% getOption("fernsicht.max_viewers")
      %||% env_or_null("FERNSICHT_MAX_VIEWERS")
      %||% 8L
    ),
    bridge_path = bridge_path
      %||% getOption("fernsicht.bridge_path")
      %||% env_or_null("FERNSICHT_BRIDGE_PATH"),
    bridge_version = bridge_version
      %||% getOption("fernsicht.bridge_version")
      %||% env_or_null("FERNSICHT_BRIDGE_VERSION")
      %||% BRIDGE_VERSION
  )
}

# SDK identifier sent in the hello command. Hardcoded for the R SDK.
sdk_name <- function() "r"

# SDK semver — read from DESCRIPTION at install time so we don't hardcode.
sdk_version <- function() {
  v <- tryCatch(
    as.character(utils::packageVersion("fernsicht")),
    error = function(e) "0.0.0"
  )
  v
}

# Protocol version we speak. Bumped only when the bridge wire format
# breaks compatibility — see BRIDGE_PROTOCOL.md.
PROTOCOL_VERSION <- 1L
