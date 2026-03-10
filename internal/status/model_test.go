package status

import (
	"testing"
	"time"
)

func TestSnapshotReturnsCopy(t *testing.T) {
	t.Parallel()

	model := New()
	model.SetComponent("portal", "running", "ready")

	snap1 := model.Snapshot()
	snap1.Components["portal"] = ComponentStatus{State: "tampered"}

	snap2 := model.Snapshot()
	if snap2.Components["portal"].State != "running" {
		t.Fatalf("expected immutable snapshot copy, got %q", snap2.Components["portal"].State)
	}
}

func TestReadinessUpdates(t *testing.T) {
	t.Parallel()

	model := New()
	model.SetReady(true, "setup portal available")

	snap := model.Snapshot()
	if !snap.Ready {
		t.Fatal("expected ready=true")
	}
	if snap.ReadyReason != "setup portal available" {
		t.Fatalf("unexpected ready reason: %q", snap.ReadyReason)
	}
}

func TestHeartbeatAndPacketTelemetry(t *testing.T) {
	t.Parallel()

	model := New()
	now := time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC)
	model.SetHeartbeat(&now, &now, true)
	model.SetPacketTelemetry(&now, &now, &now, 3)

	snap := model.Snapshot()
	if !snap.HeartbeatFresh {
		t.Fatal("expected heartbeat_fresh=true")
	}
	if snap.LastHeartbeatAck == nil || snap.LastPacketAck == nil {
		t.Fatal("expected heartbeat/packet telemetry timestamps")
	}
	if snap.IngestQueueDepth != 3 {
		t.Fatalf("expected queue depth 3, got %d", snap.IngestQueueDepth)
	}
}
