# LoRaMapr Receiver v2.16.7 (Serial Stability + Portal Auto-Refresh)

Date: 2026-03-20

This patch hardens first-run Pi behavior where Meshtastic USB attach could
destabilize runtime responsiveness.

## Highlights

- Native serial handling is more conservative under noisy/non-frame streams:
  - no-frame detection now holds the connection open instead of immediately
    tearing down and thrashing reconnects
  - repeated decode failures now enter a short recovery hold state instead of
    forcing rapid reopen loops
- Native bootstrap write is now throttled per device:
  - avoids repeated bootstrap writes on reconnect churn
  - keeps passive-read behavior as the primary steady path
- Local portal pages now auto-refresh every 5 seconds:
  - `Welcome`, `Progress`, `Troubleshooting`, and `Advanced` reflect state
    transitions without manual browser refresh

## Operator Impact

- Reduces risk of serial reopen thrash when a node emits startup noise before
  native frames are available.
- Improves first-run troubleshooting feedback loop by keeping status views fresh.

## Scope

- This patch does not change cloud ingest contract or pairing model.
- Meshtastic support remains Linux/Pi direct USB first.
