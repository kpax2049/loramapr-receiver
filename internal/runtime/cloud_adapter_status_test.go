package runtime

import (
	"testing"

	"github.com/loramapr/loramapr-receiver/internal/status"
)

func TestCloudAdapterStatusesProjectOnlySafeFacts(t *testing.T) {
	adapters := cloudAdapterStatuses([]status.AdapterStatus{
		{Name: "meshcore-companion", Protocol: "meshcore", Enabled: true, Configured: true, Lifecycle: "connected", ConnectionState: "connected", Ready: true, Transport: "physical_serial", ConfiguredDevice: "/dev/ttyACM0", Device: "/dev/ttyACM0", ProtocolVersion: "13", Profile: "v1.17.1", ProfileState: "matched", LastError: "open /secret/outbox.db", Delivery: &status.AdapterDeliveryStatus{State: "pending", PendingCount: 2, QuarantinedCount: 1, UsedBytes: 88, FailureCode: "queue_full"}},
		{Name: "meshtastic", Protocol: "meshtastic", Enabled: false, Configured: false, Lifecycle: "disabled", ConnectionState: "disabled"},
	})
	if len(adapters) != 2 {
		t.Fatalf("expected independent adapters, got %#v", adapters)
	}
	meshcore := adapters[0]
	if meshcore.Protocol != "meshcore" || !meshcore.Ready || !meshcore.Connected || meshcore.Delivery == nil || meshcore.Delivery.PendingCount != 2 || meshcore.ProfileState != "matched" {
		t.Fatalf("unexpected MeshCore cloud status: %#v", meshcore)
	}
	if meshcore.Delivery.FailureCode != "queue_full" {
		t.Fatalf("expected bounded delivery failure code, got %#v", meshcore.Delivery)
	}
	if adapters[1].Enabled || adapters[1].Configured || adapters[1].Lifecycle != "disabled" {
		t.Fatalf("disabled adapter was not distinct: %#v", adapters[1])
	}
}

func TestCloudAdapterStatusesKeepRawCaptureDistinct(t *testing.T) {
	items := cloudAdapterStatuses([]status.AdapterStatus{{Protocol: "meshcore", Enabled: true, Configured: true, Lifecycle: "connected", ConnectionState: "connected", Ready: true, ProfileState: "raw_capture_only"}})
	if len(items) != 1 || items[0].ProfileState != "raw_capture_only" || items[0].Ready {
		t.Fatalf("unexpected raw-capture-only projection: %#v", items)
	}
}
