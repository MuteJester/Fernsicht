# R6 Session class + ambient-session machinery.
#
# Plan §3 / §4.6: there is at most ONE bridge subprocess + session per
# R process. `pkgenv$session` holds the active one. All three public
# API layers funnel through `ambient_session()` so they share state
# (and the same viewer URL).
#
# This file is intentionally internal — the user-facing free functions
# (manual.R / blick.R / scope.R / accessors.R) wrap these methods.

# Package-private state. Initialized in .onLoad as well so it survives
# package reload during dev (devtools::load_all).
pkgenv <- new.env(parent = emptyenv())
pkgenv$session <- NULL
pkgenv$task_stack <- list()

# Helper: parse the bridge's RFC3339 expires_at into POSIXct (UTC).
parse_expires_at <- function(s) {
  if (is.null(s) || !nzchar(s)) return(as.POSIXct(NA))
  # Strip trailing Z (UTC) and parse. Bridge always emits UTC with Z.
  cleaned <- sub("Z$", "", s)
  out <- tryCatch(
    as.POSIXct(cleaned, format = "%Y-%m-%dT%H:%M:%S", tz = "UTC"),
    error = function(e) as.POSIXct(NA)
  )
  if (is.na(out)) return(as.POSIXct(NA))
  out
}

# Map a bridge `error` event onto the typed condition hierarchy.
# Used by Session$.handle_event() and during handshake.
handle_bridge_error_event <- function(ev) {
  fatal <- isTRUE(ev$fatal)
  code  <- ev$code %||% "INTERNAL"
  msg   <- ev$message %||% paste0("bridge error: ", code)
  if (fatal) {
    switch(code,
      "PROTOCOL_VERSION_MISMATCH" = abort_protocol(msg),
      "SESSION_FAILED"            = abort_session(msg),
      "SESSION_EXPIRED"           = abort_session_expired(
        msg,
        "\n(Sessions are capped at 12 hours; restart fernsicht to continue.)"
      ),
      "SIGNALING_UNREACHABLE"     = abort_signaling(msg),
                                    abort_internal(paste0(code, ": ", msg))
    )
  } else {
    switch(code,
      "TICKET_HANDLING_FAILED" = warn_ticket(msg),
                                 warn_command(paste0(code, ": ", msg))
    )
  }
}

# --- The R6 class itself ----------------------------------------------

