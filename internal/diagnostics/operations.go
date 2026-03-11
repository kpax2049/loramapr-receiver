package diagnostics

import (
	"strings"
	"time"
)

type OperationalCheckLevel string

const (
	CheckOK      OperationalCheckLevel = "ok"
	CheckWarn    OperationalCheckLevel = "warn"
	CheckFail    OperationalCheckLevel = "fail"
	CheckUnknown OperationalCheckLevel = "unknown"
)

type OperationalCheck struct {
	ID      string                `json:"id"`
	Level   OperationalCheckLevel `json:"level"`
	Summary string                `json:"summary"`
	Hint    string                `json:"hint,omitempty"`
}

type OperationalSummary struct {
	Overall string             `json:"overall"`
	Summary string             `json:"summary"`
	Checks  []OperationalCheck `json:"checks"`
}

type OperationalInput struct {
	Now                time.Time
	Lifecycle          string
	Ready              bool
	ReadyReason         string
	PairingPhase        string
	HasIngestCredential bool
	CloudReachable      bool
	CloudProbeStatus    string
	MeshtasticState     string
	IngestQueueDepth    int
	LastPacketQueued    *time.Time
	LastPacketAck       *time.Time
	UpdateStatus        string
}

func EvaluateOperational(input OperationalInput) OperationalSummary {
	now := input.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}

	checks := []OperationalCheck{
		evaluateServiceHealth(input),
		evaluatePairingAuthorized(input),
		evaluateCloudReachability(input),
		evaluateNodeConnection(input),
		evaluateForwarding(input, now),
		evaluateUpdateSupportability(input),
	}

	overall := "ok"
	summary := "Receiver operational checks passed."
	hasWarn := false
	hasFail := false
	blocked := false

	for _, check := range checks {
		switch check.Level {
		case CheckFail:
			hasFail = true
			if check.ID == "pairing_authorized" || check.ID == "update_supportability" || check.ID == "service_health" {
				blocked = true
			}
		case CheckWarn, CheckUnknown:
			hasWarn = true
		}
	}

	switch {
	case hasFail && blocked:
		overall = "blocked"
		summary = "Receiver has blocking operational issues."
	case hasFail || hasWarn:
		overall = "degraded"
		summary = "Receiver has degraded operational checks."
	}

	return OperationalSummary{
		Overall: overall,
		Summary: summary,
		Checks:  checks,
	}
}

func evaluateServiceHealth(input OperationalInput) OperationalCheck {
	lifecycle := strings.ToLower(strings.TrimSpace(input.Lifecycle))
	switch lifecycle {
	case "running":
		if input.Ready {
			return OperationalCheck{
				ID:      "service_health",
				Level:   CheckOK,
				Summary: "Receiver service is running and ready.",
			}
		}
		reason := strings.TrimSpace(input.ReadyReason)
		if reason == "" {
			reason = "runtime reported not ready"
		}
		return OperationalCheck{
			ID:      "service_health",
			Level:   CheckWarn,
			Summary: "Receiver service is running but not ready.",
			Hint:    reason,
		}
	case "starting", "stopping":
		return OperationalCheck{
			ID:      "service_health",
			Level:   CheckWarn,
			Summary: "Receiver service is transitioning state.",
			Hint:    "Wait for runtime state to stabilize.",
		}
	case "failed", "stopped":
		return OperationalCheck{
			ID:      "service_health",
			Level:   CheckFail,
			Summary: "Receiver service is not running normally.",
			Hint:    "Restart service and inspect local logs or doctor output.",
		}
	default:
		return OperationalCheck{
			ID:      "service_health",
			Level:   CheckUnknown,
			Summary: "Receiver service state is not available.",
			Hint:    "Check local portal status API or system service status.",
		}
	}
}

func evaluatePairingAuthorized(input OperationalInput) OperationalCheck {
	phase := strings.TrimSpace(input.PairingPhase)
	if phase == "steady_state" {
		if input.HasIngestCredential {
			return OperationalCheck{
				ID:      "pairing_authorized",
				Level:   CheckOK,
				Summary: "Receiver is paired and authorized for steady-state forwarding.",
			}
		}
		return OperationalCheck{
			ID:      "pairing_authorized",
			Level:   CheckFail,
			Summary: "Receiver is marked paired but durable credentials are missing.",
			Hint:    "Reset and re-pair receiver to restore cloud authorization.",
		}
	}

	switch phase {
	case "activated", "bootstrap_exchanged", "pairing_code_entered":
		return OperationalCheck{
			ID:      "pairing_authorized",
			Level:   CheckWarn,
			Summary: "Receiver pairing is still in progress.",
			Hint:    "Continue setup in local portal until steady state is reached.",
		}
	default:
		return OperationalCheck{
			ID:      "pairing_authorized",
			Level:   CheckFail,
			Summary: "Receiver is not paired yet.",
			Hint:    "Enter a pairing code in local portal to authorize this receiver.",
		}
	}
}

