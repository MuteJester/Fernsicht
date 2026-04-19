#!/usr/bin/env Rscript
# Standalone fake Fernsicht signaling server for integration tests.
#
# Spawned as a subprocess so its httpuv event loop can run independently
# of the parent R session (which is blocked synchronously waiting on
# bridge subprocess I/O).
#
# Usage (set via env vars before launching):
#   FAKE_SIG_PORT=NNNN              required — TCP port to bind
#   FAKE_SIG_ROOM_ID=...            default "intg-room-01"
#   FAKE_SIG_SECRET=...             default "FAKE-INTEGRATION-SECRET"
#   FAKE_SIG_API_KEY_REQUIRED=...   if non-empty, /session requires
#                                    matching X-Fernsicht-Api-Key
#   FAKE_SIG_EXPIRES_IN=N           default 3600
#
# Prints "READY <port>" to stdout once the server is listening so the
# parent can know when to start hitting it.

suppressPackageStartupMessages({
  library(httpuv)
  library(jsonlite)
})

`%||%` <- function(a, b) if (is.null(a) || (is.character(a) && !nzchar(a))) b else a

port              <- as.integer(Sys.getenv("FAKE_SIG_PORT"))
room_id           <- Sys.getenv("FAKE_SIG_ROOM_ID", "intg-room-01")
sender_secret     <- Sys.getenv("FAKE_SIG_SECRET",  "FAKE-INTEGRATION-SECRET")
api_key_required  <- Sys.getenv("FAKE_SIG_API_KEY_REQUIRED", "")
expires_in        <- as.integer(Sys.getenv("FAKE_SIG_EXPIRES_IN", "3600"))

if (is.na(port)) stop("FAKE_SIG_PORT must be set")

app <- list(
  call = function(req) {
    method <- req$REQUEST_METHOD
    path   <- req$PATH_INFO

    if (path == "/session" && method == "POST") {
      if (nzchar(api_key_required)) {
        provided <- req$HTTP_X_FERNSICHT_API_KEY %||% ""
        if (!identical(provided, api_key_required)) {
          return(list(
            status = 403L,
            headers = list("Content-Type" = "application/json"),
            body = '{"error":"invalid api key"}'
          ))
        }
      }
      body <- list(
        room_id            = room_id,
        sender_secret      = sender_secret,
        viewer_url         = paste0("https://app.example/#room=", room_id),
        signaling_url      = paste0("http://127.0.0.1:", port),
        expires_at         = format(Sys.time() + expires_in,
                                     "%Y-%m-%dT%H:%M:%SZ", tz = "UTC"),
        expires_in         = expires_in,
        max_viewers        = 8L,
        poll_interval_hint = 1L
      )
      return(list(
        status = 200L,
        headers = list("Content-Type" = "application/json"),
        body = jsonlite::toJSON(body, auto_unbox = TRUE)
      ))
    }

    if (startsWith(path, "/poll/") && method == "GET") {
      return(list(
        status = 200L,
        headers = list("Content-Type" = "application/json"),
        body = '{"tickets":[]}'
      ))
    }

    if (startsWith(path, "/ticket/")) {
      return(list(status = 404L, headers = list(), body = "no ticket"))
    }

    list(status = 404L, headers = list(), body = "")
  }
)

server <- startServer("127.0.0.1", port, app)
on.exit(try(stopServer(server), silent = TRUE), add = TRUE)

# Signal readiness, then run the event loop.
cat("READY ", port, "\n", sep = "")
flush(stdout())

while (TRUE) {
  service(timeoutMs = 100L)
  Sys.sleep(0.005)
}
