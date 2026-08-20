package meshcore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

const AdapterName = "meshcore-companion"

type ConnectionState string

const (
	StateDisabled   ConnectionState = "disabled"
	StateNotPresent ConnectionState = "not_present"
	StateDetected   ConnectionState = "detected"
	StateConnecting ConnectionState = "connecting"
	StateConnected  ConnectionState = "connected"
	StateDegraded   ConnectionState = "degraded"
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
	stream  io.ReadWriteCloser
	cancel  context.CancelFunc
	closed  bool
	started bool
	done    chan struct{}

	detectFn         func(Config) (detectionResult, error)
	openFn           func(string) (io.ReadWriteCloser, error)
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
	now := time.Now().UTC()
	return &Adapter{
		cfg: cfg, logger: logger.With("component", AdapterName), leases: leases,
		status: AdapterStatus{
			State: StateNotPresent, Transport: transport, Configured: cfg.Device, UpdatedAt: now,
		},
		done: make(chan struct{}), detectFn: detectDevice, openFn: openSerial,
		detectionDelay: 3 * time.Second, reconnectDelay: 2 * time.Second, handshakeTimeout: 15 * time.Second,
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
			status.LastError = "meshcore transport disabled"
		})
		<-runCtx.Done()
		return nil
	}
	if a.cfg.Transport != "physical_serial" {
		return fmt.Errorf("unsupported meshcore transport %q", a.cfg.Transport)
	}
	return a.run(runCtx, sink)
}

func (a *Adapter) run(ctx context.Context, sink protocoladapter.AdapterSink) error {
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
		release, err := a.acquireLease(device)
		if err != nil {
			a.setStatus(func(status *AdapterStatus) {
				status.State = StateNotPresent
				status.LastError = fmt.Sprintf("device_conflict: %v", err)
			})
			if !wait(ctx, a.reconnectDelay) {
				break
			}
			continue
		}
		stream, err := a.openFn(device)
		if err != nil {
			release()
			a.degrade(fmt.Errorf("open MeshCore serial device %s: %w", device, err))
			if !wait(ctx, a.reconnectDelay) {
				break
			}
			continue
		}
		a.setStream(stream)
		a.setStatus(func(status *AdapterStatus) { status.State = StateConnecting })
		err = a.consume(ctx, stream, device, sink)
		_ = stream.Close()
		a.clearStream(stream)
		release()
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

func (a *Adapter) consume(ctx context.Context, stream io.ReadWriteCloser, device string, sink protocoladapter.AdapterSink) error {
	session := NewCompanionSession("loramapr-receiver")
	if err := WriteFrame(stream, session.Begin()); err != nil {
		return err
	}
	var handshakeReady atomic.Bool
	timedOut := make(chan struct{})
	timer := time.AfterFunc(a.handshakeTimeout, func() {
		if handshakeReady.Load() {
			return
		}
		close(timedOut)
		_ = stream.Close()
	})
	defer timer.Stop()
	defer session.Disconnect()

	connected := false
	for {
		payload, err := ReadFrame(stream)
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
			if err := WriteFrame(stream, result.Outbound); err != nil {
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
			continue
		}
		observedAt := time.Now().UTC()
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
	return protocoladapter.AdapterSnapshot{
		Name: AdapterName, State: string(status.State), Transport: status.Transport, Device: status.Device,
		Summary:   fmt.Sprintf("frames=%d frame_errors=%d event_drops=%d reconnects=%d", status.FramesSeen, status.FrameErrors, status.EventDrops, status.Reconnects),
		LastError: status.LastError, UpdatedAt: status.UpdatedAt,
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
	stream := a.stream
	started := a.started
	done := a.done
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if stream != nil {
		_ = stream.Close()
	}
	if started {
		<-done
	}
	return nil
}

func (a *Adapter) selectDevice(detection detectionResult) (string, []string, bool) {
	candidates := append([]string(nil), detection.Candidates...)
	if strings.TrimSpace(a.cfg.Device) != "" {
		return detection.Device, candidates, detection.Device == "" && len(candidates) > 0 && a.isLeased(candidates[0])
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

func (a *Adapter) isLeased(device string) bool {
	if a.leases == nil {
		return false
	}
	_, leased := a.leases.Owner(device)
	return leased
}

func (a *Adapter) setStream(stream io.ReadWriteCloser) {
	a.mu.Lock()
	a.stream = stream
	a.mu.Unlock()
}

func (a *Adapter) clearStream(stream io.ReadWriteCloser) {
	a.mu.Lock()
	if a.stream == stream {
		a.stream = nil
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
		status.LastError = err.Error()
	})
}

func detectDevice(cfg Config) (detectionResult, error) {
	configured := strings.TrimSpace(cfg.Device)
	if configured != "" {
		if fileExists(configured) {
			return detectionResult{Device: configured, Candidates: []string{configured}}, nil
		}
		return detectionResult{Candidates: []string{configured}}, nil
	}
	candidates := discoverCandidates(serialPatterns(goruntime.GOOS))
	if len(candidates) == 0 {
		return detectionResult{}, nil
	}
	return detectionResult{Device: candidates[0], Candidates: candidates}, nil
}

func discoverCandidates(patterns []string) []string {
	set := map[string]struct{}{}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			continue
		}
		for _, match := range matches {
			if fileExists(match) {
				set[match] = struct{}{}
			}
		}
	}
	result := make([]string, 0, len(set))
	for candidate := range set {
		result = append(result, candidate)
	}
	sort.Strings(result)
	return result
}

func serialPatterns(goos string) []string {
	switch goos {
	case "linux":
		return []string{"/dev/serial/by-id/*", "/dev/ttyACM*", "/dev/ttyUSB*"}
	case "darwin":
		return []string{"/dev/cu.usbmodem*", "/dev/cu.usbserial*", "/dev/tty.usbmodem*", "/dev/tty.usbserial*"}
	default:
		return nil
	}
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
