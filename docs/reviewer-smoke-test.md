# Reviewer Smoke Test Guide (v2.12.0 Home Auto Session M4)

This guide verifies both supported install paths, portal/diagnostics behavior,
multi-receiver identity/coexistence guidance, and Home Auto Session Milestone 4
cloud-managed config behavior.

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

## 8. Multi-Receiver Identity and Coexistence

Use [Multi-Receiver Identity and Guidance](./multi-receiver-identity.md) and
validate on at least one paired receiver:

1. Verify local portal shows identity context:
   - `local_name`
   - `cloud_receiver_label` (when cloud provides it)
   - site/group labels (when cloud provides them)
2. Verify `doctor -json` and `support-snapshot` include identity fields:
   - installation/local/host hints
   - cloud receiver/site/group labels
3. Simulate replacement/revocation state and verify local guidance explains:
   - this receiver replaced
   - this receiver has been replaced
   - reset and re-pair next steps
4. Verify paired-but-node-missing guidance includes multi-receiver attachment
   checks (node may be attached to another receiver).

## 9. Home Auto Session Milestone 4

Use [Embedded Home Auto Session](./home-auto-session.md) and validate:

1. Open `/home-auto-session` and confirm module status section renders.
2. Save config in `observe` mode with:
   - home geofence lat/lon/radius
   - tracked node list
   - debounce/timeout values
3. Verify status changes to `observe_ready` and clearly indicates observe mode is
   non-controlling.
4. Switch to `control` mode on a paired receiver with cloud credentials.
5. Inject/observe tracked node outside transition and confirm:
   - `start_pending` then `active`
   - decision reason and last action/result populated
6. Inject return/inside transition (or wait idle timeout) and confirm:
   - `stop_pending` then `control_ready`
7. Restart `loramapr-receiverd` while Home Auto Session is active and verify:
   - reconciliation and active-state source are visible
   - duplicate start/stop is not issued
8. Inject stale/invalid/boundary GPS and verify:
   - no auto start is issued from unusable GPS
   - portal shows plain-language GPS reason
9. Force retryable cloud/session error and verify:
   - state transitions to `cooldown`
   - pending action is shown
   - repeated API spam does not occur
10. Force conflict/lifecycle responses and verify stable blocked states:
   - start rejected as already active -> `conflict_blocked`
   - stop rejected with state mismatch -> `conflict_blocked`
   - revoked/disabled/replaced response -> `lifecycle_blocked`
11. Verify diagnostics surfaces include Home Auto Session context:
   - `doctor -json`
   - `status`
   - `support-snapshot`
   including: `control_state`, `active_state_source`, `last_action`,
   `last_action_result`, `effective_config_source`,
   `effective_config_version`, and `last_config_apply_result`.

12. Validate cloud-managed config visibility and fallback:
    - no cloud config returned -> `local_fallback` with
      `cloud_config_missing_local_fallback`
    - valid cloud config returned -> `cloud_managed` with expected version
    - cloud config disables module -> module shows disabled via cloud policy
    - invalid cloud config returned -> local fallback stays active with
      explicit config-apply error
    - temporary cloud outage -> last effective config remains active with
      `cloud_config_fetch_failed_using_last_effective`
