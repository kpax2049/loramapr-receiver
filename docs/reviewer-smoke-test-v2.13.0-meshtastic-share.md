# Reviewer Smoke Test Guide (v2.13.0 Meshtastic Pairing Data)

## 1. Build and tests

```bash
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp go test ./...
GOCACHE=/tmp/go-build-cache GOTMPDIR=/tmp make build
```

## 2. Config summary available path

1. Start receiver with Meshtastic adapter connected.
2. Feed/observe status event including config fields (`region`, primary channel,
   PSK state).
3. Open portal `/progress` and confirm field-node pairing section shows summary.
4. Confirm `doctor -json` includes `meshtastic_config.available=true`.

## 3. Share-based path

1. Feed/observe status event including `channel_url` (or equivalent share URL
   field).
2. Confirm portal `/progress` shows share URL availability and value.
3. Confirm `doctor -json` and `status` include share availability + redacted
   share hint.
4. Confirm support snapshot excludes raw share URL and QR text.

## 4. Manual fallback path

1. Run with connected node but without config/share fields in status events.
2. Confirm portal shows explicit unavailable reason and manual fallback guidance.
3. Confirm troubleshooting hints include manual Meshtastic-app fallback.

## 5. Cloud/support safety checks

1. Verify runtime heartbeat payload contains only support-safe Meshtastic config
   hints (no raw share URL).
2. Verify support snapshot redaction list includes Meshtastic share fields.
3. Verify local advanced page shows redacted share hint for support contexts.
