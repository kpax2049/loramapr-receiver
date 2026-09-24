package meshcore

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestBLETransportRequiresVerifiedNUSAndMTU(t *testing.T) {
	t.Parallel()
	base := fakeBLEConnection{device: BLEDevice{Address: "AA:BB:CC:DD:EE:FF", Bonded: true, Connected: true}, mtu: MinimumBLEMTU, nus: true, write: true, notify: true}
	tests := []struct {
		name   string
		mutate func(*fakeBLEConnection)
	}{
		{"mtu below frame requirement", func(c *fakeBLEConnection) { c.mtu = MinimumBLEMTU - 1 }},
		{"missing NUS", func(c *fakeBLEConnection) { c.nus = false }},
		{"no request write", func(c *fakeBLEConnection) { c.write = false }},
		{"no notify", func(c *fakeBLEConnection) { c.notify = false }},
		{"not bonded", func(c *fakeBLEConnection) { c.device.Bonded = false }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			connection := base
			test.mutate(&connection)
			_, err := NewBLECompanionTransportWithBackend(BLEConfig{Adapter: "hci0", PeerAddress: "aa:bb:cc:dd:ee:ff"}, &fakeBLEBackend{connection: &connection}).Open(context.Background())
			if !errors.Is(err, ErrBLEConfiguration) || !connection.closed {
				t.Fatalf("Open error=%v closed=%v, want configuration failure and closed connection", err, connection.closed)
			}
		})
	}
}

