# Reviewer Smoke Test Guide (v2.6.0)

This guide validates automation-aligned attention/remediation signals, local
attention visibility, support bundle export/redaction, and operator guidance.

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
- `attention_state`/`attention_category`/`attention_code`/`attention_hint`
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
- attention summary (state/category/code/hint)
- operational checks

Verify snapshot excludes secret values:

- pairing code
- activation token
- ingest API secret

## 4. Portal Local Attention Visibility

Start runtime and open:

- `/progress`
- `/troubleshooting`
- `/api/ops`

Verify:

- attention state/category/code/hint are visible in portal pages
- operational checks and overall state remain visible/actionable
- `/api/ops` returns both operational checks and attention payload

## 5. Field Workflow Scenarios (Automation-Aligned)

Using docs and local tools, verify runbook paths for:

- receiver offline in cloud
- receiver online but node missing
- paired but no packets forwarding
- receiver revoked/replaced/disabled
- outdated/unsupported receiver state

Reference:

- `docs/support-operations-workflow.md`

## 6. Compatibility Snapshot Path

Simulate schema incompatibility (e.g., newer config/state schema marker) and run:

```bash
./bin/loramapr-receiverd support-snapshot -config /path/to/incompatible-config.json -out /tmp/compat-support.json
```

Verify compatibility snapshot is still exported and indicates
`local_schema_incompatible`.
