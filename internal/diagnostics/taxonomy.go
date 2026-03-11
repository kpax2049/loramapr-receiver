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
	FailurePairingNotCompleted FailureCode = "pairing_not_completed"
	FailureReceiverRevoked     FailureCode = "receiver_credential_revoked"
	FailureReceiverDisabled    FailureCode = "receiver_disabled"
	FailureReceiverReplaced    FailureCode = "receiver_replaced"
	FailureCloudUnreachable    FailureCode = "cloud_unreachable"
	FailureNetworkUnavailable  FailureCode = "network_unavailable"
	FailurePortalUnavailable   FailureCode = "portal_unavailable"
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
	RuntimeProfile        string
	PairingPhase          string
	PairingLastChange     string
	PairingLastError      string
	RuntimeLastError      string
	PortalState           string
	NetworkAvailable      bool
	NetworkAvailableKnown bool
	CloudReachable        bool
	MeshtasticState       string
	IngestQueueDepth      int
	LastPacketQueued      *time.Time
	LastPacketAck         *time.Time
	Now                   time.Time
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
	case "credential_revoked":
		return Finding{
			Code:    FailureReceiverRevoked,
			Summary: "Receiver credential was revoked by cloud",
			Hint:    "Reset local receiver credentials and re-pair from LoRaMapr Cloud.",
		}
	case "receiver_disabled":
		return Finding{
			Code:    FailureReceiverDisabled,
			Summary: "Receiver is disabled in cloud",
			Hint:    "Resolve cloud-side receiver policy, then reset and re-pair this installation.",
		}
	case "receiver_replaced":
		return Finding{
			Code:    FailureReceiverReplaced,
			Summary: "Receiver was replaced by another installation",
			Hint:    "Re-pair this machine only if it should become the active receiver again.",
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
	if strings.Contains(runtimeErr, "credential revoked") {
		return Finding{
			Code:    FailureReceiverRevoked,
			Summary: "Receiver credential was revoked by cloud",
			Hint:    "Reset local receiver credentials and re-pair from LoRaMapr Cloud.",
		}
	}
	if strings.Contains(runtimeErr, "receiver disabled") {
		return Finding{
			Code:    FailureReceiverDisabled,
			Summary: "Receiver is disabled in cloud",
			Hint:    "Resolve cloud-side receiver policy, then reset and re-pair this installation.",
		}
	}
	if strings.Contains(runtimeErr, "receiver replaced") {
		return Finding{
			Code:    FailureReceiverReplaced,
			Summary: "Receiver was replaced by another installation",
			Hint:    "Re-pair this machine only if it should become the active receiver again.",
		}
	}
	if strings.Contains(runtimeErr, "status=401") || strings.Contains(runtimeErr, "status=403") || strings.Contains(runtimeErr, "authentication rejected") {
		return Finding{
			Code:    FailureReceiverAuthInvalid,
			Summary: "Receiver credentials were rejected by cloud",
			Hint:    "Re-run pairing to refresh durable receiver credentials.",
		}
	}
	if strings.TrimSpace(input.PortalState) == "error" || strings.Contains(runtimeErr, "local portal failed") || strings.Contains(runtimeErr, "listen tcp") {
		return Finding{
			Code:    FailurePortalUnavailable,
			Summary: "Local setup portal is not available",
			Hint:    "Confirm the portal bind address is valid and no other service is using port 8080.",
		}
	}
	if input.NetworkAvailableKnown && !input.NetworkAvailable {
		return Finding{
			Code:    FailureNetworkUnavailable,
			Summary: "Local network is unavailable",
			Hint:    "Check Ethernet/Wi-Fi connectivity and DHCP assignment, then retry portal access.",
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

	if strings.EqualFold(strings.TrimSpace(input.RuntimeProfile), "appliance-pi") {
		switch strings.TrimSpace(input.PairingPhase) {
		case "", "unpaired", "pairing_code_entered", "bootstrap_exchanged":
			return Finding{
				Code:    FailurePairingNotCompleted,
				Summary: "Receiver pairing is not completed yet",
				Hint:    "Open the local portal and enter a valid pairing code from LoRaMapr Cloud.",
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
