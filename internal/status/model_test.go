package status

import "testing"

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
