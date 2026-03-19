package diagnostics

import (
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

func TestSupportSnapshotRedactsSecrets(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 11, 11, 30, 0, 0, time.UTC)
	cfg := config.Default()
	cfg.Cloud.BaseURL = "https://api.example.com"
	cfg.Meshtastic.Device = "/dev/ttyUSB0"
	cfg.HomeAutoSession.Enabled = true
	cfg.HomeAutoSession.Mode = config.HomeAutoSessionModeObserve
	cfg.HomeAutoSession.Home = config.HomeGeofenceConfig{Lat: 37.3349, Lon: -122.0090, RadiusM: 150}
	cfg.HomeAutoSession.TrackedNodeIDs = []string{"!nodeA"}
	data := state.Data{}
	data.Installation.ID = "install-abc123"
	data.Installation.LocalName = "garage-pi-a1b2c3"
	data.Installation.Hostname = "garage-pi"
	data.Pairing.Phase = state.PairingBootstrapExchanged
	data.Pairing.PairingCode = "LMR-SECRET"
	data.Pairing.ActivationToken = "act-secret"
	data.Pairing.LastError = "bootstrap exchange failed"
	data.Cloud.IngestAPIKey = "ingest-secret"
	data.Cloud.EndpointURL = "https://api.example.com"
	data.Cloud.ReceiverID = "rx-123"
	data.Cloud.ReceiverLabel = "Garage Receiver"
	data.Cloud.SiteLabel = "Home"
	data.Cloud.GroupLabel = "Outdoor"
	data.HomeAutoSession.ModuleState = "observe_ready"
	data.HomeAutoSession.ControlState = "ready"
	data.HomeAutoSession.ActiveStateSource = "local_recovered_unverified"
	data.HomeAutoSession.ReconciliationState = "clean_idle"
	data.HomeAutoSession.PendingAction = "start"
	data.HomeAutoSession.LastAction = "start"
	data.HomeAutoSession.LastActionResult = "observe_suppressed"
	data.HomeAutoSession.LastDecisionReason = "observe mode ready"
	data.HomeAutoSession.GPSStatus = "stale"
	data.HomeAutoSession.GPSReason = "position sample older than threshold"
	data.HomeAutoSession.BlockedReason = "cloud session endpoint unavailable"
	data.HomeAutoSession.ConsecutiveFailures = 2
	data.HomeAutoSession.EffectiveConfigSource = "cloud_managed"
	data.HomeAutoSession.EffectiveConfigVersion = "has-v1"
	data.HomeAutoSession.CloudConfigPresent = true
	data.HomeAutoSession.LastFetchedConfigVer = "has-v1"
	data.HomeAutoSession.LastAppliedConfigVer = "has-v1"
	data.HomeAutoSession.LastConfigApplyResult = "cloud_config_applied"
	desiredEnabled := true
	data.HomeAutoSession.DesiredConfigEnabled = &desiredEnabled
	data.HomeAutoSession.DesiredConfigMode = "observe"

	snapshot := CollectSupportSnapshot(cfg, data, Finding{Code: FailureActivationFailed}, CollectOptions{
		Now: func() time.Time { return now },
		ProbeCloud: func(_ string, _ time.Duration) CloudProbe {
			return CloudProbe{Status: "reachable"}
		},
		ProbeNetwork: func() NetworkProbe {
			return NetworkProbe{Status: "available", Interface: "eth0", Address: "192.168.1.10"}
		},
		ProbeLocal: func(_ string, _ time.Duration) LocalStatusProbe {
			return LocalStatusProbe{Status: "unreachable", Detail: "connection refused"}
		},
		DetectDevice: func(_ config.MeshtasticConfig) (meshtastic.DetectionResult, error) {
			return meshtastic.DetectionResult{Device: "/dev/ttyUSB0", Candidates: []string{"/dev/ttyUSB0"}}, nil
		},
		ConfigPath: "/etc/loramapr/receiver.json",
		StatePath:  "/var/lib/loramapr/receiver-state.json",
	})

	if snapshot.Pairing.HasCode != true || snapshot.Pairing.HasToken != true {
		t.Fatal("expected redacted booleans for pairing code/token")
	}
	if snapshot.Cloud.HasIngestCredential != true {
		t.Fatal("expected redacted boolean for ingest credential")
	}
	if snapshot.Runtime.InstallType == "" {
		t.Fatal("expected runtime install type in support snapshot")
	}
	if snapshot.Pairing.LastError == "" {
		t.Fatal("expected coarse last error to be present")
	}
	if snapshot.Redaction.OmittedFields == nil || len(snapshot.Redaction.OmittedFields) == 0 {
		t.Fatal("expected omitted fields list")
	}
	assertContains := func(value string) {
		t.Helper()
		for _, item := range snapshot.Redaction.OmittedFields {
			if item == value {
				return
			}
		}
		t.Fatalf("expected omitted fields to contain %q", value)
	}
	assertContains("cloud.ingest_api_key_secret")
	assertContains("pairing.pairing_code")
	assertContains("pairing.activation_token")
	assertContains("cloud.credential_ref")
	assertContains("meshtastic_config.share_url")
	assertContains("meshtastic_config.share_qr_text")
	if snapshot.Config.StatePath == "" || snapshot.Config.SchemaVersion == 0 {
		t.Fatal("expected config/state markers in support snapshot")
	}
	if snapshot.Identity.InstallationID != "install-abc123" {
		t.Fatalf("expected installation identity in support snapshot, got %q", snapshot.Identity.InstallationID)
	}
	if snapshot.Identity.LocalName != "garage-pi-a1b2c3" {
		t.Fatalf("expected local name in support snapshot, got %q", snapshot.Identity.LocalName)
	}
	if snapshot.Identity.CloudReceiverID != "rx-123" {
		t.Fatalf("expected cloud receiver id in support snapshot, got %q", snapshot.Identity.CloudReceiverID)
	}
	if snapshot.Identity.CloudReceiverName != "Garage Receiver" {
		t.Fatalf("expected cloud receiver name in support snapshot, got %q", snapshot.Identity.CloudReceiverName)
	}
	if len(snapshot.Operations.Checks) == 0 {
		t.Fatal("expected operational checks in support snapshot")
	}
	if snapshot.Meshtastic.ConfigSummary.PSKState == "" {
		t.Fatal("expected meshtastic config summary psk_state marker")
	}
	if !snapshot.HomeAutoSession.Enabled || snapshot.HomeAutoSession.Mode != "observe" {
		t.Fatalf("expected home auto session config in support snapshot: %#v", snapshot.HomeAutoSession)
	}
	if snapshot.HomeAutoSession.State != "observe_ready" {
		t.Fatalf("expected home auto session state in support snapshot, got %q", snapshot.HomeAutoSession.State)
	}
	if snapshot.HomeAutoSession.ReconciliationState == "" {
		t.Fatal("expected home auto reconciliation state in support snapshot")
	}
	if snapshot.HomeAutoSession.ControlState == "" {
		t.Fatal("expected home auto control state in support snapshot")
	}
	if snapshot.HomeAutoSession.ActiveStateSource == "" {
		t.Fatal("expected home auto active state source in support snapshot")
	}
	if snapshot.HomeAutoSession.LastAction == "" || snapshot.HomeAutoSession.LastActionResult == "" {
		t.Fatal("expected home auto last action/result in support snapshot")
	}
	if snapshot.HomeAutoSession.GPSStatus == "" {
		t.Fatal("expected home auto gps status in support snapshot")
	}
	if snapshot.HomeAutoSession.EffectiveConfigSource != "cloud_managed" {
		t.Fatalf("expected home auto effective config source, got %q", snapshot.HomeAutoSession.EffectiveConfigSource)
	}
	if snapshot.HomeAutoSession.LastConfigApplyResult != "cloud_config_applied" {
		t.Fatalf("expected home auto config apply result, got %q", snapshot.HomeAutoSession.LastConfigApplyResult)
	}
	if snapshot.Attention.State == AttentionNone {
		t.Fatal("expected attention state in support snapshot")
	}
	if snapshot.Attention.Code == "" {
		t.Fatal("expected attention code in support snapshot")
	}
}

