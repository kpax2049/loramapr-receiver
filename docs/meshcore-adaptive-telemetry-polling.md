# MeshCore Adaptive Telemetry Polling (M7A)

M7A adds an in-memory, receiver-controlled polling controller for one selected
canonical MeshCore field-node public key. It uses the existing MeshCore
telemetry request, response-correlation, normalization, and durable outbox
path; it does not implement a second telemetry protocol path. Stock MeshCore
Companion firmware is sufficient.

## Lifecycle boundary

The product lifecycle is Session-bound: starting an active LoRaMapr tracking
Session must start polling, and ending that Session must stop it. No active
Session will mean no automatic telemetry requests.

M7A has no cloud Session integration yet. The local receiver endpoints below
are a temporary physical-test harness only, not a permanent independent
always-on subsystem. Controller state is deliberately ephemeral and is not
restored after a receiver restart. Its `Start`/`Stop` seam is the intended
future Session lifecycle binding point.

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

There is one request path and one adapter-level request in flight. The
controller never makes tight retries. The first failure waits at least the
unknown interval; repeated failures double the interval and cap at five
minutes. A successful correlated response resets the failure count. Adapter or
BLE disconnect is treated as a failed poll and therefore suspends pressure via
that backoff. A later successful request after deliberate adapter reconnect
logs recovery and resets normal scheduling.

Stopping cancels scheduled future polls but does not cancel an already-issued
request, so an already-arriving correlated response can still follow its
normal durable-event path. The controller logs lifecycle changes, polls,
classification/interval changes, failures/backoff, and adapter unavailable or
recovered states without logging raw payloads.

## M7A.1 route observability

The controller records the last 30 attempts in the existing local-only
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

## Next physical gate

1. Stationary desk test.
2. Walking test.
3. Faster movement test.
4. Bike test: capture slow-to-fast confirmation and the 30s-to-15s change,
   then compare request route snapshots before/after direct-range loss and
   record whether a repeater-routed attempt succeeds or times out.