func TestBLETransportUsesOneRawCompanionFramePerNotification(t *testing.T) {
	t.Parallel()
	connection := &fakeBLEConnection{device: BLEDevice{Address: "AA:BB:CC:DD:EE:FF", Bonded: true, Connected: true}, mtu: MinimumBLEMTU, nus: true, write: true, notify: true, notifications: [][]byte{{PushRawData, 1, 2}, {}, make([]byte, MaxPayloadSize+1)}}
	link, err := NewBLECompanionTransportWithBackend(BLEConfig{PeerAddress: connection.device.Address}, &fakeBLEBackend{connection: connection}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := link.ReadFrame(context.Background())
	if err != nil || !bytes.Equal(payload, []byte{PushRawData, 1, 2}) {
		t.Fatalf("payload=%x err=%v", payload, err)
	}
	if _, err := link.ReadFrame(context.Background()); !errors.Is(err, ErrBLEFrameInvalid) {
		t.Fatalf("zero notification error=%v", err)
	}
	if _, err := link.ReadFrame(context.Background()); !errors.Is(err, ErrBLEFrameInvalid) {
		t.Fatalf("oversized notification error=%v", err)
	}
	if err := link.WriteFrame(context.Background(), []byte{CommandDeviceQuery, ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	if got := connection.writes; len(got) != 1 || !bytes.Equal(got[0], []byte{CommandDeviceQuery, ProtocolVersion}) {
		t.Fatalf("writes=%x", got)
	}
	if metadata := link.Metadata(); metadata.Kind != "ble" || metadata.DelegatedAdvertAllowed {
		t.Fatalf("unexpected BLE metadata: %#v", metadata)
	}
}

func TestBLETransportReadsOptionalAuthoritativeConnectionState(t *testing.T) {
	backend := &fakeBLEBackend{connectionState: BLEDevice{Address: "AA:BB:CC:DD:EE:FF", Bonded: true, Connected: false}}
	transport := NewBLECompanionTransportWithBackend(BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}, backend)
	device, err := transport.BLEConnectionState(context.Background())
	if err != nil || device.Connected || device.Address != "AA:BB:CC:DD:EE:FF" {
		t.Fatalf("connection state=%#v err=%v", device, err)
	}
}

func TestBLETransportCancellationAndReconnectHandshake(t *testing.T) {
	t.Parallel()
	connection := &fakeBLEConnection{device: BLEDevice{Address: "AA:BB:CC:DD:EE:FF", Bonded: true, Connected: true}, mtu: MinimumBLEMTU, nus: true, write: true, notify: true}
	link, err := NewBLECompanionTransportWithBackend(BLEConfig{PeerAddress: connection.device.Address}, &fakeBLEBackend{connection: connection}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := link.ReadFrame(ctx); done <- err }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("read error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("notification wait ignored cancellation")
	}
	if err := link.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBLEPairingIsExplicitAndDoesNotExposePIN(t *testing.T) {
	t.Parallel()
	backend := &fakeBLEBackend{devices: []BLEDevice{{Address: "AA:BB:CC:DD:EE:FF", Name: "MeshCore", Bonded: false}}}
	pairing := NewBLEPairingBackendWithBackend(backend)
	devices, err := pairing.Discover(context.Background(), "hci0")
	if err != nil || len(devices) != 1 || devices[0].Name != "MeshCore" {
		t.Fatalf("discover devices=%#v err=%v", devices, err)
	}
	if err := pairing.Pair(context.Background(), BLEConfig{PeerAddress: devices[0].Address}, "123456"); err != nil {
		t.Fatal(err)
	}
	if backend.pairAddress != devices[0].Address || backend.pairPin != "123456" {
		t.Fatalf("pair did not target selected peer")
	}
	backend.pairErr = errors.New("firmware echoed 999999")
	err = pairing.Pair(context.Background(), BLEConfig{PeerAddress: devices[0].Address}, "999999")
	if !errors.Is(err, ErrBLEPairingFailed) || strings.Contains(err.Error(), "999999") {
		t.Fatalf("pairing error leaked PIN: %v", err)
	}
	if err := pairing.Forget(context.Background(), BLEConfig{PeerAddress: devices[0].Address}); err != nil {
		t.Fatal(err)
	}
	if backend.forgetAddress != devices[0].Address {
		t.Fatal("forget did not target selected peer")
	}
}

func TestResolveBLEPairTargetUsesExactExistingTargetWithoutDiscovery(t *testing.T) {
	t.Parallel()
	findCalls, discoveryCalls := 0, 0
	target, err := resolveBLEPairTarget(context.Background(), func() (string, error) {
		findCalls++
		return "AA:BB:CC:DD:EE:FF", nil
	}, func(context.Context) error {
		discoveryCalls++
		return nil
	})
	if err != nil || target != "AA:BB:CC:DD:EE:FF" || findCalls != 1 || discoveryCalls != 0 {
		t.Fatalf("target=%q err=%v find=%d discovery=%d", target, err, findCalls, discoveryCalls)
	}
}

func TestResolveBLEPairTargetDiscoversOnceAndRequiresExactTarget(t *testing.T) {
	t.Parallel()
	available := false
	findCalls, discoveryCalls := 0, 0
	target, err := resolveBLEPairTarget(context.Background(), func() (string, error) {
		findCalls++
		if !available {
			return "", errBLEPeerUnavailable
		}
		return "AA:BB:CC:DD:EE:FF", nil
	}, func(context.Context) error {
		discoveryCalls++
		available = true
		return nil
	})
	if err != nil || target != "AA:BB:CC:DD:EE:FF" || findCalls != 2 || discoveryCalls != 1 {
		t.Fatalf("target=%q err=%v find=%d discovery=%d", target, err, findCalls, discoveryCalls)
	}
}

func TestResolveBLEPairTargetReturnsUnavailableAfterBoundedDiscovery(t *testing.T) {
	t.Parallel()
	findCalls, discoveryCalls := 0, 0
	_, err := resolveBLEPairTarget(context.Background(), func() (string, error) {
		findCalls++
		return "", errBLEPeerUnavailable
	}, func(context.Context) error {
		discoveryCalls++
		return nil
	})
	if !errors.Is(err, errBLEPeerUnavailable) || findCalls != 2 || discoveryCalls != 1 {
		t.Fatalf("err=%v find=%d discovery=%d", err, findCalls, discoveryCalls)
	}
}

func TestRunBoundedBLEDiscoveryCancelsAndAlwaysStops(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		wait func(context.Context) error
	}{
		{name: "success", wait: func(context.Context) error { return nil }},
		{name: "failure", wait: func(context.Context) error { return errors.New("discovery failed") }},
		{name: "cancelled", wait: func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			if test.name == "cancelled" {
				cancel()
			} else {
				defer cancel()
			}
			stopped := false
			err := runBoundedBLEDiscovery(ctx, func(context.Context) error { return nil }, func() error { stopped = true; return nil }, test.wait)
			if !stopped {
				t.Fatal("discovery was not stopped")
			}
			if test.name == "success" && err != nil {
				t.Fatalf("success error=%v", err)
			}
			if test.name == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled error=%v", err)
			}
		})
	}
}

func TestRunBoundedBLEDiscoveryDeadlineStops(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	stopped := false
	err := runBoundedBLEDiscovery(ctx, func(context.Context) error { return nil }, func() error { stopped = true; return nil }, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.DeadlineExceeded) || !stopped {
		t.Fatalf("err=%v stopped=%v", err, stopped)
	}
}

