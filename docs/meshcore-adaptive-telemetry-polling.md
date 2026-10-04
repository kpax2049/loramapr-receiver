# MeshCore adaptive telemetry polling (developer reference)

The receiver has an adaptive polling controller for one selected canonical
MeshCore field-device public key. It uses the existing MeshCore telemetry
request, response-correlation, normalization, and durable outbox path; it does
not implement a second telemetry protocol path. Stock MeshCore Companion
firmware is sufficient.

## Lifecycle boundary

The product lifecycle is Session-bound: an active LoRaMapr Session supplies
the receiver's authoritative tracking intent, and ending that Session removes
it. No active Session means no automatic telemetry requests. The local receiver
endpoints below are a diagnostic harness, not an independent always-on
subsystem. Controller state is deliberately ephemeral and is not restored after
a receiver restart; Cloud reaffirms any active Session on a later heartbeat.

## Local harness API

- `POST /api/meshcore/tracking/start` with `{ "publicKey": "<64 lowercase hex characters>" }`
- `POST /api/meshcore/tracking/stop`
- `GET /api/meshcore/tracking/status`

Only one target can be active. Starting another target returns a conflict.
Starting triggers an immediate request. The existing manual
`POST /api/meshcore/telemetry/request` shares the adapter's single in-flight
request arbiter, returning a clear conflict instead of allowing ambiguous
prefix-correlated requests.

## Policy and movement inference

The centralized conservative defaults are:

| Operational state | Poll interval |
| --- | ---: |
| unknown / startup | 30 seconds |
| stationary, under 1 km/h | 45 seconds |
| slow, 1–8 km/h | 30 seconds |
| fast, over 8 km/h | 15 seconds |
| hard minimum | 10 seconds |

Speed is calculated from successive correlated telemetry latitude/longitude
samples and receiver observation timestamps, using Haversine distance divided
by elapsed time. It needs two usable fixes. Missing/invalid GPS, duplicate
coordinates, too-close timestamps, jumps above 10 km, and implausible speeds
above 250 km/h are handled safely;
no divide-by-zero calculation is possible. A first valid inferred band exits
`unknown`; later band changes need two consecutive samples, preventing
threshold flapping.

This inference is scheduler utility only. Solicited GPS remains
request-correlated, not independently signed or trusted/current position. M7A
does not create track points, coverage, or cloud projections.

## Safety, failure, and reconnect behavior

There is one request path and one adapter-level request in flight. Manual and
diagnostic tracking never makes tight retries: the first failure waits at least
the unknown interval, repeated failures double the interval, and the delay is
capped at five minutes. A successful correlated response resets the failure
count. Adapter or BLE disconnect uses that bounded local-transport backoff for
both ownership modes, so a disconnected Companion cannot create a CPU/BLE
reconnect loop. A later successful request after deliberate adapter reconnect
logs recovery and resets normal scheduling.

Stopping cancels scheduled future polls but does not cancel an already-issued
request, so an already-arriving correlated response can still follow its
normal durable-event path. The controller logs lifecycle changes, polls,
classification/interval changes, failures/backoff, and adapter unavailable or
recovered states without logging raw payloads.

## M7A.1 route observability

The controller records the last 50 attempts in the existing local-only
`GET /api/meshcore/tracking/status` response as `recentPolls`. This history is
bounded, process-local, and cleared when a new local tracking run starts; it is
not sent to the cloud or stored in a database.

Before each telemetry request, the receiver asks the Companion for the target
contact (`CMD_GET_CONTACT_BY_KEY`, 30) and snapshots its `out_path` fields. The
pinned source writes the contact frame as public key, type, flags,
`out_path_len`, and 64 route bytes in
[`MyMesh.cpp:154-174`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L154-L174)
and returns it for that command at
[`MyMesh.cpp:1233-1240`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L1233-L1240).

Route terminology is deliberately narrow:

| `routeAttempt.mode` | Evidence |
| --- | --- |
| `zero_hop` | The selected contact has an explicit zero-length `out_path`. |
| `explicit_path` | The selected contact has one or more cached route hashes. `path` contains those hashes, **not repeater identities**. |
| `flood` | The contact has `OUT_PATH_UNKNOWN` (`0xff`), or the telemetry `RESP_CODE_SENT` states it was sent as flood. |
| `unknown` | The Companion did not expose a usable contact path, or reported only its ambiguous `direct` result. |

