package meshtastic

import (
	"bufio"
	"bytes"
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
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

type ConnectionState string

const (
	StateNotPresent ConnectionState = "not_present"
	StateDetected   ConnectionState = "detected"
	StateConnecting ConnectionState = "connecting"
	StateConnected  ConnectionState = "connected"
	StateDegraded   ConnectionState = "degraded"
)

type EventKind string

const (
	EventPacket EventKind = "packet"
	EventStatus EventKind = "status"
)

type Event struct {
	Kind     EventKind
	Packet   *Packet
	Node     *NodeStatus
	RawLine  string
	Received time.Time
}

type Packet struct {
	SourceNodeID      string
	DestinationNodeID string
	PortNum           int
	Payload           []byte
	ReceivedAt        time.Time
	Meta              map[string]string
}

type NodeStatus struct {
	LocalNodeID     string
	ObservedNodeIDs []string
}

type Snapshot struct {
	State           ConnectionState
	Transport       string
	Device          string
	DetectedDevice  string
	LocalNodeID     string
	ObservedNodeIDs []string
	PacketsSeen     int
	LastPacketAt    *time.Time
	Candidates      []string
	LastError       string
	UpdatedAt       time.Time
}

type Adapter interface {
	Start(ctx context.Context) (<-chan Event, error)
	Snapshot() Snapshot
}

type DetectionResult struct {
	Device     string
	Candidates []string
}

type Service struct {
	cfg config.MeshtasticConfig

	logger *slog.Logger

	mu   sync.RWMutex
	snap Snapshot

	events chan Event

	detectFn          func(config.MeshtasticConfig) (DetectionResult, error)
	openFn            func(path string) (io.ReadCloser, error)
	detectionInterval time.Duration
	reconnectDelay    time.Duration
}

func NewAdapter(cfg config.MeshtasticConfig, logger *slog.Logger) Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	transport := strings.TrimSpace(strings.ToLower(cfg.Transport))
	if transport == "" {
		transport = "serial"
	}
	cfg.Transport = transport

	now := time.Now().UTC()
	return &Service{
		cfg:    cfg,
		logger: logger.With("component", "meshtastic"),
		snap: Snapshot{
			State:     StateNotPresent,
			Transport: cfg.Transport,
			Device:    cfg.Device,
			UpdatedAt: now,
		},
		detectFn:          DetectDevice,
		openFn:            openReadCloser,
		detectionInterval: 3 * time.Second,
		reconnectDelay:    2 * time.Second,
	}
}

func (s *Service) Start(ctx context.Context) (<-chan Event, error) {
	s.mu.Lock()
	if s.events != nil {
		existing := s.events
		s.mu.Unlock()
		return existing, nil
	}
	if !isSupportedTransport(s.cfg.Transport) {
		s.mu.Unlock()
		return nil, fmt.Errorf("unsupported meshtastic transport %q", s.cfg.Transport)
	}
	s.events = make(chan Event, 64)
	events := s.events
	s.mu.Unlock()

	go s.run(ctx, events)
	return events, nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	out.ObservedNodeIDs = append([]string(nil), s.snap.ObservedNodeIDs...)
	out.Candidates = append([]string(nil), s.snap.Candidates...)
	return out
}

func (s *Service) run(ctx context.Context, out chan Event) {
	defer close(out)

	if s.cfg.Transport == "disabled" {
		s.setSnapshot(func(snap *Snapshot) {
			snap.State = StateNotPresent
			snap.LastError = "meshtastic transport disabled"
		})
		<-ctx.Done()
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		detection, err := s.detectFn(s.cfg)
		if err != nil {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDegraded
				snap.LastError = err.Error()
			})
			if !waitOrDone(ctx, s.reconnectDelay) {
				return
			}
			continue
		}

		if detection.Device == "" {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateNotPresent
				snap.Candidates = append([]string(nil), detection.Candidates...)
				snap.LastError = "no meshtastic device detected"
			})
			if !waitOrDone(ctx, s.detectionInterval) {
				return
			}
			continue
		}

		s.setSnapshot(func(snap *Snapshot) {
			snap.State = StateDetected
			snap.DetectedDevice = detection.Device
			snap.Candidates = append([]string(nil), detection.Candidates...)
			snap.LastError = ""
		})

		s.setSnapshot(func(snap *Snapshot) {
			snap.State = StateConnecting
		})
		stream, err := s.openFn(detection.Device)
		if err != nil {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDegraded
				snap.LastError = err.Error()
			})
			if !waitOrDone(ctx, s.reconnectDelay) {
				return
			}
			continue
		}

		s.setSnapshot(func(snap *Snapshot) {
			snap.State = StateConnected
			snap.DetectedDevice = detection.Device
			snap.LastError = ""
		})

		err = s.consumeStream(ctx, detection.Device, stream, out)
		_ = stream.Close()
		if ctx.Err() != nil {
			return
		}
		if err != nil && !errors.Is(err, io.EOF) {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDegraded
				snap.LastError = err.Error()
			})
		} else {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDetected
				snap.LastError = "connection closed"
			})
		}
		if !waitOrDone(ctx, s.reconnectDelay) {
			return
		}
	}
}

