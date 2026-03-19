# v2.16.0 Plan: Linux/Pi One-Click Install Hardening

## Scope

This plan turns fresh-Pi troubleshooting findings into a concrete hardening plan
for the supported Linux/Pi OS package install path.

Target outcome for one-command install:

- portal reachable on LAN after install
- pairing works without manual config edits
- Meshtastic USB node auto-connects on common Pi/Linux hosts
- packets forward to cloud ingest once paired
- diagnostics expose specific root cause when setup fails

Non-goals for this plan:

- non-Linux installers
- runtime architecture redesign
- advanced Meshtastic feature expansion

## Confirmed Root Causes (Current)

1. Packaged config uses dev-oriented defaults in `.deb` output.
   - `.deb` build currently copies `receiver.example.json` directly into
     `/etc/loramapr/receiver.json`.
   - `receiver.example.json` currently sets:
     - `paths.state_file = "./data/receiver-state.json"`
     - `portal.bind_address = "127.0.0.1:8080"`
     - `cloud.base_url = "https://api.loramapr.example"`
2. Portal bind default for packaged installs is localhost-only.
   - `internal/install/linux.go` also writes `127.0.0.1:8080` in generated
     Linux config.
3. Cloud base URL requires manual editing when not overridden by packaging.
   - Runtime/config defaults still point to placeholder domain.
4. Meshtastic adapter expects newline JSON but real USB node sends native serial
   frames.
   - `internal/meshtastic/adapter.go` reads with `bufio.Scanner` by line.
   - `internal/meshtastic/normalize.go` decodes JSON lines only.
5. Package install does not fully harden serial permissions.
   - `packaging/debian/scripts/postinst` creates `loramapr` user/group but does
     not ensure `dialout` access.
6. Ops/portal guidance is still too generic for this failure class.
   - `/api/ops` often reports coarse `degraded` with broad hints while real
     root cause is protocol mismatch or permission failure.

## Biggest Blocker To True One-Click

The primary blocker is the Meshtastic transport mismatch:

- packaged receiver defaults to `meshtastic.transport=serial`
- real devices on `/dev/ttyACM*` expose native Meshtastic serial frames
- current adapter cannot parse those frames without an external JSON bridge

Until native serial frame support is implemented (or an internal bridge is
embedded), one-click install cannot guarantee automatic node connectivity and
packet ingest.

## Workstreams

## 1) Packaged Defaults (Low Risk, High Impact)

Goal: ensure package installs start with production-ready Linux defaults.

Changes:

- introduce explicit packaged config template (separate from dev example)
- packaged defaults should set:
  - `paths.state_file=/var/lib/loramapr/receiver-state.json`
  - `portal.bind_address=0.0.0.0:8080`
  - `runtime.profile=linux-service`
  - `cloud.base_url` to production default (not placeholder)
  - `meshtastic.transport=serial` with empty `device` (auto-detect)
- keep `receiver.example.json` for local/dev usage if still needed

Primary files:

- `packaging/debian/build-deb.sh`
- `internal/install/linux.go`
- `receiver.example.json` (or replace with split dev/package templates)
- `docs/runtime-config-state.md`
- `docs/linux-pi-distribution.md`

## 2) Install/Postinst Hardening (Low-Medium Risk)

Goal: package install leaves service with serial access and predictable runtime
permissions.

Changes:

- ensure `loramapr` service identity has serial access:
  - add `loramapr` to `dialout` in `postinst`, and/or
  - add `SupplementaryGroups=dialout` in systemd unit
- keep `/var/lib/loramapr` and `/var/log/loramapr` ownership enforced
- add post-install validation hint when expected serial access is missing
- ensure lifecycle scripts preserve/restore behavior remains stable

Primary files:

- `packaging/debian/scripts/postinst`
- `packaging/linux/systemd/loramapr-receiverd.service`
- `packaging/debian/validate-lifecycle.sh`
- `docs/linux-package-lifecycle.md`

## 3) First-Run Cloud Bootstrap (Medium Risk)

Goal: avoid manual cloud URL edits for normal installs while still supporting
local-dev override.

Changes:

- make bootstrap/install path authoritative for cloud origin:
  - default to production cloud URL in packaged config
  - support explicit override in bootstrap script for local cloud testing
- add startup/pairing preflight messaging if cloud base URL is unreachable
- document local-dev override flow clearly (non-default path)

Primary files:

- `packaging/linux/scripts/bootstrap-apt.sh`
- `docs/linux-pi-distribution.md`
- `README.md`
- `internal/diagnostics/taxonomy.go` (cloud misconfig hints)

## 4) Native Meshtastic Serial Support (Deep Runtime Work)

Goal: parse native Meshtastic serial protocol directly in receiver.

Changes:

- add native serial framing/parser path inside `internal/meshtastic`
  (protobuf/frame decoding)
- keep `json_stream` as explicit compatibility/test mode
- preserve bounded async event emission and non-blocking forwarding
- classify parse/protocol errors into stable adapter states/reasons
- add targeted tests with captured native frame fixtures

Primary files:

- `internal/meshtastic/adapter.go`
- `internal/meshtastic/normalize.go` (or split into JSON/native normalizers)
- new native serial parser files in `internal/meshtastic/`
- `internal/runtime/service.go` (state/hint mapping)
- `docs/meshtastic-adapter.md`

## 5) Diagnostics and Portal UX Hardening (Medium Risk)

Goal: expose actionable root causes, not only coarse degraded state.

Changes:

- extend failure mapping for serial failure classes (examples):
  - permission denied/open failure
  - protocol mismatch (non-JSON/native parse failure)
  - device detected but unreadable stream
- improve `node_connection` and troubleshooting hints with exact operator next
  action
- show concise adapter root-cause detail on portal Progress/Troubleshooting
  without leaking sensitive values
- align `doctor`, `/api/ops`, and portal wording

Primary files:

- `internal/diagnostics/taxonomy.go`
- `internal/diagnostics/operations.go`
- `internal/webportal/server.go`
- `internal/webportal/templates/progress.tmpl`
- `internal/webportal/templates/troubleshooting.tmpl`
- `docs/diagnostics.md`
- `docs/support-operations-workflow.md`

## Dependencies and Execution Order

Recommended order:

1. Packaged defaults
2. Install/postinst hardening
3. First-run cloud bootstrap hardening
4. Diagnostics UX quick wins for steps 1-3
5. Native Meshtastic serial support
6. Final diagnostics pass for native serial failure classification
7. End-to-end Pi smoke tests and docs consolidation

Dependency notes:

- Workstreams 1-3 can ship incrementally and immediately remove most manual
  setup edits.
- Workstream 4 should run twice: first for packaging/bootstrap root causes, then
  again after native serial support lands.
- Workstream 5 is required for true “plug in home node and ingest works” on
  fresh Pi without sidecar tooling.

## Validation Gates (Definition of Done for hardening series)

For a fresh Raspberry Pi OS Lite host:

1. One command install succeeds.
2. Portal opens on LAN without config edits.
3. Pairing works against default cloud origin (or explicit override flag).
4. USB Meshtastic node auto-detects and reaches `connected`.
5. Packets are queued and acknowledged by cloud ingest.
6. If any step fails, `/api/ops`, portal, and `doctor` surface specific
   actionable reason.

## Next Prompt Landing Zones

Implementation prompts should land in this sequence:

1. packaging defaults + postinst hardening
2. bootstrap/cloud preflight hardening
3. native Meshtastic serial support
4. diagnostics + portal message hardening
5. integration/release smoke-test pass
