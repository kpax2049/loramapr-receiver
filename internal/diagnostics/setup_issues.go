package diagnostics

import (
	"net/url"
	"strings"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

type SetupIssue struct {
	Code      string                `json:"code"`
	Component string                `json:"component"`
	Level     OperationalCheckLevel `json:"level"`
	Summary   string                `json:"summary"`
	Guidance  string                `json:"guidance,omitempty"`
	Detail    string                `json:"detail,omitempty"`
}

func DeriveSetupIssues(snap status.Snapshot, ops OperationalSummary) []SetupIssue {
	issues := make([]SetupIssue, 0, 8)
	seen := map[string]struct{}{}
	add := func(issue SetupIssue) {
		issue.Code = strings.TrimSpace(issue.Code)
		if issue.Code == "" {
			return
		}
		if _, exists := seen[issue.Code]; exists {
			return
		}
		issue.Component = strings.TrimSpace(issue.Component)
		issue.Summary = strings.TrimSpace(issue.Summary)
		issue.Guidance = strings.TrimSpace(issue.Guidance)
		issue.Detail = strings.TrimSpace(issue.Detail)
		if issue.Level == "" {
			issue.Level = CheckWarn
		}
		seen[issue.Code] = struct{}{}
		issues = append(issues, issue)
	}

	profile := strings.ToLower(strings.TrimSpace(snap.RuntimeProfile))
	installType := strings.ToLower(strings.TrimSpace(snap.InstallType))
	portalComponent, portalOK := snapshotComponent(snap, "portal")
	if expectsLANPortal(profile, installType) {
		if portalOK && strings.EqualFold(strings.TrimSpace(portalComponent.State), "running") && portalBoundToLoopback(portalComponent.Message) {
			add(SetupIssue{
				Code:      "portal_bind_localhost",
				Component: "portal",
				Level:     CheckFail,
				Summary:   "Portal is only reachable on localhost.",
				Guidance:  "Set portal.bind_address to 0.0.0.0:8080 for Pi/Linux installs, then restart receiver service.",
				Detail:    strings.TrimSpace(portalComponent.Message),
			})
		}
	}
	if portalOK && strings.EqualFold(strings.TrimSpace(portalComponent.State), "error") {
		add(SetupIssue{
			Code:      "portal_unavailable",
			Component: "portal",
			Level:     CheckFail,
			Summary:   "Local setup portal failed to start.",
			Guidance:  "Verify portal bind address/port availability and restart receiver service.",
			Detail:    strings.TrimSpace(portalComponent.Message),
		})
	}

	cloudEndpoint := strings.TrimSpace(snap.CloudEndpoint)
	switch {
	case cloudEndpoint == "":
		add(SetupIssue{
			Code:      "cloud_base_url_missing",
			Component: "cloud",
			Level:     CheckFail,
			Summary:   "Cloud endpoint is not configured.",
			Guidance:  "Set cloud base URL with bootstrap --cloud-base-url or loramapr-receiverd configure-cloud, then retry pairing.",
		})
	case cloudEndpointPlaceholder(cloudEndpoint):
		add(SetupIssue{
			Code:      "cloud_base_url_placeholder",
			Component: "cloud",
			Level:     CheckFail,
			Summary:   "Cloud endpoint is still a placeholder URL.",
			Guidance:  "Configure the real cloud base URL and restart receiver before pairing.",
			Detail:    cloudEndpoint,
		})
	case !cloudEndpointValid(cloudEndpoint):
		add(SetupIssue{
			Code:      "cloud_base_url_invalid",
			Component: "cloud",
			Level:     CheckFail,
			Summary:   "Cloud endpoint format is invalid.",
			Guidance:  "Use a full URL such as https://loramapr.com or http://<host>:3001.",
			Detail:    cloudEndpoint,
		})
	}
	if strings.EqualFold(strings.TrimSpace(snap.CloudStatus), "unreachable") || (!snap.CloudReachable && strings.TrimSpace(snap.PairingPhase) != "") {
		add(SetupIssue{
			Code:      "cloud_unreachable",
			Component: "cloud",
			Level:     CheckFail,
			Summary:   "Cloud endpoint is not reachable from this receiver.",
			Guidance:  "Check DNS/network access and verify cloud base URL is reachable from Pi/Linux host.",
			Detail:    cloudEndpoint,
		})
	}

	meshComponent, meshOK := snapshotComponent(snap, "meshtastic")
	meshState := strings.ToLower(strings.TrimSpace(meshComponent.State))
	meshDetail := strings.TrimSpace(meshComponent.Message)
	switch meshState {
	case "not_present":
		add(SetupIssue{
			Code:      "usb_device_not_detected",
			Component: "meshtastic",
			Level:     CheckFail,
			Summary:   "No Meshtastic USB serial device was detected.",
			Guidance:  "Check USB cable/power and confirm receiver service user has serial-device access.",
			Detail:    meshDetail,
		})
	case "detected", "connecting":
		add(SetupIssue{
			Code:      "usb_detected_node_not_ready",
			Component: "meshtastic",
			Level:     CheckWarn,
			Summary:   "Meshtastic USB device is detected but node is not connected yet.",
			Guidance:  "Wait for native serial handshake or verify configured device path and node power.",
			Detail:    meshDetail,
		})
	case "degraded":
		lower := strings.ToLower(meshDetail + " " + strings.TrimSpace(snap.LastError))
		switch {
		case strings.Contains(lower, "permission denied") || strings.Contains(lower, "dialout"):
			add(SetupIssue{
				Code:      "usb_serial_permission_denied",
				Component: "meshtastic",
				Level:     CheckFail,
				Summary:   "Meshtastic USB device is present but serial access is denied.",
				Guidance:  "Ensure loramapr service user is in dialout and restart the receiver service.",
				Detail:    meshDetail,
			})
		case strings.Contains(lower, "native serial stream unreadable") ||
			strings.Contains(lower, "no meshtastic native serial frames detected") ||
			strings.Contains(lower, "native serial decode failed") ||
			strings.Contains(lower, "decode json"):
			add(SetupIssue{
				Code:      "usb_protocol_unusable",
				Component: "meshtastic",
				Level:     CheckFail,
				Summary:   "Meshtastic USB device is present but protocol stream is unusable.",
				Guidance:  "Use direct native Meshtastic USB serial stream (no console/JSON bridge output on this device path).",
				Detail:    meshDetail,
			})
		default:
			add(SetupIssue{
				Code:      "usb_device_unusable",
				Component: "meshtastic",
				Level:     CheckFail,
				Summary:   "Meshtastic adapter is degraded and device is not usable yet.",
				Guidance:  "Inspect adapter detail and verify device path, permissions, and node readiness.",
				Detail:    meshDetail,
			})
		}
	default:
		if !meshOK && strings.TrimSpace(snap.FailureCode) == string(FailureNoSerialDevice) {
			add(SetupIssue{
				Code:      "usb_device_not_detected",
				Component: "meshtastic",
				Level:     CheckFail,
				Summary:   "No Meshtastic USB serial device was detected.",
				Guidance:  "Check USB cable/power and confirm receiver service user has serial-device access.",
			})
		}
	}

	if strings.EqualFold(strings.TrimSpace(snap.PairingPhase), "steady_state") {
		if forwardingCheck, ok := findOperationalCheck(ops, "forwarding_activity"); ok &&
			(forwardingCheck.Level == CheckWarn || forwardingCheck.Level == CheckFail) {
			add(SetupIssue{
				Code:      "packets_not_ingesting",
				Component: "ingest",
				Level:     forwardingCheck.Level,
				Summary:   "Packets are not ingesting successfully yet.",
				Guidance:  strings.TrimSpace(forwardingCheck.Hint),
				Detail:    strings.TrimSpace(forwardingCheck.Summary),
			})
		}
	}

	return issues
}

func snapshotComponent(snap status.Snapshot, name string) (status.ComponentStatus, bool) {
	if snap.Components == nil {
		return status.ComponentStatus{}, false
	}
	component, ok := snap.Components[name]
	return component, ok
}

func findOperationalCheck(ops OperationalSummary, id string) (OperationalCheck, bool) {
	target := strings.TrimSpace(id)
	for _, check := range ops.Checks {
		if strings.EqualFold(strings.TrimSpace(check.ID), target) {
			return check, true
		}
	}
	return OperationalCheck{}, false
}

func expectsLANPortal(profile, installType string) bool {
	if strings.EqualFold(strings.TrimSpace(profile), "linux-service") || strings.EqualFold(strings.TrimSpace(profile), "appliance-pi") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(installType), "linux-package") || strings.EqualFold(strings.TrimSpace(installType), "pi-appliance")
}

func portalBoundToLoopback(message string) bool {
	text := strings.ToLower(strings.TrimSpace(message))
	if text == "" {
		return false
	}
	return strings.Contains(text, "127.0.0.1:") || strings.Contains(text, "localhost:") || strings.Contains(text, "[::1]:")
}

func cloudEndpointPlaceholder(endpoint string) bool {
	value := strings.ToLower(strings.TrimSpace(endpoint))
	if value == "" {
		return false
	}
	return strings.Contains(value, "api.loramapr.example") || strings.HasSuffix(value, ".example") || strings.Contains(value, ".example/")
}

func cloudEndpointValid(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return false
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return true
}
