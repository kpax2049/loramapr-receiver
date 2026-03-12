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

func TestFailureLifecycle(t *testing.T) {
	t.Parallel()

	model := New()
	model.SetFailure("cloud_unreachable", "Cloud endpoint is unreachable", "Check network and DNS")
	snap := model.Snapshot()

	if snap.FailureCode != "cloud_unreachable" {
		t.Fatalf("unexpected failure code: %q", snap.FailureCode)
	}
	if snap.FailureSince == nil {
		t.Fatal("expected failure_since to be set")
	}
	if len(snap.RecentFailures) != 1 {
		t.Fatalf("expected one recent failure entry, got %d", len(snap.RecentFailures))
	}

	model.SetFailure("", "", "")
	snap = model.Snapshot()
	if snap.FailureCode != "" {
		t.Fatalf("expected failure code cleared, got %q", snap.FailureCode)
	}
	if snap.FailureSince != nil {
		t.Fatal("expected failure_since cleared")
	}
	if len(snap.RecentFailures) != 1 {
		t.Fatalf("expected history to be retained, got %d", len(snap.RecentFailures))
	}
}

func TestBuildInfo(t *testing.T) {
	t.Parallel()

	model := New()
	model.SetBuildInfo("v1.1.0", "stable", "abc123", "2026-03-11T00:00:00Z", "build-abc123", "linux", "arm64", "linux-package")
	snap := model.Snapshot()

	if snap.ReceiverVersion != "v1.1.0" {
		t.Fatalf("unexpected receiver_version: %q", snap.ReceiverVersion)
	}
	if snap.ReleaseChannel != "stable" {
		t.Fatalf("unexpected release_channel: %q", snap.ReleaseChannel)
	}
	if snap.BuildCommit != "abc123" {
		t.Fatalf("unexpected build_commit: %q", snap.BuildCommit)
	}
	if snap.BuildDate != "2026-03-11T00:00:00Z" {
		t.Fatalf("unexpected build_date: %q", snap.BuildDate)
	}
	if snap.BuildID != "build-abc123" {
		t.Fatalf("unexpected build_id: %q", snap.BuildID)
	}
	if snap.Platform != "linux" || snap.Arch != "arm64" {
		t.Fatalf("unexpected platform/arch: %s/%s", snap.Platform, snap.Arch)
	}
	if snap.InstallType != "linux-package" {
		t.Fatalf("unexpected install_type: %q", snap.InstallType)
	}
}

func TestUpdateStatus(t *testing.T) {
	t.Parallel()

	model := New()
	now := time.Date(2026, 3, 11, 14, 0, 0, 0, time.UTC)
	model.SetUpdateStatus(
		"outdated",
		"New stable receiver release is available",
		"Upgrade via apt or appliance image refresh.",
		"v2.4.0",
		"stable",
		"v2.4.0",
		&now,
	)
	snap := model.Snapshot()

	if snap.UpdateStatus != "outdated" {
		t.Fatalf("unexpected update_status: %q", snap.UpdateStatus)
	}
	if snap.UpdateManifestVersion != "v2.4.0" {
		t.Fatalf("unexpected update_manifest_version: %q", snap.UpdateManifestVersion)
	}
	if snap.UpdateManifestChannel != "stable" {
		t.Fatalf("unexpected update_manifest_channel: %q", snap.UpdateManifestChannel)
	}
	if snap.UpdateCheckedAt == nil {
		t.Fatal("expected update_checked_at to be set")
	}
}

func TestAttentionStatus(t *testing.T) {
	t.Parallel()

	model := New()
	model.SetAttention(
		"urgent",
		"lifecycle",
		"receiver_credential_revoked",
		"Receiver credential was revoked",
		"Reset and re-pair this receiver.",
		true,
	)
	snap := model.Snapshot()

	if snap.AttentionState != "urgent" {
		t.Fatalf("unexpected attention_state: %q", snap.AttentionState)
	}
	if snap.AttentionCategory != "lifecycle" {
		t.Fatalf("unexpected attention_category: %q", snap.AttentionCategory)
	}
	if !snap.AttentionActionRequired {
		t.Fatal("expected attention_action_required=true")
	}
	if snap.AttentionUpdatedAt == nil {
		t.Fatal("expected attention_updated_at to be set")
	}

	model.SetAttention("none", "", "", "", "", false)
	snap = model.Snapshot()
	if snap.AttentionState != "none" {
		t.Fatalf("expected attention_state none, got %q", snap.AttentionState)
	}
	if snap.AttentionUpdatedAt != nil {
		t.Fatal("expected attention_updated_at to clear for none state")
	}
}