Session <- R6::R6Class("Session",
  cloneable = FALSE,
  public = list(

    # `proc` is a live processx handle that has already completed the
    # hello → hello_ack → session → session_ready handshake. `ready` is
    # the parsed session_ready event. `bridge_version` comes from the
    # earlier hello_ack and is threaded through.
    initialize = function(proc, ready, bridge_version = NA_character_,
                          label = NULL) {
      private$.proc           <- proc
      private$.room_id        <- ready$room_id
      private$.sender_secret  <- ready$sender_secret  # NEVER exposed.
      private$.viewer_url     <- ready$viewer_url
      private$.expires_at     <- parse_expires_at(ready$expires_at)
      private$.expires_in     <- ready$expires_in %||% NA_integer_
      private$.max_viewers    <- ready$max_viewers %||% NA_integer_
      private$.bridge_version <- bridge_version %||% NA_character_
      private$.label          <- label
      private$.viewer_names   <- character()
      private$.active_task    <- NULL
      private$.expiry_warned  <- FALSE
      private$.closed         <- FALSE

      # GC safety net: if the Session R6 is garbage-collected while
      # still open, close the bridge cleanly. R-exit cleanup is already
      # handled by processx (cleanup = TRUE).
      reg.finalizer(self, function(e) {
        try(e$close(), silent = TRUE)
      }, onexit = TRUE)
    },

    # --- Task lifecycle ---

    start_task = function(task_id, label = NULL) {
      private$ensure_open("start_task")
      bridge_send(private$.proc, list(
        op = "start", task_id = task_id, label = label
      ))
      private$.active_task <- task_id
      private$drain_and_handle()
      invisible(self)
    },

    tick = function(task_id, value, n = NULL, total = NULL,
                    rate = NULL, elapsed = NULL, eta = NULL,
                    unit = NULL) {
      private$ensure_open("tick")
      cmd <- list(op = "progress", task_id = task_id, value = value)
      if (!is.null(n))       cmd$n       <- n
      if (!is.null(total))   cmd$total   <- total
      if (!is.null(rate))    cmd$rate    <- rate
      if (!is.null(elapsed)) cmd$elapsed <- elapsed
      if (!is.null(eta))     cmd$eta     <- eta
      if (!is.null(unit))    cmd$unit    <- unit
      bridge_send(private$.proc, cmd)
      private$drain_and_handle()
      private$check_expiry()
      invisible(self)
    },

    end_task = function(task_id) {
      private$ensure_open("end_task")
      bridge_send(private$.proc, list(op = "end", task_id = task_id))
      if (identical(private$.active_task, task_id)) {
        private$.active_task <- NULL
      }
      private$drain_and_handle()
      invisible(self)
    },

    # --- Lifecycle ---

    close = function() {
      if (private$.closed) return(invisible(self))
      # Mark closed eagerly so re-entry (e.g. via finalizer) is a no-op.
      private$.closed <- TRUE

      proc <- private$.proc
      if (!is.null(proc) && proc$is_alive()) {
        # Best-effort graceful close. Swallow any errors — we're tearing
        # down anyway and the user shouldn't see noise from a broken
        # pipe during shutdown.
        try({
          bridge_send(proc, list(op = "close"))
          bridge_wait_for(proc, "closed", timeout = 3,
            on_event = function(ev) {
              # Suppress fatal-error mapping during close — we're done.
              invisible(NULL)
            })
        }, silent = TRUE)
        try(proc$wait(timeout = 2000), silent = TRUE)
        if (proc$is_alive()) {
          try(proc$kill_tree(), silent = TRUE)
        }
      }

      # Vacate the ambient slot if this was the registered session.
      if (identical(pkgenv$session, self)) {
        pkgenv$session <- NULL
      }
      invisible(self)
    },

    is_open = function() !private$.closed && !is.null(private$.proc) &&
      private$.proc$is_alive(),

    # --- Read-only accessors (free-function wrappers in accessors.R) ---

    viewer_url = function() private$.viewer_url,
    viewers    = function() private$.viewer_names,
    room_id    = function() private$.room_id,

    diagnostics = function() {
      mins <- if (is.na(private$.expires_at)) NA_integer_ else
        as.integer(round(as.numeric(
          difftime(private$.expires_at, Sys.time(), units = "mins")
        )))
      list(
        bridge_version = private$.bridge_version,
        label          = private$.label,
        room_id        = private$.room_id,
        viewer_url     = private$.viewer_url,
        viewer_count   = length(private$.viewer_names),
        names          = private$.viewer_names,
        bridge_pid     = if (is.null(private$.proc)) NA_integer_ else
                          private$.proc$get_pid(),
        expires_at     = if (is.na(private$.expires_at)) NA_character_ else
                          format(private$.expires_at, "%Y-%m-%dT%H:%M:%SZ",
                                 tz = "UTC"),
        expires_in_min = mins
      )
    },

    # --- Pretty-print (redacts sender_secret by construction) ---

    print = function(...) {
      cat("<fernsicht Session>\n")
      if (!is.null(private$.label) && nzchar(private$.label)) {
        cat("  label:       ", private$.label, "\n", sep = "")
      }
      cat("  room:        ", private$.room_id %||% "<unknown>", "\n", sep = "")
      cat("  viewer_url:  ", private$.viewer_url %||% "<unknown>", "\n", sep = "")
      cat("  viewers:     ", length(private$.viewer_names),
          if (length(private$.viewer_names))
            paste0(" (", paste(private$.viewer_names, collapse = ", "), ")")
          else "", "\n", sep = "")
      cat("  bridge:      ", private$.bridge_version %||% "<unknown>", "\n", sep = "")
      cat("  state:       ", if (self$is_open()) "open" else "closed", "\n", sep = "")
      invisible(self)
    },

    # --- Manual event drain (used by tests + accessor refresh) ---

    pump = function() {
      private$drain_and_handle()
      invisible(self)
    }
  ),

  private = list(
    .proc           = NULL,
    .room_id        = NULL,
    .sender_secret  = NULL,    # never exposed via any accessor.
    .viewer_url     = NULL,
    .expires_at     = NULL,
    .expires_in     = NULL,
    .max_viewers    = NULL,
    .bridge_version = NULL,
    .label          = NULL,
    .viewer_names   = character(),
    .active_task    = NULL,
    .expiry_warned  = FALSE,
    .closed         = FALSE,

    ensure_open = function(what) {
      if (private$.closed) {
        abort_internal("session is closed (cannot ", what, ")")
      }
      if (is.null(private$.proc) || !private$.proc$is_alive()) {
        abort_internal(bridge_dead_message(private$.proc, what))
      }
    },

    drain_and_handle = function() {
      events <- bridge_drain(private$.proc)
      for (ev in events) private$process_event(ev)
      invisible(NULL)
    },

    process_event = function(ev) {
      name <- ev$event %||% ""
      switch(name,
        "viewer_joined" = {
          if (!is.null(ev$name) && nzchar(ev$name)) {
            private$.viewer_names <- unique(c(private$.viewer_names, ev$name))
          }
        },
        "viewer_left" = {
          if (!is.null(ev$name)) {
            private$.viewer_names <- setdiff(private$.viewer_names, ev$name)
          }
        },
        "viewer_count" = {
          # Bridge sends `names` as JSON array → parsed to a list when
          # simplifyVector = FALSE. Flatten back to character vector.
          nm <- ev$names
          if (is.list(nm)) nm <- unlist(nm, use.names = FALSE)
          if (is.null(nm)) nm <- character()
          private$.viewer_names <- as.character(nm)
        },
        "error" = {
          handle_bridge_error_event(ev)
        },
        "closed" = {
          private$.closed <- TRUE
        },
        "_parse_error" = {
          warn_command("bridge emitted unparseable line: ", ev$raw %||% "")
        },
        # pong / hello_ack / session_ready / unknown — ignore here.
        invisible(NULL)
      )
    },

    check_expiry = function() {
      if (private$.expiry_warned) return(invisible(NULL))
      if (is.na(private$.expires_at) || is.na(private$.expires_in)) {
        return(invisible(NULL))
      }
      # Warn at 80% elapsed (i.e., 20% remaining).
      threshold <- private$.expires_at -
        as.difftime(0.2 * private$.expires_in, units = "secs")
      if (Sys.time() < threshold) return(invisible(NULL))

      remaining_h <- as.numeric(difftime(
        private$.expires_at, Sys.time(), units = "hours"
      ))
      private$.expiry_warned <- TRUE
      warn_expiry(
        "Fernsicht session expires in ", sprintf("%.1f", remaining_h),
        " hours (at ", format(private$.expires_at, "%H:%M UTC", tz = "UTC"),
        ").\nSessions cannot be refreshed in this version of the bridge. ",
        "For tasks longer than ~12 hours, restart the session to avoid ",
        "losing the live viewer mid-run."
      )
    }
  )
)

