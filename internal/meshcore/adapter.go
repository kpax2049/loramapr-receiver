package meshcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

const AdapterName = "meshcore-companion"

type ConnectionState string

const (
	StateDisabled           ConnectionState = "disabled"
	StateNotPresent         ConnectionState = "not_present"
	StateDetected           ConnectionState = "detected"
	StateOpening            ConnectionState = "opening"
	StateHandshaking        ConnectionState = "handshaking"
	StateConnecting         ConnectionState = "connecting"
	StateConnected          ConnectionState = "connected"
	StateReleased           ConnectionState = "released"
	StateIncompatible       ConnectionState = "incompatible"
	StateConfigurationError ConnectionState = "configuration_error"
	StateDegraded           ConnectionState = "degraded"
)

type AdapterEvent struct {
	Frame      PushFrame
	Session    Snapshot
	Device     string
	ObservedAt time.Time
}

type Config struct {
	Transport string
	Device    string
	BLE       BLEConfig
}

type AdapterStatus struct {
	State               ConnectionState
	Transport           string
	Configured          string
	ConnectedDevice     string
	Device              string
	Candidates          []string
	FramesSeen          uint64
	FrameErrors         uint64
	EventDrops          uint64
	Reconnects          uint64
	LastError           string
	ReconnectSuppressed bool
	ReleasedByUser      bool
	Session             Snapshot
	UpdatedAt           time.Time
}

type detectionResult struct {
	Device     string
	Candidates []string
}

type Adapter struct {
	cfg    Config
	logger *slog.Logger
	leases *protocoladapter.SerialLeaseRegistry

	mu            sync.RWMutex
	status        AdapterStatus
	link          CompanionLink
	cancel        context.CancelFunc
	attemptCancel context.CancelFunc
	closed        bool
	started       bool
	done          chan struct{}

	telemetryMu sync.Mutex
	telemetry   *telemetryRequest
	pathReset   *pathResetRequest
	// binaryTelemetryCapability is learned from the stock command response:
	// unknown probes CMD_SEND_BINARY_REQ once, supported uses it, unsupported
	// remains on the deprecated request for this connected Companion session.
	binaryTelemetryCapability telemetryBinaryCapability
	expiredTelemetryTags      map[uint32]time.Time

	detectFn         func(Config) (detectionResult, error)
	openFn           func(string) (io.ReadWriteCloser, error)
	newBLETransport  func(BLEConfig) CompanionTransport
	disconnectBLE    func(context.Context, BLEConfig) error
	detectionDelay   time.Duration
	reconnectDelay   time.Duration
	handshakeTimeout time.Duration
	shutdownTimeout  time.Duration
	reconnectWake    chan struct{}
}

func NewAdapter(cfg Config, logger *slog.Logger, leases *protocoladapter.SerialLeaseRegistry) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transport == "" {
		transport = "disabled"
	}
	cfg.Transport = transport
	cfg.Device = strings.TrimSpace(cfg.Device)
	cfg.BLE = cfg.BLE.normalized()
	configured := cfg.Device
	if transport == "ble" {
		configured = cfg.BLE.PeerAddress
	}
	now := time.Now().UTC()
	return &Adapter{
		cfg: cfg, logger: logger.With("component", AdapterName), leases: leases,
		status: AdapterStatus{
			State: StateNotPresent, Transport: transport, Configured: configured, UpdatedAt: now,
		},
		done: make(chan struct{}), detectFn: detectDevice, openFn: openSerial,
		newBLETransport: func(config BLEConfig) CompanionTransport { return NewBLECompanionTransport(config) },
		disconnectBLE: func(ctx context.Context, config BLEConfig) error {
			return NewBLECompanionTransport(config).Disconnect(ctx)
		},
		detectionDelay: 3 * time.Second, reconnectDelay: 2 * time.Second, handshakeTimeout: 15 * time.Second,
		shutdownTimeout: 3 * time.Second, reconnectWake: make(chan struct{}, 1),
		expiredTelemetryTags: make(map[uint32]time.Time),
	}
}

func (a *Adapter) Name() string { return AdapterName }

