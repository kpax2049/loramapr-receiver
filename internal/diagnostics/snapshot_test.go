package diagnostics

import (
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
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
	if snapshot.Attention.State == AttentionNone {
		t.Fatal("expected attention state in support snapshot")
	}
	if snapshot.Attention.Code == "" {
		t.Fatal("expected attention code in support snapshot")
	}
}