func TestSupportSnapshotRedactsMeshtasticShareFromLocalProbeSnapshot(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	data := state.Data{}
	data.Installation.ID = "install-xyz"
	localSnap := status.Snapshot{
		Lifecycle:    status.LifecycleRunning,
		Ready:        true,
		PairingPhase: "steady_state",
		MeshtasticConfig: status.MeshtasticConfigSnapshot{
			Available:         true,
			Region:            "EU_868",
			PrimaryChannel:    "Home Mesh",
			PSKState:          "present",
			ShareURL:          "https://meshtastic.org/e/#SECRET",
			ShareQRText:       "https://meshtastic.org/e/#SECRET",
			ShareURLRedacted:  "https://meshtastic.org/e/#<redacted>",
			ShareURLAvailable: true,
		},
	}

	snapshot := CollectSupportSnapshot(cfg, data, Finding{}, CollectOptions{
		Now: func() time.Time { return time.Now().UTC() },
		ProbeCloud: func(_ string, _ time.Duration) CloudProbe {
			return CloudProbe{Status: "reachable"}
		},
		ProbeNetwork: func() NetworkProbe {
			return NetworkProbe{Status: "available"}
		},
		ProbeLocal: func(_ string, _ time.Duration) LocalStatusProbe {
			return LocalStatusProbe{Status: "reachable", Snapshot: &localSnap}
		},
		DetectDevice: func(_ config.MeshtasticConfig) (meshtastic.DetectionResult, error) {
			return meshtastic.DetectionResult{}, nil
		},
	})

	if snapshot.Network.LocalRuntime.Snapshot == nil {
		t.Fatal("expected local runtime snapshot to be present")
	}
	if got := snapshot.Network.LocalRuntime.Snapshot.MeshtasticConfig.ShareURL; got != "" {
		t.Fatalf("expected local runtime snapshot meshtastic share URL to be redacted, got %q", got)
	}
	if got := snapshot.Network.LocalRuntime.Snapshot.MeshtasticConfig.ShareQRText; got != "" {
		t.Fatalf("expected local runtime snapshot meshtastic share QR text to be redacted, got %q", got)
	}
	if got := snapshot.Meshtastic.ConfigSummary.ShareURLRedacted; got == "" {
		t.Fatal("expected redacted meshtastic share hint in support snapshot")
	}
}

