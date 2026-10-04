package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

type fakeMeshtasticAdapter struct {
	events   chan meshtastic.Event
	snapshot meshtastic.Snapshot
}

func (a *fakeMeshtasticAdapter) Start(context.Context) (<-chan meshtastic.Event, error) {
	return a.events, nil
}

func (a *fakeMeshtasticAdapter) Snapshot() meshtastic.Snapshot { return a.snapshot }

func TestMeshtasticRadioAdapterPreservesLegacyEvent(t *testing.T) {
	legacy := &fakeMeshtasticAdapter{
		events: make(chan meshtastic.Event, 1),
		snapshot: meshtastic.Snapshot{
			State:     meshtastic.StateConnected,
			Transport: "serial",
			UpdatedAt: time.Now().UTC(),
		},
	}
	manager, err := protocoladapter.NewManager(newMeshtasticRadioAdapter(legacy))
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	events, err := manager.Start(context.Background())
	if err != nil {
		t.Fatalf("start manager: %v", err)
	}
	want := meshtastic.Event{
		Kind:     meshtastic.EventPacket,
		Packet:   &meshtastic.Packet{SourceNodeID: "!01020304", PortNum: 3},
		Received: time.Now().UTC(),
	}
	legacy.events <- want
	select {
	case got := <-events:
		if got.Adapter != meshtasticAdapterName {
			t.Fatalf("unexpected adapter name: %q", got.Adapter)
		}
		legacyEvent, ok := got.Value.(meshtastic.Event)
		if !ok || legacyEvent.Packet == nil || legacyEvent.Packet.SourceNodeID != want.Packet.SourceNodeID {
			t.Fatalf("legacy event was not preserved: %#v", got.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for wrapped Meshtastic event")
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("close manager: %v", err)
	}
}
