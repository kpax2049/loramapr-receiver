package runtime

import (
	"sort"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/meshcore"
	"github.com/loramapr/loramapr-receiver/internal/outbox"
	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

// refreshAdapterStatuses projects each adapter's existing, protocol-neutral
// runtime snapshot into the local status API. It is intentionally separate
// from the legacy Components map so existing Meshtastic status consumers keep
// their exact behavior.
func (s *Service) refreshAdapterStatuses() {
	c := s.container
	if c == nil || c.Status == nil {
		return
	}
	snapshots := []protocoladapter.AdapterSnapshot(nil)
	if c.Adapters != nil {
		snapshots = c.Adapters.Snapshots()
	}
	adapters := make([]status.AdapterStatus, 0, len(snapshots)+1)
	for _, snapshot := range snapshots {
		adapters = append(adapters, statusFromAdapterSnapshot(snapshot))
	}
	if c.MeshCore == nil {
		adapters = append(adapters, status.AdapterStatus{
			Name: meshcore.AdapterName, Protocol: "meshcore", Lifecycle: string(meshcore.StateDisabled),
			ConnectionState: "disabled", Enabled: false, Configured: false,
			Transport: "disabled", ProfileState: "not_established", UpdatedAt: time.Now().UTC(),
		})
	}

	if c.MeshCore != nil && c.OutboxEngine != nil {
		for i := range adapters {
			if adapters[i].Name != meshcore.AdapterName {
				continue
			}
			if stats, err := c.OutboxEngine.Stats(); err != nil {
				adapters[i].Delivery = &status.AdapterDeliveryStatus{State: "unavailable", FailureCode: "status_unavailable"}
			} else {
				adapters[i].Delivery = deliveryStatus(stats)
			}
		}
	}
	sort.Slice(adapters, func(i, j int) bool { return adapters[i].Name < adapters[j].Name })
	c.Status.SetAdapters(adapters)
}

func statusFromAdapterSnapshot(snapshot protocoladapter.AdapterSnapshot) status.AdapterStatus {
	updatedAt := snapshot.UpdatedAt.UTC()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	return status.AdapterStatus{
		Name: strings.TrimSpace(snapshot.Name), Protocol: strings.TrimSpace(snapshot.Protocol),
		Lifecycle: strings.TrimSpace(snapshot.State), ConnectionState: strings.TrimSpace(snapshot.ConnectionState),
		Enabled: snapshot.Enabled, Configured: snapshot.Configured, Ready: snapshot.Ready,
		Transport: strings.TrimSpace(snapshot.Transport), ConfiguredDevice: strings.TrimSpace(snapshot.ConfiguredDevice),
		Device: strings.TrimSpace(snapshot.Device), ProtocolVersion: strings.TrimSpace(snapshot.ProtocolVersion),
		Profile: strings.TrimSpace(snapshot.Profile), ProfileState: strings.TrimSpace(snapshot.ProfileState),
		LastError: strings.TrimSpace(snapshot.LastError), UpdatedAt: updatedAt,
	}
}

func deliveryStatus(stats outbox.Stats) *status.AdapterDeliveryStatus {
	stateName := "ready"
	if stats.PendingCount > 0 {
		stateName = "pending"
	}
	if stats.Recovered {
		stateName = "recovered"
	}
	if stats.MaintenanceErrorCode != "" {
		stateName = "degraded"
	}
	if stats.DispatchPause != nil {
		stateName = "paused"
	}
	return &status.AdapterDeliveryStatus{
		State: stateName, PendingCount: stats.PendingCount, QuarantinedCount: stats.QuarantinedCount,
		UsedBytes: stats.UsedBytes, RecoveryCode: strings.TrimSpace(stats.RecoveryCode),
		FailureCode: strings.TrimSpace(stats.MaintenanceErrorCode),
	}
}
