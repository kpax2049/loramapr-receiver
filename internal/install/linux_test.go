package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

func TestDefaultLinuxLayout(t *testing.T) {
	t.Parallel()

	layout := DefaultLinuxLayout("/tmp/stage")
	if layout.ConfigPath != "/tmp/stage/etc/loramapr/receiver.json" {
		t.Fatalf("unexpected config path: %s", layout.ConfigPath)
	}
	if layout.SystemdUnitPath != "/tmp/stage/etc/systemd/system/loramapr-receiverd.service" {
		t.Fatalf("unexpected unit path: %s", layout.SystemdUnitPath)
	}
}

func TestInstallLinuxSystemdDryRun(t *testing.T) {
	t.Parallel()

	result, err := InstallLinuxSystemd(LinuxInstallOptions{TargetRoot: t.TempDir(), DryRun: true})
	if err != nil {
		t.Fatalf("InstallLinuxSystemd dry-run failed: %v", err)
	}
	if len(result.Operations) == 0 {
		t.Fatal("expected planned operations")
	}
}

func TestInstallLinuxSystemdWritesFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	result, err := InstallLinuxSystemd(LinuxInstallOptions{TargetRoot: root, Force: true})
	if err != nil {
		t.Fatalf("InstallLinuxSystemd failed: %v", err)
	}

	if _, err := os.Stat(result.Layout.ConfigPath); err != nil {
		t.Fatalf("expected config file: %v", err)
	}
	if _, err := os.Stat(result.Layout.SystemdUnitPath); err != nil {
		t.Fatalf("expected systemd unit file: %v", err)
	}

	unitText, err := os.ReadFile(result.Layout.SystemdUnitPath)
	if err != nil {
		t.Fatalf("read unit file: %v", err)
	}
	if len(unitText) == 0 {
		t.Fatal("expected non-empty unit file")
	}
	if !strings.Contains(string(unitText), "SupplementaryGroups=dialout") {
		t.Fatal("expected systemd unit to include SupplementaryGroups=dialout")
	}

	cfg, err := config.Load(result.Layout.ConfigPath)
	if err != nil {
		t.Fatalf("load generated config: %v", err)
	}
	if cfg.Portal.BindAddress != "0.0.0.0:8080" {
		t.Fatalf("expected packaged bind address 0.0.0.0:8080, got %q", cfg.Portal.BindAddress)
	}
	if cfg.Paths.StateFile != "/var/lib/loramapr/receiver-state.json" {
		t.Fatalf("expected packaged state path /var/lib/loramapr/receiver-state.json, got %q", cfg.Paths.StateFile)
	}
	if cfg.Runtime.Profile != "linux-service" {
		t.Fatalf("expected packaged runtime profile linux-service, got %q", cfg.Runtime.Profile)
	}
}

func TestUninstallLinuxSystemd(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	layout := DefaultLinuxLayout(root)

	for _, path := range []string{layout.ConfigDir, layout.SystemdUnitDir, layout.StateDir} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", path, err)
		}
	}
	if err := os.WriteFile(layout.ConfigPath, []byte("{}"), 0o640); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(layout.SystemdUnitPath, []byte("unit"), 0o644); err != nil {
		t.Fatalf("write unit: %v", err)
	}
	if err := os.WriteFile(layout.StatePath, []byte("state"), 0o600); err != nil {
		t.Fatalf("write state: %v", err)
	}

	_, err := UninstallLinuxSystemd(LinuxUninstallOptions{TargetRoot: root, PurgeState: true})
	if err != nil {
		t.Fatalf("UninstallLinuxSystemd failed: %v", err)
	}

	for _, path := range []string{layout.ConfigPath, layout.SystemdUnitPath, layout.StatePath} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("expected removed file %s", path)
		}
	}
}

func TestJoinRootNormalization(t *testing.T) {
	t.Parallel()

	if got := joinRoot("/", "/etc/loramapr"); got != "/etc/loramapr" {
		t.Fatalf("unexpected root join: %s", got)
	}
	if got := joinRoot(filepath.Clean("/tmp/stage"), "/etc/loramapr"); got != "/tmp/stage/etc/loramapr" {
		t.Fatalf("unexpected staged join: %s", got)
	}
}
