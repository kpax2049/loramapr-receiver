package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "missing.json")
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Service.Mode != ModeAuto {
		t.Fatalf("expected default mode %q, got %q", ModeAuto, cfg.Service.Mode)
	}
	if cfg.Runtime.Profile != "auto" {
		t.Fatalf("expected default profile %q, got %q", "auto", cfg.Runtime.Profile)
	}
	if cfg.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, cfg.SchemaVersion)
	}
	if cfg.Portal.BindAddress != "127.0.0.1:8080" {
		t.Fatalf("unexpected default bind address: %q", cfg.Portal.BindAddress)
	}
	if cfg.Service.Heartbeat.Std() != 30*time.Second {
		t.Fatalf("unexpected default heartbeat: %s", cfg.Service.Heartbeat.Std())
	}
	if cfg.Update.CheckInterval.Std() != 6*time.Hour {
		t.Fatalf("unexpected update check interval: %s", cfg.Update.CheckInterval.Std())
	}
	if cfg.Update.RequestTimeout.Std() != 4*time.Second {
		t.Fatalf("unexpected update request timeout: %s", cfg.Update.RequestTimeout.Std())
	}
}

func TestLoadRejectsInvalidMode(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver.json")
	if err := os.WriteFile(path, []byte(`{"service":{"mode":"invalid"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid mode")
	}
}

func TestLoadRejectsInvalidMeshtasticTransport(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver.json")
	if err := os.WriteFile(path, []byte(`{"meshtastic":{"transport":"invalid"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid meshtastic transport")
	}
}

func TestLoadRejectsInvalidRuntimeProfile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver.json")
	if err := os.WriteFile(path, []byte(`{"runtime":{"profile":"pi"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid runtime profile")
	}
}

func TestLoadRejectsNewerSchemaVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":999}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for newer schema version")
	}
}

func TestLoadRejectsInvalidUpdateManifestURL(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver.json")
	if err := os.WriteFile(path, []byte(`{"update":{"manifest_url":"not-a-url"}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for invalid update manifest url")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver.json")
	cfg := Default()
	cfg.Service.Mode = ModeService
	cfg.Service.Heartbeat = Duration(45 * time.Second)
	cfg.Portal.BindAddress = "0.0.0.0:9080"
	cfg.Paths.StateFile = "/var/lib/loramapr/state.json"
	cfg.Cloud.BaseURL = "https://api.example.com"
	cfg.Logging.Format = "text"
	cfg.Logging.Level = "debug"
	cfg.Runtime.Profile = "appliance-pi"
	cfg.Update.Enabled = true
	cfg.Update.ManifestURL = "https://downloads.loramapr.com/receiver/stable/latest/cloud-manifest.fragment.json"
	cfg.Update.MinSupportedVersion = "v2.2.0"

	if err := Save(path, cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}

	if loaded.Service.Mode != ModeService {
		t.Fatalf("expected mode %q, got %q", ModeService, loaded.Service.Mode)
	}
	if loaded.Service.Heartbeat.Std() != 45*time.Second {
		t.Fatalf("expected heartbeat 45s, got %s", loaded.Service.Heartbeat.Std())
	}
	if loaded.Portal.BindAddress != "0.0.0.0:9080" {
		t.Fatalf("unexpected bind address: %s", loaded.Portal.BindAddress)
	}
	if loaded.Paths.StateFile != "/var/lib/loramapr/state.json" {
		t.Fatalf("unexpected state file: %s", loaded.Paths.StateFile)
	}
	if loaded.Runtime.Profile != "appliance-pi" {
		t.Fatalf("unexpected runtime profile: %s", loaded.Runtime.Profile)
	}
	if loaded.Update.ManifestURL == "" {
		t.Fatalf("expected update manifest URL to persist")
	}
	if loaded.Update.MinSupportedVersion != "v2.2.0" {
		t.Fatalf("unexpected update min supported version: %s", loaded.Update.MinSupportedVersion)
	}
}
