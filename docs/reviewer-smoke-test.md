# Reviewer Smoke Test Guide (v2.4.0)

This guide validates update/channel reporting, upgrade-safe behavior, and
update-status reasoning.

## Prerequisites

- Go toolchain
- local checkout of `loramapr-receiver`
- optional Debian-family host for package install checks

## 1. Build and Unit Tests

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
```

Targeted v2.4 areas:

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/update -run Test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/config -run Schema
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/state -run SchemaV2ToV3
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/runtime -run UnsupportedCloudConfig
```

Expected:

- all tests pass
- update reasoning and migration guards are enforced

## 2. Build Metadata and Status Surface Check

Build a local binary and inspect status output:

```bash
go build -o ./bin/loramapr-receiverd ./cmd/loramapr-receiverd
./bin/loramapr-receiverd status -config ./receiver.example.json
./bin/loramapr-receiverd doctor -config ./receiver.example.json -json
```

Expected fields present:

- `receiver_version`
- `release_channel`
- `build_commit`
- `build_date` (if available)
- `build_id` (if available)
- `platform`, `arch`, `install_type`

## 3. Config/State Upgrade Safety Check

Validate migration and fail-fast semantics:

1. Start runtime once with current config/state.
2. Confirm state file has `schema_version: 3`.
3. Set config `schema_version` to a future value (for example `99`) and run:
   - `loramapr-receiverd doctor -config ...`
4. Revert config to valid schema and rerun.

Expected:

- newer config schema fails with explicit compatibility hint
- valid schema resumes normal operation

## 4. Update-Status Reasoning Check

Create a local manifest fragment and point `update.manifest_url` at it
(or use test HTTP fixture flow).

Verify representative statuses by varying installed/manifest channel/version:

- `current`
- `outdated`
- `channel_mismatch`
- `ahead`
- `unsupported` (set `update.min_supported_version`)

Check surfaces:

- portal Progress/Advanced
- `doctor` JSON
- `status` JSON

## 5. Cloud Config Compatibility Check

Use runtime tests or mock cloud ACK behavior to return unsupported
`configVersion` (non-v1 major).

Expected:

- runtime enters blocked cloud-config state
- readiness reflects blocked service path
- diagnostics failure code includes `cloud_config_incompatible`
- forwarding/steady-state loop does not continue as healthy

## 6. Support Snapshot Redaction and Metadata

```bash
./bin/loramapr-receiverd support-snapshot \
  -config ./receiver.example.json \
  -out /tmp/receiver-support.json
cat /tmp/receiver-support.json
```

Expected:

- includes runtime version/channel/build/platform/install-type metadata
- includes update-status summary if available
- excludes secret values (`pairing_code`, `activation_token`, ingest API secret)

## 7. Linux/Pi Install Path Regression Check (Optional)

If validating package/install path as part of broader GA regression:

1. install current package/image path
2. ensure service starts
3. open portal and confirm pairing-ready state
4. run `status`/`doctor` and verify update/build metadata visibility

Package/appliance specifics are documented in Linux/Pi GA docs; this step is a
sanity pass for v2.4 runtime surfaces.
