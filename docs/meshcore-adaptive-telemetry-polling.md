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

Bike validation showed that a stored zero-hop route remains a zero-hop route
across telemetry timeouts: it does not automatically become a flood or
repeater-routed request merely because direct RF is lost. M7A.2 guarded that
reset with a one-per-tracking-run latch. A transient timeout could therefore
consume the latch, let the contact recover, and leave a later genuinely stale
route unrecoverable until the whole local tracking run was restarted.

M7A.3 instead scopes the guard to a recovery episode. A timeout on an actual
`zero_hop` or `explicit_path` attempt opens one episode and makes exactly one
path-reset request:

```text
stale zero-hop or explicit-path telemetry timeout
  -> CMD_RESET_PATH for that contact
  -> Session ownership retains its motion cadence; manual ownership retains its backoff
  -> next normal telemetry poll re-queries the contact
  -> Companion may send flood and later learn a usable route
```

The command is the exact pinned Companion frame
`[CMD_RESET_PATH=13][public-key x32]`. The firmware looks up that full key,
sets only its stored `out_path_len` to `OUT_PATH_UNKNOWN`, persists the
contact, and responds `RESP_CODE_OK`; an unknown contact responds with
`RESP_CODE_ERR` ([`MyMesh.cpp:1179-1189`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/examples/companion_radio/MyMesh.cpp#L1179-L1189)). The route selection after the acknowledged reset remains firmware-owned:
`sendRequest` floods for `OUT_PATH_UNKNOWN` and otherwise uses `sendDirect`
([`BaseChatMesh.cpp:576-600`](https://github.com/meshcore-dev/MeshCore/blob/d92964352441e53b93e8667b802e04f6e072b39e/src/helpers/BaseChatMesh.cpp#L576-L600)). LoRaMapr does not select a repeater or construct a custom path.

The controller fingerprints only the observable route mode and route-hash
sequence; hashes are not repeater identities. A successful usable route closes
the episode and updates its route generation. That makes a later stale
generation eligible for one new reset. Until such a success, the episode stays
active: continuous zero-hop failures, a failing flood, a firmware reset error,
or a disconnect cannot generate reset loops. Manual tracking retains its
existing 30s/60s/... capped backoff. An explicitly active Session instead
continues normal motion-cadenced discovery after remote RF failures, but still
makes no extra reset requests.

`routeRecovery` in a history record is evidence for that particular poll. It
is `flood_attempted` only when that request actually used `flood`; it is never
inferred from a reset acknowledgement or a later zero-hop failure. Reset
events (`path_reset_acknowledged` or `path_reset_failed`) are likewise attached
to the timeout that caused them. The status also exposes the current episode,
last recovery event, route generation, and route fingerprint so field testing
can distinguish a completed recovery from a still-failing route.

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
   after loss of direct range, verify exactly one reset event and then a fresh
   normal contact snapshot. Confirm a later actual flood is recorded as
   `flood_attempted`, a flood failure does not reset again, and a successful
   flood or learned route completes the episode. Return to direct range and
   confirm a zero-hop success; then make the route stale again and verify one
   new reset for that later episode, with Session failures continuing at the
   current motion cadence.
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
The recovery episode still permits only one `CMD_RESET_PATH`; subsequent polls
can be ordinary firmware-selected flood/discovery attempts at the table's
cadence, and a successful response closes the episode. Manual/diagnostic
tracking keeps its exponential backoff. Local adapter-disconnected failures
also retain bounded adapter-recovery backoff regardless of ownership, distinct
from remote coverage discovery.

The fast cadence is intentionally not reduced below 15 seconds. Each attempt
is the existing correlated telemetry request plus its existing pre-poll contact
snapshot; M7A.4 adds no new radio frame type or retry burst. At bicycle speeds
of roughly 20–25 km/h, 15 seconds corresponds to about 83–104 metres travelled,
which is the chosen coverage-discovery tradeoff for an explicitly started
Session. The cadence begins after the single in-flight request completes. The
Companion can supply a shorter estimated response timeout, but the unchanged
fallback watchdog is 45 seconds; a completely silent link can therefore make
wall-clock request starts longer than the recorded cadence. M7A.4 removes the
additional 30/60/120/240/300-second scheduler backoff; it does not alter that
single-flight telemetry timeout in this slice.
