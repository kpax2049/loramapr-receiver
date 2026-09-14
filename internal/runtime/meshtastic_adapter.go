package runtime

import (
	"context"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

const meshtasticAdapterName = "meshtastic"

type meshtasticRadioAdapter struct {
	legacy meshtastic.Adapter
}

func newMeshtasticRadioAdapter(legacy meshtastic.Adapter) protocoladapter.RadioAdapter {
	return &meshtasticRadioAdapter{legacy: legacy}
}

func (a *meshtasticRadioAdapter) Name() string { return meshtasticAdapterName }

func (a *meshtasticRadioAdapter) Start(ctx context.Context, sink protocoladapter.AdapterSink) error {
	events, err := a.legacy.Start(ctx)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-events:
			if !ok {
				return nil
			}
			if err := sink.Publish(protocoladapter.Event{
				Adapter:    meshtasticAdapterName,
				Value:      event,
				ObservedAt: event.Received,
			}); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
	}
}

func (a *meshtasticRadioAdapter) Snapshot() protocoladapter.AdapterSnapshot {
	snapshot := a.legacy.Snapshot()
	updatedAt := snapshot.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	if strings.EqualFold(strings.TrimSpace(snapshot.Transport), "disabled") {
		return protocoladapter.AdapterSnapshot{
			Name: meshtasticAdapterName, Protocol: "meshtastic", State: "disabled", ConnectionState: "disabled",
			Enabled: false, Configured: false, Ready: false, Transport: "disabled",
			Summary: "Meshtastic transport disabled by configuration", UpdatedAt: updatedAt,
		}
	}
	return protocoladapter.AdapterSnapshot{
		Name: meshtasticAdapterName, Protocol: "meshtastic", State: string(snapshot.State),
		ConnectionState: meshtasticConnectionState(snapshot.State),
		Enabled:         snapshot.Transport != "disabled", Configured: snapshot.Transport != "disabled",
		Ready: string(snapshot.State) == "connected", Transport: strings.TrimSpace(snapshot.Transport),
		ConfiguredDevice: strings.TrimSpace(snapshot.Device), Device: strings.TrimSpace(snapshot.DetectedDevice),
		Summary: meshtasticStatusMessage(snapshot), LastError: strings.TrimSpace(snapshot.LastError), UpdatedAt: updatedAt,
	}
}

func meshtasticHealthState(snapshot meshtastic.Snapshot) string {
	if strings.EqualFold(strings.TrimSpace(snapshot.Transport), "disabled") {
		return "disabled"
	}
	return string(snapshot.State)
}

func meshtasticConnectionState(state meshtastic.ConnectionState) string {
	if state == meshtastic.StateConnected {
		return "connected"
	}
	if state == meshtastic.StateConnecting {
		return "connecting"
	}
	if state == meshtastic.StateNotPresent {
		return "disconnected"
	}
	return string(state)
}

func (a *meshtasticRadioAdapter) Close() error { return nil }
