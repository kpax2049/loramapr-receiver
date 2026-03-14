package meshtastic

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

func TestServiceLifecycleAndEvents(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	adapter := NewAdapter(config.MeshtasticConfig{Transport: "serial"}, nil).(*Service)
	adapter.detectFn = func(_ config.MeshtasticConfig) (DetectionResult, error) {
		return DetectionResult{Device: "/tmp/ttyUSB0", Candidates: []string{"/tmp/ttyUSB0"}}, nil
	}
	adapter.openFn = func(_ string) (io.ReadCloser, error) {
		return reader, nil
	}
	adapter.detectionInterval = 5 * time.Millisecond
	adapter.reconnectDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := adapter.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	_, _ = writer.Write([]byte(`{"type":"status","local_node_id":"!home","observed_node_ids":["!node-1"],"region":"EU_868","primary_channel":"Home Mesh","psk_present":true,"channel_url":"https://meshtastic.org/e/#CwgB"}` + "\n"))
	_, _ = writer.Write([]byte(`{"type":"packet","from":"!node-1","payload":"hello","port":1}` + "\n"))
	_ = writer.Close()

	seenStatus := false
	seenPacket := false
	deadline := time.After(2 * time.Second)
	for !(seenStatus && seenPacket) {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for events")
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event channel closed before expected events")
			}
			switch event.Kind {
			case EventStatus:
				seenStatus = true
			case EventPacket:
				seenPacket = true
			}
		}
	}

	snapshot := adapter.Snapshot()
	if snapshot.LocalNodeID != "!home" {
		t.Fatalf("expected local node id to be updated, got %q", snapshot.LocalNodeID)
	}
	if snapshot.PacketsSeen == 0 {
		t.Fatalf("expected packet count > 0")
	}
	if snapshot.HomeConfig == nil {
		t.Fatalf("expected home node config summary in snapshot")
	}
	if snapshot.HomeConfig.Region != "EU_868" {
		t.Fatalf("unexpected region summary: %q", snapshot.HomeConfig.Region)
	}
	if !snapshot.HomeConfig.ShareURLAvailable {
		t.Fatalf("expected share url availability in home config summary")
	}
	cancel()
}

func TestServiceNotPresentWhenNoDevice(t *testing.T) {
	t.Parallel()

	adapter := NewAdapter(config.MeshtasticConfig{Transport: "serial"}, nil).(*Service)
	adapter.detectFn = func(_ config.MeshtasticConfig) (DetectionResult, error) {
		return DetectionResult{Candidates: []string{"/dev/ttyUSB0"}}, nil
	}
	adapter.detectionInterval = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := adapter.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	time.Sleep(30 * time.Millisecond)
	snapshot := adapter.Snapshot()
	if snapshot.State != StateNotPresent {
		t.Fatalf("expected state %q, got %q", StateNotPresent, snapshot.State)
	}
	if len(snapshot.Candidates) == 0 {
		t.Fatalf("expected candidate list to be populated")
	}
}

func TestDetectDeviceWithConfiguredPath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "meshtastic.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	result, err := DetectDevice(config.MeshtasticConfig{Transport: "json_stream", Device: path})
	if err != nil {
		t.Fatalf("DetectDevice returned error: %v", err)
	}
	if result.Device != path {
		t.Fatalf("expected detected device %q, got %q", path, result.Device)
	}
}

func TestDetectDeviceMissingConfiguredPath(t *testing.T) {
	t.Parallel()

	result, err := DetectDevice(config.MeshtasticConfig{Transport: "json_stream", Device: "/tmp/does-not-exist"})
	if err != nil {
		t.Fatalf("DetectDevice returned error: %v", err)
	}
	if result.Device != "" {
		t.Fatalf("expected no detected device, got %q", result.Device)
	}
}

func TestMergeNodeIDsDeduplicates(t *testing.T) {
	t.Parallel()

	merged := mergeNodeIDs([]string{"!NODE-1", "!node-2"}, []string{"!node-1", "!node-3", "  "})
	if strings.Join(merged, ",") != "!NODE-1,!node-2,!node-3" {
		t.Fatalf("unexpected merged nodes: %v", merged)
	}
}
