# Reviewer Smoke Test: Simplified Pairing and Operation

Use this guide to validate the receiver-side simplification that removes
required owner/workspace/site assumptions.

## 1. Build and test

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make test
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make build
```

## 2. Pairing-first UX check

1. Start unpaired receiver on either supported path.
2. Open portal routes:
   - `/`
   - `/pairing`
   - `/progress`
   - `/troubleshooting`
3. Confirm guidance is pairing-first and does not require workspace/site/group
   setup.

## 3. Optional metadata behavior

1. Validate operation with no cloud site/group labels returned.
2. Validate operation with cloud site/group labels present.
3. Confirm both cases are treated as valid and non-blocking.

## 4. Diagnostics and CLI check

Run:

```bash
./bin/loramapr-receiverd doctor -config ./receiver.example.json
./bin/loramapr-receiverd doctor -config ./receiver.example.json -json
./bin/loramapr-receiverd support-snapshot -config ./receiver.example.json -out /tmp/receiver-support.json
```

Confirm:

- identity includes receiver/local hints
- optional cloud labels appear only when present
- no output implies owner/workspace/site are prerequisites

## 5. Pairing and steady-state regression check

1. Submit pairing code via portal.
2. Confirm phase transitions to `steady_state` as normal.
3. Confirm node connection and packet forwarding checks still behave normally.
