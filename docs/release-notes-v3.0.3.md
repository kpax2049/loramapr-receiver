# Release Notes v3.0.3

Date: 2026-03-22

This patch hardens receiver-side liveness and Home Auto reliability during
idle/low-traffic periods.

## Highlights

- Heartbeat cadence and status visibility:
  - Added explicit heartbeat component states (`skipped`, `blocked`, `failed`,
    `sent`) in steady-state processing.
  - Added structured tick logs for supportability:
    - `status tick processed`
    - `heartbeat tick skipped`
    - `heartbeat tick sent`
    - `heartbeat tick failed`
    - `ingest retry scheduled`

- Home Auto Session recovery/liveness hardening:
  - Removed per-second state-file rewrite churn from the Home Auto loop by
    persisting only when meaningful state changes occur.
  - Added structured decision/retry/conflict logs to make control outcomes
    explicit during field triage.

- Validation coverage:
  - Added focused tests for idle heartbeat send path.
  - Added focused tests for heartbeat recovery after retryable failures.
  - Added focused tests ensuring Home Auto idle loop does not rewrite state
    every second.
  - Added focused tests for idle-timeout stop behavior under low-traffic
    conditions.

## Notes

- No cloud API contract changes in this patch.
- No packaging/install-flow redesign in this patch.