func (s *Service) consumeStream(ctx context.Context, device string, reader io.Reader, out chan<- Event) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		event, err := NormalizeLine(line, time.Now().UTC())
		if err != nil {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDegraded
				snap.LastError = "normalize event: " + err.Error()
			})
			continue
		}

		event.RawLine = string(line)
		if event.Received.IsZero() {
			event.Received = time.Now().UTC()
		}
		s.applyEvent(device, event)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- event:
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}
	return io.EOF
}

func (s *Service) applyEvent(device string, event Event) {
	s.setSnapshot(func(snap *Snapshot) {
		snap.State = StateConnected
		snap.DetectedDevice = device
		snap.LastError = ""

		now := event.Received
		if now.IsZero() {
			now = time.Now().UTC()
		}
		snap.UpdatedAt = now

		if event.Packet != nil {
			snap.PacketsSeen++
			packetTime := event.Packet.ReceivedAt
			if packetTime.IsZero() {
				packetTime = now
			}
			snap.LastPacketAt = &packetTime
			snap.ObservedNodeIDs = mergeNodeIDs(snap.ObservedNodeIDs, []string{event.Packet.SourceNodeID})
		}
		if event.Node != nil {
			if event.Node.LocalNodeID != "" {
				snap.LocalNodeID = event.Node.LocalNodeID
			}
			snap.ObservedNodeIDs = mergeNodeIDs(snap.ObservedNodeIDs, event.Node.ObservedNodeIDs)
		}
	})
}

func (s *Service) setSnapshot(update func(*Snapshot)) {
	s.mu.Lock()
	update(&s.snap)
	s.snap.UpdatedAt = time.Now().UTC()
	s.mu.Unlock()
}

func DetectDevice(cfg config.MeshtasticConfig) (DetectionResult, error) {
	transport := strings.ToLower(strings.TrimSpace(cfg.Transport))
	if transport == "" {
		transport = "serial"
	}

	device := strings.TrimSpace(cfg.Device)
	if device != "" {
		if fileExists(device) {
			return DetectionResult{Device: device, Candidates: []string{device}}, nil
		}
		return DetectionResult{Candidates: []string{device}}, nil
	}

	if transport == "json_stream" {
		return DetectionResult{}, nil
	}

	patterns := serialDevicePatterns(goruntime.GOOS)
	candidates := discoverCandidates(patterns)
	if len(candidates) == 0 {
		return DetectionResult{}, nil
	}
	return DetectionResult{Device: candidates[0], Candidates: candidates}, nil
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
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func serialDevicePatterns(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"/dev/cu.usbmodem*", "/dev/cu.usbserial*", "/dev/tty.usbmodem*", "/dev/tty.usbserial*"}
	case "linux":
		return []string{"/dev/serial/by-id/*", "/dev/ttyACM*", "/dev/ttyUSB*"}
	default:
		return []string{}
	}
}

func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func openReadCloser(path string) (io.ReadCloser, error) {
	return os.Open(path)
}

func waitOrDone(ctx context.Context, delay time.Duration) bool {
	if delay <= 0 {
		delay = 10 * time.Millisecond
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

func mergeNodeIDs(existing, incoming []string) []string {
	if len(incoming) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]string, 0, len(existing)+len(incoming))
	for _, nodeID := range existing {
		normalized := strings.TrimSpace(nodeID)
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	for _, nodeID := range incoming {
		normalized := strings.TrimSpace(nodeID)
		if normalized == "" {
			continue
		}
		key := strings.ToLower(normalized)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func isSupportedTransport(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "serial", "json_stream", "disabled":
		return true
	default:
		return false
	}
}
