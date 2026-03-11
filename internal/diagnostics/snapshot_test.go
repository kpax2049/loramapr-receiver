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
	data := state.Data{}
	data.Pairing.Phase = state.PairingBootstrapExchanged
	data.Pairing.PairingCode = "LMR-SECRET"
	data.Pairing.ActivationToken = "act-secret"
	data.Pairing.LastError = "bootstrap exchange failed"
	data.Cloud.IngestAPIKey = "ingest-secret"
	data.Cloud.EndpointURL = "https://api.example.com"

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
	if len(snapshot.Operations.Checks) == 0 {
		t.Fatal("expected operational checks in support snapshot")
	}
}
