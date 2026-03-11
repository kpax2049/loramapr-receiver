# LoRaMapr Receiver v2.5.0 (Support and Operations Maturity)

Release date: 2026-03-11

## Highlights

- Added receiver-side support/ops maturity plan and implementation for
  diagnostics export and field operations visibility.
- Hardened `support-snapshot` export with richer support-safe content:
  - runtime/version/build/install metadata
  - config/state schema markers
  - lifecycle/pairing/cloud/update summaries
  - local runtime probe summary
  - coarse operational checks
- Added compatibility snapshot path for schema mismatch scenarios so support
  export remains available when config/state loading fails.
- Added lightweight local runtime observability probe (`/api/status` consumer)
  and operational checks API:
  - `GET /api/ops`
- Expanded coarse failure taxonomy for supportability and compatibility:
  - `receiver_outdated`
  - `receiver_version_unsupported`
  - `local_schema_incompatible`
- Improved portal troubleshooting with operational checks and clearer guidance
  for update/supportability and schema issues.
- Expanded doctor/status outputs with operational checks and local runtime probe
  context.

## Scope and Safety

- No runtime architecture redesign.
- No secret-bearing exports were added.
- Existing install paths, lifecycle behavior, and update-status behavior remain
  intact.