func TestBLEPairingRejectsConcurrentRequestWithoutPassingItsPINToBlueZ(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	unblock := make(chan struct{})
	backend := &fakeBLEBackend{pairFn: func(_ context.Context, _ BLEConfig, _ string) error {
		close(started)
		<-unblock
		return nil
	}}
	pairing := NewBLEPairingBackendWithBackend(backend)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- pairing.Pair(context.Background(), BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}, "012345")
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first pairing request did not reach backend")
	}
	err := pairing.Pair(context.Background(), BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}, "654321")
	diagnostic, ok := err.(*BLEPairingDiagnostic)
	if !ok || diagnostic.Operation != "pair" || diagnostic.Code != "busy" {
		t.Fatalf("concurrent pair error=%#v; want safe pair/busy diagnostic", err)
	}
	if backend.pairPin != "012345" {
		t.Fatalf("concurrent request replaced active PIN: %q", backend.pairPin)
	}
	close(unblock)
	if err := <-firstDone; err != nil {
		t.Fatalf("first pairing request: %v", err)
	}
	if backend.pairPin != "012345" {
		t.Fatalf("first PIN was not preserved: %q", backend.pairPin)
	}
}

func TestBLERawSignedAdvertRetainsIndependentVerificationAndDelegatedAdvertFailsClosed(t *testing.T) {
	t.Parallel()
	ble := pinnedReadySnapshot()
	ble.Trust.Transport = "ble"
	ble.Trust.DelegatedAdvertAllowed = false
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	signed := normalizeFixture(t, readNormalizeHexFixture(t, "signed-log-rx-advert-v1.hex"), observedAt, ble)
	if signed["eventType"] != "device_advertisement" || mapField(t, signed, "authenticity")["method"] != "raw_ed25519" {
		t.Fatalf("BLE raw signed advert lost independent verification: %#v", signed)
	}
	delegated := normalizeFixture(t, readNormalizeHexFixture(t, "new-advert-v1.17.1.hex"), observedAt, ble)
	if delegated["eventType"] != "packet_observed" || mapField(t, delegated, "authenticity")["state"] != "unverified" {
		t.Fatalf("BLE delegated advert gained trust: %#v", delegated)
	}
	if _, ok := delegated["subject"]; ok {
		t.Fatal("BLE delegated advert gained canonical identity")
	}
}

type fakeBLEBackend struct {
	connection                          *fakeBLEConnection
	devices                             []BLEDevice
	connectionState                     BLEDevice
	connectionStateErr                  error
	pairAddress, pairPin, forgetAddress string
	disconnectAddress                   string
	pairErr                             error
	pairFn                              func(context.Context, BLEConfig, string) error
}

func (b *fakeBLEBackend) Discover(_ context.Context, _ string) ([]BLEDevice, error) {
	return append([]BLEDevice(nil), b.devices...), nil
}
func (b *fakeBLEBackend) Connect(_ context.Context, _ BLEConfig) (BLEConnection, error) {
	if b.connection == nil {
		return nil, errors.New("no connection")
	}
	return b.connection, nil
}
func (b *fakeBLEBackend) Disconnect(_ context.Context, cfg BLEConfig) error {
	b.disconnectAddress = cfg.PeerAddress
	return nil
}
func (b *fakeBLEBackend) ConnectionState(_ context.Context, _ BLEConfig) (BLEDevice, error) {
	return b.connectionState, b.connectionStateErr
}
func (b *fakeBLEBackend) Pair(ctx context.Context, cfg BLEConfig, pin string) error {
	b.pairAddress = cfg.PeerAddress
	b.pairPin = pin
	if b.pairFn != nil {
		return b.pairFn(ctx, cfg, pin)
	}
	return b.pairErr
}
func (b *fakeBLEBackend) Forget(_ context.Context, cfg BLEConfig) error {
	b.forgetAddress = cfg.PeerAddress
	return nil
}

type fakeBLEConnection struct {
	device             BLEDevice
	mtu                uint16
	nus, write, notify bool
	notifications      [][]byte
	writes             [][]byte
	closed             bool
	mu                 sync.Mutex
}

func (c *fakeBLEConnection) Device() BLEDevice     { return c.device }
func (c *fakeBLEConnection) MTU() uint16           { return c.mtu }
func (c *fakeBLEConnection) HasNUS() bool          { return c.nus }
func (c *fakeBLEConnection) CanWriteRequest() bool { return c.write }
func (c *fakeBLEConnection) CanNotify() bool       { return c.notify }
func (c *fakeBLEConnection) ReadNotification(ctx context.Context) ([]byte, error) {
	c.mu.Lock()
	if len(c.notifications) > 0 {
		v := c.notifications[0]
		c.notifications = c.notifications[1:]
		c.mu.Unlock()
		return v, nil
	}
	c.mu.Unlock()
	<-ctx.Done()
	return nil, ctx.Err()
}
func (c *fakeBLEConnection) WriteRequest(_ context.Context, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.writes = append(c.writes, append([]byte(nil), payload...))
	return nil
}
func (c *fakeBLEConnection) Close() error { c.mu.Lock(); c.closed = true; c.mu.Unlock(); return nil }