func TestSupportSnapshotIncludesSetupIssuesFromLocalStatus(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Cloud.BaseURL = "https://api.loramapr.example"
	data := state.Data{}
	data.Pairing.Phase = state.PairingSteadyState

	now := time.Now().UTC()
	localSnap := status.Snapshot{
		RuntimeProfile: "linux-service",
		InstallType:    "linux-package",
		Lifecycle:      status.LifecycleRunning,
		Ready:          true,
		PairingPhase:   "steady_state",
		CloudEndpoint:  "https://api.loramapr.example",
		CloudStatus:    "unreachable",
		CloudReachable: false,
		Components: map[string]status.ComponentStatus{
			"portal": {
				State:     "running",
				Message:   "local setup portal listening on 127.0.0.1:8080",
				UpdatedAt: now,
			},
			"meshtastic": {
				State:     "degraded",
				Message:   "device=/dev/ttyACM0 error=native serial stream unreadable",
				UpdatedAt: now,
			},
		},
	}

	snapshot := CollectSupportSnapshot(cfg, data, Finding{}, CollectOptions{
		Now: func() time.Time { return now },
		ProbeCloud: func(_ string, _ time.Duration) CloudProbe {
			return CloudProbe{Status: "unreachable", Detail: "dial tcp timeout"}
		},
		ProbeNetwork: func() NetworkProbe {
			return NetworkProbe{Status: "available"}
		},
		ProbeLocal: func(_ string, _ time.Duration) LocalStatusProbe {
			return LocalStatusProbe{Status: "reachable", Snapshot: &localSnap}
		},
		DetectDevice: func(_ config.MeshtasticConfig) (meshtastic.DetectionResult, error) {
			return meshtastic.DetectionResult{Device: "/dev/ttyACM0"}, nil
		},
	})

	if len(snapshot.Setup.Issues) == 0 {
		t.Fatal("expected setup issues in support snapshot")
	}

	hasPortal := false
	hasCloud := false
	hasUSB := false
	for _, issue := range snapshot.Setup.Issues {
		switch issue.Code {
		case "portal_bind_localhost":
			hasPortal = true
		case "cloud_base_url_placeholder":
			hasCloud = true
		case "usb_protocol_unusable":
			hasUSB = true
		}
	}
	if !hasPortal || !hasCloud || !hasUSB {
		t.Fatalf("expected portal/cloud/usb setup issues, got %#v", snapshot.Setup.Issues)
	}
}
