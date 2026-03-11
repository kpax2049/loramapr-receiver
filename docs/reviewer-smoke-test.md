# Reviewer Smoke Test Guide (v2.3.0)

This guide validates receiver-side lifecycle behavior for revoked/disabled/
replaced/reset/re-pair scenarios.

## Prerequisites

- Go toolchain
- receiver config file (for local runtime checks)
- optional Debian-family host for package lifecycle checks

## 1. Build and Unit Tests

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
```

Targeted lifecycle tests:

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/runtime -run Lifecycle
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/pairing -run Lifecycle
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/diagnostics -run Taxonomy
```

Expected:

- tests pass
- lifecycle transitions map to explicit local failure codes

## 2. Local Reset/Re-pair Flow

Run explicit local reset:

```bash
loramapr-receiverd reset-pairing -config /etc/loramapr/receiver.json
loramapr-receiverd status -config /etc/loramapr/receiver.json
```

Expected:

- pairing phase becomes `unpaired`
- status reflects setup-ready pairing flow
- durable credentials are cleared after default deauthorize reset

## 3. Portal Lifecycle Recovery Path

1. Open local portal: `http://<receiver-host>:8080/troubleshooting`
2. Use `Reset And Re-pair`.
3. Navigate to Pairing page and submit a fresh pairing code.

Expected:

- reset action redirects to Pairing with reset confirmation
- receiver returns to pairing flow without reinstall

## 4. Lifecycle Failure Visibility

Use diagnostics surfaces:

```bash
loramapr-receiverd doctor -config /etc/loramapr/receiver.json
loramapr-receiverd support-snapshot -config /etc/loramapr/receiver.json -out /tmp/receiver-support.json
cat /tmp/receiver-support.json | jq
```

Expected lifecycle codes when applicable:

- `receiver_credential_revoked`
- `receiver_disabled`
- `receiver_replaced`

Support snapshot remains redacted (no pairing code/token/ingest secret values).

## 5. Reinstall/Recovery Semantics (Debian-family)

Validate package lifecycle expectations:

1. install package
2. run `reset-pairing`
3. `apt remove` then reinstall
4. confirm runtime starts and pairing portal is available

Policy expectations are documented in:

- `docs/linux-package-lifecycle.md`
- `docs/receiver-lifecycle.md`
