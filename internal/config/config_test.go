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
	if cfg.Portal.BindAddress != "127.0.0.1:8080" {
		t.Fatalf("unexpected default bind address: %q", cfg.Portal.BindAddress)
	}
	if cfg.Service.Heartbeat.Std() != 30*time.Second {
		t.Fatalf("unexpected default heartbeat: %s", cfg.Service.Heartbeat.Std())
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
}
