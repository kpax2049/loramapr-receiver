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

func TestIdentityHints(t *testing.T) {
	t.Parallel()

	model := New()
	model.SetInstallationID("install-xyz")
	model.SetIdentity("garage-pi-a1b2c3", "garage-pi", "rx-123", "Garage Receiver", "Home", "Outdoor")
	snap := model.Snapshot()

	if snap.InstallationID != "install-xyz" {
		t.Fatalf("unexpected installation_id: %q", snap.InstallationID)
	}
	if snap.LocalName != "garage-pi-a1b2c3" {
		t.Fatalf("unexpected local_name: %q", snap.LocalName)
	}
	if snap.Hostname != "garage-pi" {
		t.Fatalf("unexpected hostname: %q", snap.Hostname)
	}
	if snap.CloudReceiverID != "rx-123" {
		t.Fatalf("unexpected cloud_receiver_id: %q", snap.CloudReceiverID)
	}
	if snap.CloudReceiverLabel != "Garage Receiver" {
		t.Fatalf("unexpected cloud_receiver_label: %q", snap.CloudReceiverLabel)
	}
	if snap.CloudSiteLabel != "Home" {
		t.Fatalf("unexpected cloud_site_label: %q", snap.CloudSiteLabel)
	}
	if snap.CloudGroupLabel != "Outdoor" {
		t.Fatalf("unexpected cloud_group_label: %q", snap.CloudGroupLabel)
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

func TestHomeAutoSessionStatus(t *testing.T) {
	t.Parallel()

	model := New()
	pendingSince := time.Date(2026, 3, 12, 12, 0, 0, 0, time.UTC)
	lastSuccess := pendingSince.Add(-2 * time.Minute)
	cooldownUntil := pendingSince.Add(30 * time.Second)
	gpsAt := pendingSince.Add(-10 * time.Second)
	gpsDistance := 182.5
	model.SetHomeAutoSession(HomeAutoSessionSnapshot{
		Enabled:               true,
		Mode:                  "observe",
		State:                 "start_pending",
		ControlState:          "pending_start",
		ActiveStateSource:     "local_recovered_unverified",
		Summary:               "waiting for start debounce",
		HomeSummary:           "37.3349,-122.0090 radius 150m",
		TrackedNodeIDs:        []string{"!nodeA", "!nodeB"},
		TrackedNodeState:      "node !nodeA is outside home geofence",
		ReconciliationState:   "pending_start_recovering",
		PendingAction:         "start",
		PendingSince:          &pendingSince,
		ActiveSessionID:       "session-1",
		ActiveTriggerNode:     "!nodeA",
		LastDecisionReason:    "inside->outside transition",
		LastError:             "",
		LastAction:            "start",
		LastActionResult:      "retry_scheduled",
		LastActionAt:          &pendingSince,
		LastSuccessfulAction:  "start",
		LastSuccessfulAt:      &lastSuccess,
		BlockedReason:         "",
		ConsecutiveFailures:   2,
		CooldownUntil:         &cooldownUntil,
		DecisionCooldownUntil: &cooldownUntil,
		GPSStatus:             "valid",
		GPSReason:             "tracked node position valid",
		GPSNodeID:             "!nodeA",
		GPSUpdatedAt:          &gpsAt,
		GPSDistanceM:          &gpsDistance,
		ObservedQueueDepth:    3,
		ObservedDropped:       1,
	})
	snap := model.Snapshot()

	if !snap.HomeAutoSession.Enabled {
		t.Fatal("expected home_auto_session enabled")
	}
	if snap.HomeAutoSession.Mode != "observe" {
		t.Fatalf("unexpected home_auto_session mode: %q", snap.HomeAutoSession.Mode)
	}
	if snap.HomeAutoSession.State != "start_pending" {
		t.Fatalf("unexpected home_auto_session state: %q", snap.HomeAutoSession.State)
	}
	if snap.HomeAutoSession.PendingAction != "start" {
		t.Fatalf("unexpected home_auto_session pending action: %q", snap.HomeAutoSession.PendingAction)
	}
	if snap.HomeAutoSession.LastAction != "start" {
		t.Fatalf("unexpected home_auto_session last action: %q", snap.HomeAutoSession.LastAction)
	}
	if snap.HomeAutoSession.ControlState != "pending_start" {
		t.Fatalf("unexpected home_auto_session control state: %q", snap.HomeAutoSession.ControlState)
	}
	if snap.HomeAutoSession.ActiveStateSource != "local_recovered_unverified" {
		t.Fatalf("unexpected home_auto_session active state source: %q", snap.HomeAutoSession.ActiveStateSource)
	}
	if snap.HomeAutoSession.LastActionResult != "retry_scheduled" {
		t.Fatalf("unexpected home_auto_session last action result: %q", snap.HomeAutoSession.LastActionResult)
	}
	if snap.HomeAutoSession.GPSDistanceM == nil {
		t.Fatal("expected gps distance pointer")
	}
	if len(snap.HomeAutoSession.TrackedNodeIDs) != 2 {
		t.Fatalf("unexpected tracked nodes: %#v", snap.HomeAutoSession.TrackedNodeIDs)
	}

	snap.HomeAutoSession.TrackedNodeIDs[0] = "tampered"
	*snap.HomeAutoSession.GPSDistanceM = 1
	snap2 := model.Snapshot()
	if snap2.HomeAutoSession.TrackedNodeIDs[0] != "!nodeA" {
		t.Fatalf("expected snapshot copy for tracked node IDs, got %#v", snap2.HomeAutoSession.TrackedNodeIDs)
	}
	if snap2.HomeAutoSession.GPSDistanceM == nil || *snap2.HomeAutoSession.GPSDistanceM != gpsDistance {
		t.Fatalf("expected snapshot copy for home_auto_session GPSDistanceM, got %#v", snap2.HomeAutoSession.GPSDistanceM)
	}
}
