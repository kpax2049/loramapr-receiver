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

## Next physical gate

1. Stationary desk test.
2. Walking test.
3. Faster movement test.
4. Later repeater-path test.
