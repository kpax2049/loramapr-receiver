package meshcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
		status.ConnectedDevice = "AA:BB:CC:DD:EE:FF"
		status.Session = Snapshot{
			State:      SessionReady,
			DeviceInfo: &DeviceInfo{ProtocolVersion: ProtocolVersion, FirmwareVersion: PinnedFirmwareVersion},
			Trust:      TrustProfile{Trusted: true},
		}
	})
	ready := adapter.Snapshot()
	if !ready.Enabled || !ready.Configured || !ready.Ready || ready.ConnectionState != "connected" || ready.ConnectedDevice != "AA:BB:CC:DD:EE:FF" || ready.ProtocolVersion != "13" || ready.Profile != PinnedFirmwareVersion || ready.ProfileState != "matched" {
		t.Fatalf("unexpected ready snapshot: %#v", ready)
	}
}

func TestAdapterSnapshotDistinguishesReconnectReleaseAndErrorStates(t *testing.T) {
	t.Parallel()
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	for state, want := range map[ConnectionState]string{
		StateDegraded:           "reconnecting",
		StateConfigurationError: "error",
		StateReleased:           "released",
	} {
		adapter.setStatus(func(status *AdapterStatus) { status.State = state })
		if got := adapter.Snapshot().ConnectionState; got != want {
			t.Fatalf("state %q connectionState=%q, want %q", state, got, want)
		}
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

func TestAdapterConsumeUsesCompleteCompanionLinkFrames(t *testing.T) {
	t.Parallel()

	link := &scriptedCompanionLink{frames: [][]byte{
		readHexFixture(t, "device-info-v1.17.1.hex"),
		readHexFixture(t, "self-info-v1.17.1.hex"),
		{PushLogRXData, 25, 0xA0, 0x11, 0x22},
	}}
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: "/dev/ttyACM0"}, nil, nil)
	sink := &recordingSink{events: make(chan protocoladapter.Event, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.consume(ctx, link, "/dev/ttyACM0", sink) }()

	select {
	case event := <-sink.events:
		value, ok := event.Value.(AdapterEvent)
		if !ok || value.Frame.Opcode != PushLogRXData || !bytes.Equal(value.Frame.Payload, []byte{PushLogRXData, 25, 0xA0, 0x11, 0x22}) {
			t.Fatalf("unexpected complete-link event: %#v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for complete Companion link frame")
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("consume returned %v, want context cancellation", err)
	}

	if got, want := link.Metadata().Kind, "test"; got != want {
		t.Fatalf("link metadata kind=%q, want %q", got, want)
	}
	if len(link.writes) != 2 || !bytes.Equal(link.writes[0], []byte{CommandDeviceQuery, ProtocolVersion}) || len(link.writes[1]) < 8 || link.writes[1][0] != CommandAppStart || link.writes[1][1] != ProtocolVersion {
		t.Fatalf("unexpected complete Companion writes: %x", link.writes)
	}
}

func TestAdapterBLEUsesSharedHandshakeAndExistingLifecycle(t *testing.T) {
	t.Parallel()
	link := &scriptedCompanionLink{frames: [][]byte{
		readHexFixture(t, "device-info-v1.17.1.hex"),
		readHexFixture(t, "self-info-v1.17.1.hex"),
	}}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{Adapter: "hci0", PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	opened := make(chan BLEConfig, 1)
	adapter.newBLETransport = func(cfg BLEConfig) CompanionTransport {
		opened <- cfg
		return staticCompanionTransport{link: link}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	select {
	case cfg := <-opened:
		if cfg.Adapter != "hci0" || cfg.PeerAddress != "AA:BB:CC:DD:EE:FF" {
			t.Fatalf("unexpected BLE config: %#v", cfg)
		}
	case <-time.After(time.Second):
		t.Fatal("BLE transport was not opened")
	}
	deadline := time.Now().Add(time.Second)
	for adapter.DetailedSnapshot().State != StateConnected && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if snapshot := adapter.Snapshot(); !snapshot.Ready || snapshot.Transport != "ble" || snapshot.ProfileState != "matched" {
		t.Fatalf("unexpected BLE adapter snapshot: %#v", snapshot)
	}
	cancel()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(link.writes) != 2 || !bytes.Equal(link.writes[0], []byte{CommandDeviceQuery, ProtocolVersion}) || link.writes[1][0] != CommandAppStart {
		t.Fatalf("BLE did not use shared handshake: %x", link.writes)
	}
}

func TestAdapterPhysicalSerialHandshakeCancelsWithoutWaitingForTimeout(t *testing.T) {
	t.Parallel()
	device := existingDeviceFixture(t)
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, nil)
	opened := make(chan struct{}, 1)
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		host, radio := net.Pipe()
		opened <- struct{}{}
		go func() { <-time.After(time.Second); _ = radio.Close() }()
		return host, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("serial transport was not opened")
	}
	deadline := time.Now().Add(time.Second)
	for adapter.DetailedSnapshot().State != StateHandshaking && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if adapter.DetailedSnapshot().State != StateHandshaking {
		t.Fatalf("did not reach handshake: %#v", adapter.DetailedSnapshot())
	}
	cancel()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("shutdown error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("physical serial handshake did not cancel")
	}
}

func TestAdapterBLEReconnectRepeatsSharedHandshake(t *testing.T) {
	t.Parallel()
	first := &scriptedCompanionLink{frames: [][]byte{readHexFixture(t, "device-info-v1.17.1.hex"), readHexFixture(t, "self-info-v1.17.1.hex")}, terminal: io.EOF}
	second := &scriptedCompanionLink{frames: [][]byte{readHexFixture(t, "device-info-v1.17.1.hex"), readHexFixture(t, "self-info-v1.17.1.hex")}}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.reconnectDelay = time.Millisecond
	var opens atomic.Int32
	adapter.newBLETransport = func(BLEConfig) CompanionTransport {
		if opens.Add(1) == 1 {
			return staticCompanionTransport{link: first}
		}
		return staticCompanionTransport{link: second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	deadline := time.Now().Add(time.Second)
	for opens.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if opens.Load() < 2 {
		t.Fatalf("BLE did not reconnect: opens=%d", opens.Load())
	}
	cancel()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, link := range []*scriptedCompanionLink{first, second} {
		if len(link.writes) != 2 || !bytes.Equal(link.writes[0], []byte{CommandDeviceQuery, ProtocolVersion}) || link.writes[1][0] != CommandAppStart {
			t.Fatalf("reconnect did not repeat handshake: %x", link.writes)
		}
	}
}

func TestAdapterBLEOpenTimeoutDegradesAndRetries(t *testing.T) {
	firstOpened := make(chan struct{}, 1)
	firstCancelled := make(chan error, 1)
	second := &scriptedCompanionLink{frames: [][]byte{
		readHexFixture(t, "device-info-v1.17.1.hex"), readHexFixture(t, "self-info-v1.17.1.hex"),
	}}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.bleOpenTimeout = 10 * time.Millisecond
	adapter.reconnectDelay = 100 * time.Millisecond
	var opens atomic.Int32
	adapter.newBLETransport = func(BLEConfig) CompanionTransport {
		if opens.Add(1) == 1 {
			return blockingOpenTransport{opened: firstOpened, cancelled: firstCancelled}
		}
		return staticCompanionTransport{link: second}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	select {
	case <-firstOpened:
	case <-time.After(time.Second):
		t.Fatal("BLE open did not begin")
	}
	select {
	case err := <-firstCancelled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("BLE open cancellation=%v, want deadline exceeded", err)
		}
	case <-time.After(time.Second):
		t.Fatal("BLE open was not cancelled at its deadline")
	}
	waitForAdapterState(t, adapter, StateDegraded)
	status := adapter.DetailedSnapshot()
	if !strings.Contains(status.LastError, "BLE open timed out") || status.Reconnects != 1 {
		t.Fatalf("timeout was not actionable/retryable: %#v", status)
	}
	waitForAdapterState(t, adapter, StateConnected)
	if opens.Load() != 2 {
		t.Fatalf("BLE open retry count=%d, want 2", opens.Load())
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdapterBLEReleaseDisconnectsAndSuppressesReconnect(t *testing.T) {
	link := &countingCompanionLink{scriptedCompanionLink: scriptedCompanionLink{frames: [][]byte{
		readHexFixture(t, "device-info-v1.17.1.hex"), readHexFixture(t, "self-info-v1.17.1.hex"),
	}}}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.reconnectDelay = time.Millisecond
	var opens atomic.Int32
	adapter.newBLETransport = func(BLEConfig) CompanionTransport {
		opens.Add(1)
		return staticCompanionTransport{link: link}
	}
	var deviceDisconnects atomic.Int32
	adapter.disconnectBLE = func(context.Context, BLEConfig) error {
		deviceDisconnects.Add(1)
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- adapter.Start(ctx, &discardSink{}) }()
	waitForAdapterState(t, adapter, StateConnected)
	if err := adapter.Release(); err != nil {
		t.Fatal(err)
	}
	if status := adapter.DetailedSnapshot(); status.State != StateReleased || !status.ReconnectSuppressed || !status.ReleasedByUser || status.LastError != "" || status.Device != "" || status.ConnectedDevice != "" {
		t.Fatalf("unexpected released status: %#v", status)
	}
	if link.closes.Load() == 0 {
		t.Fatal("release did not close the active BLE link")
	}
	if deviceDisconnects.Load() == 0 {
		t.Fatal("release did not explicitly disconnect the configured BLE device")
	}
	opened := opens.Load()
	time.Sleep(20 * time.Millisecond)
	if opens.Load() != opened {
		t.Fatalf("release allowed reconnect attempts: before=%d after=%d", opened, opens.Load())
	}
	if err := adapter.Release(); err != nil {
		t.Fatalf("repeated release: %v", err)
	}
	if err := adapter.Resume(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for opens.Load() < opened+1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if opens.Load() < opened+1 {
		t.Fatal("resume did not re-enable BLE reconnect")
	}
	if err := adapter.Resume(); err != nil {
		t.Fatalf("repeated resume: %v", err)
	}
	closesBeforeShutdown := link.closes.Load()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if link.closes.Load() <= closesBeforeShutdown {
		t.Fatal("graceful shutdown did not close the active BLE link")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdapterBLEReconfigureClearAndRejectReconnectWithoutTarget(t *testing.T) {
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{Adapter: "hci0", PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	var disconnected []string
	adapter.disconnectBLE = func(_ context.Context, cfg BLEConfig) error {
		disconnected = append(disconnected, cfg.PeerAddress)
		return nil
	}
	if err := adapter.ConfigureBLE(BLEConfig{Adapter: "hci0", PeerAddress: "11:22:33:44:55:66"}); err != nil {
		t.Fatalf("configure: %v", err)
	}
	status := adapter.DetailedSnapshot()
	if status.Configured != "11:22:33:44:55:66" || status.State != StateConnecting || status.ReconnectSuppressed || status.ReleasedByUser {
		t.Fatalf("configured status=%#v", status)
	}
	if !reflect.DeepEqual(disconnected, []string{"AA:BB:CC:DD:EE:FF"}) {
		t.Fatalf("disconnected=%#v", disconnected)
	}
	if err := adapter.ReconnectBLE(); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if !reflect.DeepEqual(disconnected, []string{"AA:BB:CC:DD:EE:FF", "11:22:33:44:55:66"}) {
		t.Fatalf("reconnect disconnected=%#v", disconnected)
	}
	if err := adapter.ClearBLEConfig(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	status = adapter.DetailedSnapshot()
	if status.Configured != "" || status.State != StateNotPresent || status.ReconnectSuppressed || status.ReleasedByUser {
		t.Fatalf("cleared status=%#v", status)
	}
	if err := adapter.ReconnectBLE(); !errors.Is(err, ErrBLEConfiguration) {
		t.Fatalf("reconnect without target=%v", err)
	}
}

func TestAdapterBLEReleaseDoesNotReportIncompleteDisconnectAfterDeviceDisconnectAcknowledged(t *testing.T) {
	var logs bytes.Buffer
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), nil)
	adapter.shutdownTimeout = 20 * time.Millisecond
	adapter.setLink(&errorCloseCompanionLink{scriptedCompanionLink: scriptedCompanionLink{}, err: context.DeadlineExceeded})
	adapter.disconnectBLE = func(context.Context, BLEConfig) error { return nil }

	if err := adapter.Release(); err != nil {
		t.Fatal(err)
	}
	if status := adapter.DetailedSnapshot(); status.State != StateReleased || !status.ReconnectSuppressed || !status.ReleasedByUser {
		t.Fatalf("release state=%#v", status)
	}
	output := logs.String()
	if strings.Contains(output, "release device disconnect did not complete") || strings.Contains(output, "release disconnect did not complete") {
		t.Fatalf("successful device disconnect was reported as incomplete: %s", output)
	}
	if !strings.Contains(output, "GATT cleanup did not complete before device disconnect was acknowledged") {
		t.Fatalf("missing bounded cleanup diagnostic: %s", output)
	}
}

func TestAdapterBLEReleaseStillReportsFailedDeviceDisconnect(t *testing.T) {
	var logs bytes.Buffer
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})), nil)
	adapter.disconnectBLE = func(context.Context, BLEConfig) error { return context.DeadlineExceeded }

	if err := adapter.Release(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(logs.String(), "release device disconnect did not complete") {
		t.Fatalf("failed device disconnect was not reported: %s", logs.String())
	}
}

func TestAdapterBLEReleaseDuringOpenCancelsAndResumeReconnects(t *testing.T) {
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.bleOpenTimeout = time.Hour
	adapter.reconnectDelay = time.Millisecond
	opened := make(chan struct{}, 1)
	cancelled := make(chan error, 1)
	connected := &scriptedCompanionLink{frames: [][]byte{
		readHexFixture(t, "device-info-v1.17.1.hex"), readHexFixture(t, "self-info-v1.17.1.hex"),
	}}
	var opens atomic.Int32
	adapter.newBLETransport = func(BLEConfig) CompanionTransport {
		if opens.Add(1) == 1 {
			return blockingOpenTransport{opened: opened, cancelled: cancelled}
		}
		return staticCompanionTransport{link: connected}
	}
	deviceDisconnect := make(chan struct{}, 1)
	adapter.disconnectBLE = func(context.Context, BLEConfig) error {
		deviceDisconnect <- struct{}{}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- adapter.Start(context.Background(), &discardSink{}) }()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("BLE open did not begin")
	}
	if err := adapter.Release(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("BLE open cancellation=%v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("release did not cancel an in-flight BLE open")
	}
	select {
	case <-deviceDisconnect:
	case <-time.After(time.Second):
		t.Fatal("release did not disconnect a BLE device during an in-flight open")
	}
	if status := adapter.DetailedSnapshot(); status.State != StateReleased || !status.ReconnectSuppressed || !status.ReleasedByUser {
		t.Fatalf("release did not remain authoritative: %#v", status)
	}
	time.Sleep(20 * time.Millisecond)
	if opens.Load() != 1 {
		t.Fatalf("release allowed a reconnect attempt: opens=%d", opens.Load())
	}
	if err := adapter.Resume(); err != nil {
		t.Fatal(err)
	}
	waitForAdapterState(t, adapter, StateConnected)
	if opens.Load() != 2 {
		t.Fatalf("resume did not start another BLE open: opens=%d", opens.Load())
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdapterBLEShutdownDisconnectsInFlightOpen(t *testing.T) {
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	opened := make(chan struct{})
	adapter.newBLETransport = func(BLEConfig) CompanionTransport {
		return blockingOpenTransport{opened: opened}
	}
	deviceDisconnect := make(chan struct{}, 1)
	adapter.disconnectBLE = func(context.Context, BLEConfig) error {
		deviceDisconnect <- struct{}{}
		return nil
	}
	done := make(chan error, 1)
	go func() { done <- adapter.Start(context.Background(), &discardSink{}) }()
	select {
	case <-opened:
	case <-time.After(time.Second):
		t.Fatal("BLE open did not begin")
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-deviceDisconnect:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not disconnect a BLE device during an in-flight open")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestAdapterBLEShutdownDisconnectIsBoundedWhenCloseFails(t *testing.T) {
	link := &blockingCloseCompanionLink{scriptedCompanionLink: scriptedCompanionLink{frames: [][]byte{
		readHexFixture(t, "device-info-v1.17.1.hex"), readHexFixture(t, "self-info-v1.17.1.hex"),
	}}}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.shutdownTimeout = 10 * time.Millisecond
	adapter.newBLETransport = func(BLEConfig) CompanionTransport { return staticCompanionTransport{link: link} }
	done := make(chan error, 1)
	go func() { done <- adapter.Start(context.Background(), &discardSink{}) }()
	waitForAdapterState(t, adapter, StateConnected)
	started := time.Now()
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("shutdown was not bounded: %s", elapsed)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("adapter did not exit after bounded BLE disconnect")
	}
}

func TestAdapterBLEShutdownWhileDisconnectedIsSafe(t *testing.T) {
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.reconnectDelay = time.Millisecond
	adapter.newBLETransport = func(BLEConfig) CompanionTransport { return failingCompanionTransport{} }
	done := make(chan error, 1)
	go func() { done <- adapter.Start(context.Background(), &discardSink{}) }()
	deadline := time.Now().Add(time.Second)
	for adapter.DetailedSnapshot().State != StateDegraded && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if err := adapter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func waitForAdapterState(t *testing.T, adapter *Adapter, want ConnectionState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for adapter.DetailedSnapshot().State != want && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := adapter.DetailedSnapshot().State; got != want {
		t.Fatalf("adapter state=%q, want %q: %#v", got, want, adapter.DetailedSnapshot())
	}
}

type staticCompanionTransport struct{ link CompanionLink }

func (t staticCompanionTransport) Open(context.Context) (CompanionLink, error) { return t.link, nil }

type failingCompanionTransport struct{}

func (failingCompanionTransport) Open(context.Context) (CompanionLink, error) {
	return nil, errors.New("BlueZ unavailable")
}

type blockingOpenTransport struct {
	opened    chan<- struct{}
	cancelled chan<- error
}

func (t blockingOpenTransport) Open(ctx context.Context) (CompanionLink, error) {
	select {
	case t.opened <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	<-ctx.Done()
	err := ctx.Err()
	if t.cancelled != nil {
		select {
		case t.cancelled <- err:
		default:
		}
	}
	return nil, err
}

type discardSink struct{}

func (*discardSink) Publish(protocoladapter.Event) error    { return nil }
func (*discardSink) TryPublish(protocoladapter.Event) error { return nil }

type recordingSink struct {
	events chan protocoladapter.Event
}

func (s *recordingSink) Publish(event protocoladapter.Event) error {
	s.events <- event
	return nil
}

func (s *recordingSink) TryPublish(event protocoladapter.Event) error {
	return s.Publish(event)
}

type scriptedCompanionLink struct {
	frames   [][]byte
	reads    int
	writes   [][]byte
	terminal error
}

func (l *scriptedCompanionLink) ReadFrame(ctx context.Context) ([]byte, error) {
	if l.reads < len(l.frames) {
		frame := append([]byte(nil), l.frames[l.reads]...)
		l.reads++
		return frame, nil
	}
	if l.terminal != nil {
		return nil, l.terminal
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (l *scriptedCompanionLink) WriteFrame(_ context.Context, payload []byte) error {
	l.writes = append(l.writes, append([]byte(nil), payload...))
	return nil
}

func (*scriptedCompanionLink) Metadata() TransportMetadata {
	return TransportMetadata{Kind: "test"}
}

func (*scriptedCompanionLink) Close() error { return nil }

type countingCompanionLink struct {
	scriptedCompanionLink
	closes atomic.Int32
}

func (l *countingCompanionLink) Close() error {
	l.closes.Add(1)
	return nil
}

type blockingCloseCompanionLink struct{ scriptedCompanionLink }

func (*blockingCloseCompanionLink) Close() error {
	select {}
}

type errorCloseCompanionLink struct {
	scriptedCompanionLink
	err error
}

func (l *errorCloseCompanionLink) Close() error { return l.err }

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