func (a *Adapter) Start(ctx context.Context, sink protocoladapter.AdapterSink) error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("meshcore adapter is closed")
	}
	if a.started {
		a.mu.Unlock()
		return errors.New("meshcore adapter is already started")
	}
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.started = true
	a.mu.Unlock()
	defer close(a.done)
	if a.cfg.Transport == "disabled" {
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateDisabled
			status.LastError = ""
		})
		<-runCtx.Done()
		return nil
	}
	if a.cfg.Transport == "ble" {
		return a.runBLE(runCtx, sink)
	}
	if a.cfg.Transport != "physical_serial" {
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateConfigurationError
			status.LastError = fmt.Sprintf("unsupported meshcore transport %q", a.cfg.Transport)
		})
		return fmt.Errorf("unsupported meshcore transport %q", a.cfg.Transport)
	}
	return a.run(runCtx, sink)
}

func (a *Adapter) run(ctx context.Context, sink protocoladapter.AdapterSink) error {
	release, err := a.acquireLease(a.cfg.Device)
	if err != nil {
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateNotPresent
			status.LastError = fmt.Sprintf("device_conflict: %v", err)
		})
		<-ctx.Done()
		return nil
	}
	defer release()
	for ctx.Err() == nil {
		detection, err := a.detectFn(a.cfg)
		if err != nil {
			a.degrade(err)
			if !wait(ctx, a.reconnectDelay) {
				break
			}
			continue
		}
		device, candidates, conflict := a.selectDevice(detection)
		if device == "" {
			a.setStatus(func(status *AdapterStatus) {
				status.State = StateNotPresent
				status.Candidates = append([]string(nil), candidates...)
				if conflict {
					status.LastError = "device_conflict: all detected serial devices are leased"
				} else {
					status.LastError = "no MeshCore Companion serial device detected"
				}
			})
			if !wait(ctx, a.detectionDelay) {
				break
			}
			continue
		}
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateDetected
			status.Device = device
			status.ConnectedDevice = ""
			status.Candidates = append([]string(nil), candidates...)
			status.LastError = ""
		})
		a.setStatus(func(status *AdapterStatus) { status.State = StateOpening })
		transport := NewPhysicalSerialTransport(device, a.openFn)
		link, err := transport.Open(ctx)
		if err != nil {
			a.degrade(fmt.Errorf("open MeshCore serial device %s: %w", device, err))
			if !wait(ctx, a.reconnectDelay) {
				break
			}
			continue
		}
		a.setLink(link)
		a.setStatus(func(status *AdapterStatus) { status.State = StateConnecting })
		err = a.consume(ctx, link, device, sink)
		_ = link.Close()
		a.clearLink(link)
		a.setStatus(func(status *AdapterStatus) { status.ConnectedDevice = "" })
		if ctx.Err() != nil {
			break
		}
		a.setStatus(func(status *AdapterStatus) { status.Reconnects++ })
		a.degrade(err)
		if !wait(ctx, a.reconnectDelay) {
			break
		}
	}
	return nil
}

func (a *Adapter) runBLE(ctx context.Context, sink protocoladapter.AdapterSink) error {
	peer := a.cfg.BLE.PeerAddress
	if err := a.cfg.BLE.validate(); err != nil {
		a.degrade(err)
		return err
	}
	for ctx.Err() == nil {
		if !a.waitForBLEReconnectPermission(ctx) {
			break
		}
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateDetected
			status.Device = peer
			status.ConnectedDevice = ""
			status.Candidates = nil
			status.LastError = ""
		})
		a.setStatus(func(status *AdapterStatus) { status.State = StateOpening })
		attemptCtx, cancelAttempt := context.WithCancel(ctx)
		a.setAttemptCancel(cancelAttempt)
		transport := a.newBLETransport(a.cfg.BLE)
		link, err := transport.Open(attemptCtx)
		a.clearAttemptCancel(cancelAttempt)
		if err != nil {
			cancelAttempt()
			if a.reconnectIsSuppressed() {
				continue
			}
			a.degrade(err)
			if !wait(ctx, a.reconnectDelay) {
				break
			}
			continue
		}
		if a.reconnectIsSuppressed() {
			cancelAttempt()
			_ = a.closeLinkBounded(link)
			continue
		}
		a.setLink(link)
		a.setStatus(func(status *AdapterStatus) { status.State = StateConnecting })
		a.setAttemptCancel(cancelAttempt)
		err = a.consume(attemptCtx, link, peer, sink)
		a.clearAttemptCancel(cancelAttempt)
		cancelAttempt()
		_ = a.closeLinkBounded(link)
		a.clearLink(link)
		a.setStatus(func(status *AdapterStatus) { status.ConnectedDevice = "" })
		if ctx.Err() != nil {
			break
		}
		if a.reconnectIsSuppressed() {
			continue
		}
		a.setStatus(func(status *AdapterStatus) { status.Reconnects++ })
		a.degrade(err)
		if !wait(ctx, a.reconnectDelay) {
			break
		}
	}
	return nil
}

