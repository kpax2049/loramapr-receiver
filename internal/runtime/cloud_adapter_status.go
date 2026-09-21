package runtime

import (
	"strings"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

// cloudAdapterStatuses is the sole local-to-cloud adapter-status boundary.
// It drops local-only paths and arbitrary error text in favour of a bounded
// classification derived from the already authoritative local lifecycle.
func cloudAdapterStatuses(adapters []status.AdapterStatus) []cloudclient.ReceiverAdapterStatus {
	out := make([]cloudclient.ReceiverAdapterStatus, 0, len(adapters))
	for _, adapter := range adapters {
		protocol := strings.TrimSpace(adapter.Protocol)
		if protocol == "" || !allowedAdapterProtocol(protocol) {
			continue
		}
		lifecycle := strings.TrimSpace(adapter.Lifecycle)
		if !allowedAdapterLifecycle(lifecycle) {
			lifecycle = "unknown"
		}
		profileState := cloudProfileState(adapter.ProfileState)
		item := cloudclient.ReceiverAdapterStatus{
			Protocol: protocol, Enabled: adapter.Enabled, Configured: adapter.Configured,
			Lifecycle: lifecycle, ConnectionState: cloudConnectionState(adapter.ConnectionState),
			Connected: adapter.ConnectionState == "connected", Ready: adapter.Ready,
			Transport: cloudTransport(adapter.Transport), ProtocolVersion: boundedProtocolVersion(adapter.ProtocolVersion),
			ProfileState: profileState, ErrorCode: cloudAdapterErrorCode(lifecycle),
			IntentionalRelease: adapter.ReleasedByUser,
			ConfiguredDevice:   cloudBLEDeviceAddress(adapter.ConfiguredDevice),
			ConnectedDevice:    cloudBLEDeviceAddress(adapter.ConnectedDevice),
		}
		if adapter.Delivery != nil {
			item.Delivery = &cloudclient.ReceiverAdapterDeliveryStatus{
				State: cloudDeliveryState(adapter.Delivery.State), PendingCount: max(adapter.Delivery.PendingCount, 0),
				QuarantinedCount: max(adapter.Delivery.QuarantinedCount, 0), FailureCode: cloudDeliveryFailureCode(adapter.Delivery.FailureCode),
			}
		}
		out = append(out, item)
	}
	return out
}

func cloudBLEDeviceAddress(value string) string {
	value = strings.ToUpper(strings.TrimSpace(value))
	if len(value) != 17 {
		return ""
	}
	for index, ch := range value {
		if index%3 == 2 {
			if ch != ':' {
				return ""
			}
		} else if !(ch >= '0' && ch <= '9' || ch >= 'A' && ch <= 'F') {
			return ""
		}
	}
	return value
}

func allowedAdapterProtocol(value string) bool { return value == "meshcore" || value == "meshtastic" }
func allowedAdapterLifecycle(value string) bool {
	switch value {
	case "disabled", "not_present", "detected", "opening", "handshaking", "connecting", "connected", "released", "incompatible", "configuration_error", "degraded":
		return true
	}
	return false
}
func cloudTransport(value string) string {
	if value == "physical_serial" || value == "ble" || value == "serial" || value == "bridge" || value == "json_stream" || value == "disabled" {
		return value
	}
	return ""
}
func cloudConnectionState(value string) string {
	switch value {
	case "connecting", "reconnecting", "connected", "disconnected", "unknown":
		return value
	}
	return "unknown"
}
func boundedProtocolVersion(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 40 {
		return ""
	}
	return value
}
func cloudProfileState(value string) string {
	switch value {
	case "not_established", "negotiating", "matched", "raw_capture_only", "failed":
		return value
	}
	return ""
}
func cloudAdapterErrorCode(lifecycle string) string {
	switch lifecycle {
	case "incompatible":
		return "incompatible"
	case "configuration_error":
		return "configuration_error"
	case "degraded":
		return "degraded"
	}
	return ""
}
func cloudDeliveryState(value string) string {
	switch value {
	case "ready", "pending", "recovered", "degraded", "paused", "unavailable":
		return value
	}
	return "unknown"
}
func cloudDeliveryFailureCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 64 {
		return ""
	}
	for _, ch := range value {
		if !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_') {
			return ""
		}
	}
	return value
}
