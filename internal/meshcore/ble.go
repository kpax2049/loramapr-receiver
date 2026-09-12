package meshcore

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

const (
	NUSServiceUUID = "6e400001-b5a3-f393-e0a9-e50e24dcca9e"
	NUSRXUUID      = "6e400002-b5a3-f393-e0a9-e50e24dcca9e" // App -> Companion
	NUSTXUUID      = "6e400003-b5a3-f393-e0a9-e50e24dcca9e" // Companion -> App
	MinimumBLEMTU  = 179
)

var (
	ErrBLEUnsupported   = errors.New("meshcore BLE transport is unsupported on this platform")
	ErrBLEConfiguration = errors.New("meshcore BLE transport configuration error")
	ErrBLEFrameInvalid  = errors.New("invalid meshcore BLE Companion frame")
	ErrBLEPairingFailed = errors.New("meshcore BLE pairing failed")
)

// BLEConfig is deliberately transport-local. PeerAddress is a BlueZ locator,
// never an Ed25519 MeshCore identity.
type BLEConfig struct {
	Adapter     string
	PeerAddress string
}

func (c BLEConfig) normalized() BLEConfig {
	c.Adapter = strings.TrimSpace(c.Adapter)
	if c.Adapter == "" {
		c.Adapter = "hci0"
	}
	c.PeerAddress = strings.ToUpper(strings.TrimSpace(c.PeerAddress))
	return c
}

func (c BLEConfig) validate() error {
	c = c.normalized()
	if c.PeerAddress == "" {
		return fmt.Errorf("%w: selected BLE peer is required", ErrBLEConfiguration)
	}
	return nil
}

// BLEDevice is the safe local discovery projection. Address is a local
// transport selector only; callers must never treat display name or address as
// MeshCore identity.
type BLEDevice struct {
	Address   string `json:"address"`
	Name      string `json:"name,omitempty"`
	Bonded    bool   `json:"bonded"`
	Connected bool   `json:"connected"`
}

// BLEBackend isolates BlueZ/D-Bus from transport policy. Its production
// implementation is Linux-only; fakes make all transport behavior testable
// without a radio or system bus.
type BLEBackend interface {
	Discover(context.Context, string) ([]BLEDevice, error)
	Connect(context.Context, BLEConfig) (BLEConnection, error)
	Pair(context.Context, BLEConfig, string) error
	Forget(context.Context, BLEConfig) error
}

// BLEConnection is a connected, bonded NUS view. Values are raw Companion
// frames; this boundary intentionally has no serial marker or length framing.
type BLEConnection interface {
	Device() BLEDevice
	MTU() uint16
	HasNUS() bool
	CanWriteRequest() bool
	CanNotify() bool
	ReadNotification(context.Context) ([]byte, error)
	WriteRequest(context.Context, []byte) error
	Close() error
}

type BLECompanionTransport struct {
	cfg     BLEConfig
	backend BLEBackend
}

func NewBLECompanionTransport(cfg BLEConfig) *BLECompanionTransport {
	return NewBLECompanionTransportWithBackend(cfg, newSystemBLEBackend())
}

func NewBLECompanionTransportWithBackend(cfg BLEConfig, backend BLEBackend) *BLECompanionTransport {
	return &BLECompanionTransport{cfg: cfg.normalized(), backend: backend}
}

func (t *BLECompanionTransport) Open(ctx context.Context) (CompanionLink, error) {
	if err := t.cfg.validate(); err != nil {
		return nil, err
	}
	if t.backend == nil {
		return nil, ErrBLEUnsupported
	}
	connection, err := t.backend.Connect(ctx, t.cfg)
	if err != nil {
		return nil, err
	}
	if err := validateBLEConnection(t.cfg, connection); err != nil {
		_ = connection.Close()
		return nil, err
	}
	return &bleCompanionLink{connection: connection, peer: t.cfg.PeerAddress}, nil
}

