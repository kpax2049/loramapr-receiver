package runtime

import (
	"fmt"
	"strings"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshcore"
	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

func reserveConfiguredSerialPaths(cfg config.Config, leases *protocoladapter.SerialLeaseRegistry) ([]func(), error) {
	type reservation struct {
		path  string
		owner string
	}
	reservations := make([]reservation, 0, 2)
	if strings.EqualFold(strings.TrimSpace(cfg.MeshCore.Transport), "physical_serial") {
		reservations = append(reservations, reservation{path: cfg.MeshCore.Device, owner: meshcore.AdapterName})
	}
	meshtasticTransport := strings.ToLower(strings.TrimSpace(cfg.Meshtastic.Transport))
	if (meshtasticTransport == "serial" || meshtasticTransport == "bridge") && strings.TrimSpace(cfg.Meshtastic.Device) != "" {
		reservations = append(reservations, reservation{path: cfg.Meshtastic.Device, owner: "meshtastic"})
	}

	releases := make([]func(), 0, len(reservations))
	for _, item := range reservations {
		release, err := leases.Acquire(item.path, item.owner)
		if err != nil {
			releaseSerialReservations(releases)
			return nil, fmt.Errorf("serial_device_conflict: reserve %s device %q: %w", item.owner, item.path, err)
		}
		releases = append(releases, release)
	}
	return releases, nil
}

func releaseSerialReservations(releases []func()) {
	for index := len(releases) - 1; index >= 0; index-- {
		if releases[index] != nil {
			releases[index]()
		}
	}
}
