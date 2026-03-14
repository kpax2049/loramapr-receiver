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

func TestNormalizeStatusEventHomeConfigSummary(t *testing.T) {
	t.Parallel()

	line := []byte(`{
		"type":"status",
		"local_node_id":"!home",
		"region":"EU_868",
		"primary_channel":"Home Mesh",
		"psk_state":"present",
		"lora_preset":"LONG_FAST",
		"channel_url":"https://meshtastic.org/e/#CwgB"
	}`)

	event, err := NormalizeLine(line, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeLine returned error: %v", err)
	}
	if event.Node == nil || event.Node.HomeConfig == nil {
		t.Fatalf("expected home config summary in status event")
	}
	cfg := event.Node.HomeConfig
	if !cfg.Available {
		t.Fatal("expected home config summary available")
	}
	if cfg.Region != "EU_868" {
		t.Fatalf("unexpected region: %q", cfg.Region)
	}
	if cfg.PrimaryChannel != "Home Mesh" {
		t.Fatalf("unexpected primary channel: %q", cfg.PrimaryChannel)
	}
	if cfg.PSKState != "present" {
		t.Fatalf("unexpected psk state: %q", cfg.PSKState)
	}
	if !cfg.ShareURLAvailable {
		t.Fatal("expected share URL availability")
	}
	if cfg.ShareURL != "https://meshtastic.org/e/#CwgB" {
		t.Fatalf("unexpected share URL: %q", cfg.ShareURL)
	}
	if cfg.ShareURLRedacted != "https://meshtastic.org/e/#<redacted>" {
		t.Fatalf("unexpected redacted share URL: %q", cfg.ShareURLRedacted)
	}
}

func TestNormalizeStatusEventHomeConfigOnly(t *testing.T) {
	t.Parallel()

	line := []byte(`{
		"type":"status",
		"region":"US",
		"primary_channel":"Farm Mesh",
		"psk_present":false
	}`)

	event, err := NormalizeLine(line, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeLine returned error: %v", err)
	}
	if event.Node == nil || event.Node.HomeConfig == nil {
		t.Fatalf("expected home config summary")
	}
	if got := event.Node.HomeConfig.PSKState; got != "not_set" {
		t.Fatalf("expected psk state not_set, got %q", got)
	}
}

func TestNormalizeConfigEventAlias(t *testing.T) {
	t.Parallel()

	line := []byte(`{
		"type":"config",
		"region":"EU_868",
		"primary_channel":"Home Mesh",
		"psk_state":"present"
	}`)

	event, err := NormalizeLine(line, time.Now().UTC())
	if err != nil {
		t.Fatalf("NormalizeLine returned error: %v", err)
	}
	if event.Kind != EventStatus {
		t.Fatalf("expected status event kind from config alias, got %q", event.Kind)
	}
	if event.Node == nil || event.Node.HomeConfig == nil {
		t.Fatalf("expected home config summary from config alias event")
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