The telemetry request implementation calls the firmware's `sendRequest`; that
function floods only when `out_path_len == OUT_PATH_UNKNOWN`, otherwise it uses
`sendDirect` with the cached path
([`BaseChatMesh.cpp`, `sendRequest`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/src/helpers/BaseChatMesh.cpp)).
The telemetry handler reports `RESP_CODE_SENT[1]` as flood (1) or firmware
`direct` (0) at
[`MyMesh.cpp:1534-1549`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L1534-L1549).
Firmware `direct` is not treated as proof of zero-hop: it can be an explicit
routed path. The contact snapshot is needed to distinguish the two.

`CMD_SEND_BINARY_REQ` has no supported per-request flood/direct/explicit-path
selector: its frame carries the target key and request payload, and Companion
passes the looked-up contact to `sendRequest`. The supported recovery controls
are deliberately narrow. `CMD_RESET_PATH` persists `OUT_PATH_UNKNOWN`, so a
later ordinary telemetry request floods; and `CMD_SEND_PATH_DISCOVERY_REQ`
temporarily forces its own special telemetry request to flood, returning
path-discovery data rather than the normal tagged telemetry response. Receiver
does not use discovery as a second polling path. The source also has
`CMD_SEND_TRACE_PATH` for a caller-supplied raw trace path, but that is not a
tagged telemetry send and is not used here. Receiver never chooses a repeater
or serializes route hash `18` into a telemetry command.

There is no response-route attribution in this protocol path. The telemetry
push contains only reserved byte, six-byte source-key prefix, and telemetry
payload ([`MyMesh.cpp:670-679`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L670-L679)); it does not contain return path, flood/direct mode, RSSI, or SNR.
Every history record therefore keeps `responseRoute: null` and
`responseRouteUnknown: true`, including successful polls. A timeout says only
that the request was attempted using `routeAttempt`; it says nothing definite
about a return route or which RF hop failed.

Path discovery exists separately: the source forces a telemetry request to
flood and emits explicit in/out path data for
`PUSH_CODE_PATH_DISCOVERY_RESPONSE`
([`MyMesh.cpp:1505-1529`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L1505-L1529),
[`MyMesh.cpp:692-718`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L692-L718)). M7A.1 does not invoke it, preserving the polling policy. `PUSH_CODE_PATH_UPDATED` announces only the contact public key, not its path
([`MyMesh.cpp:377-382`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L377-L382)); the next pre-poll contact snapshot is how a changed cached path becomes visible.

For a field timeout, compare consecutive `routeAttempt` values. A transition
from `zero_hop`/`explicit_path` to `flood`, a changed hash sequence, or a
successful response after that transition is useful evidence. A stable route
snapshot followed by timeout is not evidence that routing was absent, that a
specific repeater was used, or that the response travelled the same path.

## M7A.3 repeatable stale-route recovery

Bike validation showed that a stored zero-hop or explicit route remains that
route across telemetry timeouts: it does not automatically become a flood or
repeater-routed request merely because RF is lost. A single timeout is not
enough evidence to discard a useful cached path, but retrying it indefinitely
is not acceptable for a moving field node.

M7A.3 scopes the guard to a recovery episode. It first requires **three
consecutive timeouts on the same actual `zero_hop` or `explicit_path`
attempt**. “Actual” means the contact snapshot and `RESP_CODE_SENT` both
reported direct (`routeAttempt.source = contact_out_path+response_sent`), not
merely that Receiver observed an old contact record. The first two failures
retain the cached route; the third opens one episode and makes exactly one
path-reset request:

```text
stale zero-hop or explicit-path telemetry timeout
  -> CMD_RESET_PATH for that contact
  -> Session ownership retains its motion cadence; manual ownership retains its backoff
  -> next normal telemetry poll re-queries the contact
  -> Companion may send flood and later learn a usable route
```