# --- Factory + ambient orchestration ---------------------------------

# Spawn the bridge (or use an injected one), perform the
# hello / session handshake, and return a Session R6.
#
# `.proc` is a test seam: pass an already-spawned processx handle to
# bypass binary discovery + spawn. Production callers leave it NULL.
new_session <- function(server_url     = NULL,
                        join_secret    = NULL,
                        max_viewers    = NULL,
                        bridge_path    = NULL,
                        bridge_version = NULL,
                        label           = NULL,
                        hello_timeout   = 5,
                        session_timeout = 30,
                        .proc           = NULL,
                        .runner         = processx::run,
                        .downloader     = utils::download.file,
                        .hasher         = function(p) digest::digest(p, algo = "sha256", file = TRUE)) {
  cfg <- resolve_config(server_url     = server_url,
                        join_secret    = join_secret,
                        max_viewers    = max_viewers,
                        bridge_path    = bridge_path,
                        bridge_version = bridge_version)

  proc <- .proc
  if (is.null(proc)) {
    binary <- find_or_download_binary(
      version     = cfg$bridge_version,
      bridge_path = cfg$bridge_path,
      .runner     = .runner,
      .downloader = .downloader,
      .hasher     = .hasher
    )
    proc <- bridge_spawn(binary)
  }

  # 1. Hello handshake.
  bridge_send(proc, list(
    op = "hello",
    sdk = sdk_name(),
    sdk_version = sdk_version(),
    protocol = PROTOCOL_VERSION
  ))
  ack <- bridge_wait_for(proc, "hello_ack", timeout = hello_timeout,
    on_event = function(ev) {
      if (identical(ev$event, "error")) handle_bridge_error_event(ev)
    })
  bridge_version <- ack$event$bridge_version %||% NA_character_

  # 2. Open publishing session.
  session_cmd <- list(
    op = "session",
    base_url = cfg$server_url,
    max_viewers = cfg$max_viewers
  )
  if (!is.null(cfg$join_secret)) session_cmd$join_secret <- cfg$join_secret

  bridge_send(proc, session_cmd)
  ready <- bridge_wait_for(proc, "session_ready", timeout = session_timeout,
    on_event = function(ev) {
      if (identical(ev$event, "error")) handle_bridge_error_event(ev)
    })

  Session$new(proc, ready$event, bridge_version = bridge_version,
              label = label)
}

# Get-or-create the per-process ambient session. All three API layers
# call this so they share one bridge subprocess + viewer URL.
ambient_session <- function(...) {
  if (!is.null(pkgenv$session) && pkgenv$session$is_open()) {
    return(pkgenv$session)
  }
  s <- new_session(...)
  pkgenv$session <- s
  s
}
