# Reviewer Smoke Test Guide (v2.5.0)

This guide validates support-bundle export, redaction, coarse ops checks, and
field troubleshooting guidance.

## 1. Build and Tests

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
```

Focused coverage:

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/diagnostics -run Test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/webportal -run Ops
```

## 2. Doctor and Status Operational Checks

```bash
./bin/loramapr-receiverd doctor -config ./receiver.example.json
./bin/loramapr-receiverd doctor -config ./receiver.example.json -json
./bin/loramapr-receiverd status -config ./receiver.example.json
```

Verify outputs include:

- `failure_code`/`failure_summary`/`failure_hint`
- `local_runtime_probe`
- `operational_status`/`operational_summary`
- `operational_checks[]`
- config/state schema markers

## 3. Support Snapshot Export and Redaction

```bash
./bin/loramapr-receiverd support-snapshot -config ./receiver.example.json -out /tmp/receiver-support.json
cat /tmp/receiver-support.json
```

Verify snapshot includes:

- runtime metadata (version/channel/build/platform/install type)
- config/state markers
- lifecycle/update/cloud/node summaries
- operational checks

Verify snapshot excludes secret values:

- pairing code
- activation token
- ingest API secret

## 4. Portal Ops Visibility

Start runtime and open:

- `/progress`
- `/troubleshooting`
- `/api/ops`

Verify operational checks and overall state are visible and actionable.

## 5. Field Workflow Scenarios

Using docs and local tools, verify runbook paths for:

- receiver offline in cloud
- receiver online but node missing
- paired but no packets forwarding
- receiver revoked/replaced/disabled

Reference:

- `docs/support-operations-workflow.md`

## 6. Compatibility Snapshot Path

Simulate schema incompatibility (e.g., newer config/state schema marker) and run:

```bash
./bin/loramapr-receiverd support-snapshot -config /path/to/incompatible-config.json -out /tmp/compat-support.json
```

Verify compatibility snapshot is still exported and indicates
`local_schema_incompatible`.
