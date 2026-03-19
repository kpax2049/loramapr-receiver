# LoRaMapr Receiver v2.16.0 (Linux/Pi One-Click Hardening)

Date: 2026-03-19

This release hardens the supported Raspberry Pi OS Lite / Debian-family install
path so first-run setup is closer to true one-command behavior.

## Highlights

- Packaged Linux/Pi defaults now align with production behavior:
  - `runtime.profile=linux-service`
  - `paths.state_file=/var/lib/loramapr/receiver-state.json`
  - `portal.bind_address=0.0.0.0:8080`
  - production cloud URL default for package installs
- Package/postinst hardening for fresh Pi/Linux hosts:
  - serial prerequisites and service account expectations tightened
  - runtime/log directory ownership/permissions normalized
- First-run cloud bootstrap no longer requires manual JSON edits:
  - bootstrap supports `--cloud-base-url`
  - `loramapr-receiverd configure-cloud` command available for post-install
    endpoint changes
- Meshtastic direct USB path now uses native serial protocol framing as primary
  supported mode:
  - native framed/protobuf decode for `meshtastic.transport=serial`
  - `json_stream` retained as explicit compatibility/test mode
- First-run diagnostics UX improved:
  - portal now surfaces concrete **Setup Root Cause** issues
  - `/api/ops` now includes `setup_issues[]`
  - support snapshot now includes `setup.issues`
  - root causes cover bind-address, cloud endpoint, USB detect/protocol, and
    ingest-forwarding readiness

## Operator Impact

- Fresh Pi/Linux installs should no longer need manual config edits for:
  - portal LAN access
  - state path correctness
  - initial cloud endpoint setup in local/self-hosted flows
- Directly attached Meshtastic USB nodes no longer require a JSON bridge for
  the supported path.
- Setup failures are reported in product terms with concrete next steps.

## Compatibility Notes

- Existing `json_stream` Meshtastic transport remains available for test/compat
  use cases.
- No changes were made to cloud ingest architecture; receiver still forwards to
  existing backend ingest endpoints.

