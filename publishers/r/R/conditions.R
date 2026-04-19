# Typed condition class hierarchy for the R SDK.
#
# Plan §9 mapping:
#
#   fernsicht_error  (base)
#     ├─ fernsicht_protocol_error    PROTOCOL_VERSION_MISMATCH
#     ├─ fernsicht_session_error     SESSION_FAILED
#     ├─ fernsicht_session_expired   SESSION_EXPIRED (12h ceiling hit)
#     ├─ fernsicht_signaling_error   SIGNALING_UNREACHABLE
#     ├─ fernsicht_binary_error      download / verify / smoke test
#     └─ fernsicht_internal_error    INTERNAL — bridge bug / crash
#
#   fernsicht_warning  (base)
#     ├─ fernsicht_command_warning   INVALID_COMMAND, NO_ACTIVE_TASK
#     ├─ fernsicht_ticket_warning    TICKET_HANDLING_FAILED
#     └─ fernsicht_expiry_warning    1h-before-expiry warning

new_fernsicht_condition <- function(class, message, ..., call = sys.call(-1)) {
  structure(
    class = c(class, "fernsicht_error", "error", "condition"),
    list(message = message, call = call, ...)
  )
}

new_fernsicht_warning_condition <- function(class, message, ..., call = sys.call(-1)) {
  structure(
    class = c(class, "fernsicht_warning", "warning", "condition"),
    list(message = message, call = call, ...)
  )
}

# --- Errors ---

abort_protocol <- function(...) {
  stop(new_fernsicht_condition("fernsicht_protocol_error", paste0(...)))
}

abort_session <- function(...) {
  stop(new_fernsicht_condition("fernsicht_session_error", paste0(...)))
}

abort_session_expired <- function(...) {
  stop(new_fernsicht_condition("fernsicht_session_expired", paste0(...)))
}

abort_signaling <- function(...) {
  stop(new_fernsicht_condition("fernsicht_signaling_error", paste0(...)))
}

abort_binary <- function(...) {
  stop(new_fernsicht_condition("fernsicht_binary_error", paste0(...)))
}

abort_internal <- function(...) {
  stop(new_fernsicht_condition("fernsicht_internal_error", paste0(...)))
}

# --- Warnings ---

warn_command <- function(...) {
  warning(new_fernsicht_warning_condition("fernsicht_command_warning", paste0(...)))
}

warn_ticket <- function(...) {
  warning(new_fernsicht_warning_condition("fernsicht_ticket_warning", paste0(...)))
}

warn_expiry <- function(...) {
  warning(new_fernsicht_warning_condition("fernsicht_expiry_warning", paste0(...)))
}
