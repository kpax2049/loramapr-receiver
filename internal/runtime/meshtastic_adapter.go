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
	return protocoladapter.AdapterSnapshot{
		Name:      meshtasticAdapterName,
		State:     string(snapshot.State),
		Transport: strings.TrimSpace(snapshot.Transport),
		Device:    strings.TrimSpace(snapshot.DetectedDevice),
		Summary:   meshtasticStatusMessage(snapshot),
		LastError: strings.TrimSpace(snapshot.LastError),
		UpdatedAt: updatedAt,
	}
}

func (a *meshtasticRadioAdapter) Close() error { return nil }
