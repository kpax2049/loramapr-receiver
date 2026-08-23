package meshcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

func TestAdapterNegotiatesPhysicalSerialAndPublishesRawFrame(t *testing.T) {
	device := existingDeviceFixture(t)
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, protocoladapter.NewSerialLeaseRegistry())
	adapter.openFn = func(path string) (io.ReadWriteCloser, error) {
		if path != device {
			t.Fatalf("opened device %q", path)
		}
		host, radio := net.Pipe()
		go serveCompanion(t, radio, []byte{PushLogRXData, 25, 0xA0, 0x11, 0x22})
		return host, nil
	}
	manager, err := protocoladapter.NewManager(adapter)
	if err != nil {
		t.Fatal(err)
	}
	events, err := manager.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		value, ok := event.Value.(AdapterEvent)
		if !ok {
			t.Fatalf("unexpected event value %T", event.Value)
		}
		if event.Adapter != AdapterName || value.Frame.Opcode != PushLogRXData || !bytes.Equal(value.Frame.Payload, []byte{PushLogRXData, 25, 0xA0, 0x11, 0x22}) {
			t.Fatalf("unexpected adapter event: %#v", event)
		}
		if !value.Session.Trust.Trusted || value.Session.Trust.DeviceAttested || value.Session.Trust.AllowlistCommit != PinnedSourceCommit {
			t.Fatalf("unexpected trust profile: %#v", value.Session.Trust)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for MeshCore frame")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	status := adapter.DetailedSnapshot()
	if status.FramesSeen != 1 || status.Transport != "physical_serial" {
		t.Fatalf("unexpected adapter status: %#v", status)
	}
}

