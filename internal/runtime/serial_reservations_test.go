package runtime

import (
	"strings"
	"testing"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshcore"
	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

func TestReserveConfiguredSerialPathsRejectsExplicitCollision(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.MeshCore.Transport = "physical_serial"
	cfg.MeshCore.Device = dir + "/future/../radio"
	cfg.Meshtastic.Transport = "serial"
	cfg.Meshtastic.Device = dir + "/radio"
	leases := protocoladapter.NewSerialLeaseRegistry()
	releases, err := reserveConfiguredSerialPaths(cfg, leases)
	if err == nil || !strings.Contains(err.Error(), "serial_device_conflict") {
		t.Fatalf("expected deterministic serial conflict, got releases=%d err=%v", len(releases), err)
	}
	if _, ok := leases.Owner(cfg.MeshCore.Device); ok {
		t.Fatal("failed reservation must roll back earlier reservations")
	}
}

func TestReserveConfiguredSerialPathsReservesMeshCoreForRuntimeLifetime(t *testing.T) {
	cfg := config.Default()
	cfg.MeshCore.Transport = "physical_serial"
	cfg.MeshCore.Device = t.TempDir() + "/radio"
	leases := protocoladapter.NewSerialLeaseRegistry()
	releases, err := reserveConfiguredSerialPaths(cfg, leases)
	if err != nil {
		t.Fatal(err)
	}
	if owner, ok := leases.Owner(cfg.MeshCore.Device); !ok || owner != meshcore.AdapterName {
		t.Fatalf("unexpected reservation owner=%q ok=%v", owner, ok)
	}
	if _, err := leases.Acquire(cfg.MeshCore.Device, "meshtastic"); err == nil {
		t.Fatal("runtime reservation must exclude another adapter")
	}
	releaseSerialReservations(releases)
	if _, ok := leases.Owner(cfg.MeshCore.Device); ok {
		t.Fatal("runtime shutdown must release reservation")
	}
}
