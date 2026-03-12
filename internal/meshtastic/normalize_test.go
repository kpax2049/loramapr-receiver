package meshtastic

import (
	"encoding/base64"
	"testing"
	"time"
)

func TestNormalizePacketEventBase64(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 10, 21, 0, 0, 0, time.UTC)
	payload := base64.StdEncoding.EncodeToString([]byte("hello"))
	line := []byte(`{
		"type":"packet",
		"from":"!home",
		"to":"!dest",
		"payload":"` + payload + `",
		"payload_encoding":"base64",
		"port":2,
		"received_at":"2026-03-10T20:59:00Z"
	}`)

	event, err := NormalizeLine(line, now)
	if err != nil {
		t.Fatalf("NormalizeLine returned error: %v", err)
	}
	if event.Kind != EventPacket || event.Packet == nil {
		t.Fatalf("expected packet event")
	}
	if string(event.Packet.Payload) != "hello" {
		t.Fatalf("unexpected payload: %q", string(event.Packet.Payload))
	}
	if event.Packet.SourceNodeID != "!home" {
		t.Fatalf("unexpected source node: %q", event.Packet.SourceNodeID)
	}
	if event.Packet.PortNum != 2 {
		t.Fatalf("unexpected port: %d", event.Packet.PortNum)
	}
}

func TestNormalizeStatusEvent(t *testing.T) {
	t.Parallel()

	line := []byte(`{
		"type":"status",
		"local_node_id":"!home",
		"observed_node_ids":["!field-1","!field-2"]
	}`)

	event, err := NormalizeLine(line, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeLine returned error: %v", err)
	}
	if event.Kind != EventStatus || event.Node == nil {
		t.Fatalf("expected status event")
	}
	if event.Node.LocalNodeID != "!home" {
		t.Fatalf("unexpected local node id: %q", event.Node.LocalNodeID)
	}
	if len(event.Node.ObservedNodeIDs) != 2 {
		t.Fatalf("unexpected observed nodes: %v", event.Node.ObservedNodeIDs)
	}
}

func TestNormalizeRejectsUnknownEvent(t *testing.T) {
	t.Parallel()

	_, err := NormalizeLine([]byte(`{"type":"unknown"}`), time.Now().UTC())
	if err == nil {
		t.Fatal("expected error for unknown event type")
	}
}

func TestNormalizePacketEventPosition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	line := []byte(`{
		"type":"packet",
		"from":"!node-a",
		"to":"!gw",
		"payload":"ping",
		"port":1,
		"position":{"lat":37.3349,"lon":-122.0090}
	}`)

	event, err := NormalizeLine(line, now)
	if err != nil {
		t.Fatalf("NormalizeLine returned error: %v", err)
	}
	if event.Packet == nil || event.Packet.Position == nil {
		t.Fatalf("expected packet position")
	}
	if event.Packet.Position.Lat != 37.3349 || event.Packet.Position.Lon != -122.0090 {
		t.Fatalf("unexpected normalized position: %#v", event.Packet.Position)
	}
}
