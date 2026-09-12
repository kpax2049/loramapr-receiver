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
	State       ConnectionState
	Transport   string
	Configured  string
	Device      string
	Candidates  []string
	FramesSeen  uint64
	FrameErrors uint64
	EventDrops  uint64
	Reconnects  uint64
	LastError   string
	Session     Snapshot
	UpdatedAt   time.Time
}

type detectionResult struct {
	Device     string
	Candidates []string
}

type Adapter struct {
	cfg    Config
	logger *slog.Logger
	leases *protocoladapter.SerialLeaseRegistry

	mu      sync.RWMutex
	status  AdapterStatus
	link    CompanionLink
	cancel  context.CancelFunc
	closed  bool
	started bool
	done    chan struct{}

	telemetryMu sync.Mutex
	telemetry   *telemetryRequest

	detectFn         func(Config) (detectionResult, error)
	openFn           func(string) (io.ReadWriteCloser, error)
	newBLETransport  func(BLEConfig) CompanionTransport
	detectionDelay   time.Duration
	reconnectDelay   time.Duration
	handshakeTimeout time.Duration
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
		detectionDelay:  3 * time.Second, reconnectDelay: 2 * time.Second, handshakeTimeout: 15 * time.Second,
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
		a.setStatus(func(status *AdapterStatus) {
			status.State = StateDetected
			status.Device = peer
			status.Candidates = nil
			status.LastError = ""
		})
		a.setStatus(func(status *AdapterStatus) { status.State = StateOpening })
		transport := a.newBLETransport(a.cfg.BLE)
		link, err := transport.Open(ctx)
		if err != nil {
			a.degrade(err)
			if !wait(ctx, a.reconnectDelay) {
				break
			}
			continue
		}
		a.setLink(link)
		a.setStatus(func(status *AdapterStatus) { status.State = StateConnecting })
		err = a.consume(ctx, link, peer, sink)
		_ = link.Close()
		a.clearLink(link)
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

func (a *Adapter) consume(ctx context.Context, link CompanionLink, device string, sink protocoladapter.AdapterSink) error {
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
		Transport:  status.Transport, ConfiguredDevice: status.Configured, Device: status.Device,
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
	cancel := a.cancel
	link := a.link
	started := a.started
	done := a.done
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if link != nil {
		_ = link.Close()
	}
	if started {
		<-done
	}
	return nil
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