func TestAdapterReconnectInvalidatesAndReestablishesProfile(t *testing.T) {
	device := existingDeviceFixture(t)
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, protocoladapter.NewSerialLeaseRegistry())
	adapter.reconnectDelay = time.Millisecond
	var connections atomic.Int32
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		n := connections.Add(1)
		host, radio := net.Pipe()
		go serveCompanion(t, radio, []byte{PushLogRXData, byte(n), 0xA0, byte(n)})
		return host, nil
	}
	manager, err := protocoladapter.NewManager(adapter)
	if err != nil {
		t.Fatal(err)
	}
	events, err := manager.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for count := 0; count < 2; count++ {
		select {
		case event := <-events:
			value := event.Value.(AdapterEvent)
			if !value.Session.Trust.Trusted {
				t.Fatal("reconnected event used stale/untrusted profile")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("timed out waiting for reconnect event")
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	if connections.Load() < 2 || adapter.DetailedSnapshot().Reconnects < 1 {
		t.Fatalf("reconnect was not observed: connections=%d status=%#v", connections.Load(), adapter.DetailedSnapshot())
	}
}

func TestAdapterProfileMismatchStaysConnectedForRawCapture(t *testing.T) {
	device := existingDeviceFixture(t)
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, protocoladapter.NewSerialLeaseRegistry())
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		host, radio := net.Pipe()
		go serveCompatibleMismatchedCompanion(t, radio, []byte{0x99, 0x01, 0x02})
		return host, nil
	}
	manager, err := protocoladapter.NewManager(adapter)
	if err != nil {
		t.Fatal(err)
	}
	events, err := manager.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-events:
		value := event.Value.(AdapterEvent)
		if value.Frame.Opcode != 0x99 || value.Session.Trust.Trusted || !value.Session.Trust.ProtocolCompatible || value.Session.Trust.ProfileMatched {
			t.Fatalf("unexpected raw-compatible event/session: %#v", value)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mismatched-profile raw event")
	}
	if status := adapter.DetailedSnapshot(); status.State != StateConnected || status.Reconnects != 0 {
		t.Fatalf("profile mismatch forced reconnect: %#v", status)
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestAdapterSnapshotReportsProfileAndConnectionFacts(t *testing.T) {
	t.Parallel()

	adapter := NewAdapter(Config{Transport: "physical_serial", Device: "/dev/ttyACM0"}, nil, nil)
	adapter.setStatus(func(status *AdapterStatus) {
		status.State = StateHandshaking
		status.Session = NewCompanionSession("test").Snapshot()
	})
	negotiating := adapter.Snapshot()
	if negotiating.ConnectionState != "connecting" || negotiating.ProfileState != "not_established" || negotiating.Ready {
		t.Fatalf("unexpected handshaking snapshot: %#v", negotiating)
	}

	adapter.setStatus(func(status *AdapterStatus) {
		status.State = StateConnected
		status.Session = Snapshot{
			State:      SessionReady,
			DeviceInfo: &DeviceInfo{ProtocolVersion: ProtocolVersion, FirmwareVersion: PinnedFirmwareVersion},
			Trust:      TrustProfile{Trusted: true},
		}
	})
	ready := adapter.Snapshot()
	if !ready.Enabled || !ready.Configured || !ready.Ready || ready.ConnectionState != "connected" || ready.ProtocolVersion != "13" || ready.Profile != PinnedFirmwareVersion || ready.ProfileState != "matched" {
		t.Fatalf("unexpected ready snapshot: %#v", ready)
	}
}

func TestAdapterPreservesLeaseAcrossReconnectAttempts(t *testing.T) {
	device := existingDeviceFixture(t)
	leases := protocoladapter.NewSerialLeaseRegistry()
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, leases)
	adapter.reconnectDelay = time.Millisecond
	var attempts atomic.Int32
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		attempts.Add(1)
		return nil, errors.New("disconnected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	deadline := time.Now().Add(time.Second)
	for attempts.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if attempts.Load() < 2 {
		t.Fatalf("expected reconnect attempts, got %d", attempts.Load())
	}
	if owner, ok := leases.Owner(device); !ok || owner != AdapterName {
		t.Fatalf("lease was not preserved across reconnect: owner=%q ok=%v", owner, ok)
	}
	if _, err := leases.Acquire(device, "meshtastic"); err == nil {
		t.Fatal("another adapter acquired the path between reconnect attempts")
	}
	cancel()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, ok := leases.Owner(device); ok {
		t.Fatal("lease survived adapter shutdown")
	}
}

func TestAdapterReportsExplicitSerialLeaseConflictWithoutOpening(t *testing.T) {
	device := existingDeviceFixture(t)
	leases := protocoladapter.NewSerialLeaseRegistry()
	release, err := leases.Acquire(device, "meshtastic")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, leases)
	adapter.reconnectDelay = time.Millisecond
	adapter.detectionDelay = time.Millisecond
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		t.Fatal("leased device must not be opened")
		return nil, errors.New("unreachable")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	deadline := time.Now().Add(time.Second)
	for {
		status := adapter.DetailedSnapshot()
		if status.State == StateNotPresent && bytes.Contains([]byte(status.LastError), []byte("device_conflict")) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("lease conflict not surfaced: %#v", status)
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdapterDisabledDoesNotOpenSerial(t *testing.T) {
	adapter := NewAdapter(Config{Transport: "disabled"}, nil, nil)
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		t.Fatal("disabled adapter opened serial")
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	deadline := time.Now().Add(time.Second)
	for adapter.DetailedSnapshot().State != StateDisabled && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

type discardSink struct{}

func (*discardSink) Publish(protocoladapter.Event) error    { return nil }
func (*discardSink) TryPublish(protocoladapter.Event) error { return nil }

func existingDeviceFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ttyACM0")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func serveCompanion(t *testing.T, connection net.Conn, push []byte) {
	t.Helper()
	defer connection.Close()
	query, err := readHostFrame(connection)
	if err != nil {
		return
	}
	if !bytes.Equal(query, []byte{CommandDeviceQuery, ProtocolVersion}) {
		t.Errorf("unexpected DEVICE_QUERY: %x", query)
		return
	}
	if err := writeCompanionFrame(connection, readHexFixture(t, "device-info-v1.17.1.hex")); err != nil {
		return
	}
	appStart, err := readHostFrame(connection)
	if err != nil {
		return
	}
	if len(appStart) < 8 || appStart[0] != CommandAppStart || appStart[1] != ProtocolVersion {
		t.Errorf("unexpected APP_START: %x", appStart)
		return
	}
	if err := writeCompanionFrame(connection, readHexFixture(t, "self-info-v1.17.1.hex")); err != nil {
		return
	}
	_ = writeCompanionFrame(connection, push)
}

func serveCompatibleMismatchedCompanion(t *testing.T, connection net.Conn, push []byte) {
	t.Helper()
	defer connection.Close()
	query, err := readHostFrame(connection)
	if err != nil {
		return
	}
	if !bytes.Equal(query, []byte{CommandDeviceQuery, ProtocolVersion}) {
		t.Errorf("unexpected DEVICE_QUERY: %x", query)
		return
	}
	deviceInfo := readHexFixture(t, "device-info-v1.17.1.hex")
	clear(deviceInfo[8:20])
	copy(deviceInfo[8:20], []byte("13 Aug 2026"))
	if err := writeCompanionFrame(connection, deviceInfo); err != nil {
		return
	}
	appStart, err := readHostFrame(connection)
	if err != nil {
		return
	}
	if len(appStart) < 8 || appStart[0] != CommandAppStart {
		t.Errorf("profile mismatch did not continue APP_START: %x", appStart)
		return
	}
	if err := writeCompanionFrame(connection, readHexFixture(t, "self-info-v1.17.1.hex")); err != nil {
		return
	}
	if err := writeCompanionFrame(connection, push); err != nil {
		return
	}
	_, _ = io.Copy(io.Discard, connection)
}

func readHostFrame(reader io.Reader) ([]byte, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, err
	}
	if header[0] != '<' {
		return nil, errors.New("wrong host frame marker")
	}
	payload := make([]byte, int(binary.LittleEndian.Uint16(header[1:3])))
	_, err := io.ReadFull(reader, payload)
	return payload, err
}

func writeCompanionFrame(writer io.Writer, payload []byte) error {
	frame := make([]byte, 3+len(payload))
	frame[0] = '>'
	binary.LittleEndian.PutUint16(frame[1:3], uint16(len(payload)))
	copy(frame[3:], payload)
	_, err := writer.Write(frame)
	return err
}
