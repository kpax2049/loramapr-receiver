package meshtastic

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestBridgeEventRecordPacket(t *testing.T) {
	t.Parallel()

	event := Event{
		Kind: EventPacket,
		Packet: &Packet{
			SourceNodeID:      "!source",
			DestinationNodeID: "!dest",
			PortNum:           1,
			Payload:           []byte("hello"),
			ReceivedAt:        time.Date(2026, 3, 21, 10, 0, 0, 0, time.UTC),
			Meta: map[string]string{
				"rssi": "-70",
			},
		},
	}

	record, ok := bridgeEventRecord(event)
	if !ok {
		t.Fatal("expected packet bridge record")
	}
	if got := record["type"]; got != "packet" {
		t.Fatalf("unexpected type: %#v", got)
	}
	if got := record["fromId"]; got != "!source" {
		t.Fatalf("unexpected fromId: %#v", got)
	}
	if got := record["payload_b64"]; got != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("unexpected payload_b64: %#v", got)
	}
	decoded, ok := record["decoded"].(map[string]any)
	if !ok {
		t.Fatalf("expected decoded map in packet bridge record, got %#v", record["decoded"])
	}
	if got := decoded["portnum"]; got != 1 {
		t.Fatalf("unexpected decoded portnum: %#v", got)
	}
	if got := record["rssi"]; got != "-70" {
		t.Fatalf("expected packet meta in bridge record, got %#v", got)
	}
}

func TestBridgeEventRecordStatus(t *testing.T) {
	t.Parallel()

	event := Event{
		Kind: EventStatus,
		Node: &NodeStatus{
			LocalNodeID:     "!home",
			ObservedNodeIDs: []string{"!node-1"},
			HomeConfig: &HomeNodeConfigSummary{
				Region:         "EU_868",
				PrimaryChannel: "Home Mesh",
				PSKState:       "present",
				ShareURL:       "https://meshtastic.org/e/#CwgB",
			},
		},
		Received: time.Date(2026, 3, 21, 10, 5, 0, 0, time.UTC),
	}

	record, ok := bridgeEventRecord(event)
	if !ok {
		t.Fatal("expected status bridge record")
	}
	if got := record["type"]; got != "status" {
		t.Fatalf("unexpected type: %#v", got)
	}
	if got := record["region"]; got != "EU_868" {
		t.Fatalf("unexpected region: %#v", got)
	}
	if got := record["channel_url"]; got != "https://meshtastic.org/e/#CwgB" {
		t.Fatalf("unexpected share url: %#v", got)
	}
}