// Release disconnects a configured BLE Companion and suspends all automatic
// reconnect work. It deliberately leaves BlueZ pairing/bonding untouched.
func (a *Adapter) Release() error {
	if a == nil || a.cfg.Transport != "ble" {
		return ErrBLEUnsupported
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("meshcore adapter is closed")
	}
	a.status.ReconnectSuppressed = true
	a.status.ReleasedByUser = true
	a.status.State = StateReleased
	a.status.ConnectedDevice = ""
	a.status.Device = ""
	a.status.LastError = ""
	a.status.UpdatedAt = time.Now().UTC()
	attemptCancel := a.attemptCancel
	link := a.link
	a.mu.Unlock()
	if attemptCancel != nil {
		attemptCancel()
	}
	if link != nil {
		if err := a.closeLinkBounded(link); err != nil {
			a.logger.Warn("MeshCore BLE release disconnect did not complete", "err", err)
		}
	}
	if err := a.disconnectBLEBounded(); err != nil {
		a.logger.Warn("MeshCore BLE release device disconnect did not complete", "err", err)
	}
	return nil
}

// Resume restores normal configured BLE discovery, connection, and handshake
// behavior after a receiver-local release. Release state is never persisted.
func (a *Adapter) Resume() error {
	if a == nil || a.cfg.Transport != "ble" {
		return ErrBLEUnsupported
	}
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return errors.New("meshcore adapter is closed")
	}
	a.status.ReconnectSuppressed = false
	a.status.ReleasedByUser = false
	// Resume is an affirmative request to reconnect. Keep that authoritative
	// transitional state visible even before the worker has opened BlueZ, so a
	// portal reload cannot make an in-progress reconnect look idle.
	a.status.State = StateConnecting
	a.status.LastError = ""
	a.status.UpdatedAt = time.Now().UTC()
	a.mu.Unlock()
	select {
	case a.reconnectWake <- struct{}{}:
	default:
	}
	return nil
}

func (a *Adapter) waitForBLEReconnectPermission(ctx context.Context) bool {
	for a.reconnectIsSuppressed() {
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateReleased
			status.Device = ""
			status.ConnectedDevice = ""
			status.LastError = ""
		})
		select {
		case <-ctx.Done():
			return false
		case <-a.reconnectWake:
		}
	}
	return true
}

func (a *Adapter) reconnectIsSuppressed() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.status.ReconnectSuppressed
}

// ReconnectSuppressed reports the receiver-local release gate. It is used by
// higher-level schedulers to avoid starting telemetry polling while the radio
// has intentionally been handed back to another client.
func (a *Adapter) ReconnectSuppressed() bool {
	if a == nil {
		return false
	}
	return a.reconnectIsSuppressed()
}

func (a *Adapter) setAttemptCancel(cancel context.CancelFunc) {
	a.mu.Lock()
	a.attemptCancel = cancel
	a.mu.Unlock()
}

func (a *Adapter) clearAttemptCancel(cancel context.CancelFunc) {
	a.mu.Lock()
	if a.attemptCancel != nil {
		a.attemptCancel = nil
	}
	a.mu.Unlock()
}