Three was chosen as the smallest bounded streak that absorbs an isolated loss
and one immediate retry while normally reaching discovery after only a few
polls (rather than the 70 observed in the field). It does not change the
telemetry timeout or polling rate. The command is the exact pinned Companion frame
`[CMD_RESET_PATH=13][public-key x32]`. The firmware looks up that full key,
sets only its stored `out_path_len` to `OUT_PATH_UNKNOWN`, persists the
contact, and responds `RESP_CODE_OK`; an unknown contact responds with
`RESP_CODE_ERR` ([`MyMesh.cpp:1179-1189`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L1179-L1189)). The route selection after the acknowledged reset remains firmware-owned:
`sendRequest` floods for `OUT_PATH_UNKNOWN` and otherwise uses `sendDirect`
([`BaseChatMesh.cpp:576-600`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/src/helpers/BaseChatMesh.cpp#L576-L600)). LoRaMapr does not select a repeater or construct a custom path.

The controller fingerprints only the observable route mode and route-hash
sequence; hashes are not repeater identities. A successful usable route clears
the streak, closes the episode, and updates its route generation. An
acknowledged reset is not repeated while Companion remains on that same route
or in flood. However, Companion can learn a new route before Receiver accepts
a telemetry response. The controller rearms a new bounded episode when it
observes a changed route-hash sequence, a flood-to-direct transition after the
reset, or a `PUSH_CODE_PATH_UPDATED` notice followed by direct traffic. Thus a
new route can receive its own three-timeout reset even if its compact hash is
the same as the route that was reset. If the reset command itself fails,
Companion state is unchanged, so Receiver retries it only after another full
three-timeout streak on the same confirmed direct route (at 3, 6, 9, ...
failures). Manual tracking retains its existing 30s/60s/... capped backoff. An explicitly active Session instead
continues normal motion-cadenced discovery after remote RF failures, but still
makes no extra reset requests.

`routeRecovery` in a history record is evidence for that particular poll. It
is `flood_attempted` only when that request actually used `flood`; it is never
inferred from a reset acknowledgement or a later zero-hop failure. Reset
events (`path_reset_acknowledged` or `path_reset_failed`) are likewise attached
to the timeout that caused them. Each poll additionally exposes
`staleRouteFailures`; status exposes that streak, its threshold, and
`pathResetAttempts` alongside the current episode, last recovery event, route generation, and route
fingerprint. `new_route_observed` identifies rearming before an accepted
telemetry response. Field testing can therefore distinguish a first/second
retained explicit-path failure, the threshold-triggered reset, a fallback
flood, a newly learned route, and a completed recovery.

`PUSH_CODE_PATH_UPDATED` carries only the contact public key
([`MyMesh.cpp:377-382`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L377-L382)). It marks the active target for a fresh normal pre-poll contact
snapshot; that next poll exposes `flood` or the newly learned `explicit_path`
in `recentPolls`. No identity is inferred from route hashes, and telemetry
response routing remains unknown.

## Next physical gate

1. Stationary desk test.
2. Walking test.
3. Faster movement test.
4. Bike test: capture slow-to-fast confirmation and the 30s-to-15s change.
   Verify every visible direct-range poll is `zero_hop` with `pathLength: 0`;
   after three same-route direct failures, verify exactly one reset event and then a fresh
   normal contact snapshot. Confirm a later actual flood is recorded as
   `flood_attempted`, and that flood failures do not reset again. If a path
   update or flood-to-direct transition supplies a new direct route before a
   response, verify `new_route_observed` then one reset after that route's
   third timeout. Return to direct range and confirm a zero-hop success, with
   Session failures continuing at the current motion cadence.
# Session-managed operation

The adaptive poller can be owned by an active LoRaMapr Session. The receiver
gets an authenticated, authoritative MeshCore tracking snapshot on its normal
cloud heartbeat response and reconciles it with the existing controller. An
empty snapshot stops Session-managed polling. This survives receiver restart
and cloud reconnect without a separate command channel or persisted local
Session state.

The snapshot carries the full MeshCore Ed25519 public key, Session/device IDs,
receiver installation binding, and version. It is accepted only by the bound
installation. BLE Release stops the poller; Resume waits for another cloud
heartbeat rather than blindly restoring a remembered target.

The local tracking start/stop API remains a diagnostic surface. An active
Session-managed intent takes precedence and manual start/stop returns a
conflict. Solicited telemetry continues through the normalized durable event
path; it is operational request-correlated data, not signed position evidence.

### Session tracking availability diagnostic

Two consecutive Session-managed failures classified as local adapter transport
loss raise the cloud-safe `meshcore_tracking_unavailable` diagnostic. The first
failure remains a normal bounded adapter-recovery retry; the second confirms
the condition and avoids warning on a single transient interruption. The
diagnostic means that telemetry requests cannot currently reach the attached
MeshCore device. It does not change Receiver lifecycle, heartbeat, Cloud
reachability, or Session state. A successful telemetry response clears it.
It remains visible across a later Session start in the same Receiver process,
so an already-known transport problem is visible before the next Session is
started; a Receiver restart clears this ephemeral observation.

Only the fixed code reaches Cloud. Local BlueZ, NUS, and wrapped transport
error text remain Receiver-local and are never included in the heartbeat or
browser response.

### Session recovery cadence (M7A.4)

An explicitly active Session is a coverage-discovery operation. A remote
telemetry timeout or unavailable contact route therefore does not replace the
motion-derived cadence with exponential delay:

| Last valid motion state | Session recovery cadence |
| --- | ---: |
| fast | 15 seconds |
| slow | 30 seconds |
| stationary | 45 seconds |
| unknown / no usable telemetry yet | 30 seconds |

`unknown` deliberately remains the existing conservative 30-second startup
cadence. The receiver does not infer a speed from missing telemetry. Once a
valid motion state exists, a timeout does not downgrade it: that last-known
state remains the scheduling input until a later valid telemetry response
changes it through the normal hysteresis rules.

This applies only to remote RF/path failures while `controlSource=session`.
The recovery episode permits only one `CMD_RESET_PATH` per observed route
generation; subsequent polls can be ordinary firmware-selected flood/discovery
attempts at the table's cadence, and a successful response closes the episode.
Manual/diagnostic
tracking keeps its exponential backoff. Local adapter-disconnected failures
also retain bounded adapter-recovery backoff regardless of ownership, distinct
from remote coverage discovery.

The fast cadence is intentionally not reduced below 15 seconds. Each attempt
is the existing correlated telemetry request plus its existing pre-poll contact
snapshot; M7A.4 adds no new radio frame type or retry burst. At bicycle speeds
of roughly 20–25 km/h, 15 seconds corresponds to about 83–104 metres travelled,
which is the chosen coverage-discovery tradeoff for an explicitly started
Session. M7A.5 refines the tagged-binary Session path so its cadence is
anchored to the prior request start, not timeout completion. If an attempt
consumes more than the target cadence, the next request starts as soon as the
single-flight arbiter permits it; requests never overlap. Legacy telemetry
retains its conservative completion-based scheduling. M7A.4 removes the
additional 30/60/120/240/300-second scheduler backoff; it does not alter the
single-flight telemetry timeout in this slice.

### Tagged binary transport and field diagnostics (M7A.5)

The receiver first uses stock Companion tagged binary telemetry:

```text
request:  [CMD_SEND_BINARY_REQUEST=0x32][full public key x32][app data 0x03]
response: [PUSH_BINARY_RESPONSE=0x8c][reserved][tag little-endian u32][LPP]
```

The receiver generates the request tag and accepts a tagged response only for
the matching in-flight request. On timeout it records a short-lived expired
tag tombstone; a late response bearing that tag is rejected and cannot satisfy
a newer request. A Companion `unsupported command` response falls back to the
existing legacy telemetry request. The fallback is capability-scoped, so a
supported tagged path is not silently changed by an unrelated timeout.

`GET /api/meshcore/tracking/status` is the operator surface. It exposes the
authoritative `controlSource` used by the scheduler, the transport and
capability, last request/response/expired tags, fallback reason, and
`lastFailureScheduleSource`. The last bounded history entry also reports its
request/response tags and schedule source. It does not require exposing a
full target key or raw telemetry payload.

For a Session RF-loss check, inspect only these fields:

```bash
curl -fsS http://127.0.0.1:8080/api/meshcore/tracking/status |
  jq '{controlSource,motionState,currentIntervalSeconds,lastRequestAt,nextRequestAt,
       consecutiveFailures,lastFailureScheduleSource,telemetryTransport,
       telemetryCapability,lastTelemetryRequestTag,lastTelemetryResponseTag,
       lastTelemetryExpiredTag,lastTelemetryFallbackReason,
       lastPoll:(.recentPolls[-1] | {requestAt,responseAt,outcome,intervalSeconds,
       scheduleSource,requestTag,responseTag,telemetryTransport})}'
```

For `controlSource=session`, remote RF failures must report
`scheduleSource=motion` and keep approximately 45/30/15-second request-start
spacing for stationary/slow/fast motion respectively. `manual_backoff` is
valid only for manual/diagnostic tracking. Adapter-disconnected failures remain
bounded local recovery. Structured receiver logs record capability detection,
tag acceptance/correlation/expiry, legacy fallback, late-expired rejection,
and both `schedule_source` and `control_source` on a tracking failure.
