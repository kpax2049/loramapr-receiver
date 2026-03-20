package meshtastic

import (
	"bytes"
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
	adapter.openFn = func(_ string) (io.ReadWriteCloser, error) {
		return &testReadWriteStream{Reader: reader, Writer: io.Discard, Closer: reader}, nil
	}
	adapter.detectionInterval = 5 * time.Millisecond
	adapter.reconnectDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := adapter.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	myInfo := testBytesField(3, testVarintField(1, 0x1234ABCD))
	_, _ = writer.Write(buildNativeFrame(myInfo))

	loraConfig := append(
		testVarintField(7, 3), // EU_868
		testVarintField(2, 0)...,
	)
	configPayload := testBytesField(6, loraConfig)
	_, _ = writer.Write(buildNativeFrame(testBytesField(5, configPayload)))

	channelSettings := append(
		testBytesField(2, []byte{1, 2, 3, 4}),
		testBytesField(3, []byte("Home Mesh"))...,
	)
	channelPayload := append(
		testVarintField(1, 1),
		testBytesField(2, channelSettings)...,
	)
	_, _ = writer.Write(buildNativeFrame(testBytesField(10, channelPayload)))

	decodedData := append(
		testVarintField(1, 1),
		testBytesField(2, []byte("hello"))...,
	)
	meshPacket := append(
		testFixed32Field(1, 0x1234ABCD),
		testFixed32Field(2, 0x00000001)...,
	)
	meshPacket = append(meshPacket, testBytesField(4, decodedData)...)
	_, _ = writer.Write(buildNativeFrame(testBytesField(2, meshPacket)))
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
	if snapshot.LocalNodeID != "!1234abcd" {
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
	if snapshot.HomeConfig.PrimaryChannel != "Home Mesh" {
		t.Fatalf("unexpected primary channel summary: %q", snapshot.HomeConfig.PrimaryChannel)
	}
	if snapshot.HomeConfig.PSKState != "present" {
		t.Fatalf("unexpected psk state summary: %q", snapshot.HomeConfig.PSKState)
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

func TestServiceJSONStreamCompatibility(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	adapter := NewAdapter(config.MeshtasticConfig{Transport: "json_stream", Device: "/tmp/mesh.json"}, nil).(*Service)
	adapter.detectFn = func(_ config.MeshtasticConfig) (DetectionResult, error) {
		return DetectionResult{Device: "/tmp/mesh.json", Candidates: []string{"/tmp/mesh.json"}}, nil
	}
	adapter.openFn = func(_ string) (io.ReadWriteCloser, error) {
		return &testReadWriteStream{Reader: reader, Writer: io.Discard, Closer: reader}, nil
	}
	adapter.detectionInterval = 5 * time.Millisecond
	adapter.reconnectDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := adapter.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	_, _ = writer.Write([]byte(`{"type":"status","local_node_id":"!home","observed_node_ids":["!node-1"]}` + "\n"))
	_, _ = writer.Write([]byte(`{"type":"packet","from":"!node-1","payload":"hello","port":1}` + "\n"))
	_ = writer.Close()

	seenStatus := false
	seenPacket := false
	timeout := time.After(2 * time.Second)
	for !(seenStatus && seenPacket) {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for json_stream events")
		case event := <-events:
			switch event.Kind {
			case EventStatus:
				seenStatus = true
			case EventPacket:
				seenPacket = true
			}
		}
	}
}

func TestServiceDegradedWhenNativeSerialUnreadable(t *testing.T) {
	t.Parallel()

	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()

	adapter := NewAdapter(config.MeshtasticConfig{Transport: "serial"}, nil).(*Service)
	adapter.detectFn = func(_ config.MeshtasticConfig) (DetectionResult, error) {
		return DetectionResult{Device: "/tmp/ttyACM0", Candidates: []string{"/tmp/ttyACM0"}}, nil
	}
	adapter.openFn = func(_ string) (io.ReadWriteCloser, error) {
		return &testReadWriteStream{Reader: reader, Writer: io.Discard, Closer: reader}, nil
	}
	adapter.detectionInterval = 5 * time.Millisecond
	adapter.reconnectDelay = 5 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := adapter.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	_, _ = writer.Write(bytes.Repeat([]byte{0x1b}, nativeMaxDiscardedBytes+64))
	_ = writer.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := adapter.Snapshot()
		if snap.State == StateDegraded && strings.TrimSpace(snap.LastError) != "" {
			lowerErr := strings.ToLower(snap.LastError)
			if strings.Contains(lowerErr, "native serial") || strings.Contains(lowerErr, "closed pipe") {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	snap := adapter.Snapshot()
	t.Fatalf("expected degraded native serial state, got state=%q err=%q", snap.State, snap.LastError)
}

func TestServiceRecoversAdapterPanic(t *testing.T) {
	t.Parallel()

	adapter := NewAdapter(config.MeshtasticConfig{Transport: "serial"}, nil).(*Service)
	adapter.detectFn = func(_ config.MeshtasticConfig) (DetectionResult, error) {
		panic("boom")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events, err := adapter.Start(ctx)
	if err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	select {
	case _, ok := <-events:
		if ok {
			t.Fatal("expected event channel to close after panic recovery")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event channel to close")
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		snap := adapter.Snapshot()
		if snap.State == StateDegraded && strings.Contains(snap.LastError, "panic recovered") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	snap := adapter.Snapshot()
	t.Fatalf("expected degraded panic recovery snapshot, got state=%q err=%q", snap.State, snap.LastError)
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

type testReadWriteStream struct {
	io.Reader
	io.Writer
	io.Closer
}