func (a *Adapter) consume(ctx context.Context, link CompanionLink, device string, sink protocoladapter.AdapterSink) error {
	a.telemetryMu.Lock()
	a.binaryTelemetryCapability = telemetryBinaryUnknown
	a.expiredTelemetryTags = make(map[uint32]time.Time)
	a.telemetryMu.Unlock()
	session := NewCompanionSessionForTransport("loramapr-receiver", link.Metadata())
	a.setStatus(func(status *AdapterStatus) {
		status.State = StateHandshaking
		status.Session = session.Snapshot()
	})
	if err := link.WriteFrame(ctx, session.Begin()); err != nil {
		return err
	}
	var handshakeReady atomic.Bool
	timedOut := make(chan struct{})
	timer := time.AfterFunc(a.handshakeTimeout, func() {
		if handshakeReady.Load() {
			return
		}
		close(timedOut)
		_ = link.Close()
	})
	defer timer.Stop()
	defer session.Disconnect()
	defer a.finishCurrentTelemetry(TelemetryResult{}, ErrTelemetryAdapterDisconnected)
	defer a.finishCurrentPathReset(ErrTelemetryAdapterDisconnected)

	connected := false
	for {
		payload, err := link.ReadFrame(ctx)
		if err != nil {
			a.setStatus(func(status *AdapterStatus) { status.FrameErrors++ })
			select {
			case <-timedOut:
				return errors.New("meshcore companion handshake timed out")
			default:
			}
			return err
		}
		result, err := session.Handle(payload)
		if err != nil {
			if connected && (errors.Is(err, ErrUnsupportedPush) || errors.Is(err, ErrInvalidPush)) {
				a.setStatus(func(status *AdapterStatus) {
					status.FrameErrors++
					status.LastError = err.Error()
				})
				continue
			}
			return err
		}
		if len(result.Outbound) > 0 {
			if err := link.WriteFrame(ctx, result.Outbound); err != nil {
				return err
			}
		}
		if result.Ready && !connected {
			connected = true
			handshakeReady.Store(true)
			a.setStatus(func(status *AdapterStatus) {
				status.State = StateConnected
				status.ConnectedDevice = device
				status.Session = session.Snapshot()
				status.LastError = ""
			})
		}
		if result.Push == nil {
			if result.Response != nil {
				a.handleTelemetryCommandResponse(*result.Response)
			}
			continue
		}
		observedAt := time.Now().UTC()
		if result.Push.Opcode == PushTelemetryResponse {
			a.handleTelemetryResponse(result.Push.Payload, observedAt)
			continue
		}
		if result.Push.Opcode == PushBinaryResponse {
			a.handleBinaryTelemetryResponse(result.Push.Payload, observedAt)
			continue
		}
		event := AdapterEvent{
			Frame: *result.Push, Session: session.Snapshot(), Device: device, ObservedAt: observedAt,
		}
		err = protocoladapter.TryPublish(sink, protocoladapter.Event{
			Adapter: AdapterName, Value: event, ObservedAt: observedAt,
		})
		if err != nil {
			if errors.Is(err, protocoladapter.ErrEventBufferFull) {
				a.setStatus(func(status *AdapterStatus) {
					status.State = StateDegraded
					status.EventDrops++
					status.LastError = "adapter_event_buffer_full"
				})
				continue
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateConnected
			status.ConnectedDevice = device
			status.FramesSeen++
			status.Session = session.Snapshot()
			status.LastError = ""
		})
	}
}

func (a *Adapter) Snapshot() protocoladapter.AdapterSnapshot {
	status := a.DetailedSnapshot()
	session := status.Session
	profileState := "not_established"
	if session.State == SessionAwaitDeviceInfo || session.State == SessionAwaitSelfInfo {
		profileState = "negotiating"
	}
	if session.State == SessionReady {
		if session.Trust.Trusted {
			profileState = "matched"
		} else {
			profileState = "raw_capture_only"
		}
	}
	if session.State == SessionFailed {
		profileState = "failed"
	}
	profile := ""
	protocolVersion := ""
	if session.DeviceInfo != nil {
		protocolVersion = fmt.Sprintf("%d", session.DeviceInfo.ProtocolVersion)
		profile = strings.TrimSpace(session.DeviceInfo.FirmwareVersion)
	}
	return protocoladapter.AdapterSnapshot{
		Name: AdapterName, Protocol: "meshcore", State: string(status.State),
		ConnectionState: meshcoreConnectionState(status.State), Enabled: status.Transport != "disabled",
		Configured: (status.Transport == "physical_serial" || status.Transport == "ble") && strings.TrimSpace(status.Configured) != "",
		Ready:      status.State == StateConnected && session.State == SessionReady,
		Transport:  status.Transport, ConfiguredDevice: status.Configured, ConnectedDevice: status.ConnectedDevice, Device: status.Device,
		ReconnectSuppressed: status.ReconnectSuppressed, ReleasedByUser: status.ReleasedByUser,
		ProtocolVersion: protocolVersion, Profile: profile, ProfileState: profileState,
		Summary:   fmt.Sprintf("frames=%d frame_errors=%d event_drops=%d reconnects=%d", status.FramesSeen, status.FrameErrors, status.EventDrops, status.Reconnects),
		LastError: status.LastError, UpdatedAt: status.UpdatedAt,
	}
}

func meshcoreConnectionState(state ConnectionState) string {
	switch state {
	case StateDisabled:
		return "disabled"
	case StateOpening, StateHandshaking, StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReleased:
		return "released"
	case StateDegraded:
		return "reconnecting"
	case StateConfigurationError, StateIncompatible:
		return "error"
	default:
		return "disconnected"
	}
}