func evaluateCloudReachability(input OperationalInput) OperationalCheck {
	if input.CloudReachable || strings.EqualFold(strings.TrimSpace(input.CloudProbeStatus), "reachable") {
		return OperationalCheck{
			ID:      "cloud_reachability",
			Level:   CheckOK,
			Summary: "Cloud endpoint is reachable.",
		}
	}

	phase := strings.TrimSpace(input.PairingPhase)
	if phase != "steady_state" {
		return OperationalCheck{
			ID:      "cloud_reachability",
			Level:   CheckWarn,
			Summary: "Cloud endpoint is not currently reachable.",
			Hint:    "Receiver may still be in setup; verify internet connectivity before pairing/activation.",
		}
	}
	return OperationalCheck{
		ID:      "cloud_reachability",
		Level:   CheckFail,
		Summary: "Cloud endpoint is unreachable in steady state.",
		Hint:    "Check internet/DNS/firewall and cloud base URL configuration.",
	}
}

func evaluateNodeConnection(input OperationalInput) OperationalCheck {
	state := strings.ToLower(strings.TrimSpace(input.MeshtasticState))
	switch state {
	case "connected":
		return OperationalCheck{
			ID:      "node_connection",
			Level:   CheckOK,
			Summary: "Meshtastic node connection is active.",
		}
	case "detected", "connecting":
		return OperationalCheck{
			ID:      "node_connection",
			Level:   CheckWarn,
			Summary: "Meshtastic device is detected but not fully connected.",
			Hint:    "Verify serial path, device readiness, and allow connection to stabilize.",
		}
	case "not_present":
		return OperationalCheck{
			ID:      "node_connection",
			Level:   CheckFail,
			Summary: "No Meshtastic device is connected.",
			Hint:    "Check USB cable, power, and serial permissions.",
		}
	case "degraded":
		return OperationalCheck{
			ID:      "node_connection",
			Level:   CheckFail,
			Summary: "Meshtastic adapter is degraded.",
			Hint:    "Inspect adapter error and validate transport configuration.",
		}
	default:
		return OperationalCheck{
			ID:      "node_connection",
			Level:   CheckUnknown,
			Summary: "Meshtastic connection state is unknown.",
		}
	}
}

func evaluateForwarding(input OperationalInput, now time.Time) OperationalCheck {
	if strings.TrimSpace(input.PairingPhase) != "steady_state" {
		return OperationalCheck{
			ID:      "forwarding_activity",
			Level:   CheckUnknown,
			Summary: "Forwarding activity cannot be evaluated before pairing completes.",
		}
	}

	if input.LastPacketAck != nil && now.Sub(input.LastPacketAck.UTC()) <= 5*time.Minute {
		return OperationalCheck{
			ID:      "forwarding_activity",
			Level:   CheckOK,
			Summary: "Packet forwarding has recent acknowledgements.",
		}
	}

	if input.IngestQueueDepth > 0 {
		if queueBacklogStale(input.LastPacketQueued, input.LastPacketAck, now) {
			return OperationalCheck{
				ID:      "forwarding_activity",
				Level:   CheckFail,
				Summary: "Packets are queued but not being acknowledged.",
				Hint:    "Check cloud connectivity and receiver credential validity.",
			}
		}
		return OperationalCheck{
			ID:      "forwarding_activity",
			Level:   CheckWarn,
			Summary: "Packets are queued and waiting for delivery.",
			Hint:    "Monitor queue depth and cloud reachability for recovery.",
		}
	}

	return OperationalCheck{
		ID:      "forwarding_activity",
		Level:   CheckWarn,
		Summary: "No recent packet acknowledgements observed.",
		Hint:    "Confirm node traffic is present and forwarding pipeline is active.",
	}
}

func evaluateUpdateSupportability(input OperationalInput) OperationalCheck {
	switch strings.ToLower(strings.TrimSpace(input.UpdateStatus)) {
	case "current", "ahead":
		return OperationalCheck{
			ID:      "update_supportability",
			Level:   CheckOK,
			Summary: "Receiver build is supportable for current channel policy.",
		}
	case "unsupported":
		return OperationalCheck{
			ID:      "update_supportability",
			Level:   CheckFail,
			Summary: "Receiver build is unsupported by update policy.",
			Hint:    "Upgrade to a supported release before continued operation.",
		}
	case "outdated", "channel_mismatch":
		return OperationalCheck{
			ID:      "update_supportability",
			Level:   CheckWarn,
			Summary: "Receiver build should be upgraded or channel alignment reviewed.",
			Hint:    "Use release artifacts for the recommended version/channel.",
		}
	case "disabled":
		return OperationalCheck{
			ID:      "update_supportability",
			Level:   CheckUnknown,
			Summary: "Update checks are disabled.",
			Hint:    "Enable update checks to evaluate supportability status.",
		}
	default:
		return OperationalCheck{
			ID:      "update_supportability",
			Level:   CheckUnknown,
			Summary: "Update supportability state is unknown.",
		}
	}
}
