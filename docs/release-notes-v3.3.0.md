# LoRaMapr Receiver v3.3.0

Date: 2026-04-15

`v3.3.0` hardens embedded Home Auto Session control-path recovery for cloud-side
session-state conflicts and transient stop failures.

## Highlights

- Added Home Auto Session stop fallback behavior:
  - stop first attempts with `sessionId`
  - on retryable/stale-session failures, performs bounded fallback stop without
    `sessionId` (device-based stop) using same idempotency key
- Added local active-session reconciliation improvements:
  - clears local active session markers on stop success-like outcomes
    (`stopped`, `already_stopped`, `already_closed`)
  - when cloud reports start conflict with a valid `sessionId`, receiver syncs
    local active session to cloud session id
- Added explicit handling for:
  - `HTTP 409` + `home auto session start is missing sessionId`
  - receiver clears failed start dedupe state and retries with a fresh dedupe
    key using bounded backoff+jitter
- Expanded Home Auto Session observability:
  - request/response logging for start/stop actions
  - endpoint, attempt, status code, cloud request id, and
    `session_id_included` markers
  - explicit conflict retry logging with `dedupe_key_hash` and `next_retry_at`

## Safety/Behavior Notes

- Retry behavior remains bounded with max cooldown cap.
- Stop fallback remains idempotent by preserving dedupe semantics.
- No cloud contract break: receiver changes are backward compatible and scoped
  to Home Auto Session client behavior/diagnostics.
