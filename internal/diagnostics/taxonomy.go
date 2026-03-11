package diagnostics

import (
	"strings"
	"time"
)

type FailureCode string

const (
	FailureNone                FailureCode = ""
	FailurePairingCodeInvalid  FailureCode = "pairing_code_invalid"
	FailurePairingCodeExpired  FailureCode = "pairing_code_expired"
	FailureActivationFailed    FailureCode = "activation_failed"
	FailureCloudUnreachable    FailureCode = "cloud_unreachable"
	FailureReceiverAuthInvalid FailureCode = "receiver_auth_invalid"
	FailureNoSerialDevice      FailureCode = "no_serial_device_detected"
	FailureNodeNotConnected    FailureCode = "node_detected_not_connected"
	FailureEventsNotForwarding FailureCode = "events_not_forwarding"
)

type Finding struct {
	Code    FailureCode
	Summary string
	Hint    string
}

type Input struct {
	PairingPhase      string
	PairingLastChange string
	PairingLastError  string
	RuntimeLastError  string
	CloudReachable    bool
	MeshtasticState   string
	IngestQueueDepth  int
	LastPacketQueued  *time.Time
	LastPacketAck     *time.Time
	Now               time.Time
}

func Evaluate(input Input) Finding {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	lastChange := strings.ToLower(strings.TrimSpace(input.PairingLastChange))
	switch lastChange {
	case "pairing_code_invalid":
		return Finding{
			Code:    FailurePairingCodeInvalid,
			Summary: "Pairing code was rejected by cloud",
			Hint:    "Generate a new pairing code in LoRaMapr Cloud and submit it again.",
		}
	case "pairing_code_expired":
		return Finding{
			Code:    FailurePairingCodeExpired,
			Summary: "Pairing code expired before bootstrap exchange",
			Hint:    "Generate a fresh pairing code and retry pairing from the local portal.",
		}
	case "activation_failed":
		return Finding{
			Code:    FailureActivationFailed,
			Summary: "Receiver activation failed",
			Hint:    "Retry pairing and confirm cloud onboarding session is still active.",
		}
	}

	pairingErr := strings.ToLower(strings.TrimSpace(input.PairingLastError))
	if strings.Contains(pairingErr, "pairing") && strings.Contains(pairingErr, "expired") {
		return Finding{
			Code:    FailurePairingCodeExpired,
			Summary: "Pairing code expired before bootstrap exchange",
			Hint:    "Generate a fresh pairing code and retry pairing from the local portal.",
		}
	}
	if strings.Contains(pairingErr, "pairing") && (strings.Contains(pairingErr, "invalid") || strings.Contains(pairingErr, "not found")) {
		return Finding{
			Code:    FailurePairingCodeInvalid,
			Summary: "Pairing code was rejected by cloud",
			Hint:    "Generate a new pairing code in LoRaMapr Cloud and submit it again.",
		}
	}

	runtimeErr := strings.ToLower(strings.TrimSpace(input.RuntimeLastError))
	if strings.Contains(runtimeErr, "status=401") || strings.Contains(runtimeErr, "status=403") || strings.Contains(runtimeErr, "authentication rejected") {
		return Finding{
			Code:    FailureReceiverAuthInvalid,
			Summary: "Receiver credentials were rejected by cloud",
			Hint:    "Re-run pairing to refresh durable receiver credentials.",
		}
	}

	if input.PairingPhase == "steady_state" && !input.CloudReachable {
		return Finding{
			Code:    FailureCloudUnreachable,
			Summary: "Cloud endpoint is currently unreachable",
			Hint:    "Check internet connectivity, DNS, and outbound firewall rules.",
		}
	}

	meshState := strings.ToLower(strings.TrimSpace(input.MeshtasticState))
	switch meshState {
	case "not_present":
		return Finding{
			Code:    FailureNoSerialDevice,
			Summary: "No Meshtastic serial device detected",
			Hint:    "Check USB cable, power, and device permissions on this host.",
		}
	case "detected", "connecting":
		return Finding{
			Code:    FailureNodeNotConnected,
			Summary: "Meshtastic device detected but connection is not ready",
			Hint:    "Wait for connection to stabilize or verify serial path and protocol bridge.",
		}
	}

	if input.IngestQueueDepth > 0 {
		if queueBacklogStale(input.LastPacketQueued, input.LastPacketAck, now) {
			return Finding{
				Code:    FailureEventsNotForwarding,
				Summary: "Packets are queued but not forwarding to cloud",
				Hint:    "Check cloud reachability and receiver authentication status.",
			}
		}
	}

	return Finding{}
}

func queueBacklogStale(lastQueued, lastAck *time.Time, now time.Time) bool {
	if lastQueued == nil {
		return false
	}
	queuedAt := lastQueued.UTC()
	if queuedAt.IsZero() {
		return false
	}

	if lastAck == nil {
		return now.Sub(queuedAt) > 90*time.Second
	}
	ackAt := lastAck.UTC()
	if queuedAt.After(ackAt) {
		return now.Sub(ackAt) > 90*time.Second
	}
	return false
}