func validateBLEConnection(cfg BLEConfig, connection BLEConnection) error {
	if connection == nil {
		return fmt.Errorf("%w: BlueZ returned no BLE connection", ErrBLEConfiguration)
	}
	device := connection.Device()
	if !strings.EqualFold(strings.TrimSpace(device.Address), cfg.PeerAddress) {
		return fmt.Errorf("%w: connected peer does not match selected BLE address", ErrBLEConfiguration)
	}
	if !device.Connected || !device.Bonded {
		return fmt.Errorf("%w: selected BLE peer must be connected and bonded", ErrBLEConfiguration)
	}
	if !connection.HasNUS() || !connection.CanWriteRequest() || !connection.CanNotify() {
		return fmt.Errorf("%w: selected BLE peer does not expose required encrypted MeshCore NUS characteristics", ErrBLEConfiguration)
	}
	if connection.MTU() < MinimumBLEMTU {
		return fmt.Errorf("%w: negotiated ATT MTU %d is below required %d", ErrBLEConfiguration, connection.MTU(), MinimumBLEMTU)
	}
	return nil
}

type bleCompanionLink struct {
	connection BLEConnection
	peer       string
}

func (l *bleCompanionLink) ReadFrame(ctx context.Context) ([]byte, error) {
	payload, err := l.connection.ReadNotification(ctx)
	if err != nil {
		return nil, err
	}
	if len(payload) == 0 || len(payload) > MaxPayloadSize {
		return nil, fmt.Errorf("%w: notification is %d bytes", ErrBLEFrameInvalid, len(payload))
	}
	return append([]byte(nil), payload...), nil
}

func (l *bleCompanionLink) WriteFrame(ctx context.Context, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxPayloadSize {
		return fmt.Errorf("%w: write is %d bytes", ErrBLEFrameInvalid, len(payload))
	}
	return l.connection.WriteRequest(ctx, append([]byte(nil), payload...))
}

func (l *bleCompanionLink) Metadata() TransportMetadata {
	return TransportMetadata{Kind: "ble", DelegatedAdvertAllowed: false, PeerSelector: l.peer}
}

func (l *bleCompanionLink) Close() error { return l.connection.Close() }

// BLEPairingBackend exposes explicit local pairing actions for a later portal
// UI. The six-digit PIN is passed only to the current call and is neither
// stored here nor included in returned errors.
type BLEPairingBackend struct{ backend BLEBackend }

func NewBLEPairingBackend() *BLEPairingBackend {
	return &BLEPairingBackend{backend: newSystemBLEBackend()}
}

func NewBLEPairingBackendWithBackend(backend BLEBackend) *BLEPairingBackend {
	return &BLEPairingBackend{backend: backend}
}

func (p *BLEPairingBackend) Discover(ctx context.Context, adapter string) ([]BLEDevice, error) {
	if p == nil || p.backend == nil {
		return nil, ErrBLEUnsupported
	}
	return p.backend.Discover(ctx, BLEConfig{Adapter: adapter}.normalized().Adapter)
}

func (p *BLEPairingBackend) Pair(ctx context.Context, cfg BLEConfig, pin string) error {
	if p == nil || p.backend == nil {
		return ErrBLEUnsupported
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	if len(pin) != 6 {
		return fmt.Errorf("%w: PIN must contain exactly six digits", ErrBLEPairingFailed)
	}
	for _, digit := range pin {
		if digit < '0' || digit > '9' {
			return fmt.Errorf("%w: PIN must contain exactly six digits", ErrBLEPairingFailed)
		}
	}
	if err := p.backend.Pair(ctx, cfg, pin); err != nil {
		// Do not surface D-Bus errors because an agent or daemon might echo
		// user-provided material. The PIN is intentionally not retained.
		return ErrBLEPairingFailed
	}
	return nil
}

func (p *BLEPairingBackend) Forget(ctx context.Context, cfg BLEConfig) error {
	if p == nil || p.backend == nil {
		return ErrBLEUnsupported
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	return p.backend.Forget(ctx, cfg)
}
