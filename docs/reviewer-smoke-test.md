# Reviewer Smoke Test Guide (v2.7.0 Public Launch)

This guide verifies public launch readiness for both supported install paths,
portal guidance, and troubleshooting/support flows.

## 1. Build and Baseline Tests

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make build
```

Focused coverage:

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/webportal -run Test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./internal/diagnostics -run Test
```

## 2. Path A: Raspberry Pi Appliance (Public Recommended Path)

Validate docs flow using [Raspberry Pi Appliance Path](./raspberry-pi-appliance.md):

1. Flash published Pi image.
2. Boot Pi on LAN and connect Meshtastic device.
3. Open local portal via `.local` or LAN IP.
4. Confirm pairing page is reachable and pairing-ready state is clear.
5. Confirm Troubleshooting page offers actionable guidance without SSH-first assumptions.

## 3. Path B: Existing Linux/Pi OS Package Install

Validate docs flow using [Linux/Pi Existing-OS Install Path](./linux-pi-distribution.md):

1. Configure signed APT repo.
2. Install `loramapr-receiver` package.
3. Confirm `loramapr-receiverd` service starts.
4. Open local portal and confirm pairing-ready flow.
5. Confirm remove/purge behavior in docs is coherent with package lifecycle policy.

## 4. Local Portal and Attention Guidance

Validate portal copy on:

- `/`
- `/pairing`
- `/progress`
- `/troubleshooting`

Check:

- first-time next-step guidance is clear
- attention states are understandable (`none/info/action_required/urgent`)
- raw codes are present but secondary to actionable guidance

## 5. Diagnostics and Support Export

Run:

```bash
./bin/loramapr-receiverd doctor -config ./receiver.example.json
./bin/loramapr-receiverd doctor -config ./receiver.example.json -json
./bin/loramapr-receiverd support-snapshot -config ./receiver.example.json -out /tmp/receiver-support.json
```

Verify:

- failure + attention + operational fields are present
- support snapshot is useful and redacted (no secrets)

## 6. Troubleshooting and Recovery Flows

Cross-check [Support and Troubleshooting Workflow](./support-operations-workflow.md)
for representative cases:

- cloud unreachable
- node missing/not connected
- paired but no forwarding
- revoked/replaced/disabled lifecycle state
- outdated/unsupported receiver state

## 7. Release Surface and Artifact Clarity

Validate docs explain user download choice clearly:

- Pi appliance image vs Linux package path
- artifact naming and checksum verification

References:

- [Release Artifact Mapping](./release-artifacts.md)
- [Release Notes Index](./release-notes.md)
