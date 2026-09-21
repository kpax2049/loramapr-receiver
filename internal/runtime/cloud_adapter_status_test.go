package runtime

import (
	"testing"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

func TestCloudAdapterStatusesProjectOnlySafeFacts(t *testing.T) {
	adapters := cloudAdapterStatuses([]status.AdapterStatus{
		{Name: "meshcore-companion", Protocol: "meshcore", Enabled: true, Configured: true, Lifecycle: "connected", ConnectionState: "connected", Ready: true, Transport: "ble", ConfiguredDevice: "FD:B5:13:6A:44:54", ConnectedDevice: "FD:B5:13:6A:44:54", Device: "hci0", ProtocolVersion: "13", Profile: "v1.17.1", ProfileState: "matched", LastError: "open /secret/outbox.db", Delivery: &status.AdapterDeliveryStatus{State: "pending", PendingCount: 2, QuarantinedCount: 1, UsedBytes: 88, FailureCode: "queue_full"}},
		{Name: "meshtastic", Protocol: "meshtastic", Enabled: false, Configured: false, Lifecycle: "disabled", ConnectionState: "disabled"},
	})
	if len(adapters) != 2 {
		t.Fatalf("expected independent adapters, got %#v", adapters)
	}
	meshcore := adapters[0]
	if meshcore.Protocol != "meshcore" || !meshcore.Ready || !meshcore.Connected || meshcore.ConnectionState != "connected" || meshcore.Transport != "ble" || meshcore.ConfiguredDevice != "FD:B5:13:6A:44:54" || meshcore.ConnectedDevice != "FD:B5:13:6A:44:54" || meshcore.Delivery == nil || meshcore.Delivery.PendingCount != 2 || meshcore.ProfileState != "matched" {
		t.Fatalf("unexpected MeshCore cloud status: %#v", meshcore)
	}
	if meshcore.Delivery.FailureCode != "queue_full" {
		t.Fatalf("expected bounded delivery failure code, got %#v", meshcore.Delivery)
	}
	if adapters[1].Enabled || adapters[1].Configured || adapters[1].Lifecycle != "disabled" {
		t.Fatalf("disabled adapter was not distinct: %#v", adapters[1])
	}
}

func TestCloudAdapterStatusesPreserveReadyRawCaptureState(t *testing.T) {
	items := cloudAdapterStatuses([]status.AdapterStatus{{Protocol: "meshcore", Enabled: true, Configured: true, Lifecycle: "connected", ConnectionState: "connected", Ready: true, ProfileState: "raw_capture_only"}})
	if len(items) != 1 || items[0].ProfileState != "raw_capture_only" || !items[0].Ready {
		t.Fatalf("unexpected raw-capture-only projection: %#v", items)
	}
}

func TestCloudAdapterStatusesMarksIntentionalBLEReleaseWithSafeDeviceIdentity(t *testing.T) {
	items := cloudAdapterStatuses([]status.AdapterStatus{{Protocol: "meshcore", Enabled: true, Configured: true, Lifecycle: "released", ConnectionState: "disconnected", Transport: "ble", ReleasedByUser: true, ConfiguredDevice: "AA:BB:CC:DD:EE:FF"}})
	if len(items) != 1 || !items[0].IntentionalRelease || items[0].Lifecycle != "released" || items[0].Connected || items[0].ConfiguredDevice != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("unexpected released cloud status: %#v", items)
	}
}

func TestCloudAdapterStatusesRejectsLocalPathAsDeviceIdentity(t *testing.T) {
	items := cloudAdapterStatuses([]status.AdapterStatus{{Protocol: "meshcore", Enabled: true, Configured: true, Lifecycle: "connected", ConnectionState: "connected", Ready: true, Transport: "physical_serial", ConfiguredDevice: "/dev/ttyACM0"}})
	if len(items) != 1 || items[0].ConfiguredDevice != "" {
		t.Fatalf("unsafe configured device projection: %#v", items)
	}
}