func (a *Adapter) DetailedSnapshot() AdapterStatus {
	a.mu.RLock()
	defer a.mu.RUnlock()
	status := a.status
	status.Candidates = append([]string(nil), a.status.Candidates...)
	return status
}

func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	// Set the terminal reconnect gate before cancellation or the explicit
	// device disconnect. This makes every runBLE reconnect path a no-op even
	// if BlueZ reports the disconnect before runCtx observes cancellation.
	a.status.ReconnectSuppressed = true
	a.status.UpdatedAt = time.Now().UTC()
	cancel := a.cancel
	attemptCancel := a.attemptCancel
	link := a.link
	started := a.started
	done := a.done
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if attemptCancel != nil {
		attemptCancel()
	}
	if link != nil {
		if err := a.closeLinkBounded(link); err != nil {
			a.logger.Warn("MeshCore adapter shutdown disconnect did not complete", "err", err)
		}
	}
	if a.cfg.Transport == "ble" {
		if err := a.disconnectBLEBounded(); err != nil {
			a.logger.Warn("MeshCore adapter shutdown device disconnect did not complete", "err", err)
		}
	}
	if started {
		// A link close can be in flight concurrently with the run loop's own
		// cleanup. Allow those two bounded calls to settle before giving up.
		timer := time.NewTimer(3 * a.shutdownTimeout)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
			return errors.New("meshcore adapter shutdown timed out")
		}
	}
	return nil
}

func (a *Adapter) closeLinkBounded(link CompanionLink) error {
	if link == nil {
		return nil
	}
	result := make(chan error, 1)
	go func() { result <- link.Close() }()
	timer := time.NewTimer(a.shutdownTimeout)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		return errors.New("meshcore BLE disconnect timed out")
	}
}

// disconnectBLEBounded explicitly releases the configured BlueZ device even
// when an Open attempt was cancelled before it yielded a CompanionLink.
func (a *Adapter) disconnectBLEBounded() error {
	if a == nil || a.cfg.Transport != "ble" || a.disconnectBLE == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.shutdownTimeout)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- a.disconnectBLE(ctx, a.cfg.BLE) }()
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Adapter) selectDevice(detection detectionResult) (string, []string, bool) {
	candidates := append([]string(nil), detection.Candidates...)
	if strings.TrimSpace(a.cfg.Device) != "" {
		return detection.Device, candidates, false
	}
	if a.leases == nil {
		if detection.Device != "" {
			return detection.Device, candidates, false
		}
		return "", candidates, false
	}
	unleased := a.leases.Unleased(candidates)
	if len(unleased) == 0 {
		return "", candidates, len(candidates) > 0
	}
	return unleased[0], candidates, false
}

func (a *Adapter) acquireLease(device string) (func(), error) {
	if a.leases == nil {
		return func() {}, nil
	}
	return a.leases.Acquire(device, AdapterName)
}

func (a *Adapter) setLink(link CompanionLink) {
	a.mu.Lock()
	a.link = link
	a.mu.Unlock()
}

func (a *Adapter) clearLink(link CompanionLink) {
	a.mu.Lock()
	if a.link == link {
		a.link = nil
	}
	a.mu.Unlock()
}

func (a *Adapter) setStatus(update func(*AdapterStatus)) {
	a.mu.Lock()
	update(&a.status)
	a.status.UpdatedAt = time.Now().UTC()
	a.mu.Unlock()
}

func (a *Adapter) degrade(err error) {
	if err == nil {
		err = errors.New("meshcore serial connection closed")
	}
	a.setStatus(func(status *AdapterStatus) {
		status.State = StateDegraded
		if errors.Is(err, ErrUnsupportedProtocol) {
			status.State = StateIncompatible
		}
		if strings.Contains(err.Error(), "requires an explicit device path") || errors.Is(err, ErrBLEConfiguration) || errors.Is(err, ErrBLEUnsupported) {
			status.State = StateConfigurationError
		}
		status.LastError = err.Error()
	})
}

func detectDevice(cfg Config) (detectionResult, error) {
	configured := strings.TrimSpace(cfg.Device)
	if configured == "" {
		return detectionResult{}, errors.New("meshcore physical serial requires an explicit device path")
	}
	if fileExists(configured) {
		return detectionResult{Device: configured, Candidates: []string{configured}}, nil
	}
	return detectionResult{Candidates: []string{configured}}, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func wait(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		delay = time.Millisecond
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
