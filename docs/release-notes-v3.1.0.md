# Release Notes v3.1.0

Date: 2026-03-23

This release ships minimum receiver-side observability hardening for structured
logging and cloud-call correlation.

## Highlights

- Standardized structured JSON logs for runtime and bridge paths with required
  support fields:
  - `timestamp`
  - `level`
  - `service`
  - `environment`
  - `message`
  - `requestId`
  - `receiverId`
  - `operation`
  - `statusCode`
  - `errorCode`

- Added request-ID propagation for outbound cloud calls:
  - receiver now generates/reuses request IDs per outbound request
  - sends `X-Request-Id` on ingest and heartbeat calls

- Added correlated outbound cloud result logging:
  - success/failure logs include request ID, operation, route, status code, and
    coarse error code

## Notes

- Heartbeat sender behavior remains startup + fixed interval (existing
  `service.heartbeat` config).
- No architecture redesign or external telemetry stack changes in this release.
