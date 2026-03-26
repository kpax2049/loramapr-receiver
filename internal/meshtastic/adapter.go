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
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
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
	Position          *Position
	Meta              map[string]string
}

type Position struct {
	Lat float64
	Lon float64
}

type NodeStatus struct {
	LocalNodeID     string
	ObservedNodeIDs []string
	HomeConfig      *HomeNodeConfigSummary
}

type HomeNodeConfigSummary struct {
	Available         bool
	UnavailableReason string
	Region            string
	PrimaryChannel    string
	PrimaryChannelIdx int
	PSKState          string
	LoRaPreset        string
	LoRaBandwidth     string
	LoRaSpreading     string
	LoRaCodingRate    string
	ShareURL          string
	ShareURLRedacted  string
	ShareURLAvailable bool
	ShareQRText       string
	Source            string
	UpdatedAt         time.Time
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
	HomeConfig      *HomeNodeConfigSummary
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
	openFn            func(path string) (io.ReadWriteCloser, error)
	startBridgeFn     func(context.Context, config.MeshtasticConfig, string, *slog.Logger) (*bridgeCommandSession, error)
	detectionInterval time.Duration
	reconnectDelay    time.Duration
	bridgeStartupTime time.Duration
	bridgeIdleTimeout time.Duration
	bridgeIdleProbe   time.Duration
}

const nativeNoFrameReconnectDelay = 15 * time.Second
const nativeBootstrapCooldown = 5 * time.Minute
const bridgeSessionStartupTimeout = 45 * time.Second
const bridgeSessionIdleTimeout = 90 * time.Second
const bridgeSessionIdleCheckInterval = 5 * time.Second

var errBridgeStartupTimeout = errors.New("meshtastic bridge startup timeout")
var errBridgeIdleTimeout = errors.New("meshtastic bridge output idle timeout")

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
	openStream := openReadWriteCloser
	if cfg.Transport == "serial" && !cfg.BootstrapWrite {
		openStream = openReadOnlyCloser
	}
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
		openFn:            openStream,
		startBridgeFn:     startBridgeCommand,
		detectionInterval: 3 * time.Second,
		reconnectDelay:    2 * time.Second,
		bridgeStartupTime: bridgeSessionStartupTimeout,
		bridgeIdleTimeout: bridgeSessionIdleTimeout,
		bridgeIdleProbe:   bridgeSessionIdleCheckInterval,
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

	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.setSnapshot(func(snap *Snapshot) {
					snap.State = StateDegraded
					snap.LastError = fmt.Sprintf("meshtastic adapter panic recovered: %v", recovered)
				})
				s.logger.Error(
					"meshtastic adapter panic recovered",
					"panic", recovered,
					"stack", string(debug.Stack()),
				)
			}
		}()
		s.run(ctx, events)
	}()
	return events, nil
}

func (s *Service) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.snap
	out.ObservedNodeIDs = append([]string(nil), s.snap.ObservedNodeIDs...)
	out.Candidates = append([]string(nil), s.snap.Candidates...)
	out.HomeConfig = cloneHomeNodeConfig(s.snap.HomeConfig)
	return out
}

func (s *Service) run(ctx context.Context, out chan Event) {
	defer close(out)
	bootstrapLast := map[string]time.Time{}

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
		nextReconnectDelay := s.reconnectDelay
		if strings.EqualFold(s.cfg.Transport, "bridge") {
			nextReconnectDelay, err = s.consumeBridge(ctx, detection, out)
		} else {
			nextReconnectDelay, err = s.consumeDirect(ctx, detection, bootstrapLast, out)
		}
		if ctx.Err() != nil {
			return
		}

		if err != nil && !errors.Is(err, io.EOF) {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDegraded
				snap.LastError = err.Error()
			})
			if errors.Is(err, errNoNativeFrames) {
				nextReconnectDelay = maxDuration(nextReconnectDelay, nativeNoFrameReconnectDelay)
			}
		} else {
			s.setSnapshot(func(snap *Snapshot) {
				snap.State = StateDetected
				snap.LastError = "connection closed"
			})
		}
		if !waitOrDone(ctx, nextReconnectDelay) {
			return
		}
	}
}

func (s *Service) consumeDirect(ctx context.Context, detection DetectionResult, bootstrapLast map[string]time.Time, out chan<- Event) (time.Duration, error) {
	stream, err := s.openFn(detection.Device)
	if err != nil {
		return s.reconnectDelay, err
	}
	defer stream.Close()

	// Issue a throttled best-effort bootstrap request to encourage native API frames.
	// Failures are non-fatal so serial streams can still run in passive mode.
	if s.cfg.Transport == "serial" && s.cfg.BootstrapWrite && shouldBootstrapDevice(bootstrapLast, detection.Device, time.Now().UTC()) {
		if err := s.bootstrapNativeSession(stream); err != nil {
			s.logger.Warn(
				"native serial bootstrap write failed; continuing in passive mode",
				"device",
				detection.Device,
				"err",
				err,
			)
		}
	}

	s.setSnapshot(func(snap *Snapshot) {
		snap.State = StateConnected
		snap.DetectedDevice = detection.Device
		snap.LastError = ""
	})
	err = s.consumeStream(ctx, detection.Device, stream, out)
	return s.reconnectDelay, err
}

func (s *Service) consumeBridge(ctx context.Context, detection DetectionResult, out chan<- Event) (time.Duration, error) {
	startedAt := time.Now().UTC()
	session, err := s.startBridgeFn(ctx, s.cfg, detection.Device, s.logger)
	if err != nil {
		return s.reconnectDelay, err
	}
	defer session.stop()

	s.setSnapshot(func(snap *Snapshot) {
		snap.State = StateConnected
		snap.DetectedDevice = detection.Device
		snap.LastError = ""
	})

	tracker := newStreamActivityReader(session.stdout)
	consumeErrCh := make(chan error, 1)
	go func() {
		consumeErrCh <- s.consumeJSONStream(ctx, detection.Device, tracker, out)
	}()

	startupTimeout := s.bridgeStartupTime
	if startupTimeout <= 0 {
		startupTimeout = bridgeSessionStartupTimeout
	}
	idleTimeout := s.bridgeIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = bridgeSessionIdleTimeout
	}
	idleProbe := s.bridgeIdleProbe
	if idleProbe <= 0 {
		idleProbe = bridgeSessionIdleCheckInterval
	}
	idleTicker := time.NewTicker(idleProbe)
	defer idleTicker.Stop()

	var consumeErr error
bridgeConsumeLoop:
	for {
		select {
		case <-ctx.Done():
			consumeErr = ctx.Err()
			break bridgeConsumeLoop
		case consumeErr = <-consumeErrCh:
			break bridgeConsumeLoop
		case <-idleTicker.C:
			now := time.Now().UTC()
			if tracker.BytesRead() == 0 {
				if now.Sub(startedAt) >= startupTimeout {
					consumeErr = fmt.Errorf("%w: no output for %s", errBridgeStartupTimeout, startupTimeout)
					s.logger.Warn(
						"meshtastic bridge startup timeout; restarting session",
						"device",
						detection.Device,
						"timeout",
						startupTimeout,
					)
					_ = session.stop()
					break bridgeConsumeLoop
				}
				continue
			}

			lastRead := tracker.LastReadAt()
			if !lastRead.IsZero() && now.Sub(lastRead) >= idleTimeout {
				consumeErr = fmt.Errorf("%w: idle for %s", errBridgeIdleTimeout, idleTimeout)
				s.logger.Warn(
					"meshtastic bridge idle timeout; restarting session",
					"device",
					detection.Device,
					"idle_for",
					now.Sub(lastRead),
					"timeout",
					idleTimeout,
				)
				_ = session.stop()
				break bridgeConsumeLoop
			}
		}
	}

	waitErr := session.stop()

	if ctx.Err() != nil {
		return s.reconnectDelay, ctx.Err()
	}
	if consumeErr == nil || errors.Is(consumeErr, io.EOF) {
		if waitErr != nil && !errors.Is(waitErr, context.Canceled) {
			consumeErr = fmt.Errorf("meshtastic bridge command exited: %w", waitErr)
		}
	}
	if consumeErr != nil && !errors.Is(consumeErr, io.EOF) {
		if time.Since(startedAt) < 3*time.Second &&
			!errors.Is(consumeErr, errBridgeStartupTimeout) &&
			!errors.Is(consumeErr, errBridgeIdleTimeout) {
			return maxDuration(s.reconnectDelay, 8*time.Second), consumeErr
		}
		return s.reconnectDelay, consumeErr
	}
	return s.reconnectDelay, consumeErr
}

func shouldBootstrapDevice(history map[string]time.Time, device string, now time.Time) bool {
	normalized := strings.TrimSpace(device)
	if normalized == "" {
		return false
	}
	last, ok := history[normalized]
	if ok && now.Sub(last) < nativeBootstrapCooldown {
		return false
	}
	history[normalized] = now
	return true
}

func (s *Service) consumeStream(ctx context.Context, device string, stream io.ReadWriteCloser, out chan<- Event) error {
	if strings.EqualFold(s.cfg.Transport, "serial") {
		return s.consumeNativeSerial(ctx, device, stream, out)
	}
	return s.consumeJSONStream(ctx, device, stream, out)
}

func (s *Service) consumeJSONStream(ctx context.Context, device string, reader io.Reader, out chan<- Event) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	consecutiveNormalizeFailures := 0

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		event, err := NormalizeLine(line, time.Now().UTC())
		if err != nil {
			consecutiveNormalizeFailures++
			if consecutiveNormalizeFailures >= 8 {
				s.setSnapshot(func(snap *Snapshot) {
					snap.State = StateDegraded
					snap.LastError = "normalize event: " + err.Error()
				})
			} else {
				s.logger.Debug(
					"skipping bridge line that did not normalize",
					"device",
					device,
					"err",
					err,
					"consecutive_failures",
					consecutiveNormalizeFailures,
				)
			}
			continue
		}
		consecutiveNormalizeFailures = 0

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
			if event.Node.HomeConfig != nil {
				next := mergeHomeNodeConfig(snap.HomeConfig, event.Node.HomeConfig)
				if next != nil {
					next.UpdatedAt = now
				}
				snap.HomeConfig = next
			}
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

func maxDuration(a, b time.Duration) time.Duration {
	if b > a {
		return b
	}
	return a
}

type streamActivityReader struct {
	reader   io.Reader
	lastRead atomic.Int64
	bytes    atomic.Int64
}

func newStreamActivityReader(reader io.Reader) *streamActivityReader {
	tracker := &streamActivityReader{reader: reader}
	tracker.lastRead.Store(time.Now().UTC().UnixNano())
	return tracker
}

func (r *streamActivityReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.bytes.Add(int64(n))
		r.lastRead.Store(time.Now().UTC().UnixNano())
	}
	return n, err
}

func (r *streamActivityReader) LastReadAt() time.Time {
	value := r.lastRead.Load()
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(0, value).UTC()
}

func (r *streamActivityReader) BytesRead() int64 {
	return r.bytes.Load()
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
	case "serial", "bridge", "json_stream", "disabled":
		return true
	default:
		return false
	}
}

func cloneHomeNodeConfig(input *HomeNodeConfigSummary) *HomeNodeConfigSummary {
	if input == nil {
		return nil
	}
	out := *input
	out.UpdatedAt = input.UpdatedAt.UTC()
	return &out
}

func mergeHomeNodeConfig(current, update *HomeNodeConfigSummary) *HomeNodeConfigSummary {
	if update == nil {
		return cloneHomeNodeConfig(current)
	}
	if current == nil {
		return cloneHomeNodeConfig(update)
	}
	merged := cloneHomeNodeConfig(current)
	if merged == nil {
		merged = &HomeNodeConfigSummary{}
	}
	next := cloneHomeNodeConfig(update)
	if next == nil {
		return merged
	}

	merged.Available = merged.Available || next.Available
	if next.UnavailableReason != "" {
		merged.UnavailableReason = next.UnavailableReason
	}
	if next.Region != "" {
		merged.Region = next.Region
	}
	if next.PrimaryChannel != "" {
		merged.PrimaryChannel = next.PrimaryChannel
	}
	if next.PrimaryChannelIdx > 0 {
		merged.PrimaryChannelIdx = next.PrimaryChannelIdx
	}
	if next.PSKState != "" && next.PSKState != "unknown" {
		merged.PSKState = next.PSKState
	}
	if next.LoRaPreset != "" {
		merged.LoRaPreset = next.LoRaPreset
	}
	if next.LoRaBandwidth != "" {
		merged.LoRaBandwidth = next.LoRaBandwidth
	}
	if next.LoRaSpreading != "" {
		merged.LoRaSpreading = next.LoRaSpreading
	}
	if next.LoRaCodingRate != "" {
		merged.LoRaCodingRate = next.LoRaCodingRate
	}
	if next.ShareURL != "" {
		merged.ShareURL = next.ShareURL
		merged.ShareURLRedacted = next.ShareURLRedacted
		merged.ShareQRText = next.ShareQRText
		merged.ShareURLAvailable = true
	}
	if next.Source != "" {
		merged.Source = next.Source
	}
	if !next.UpdatedAt.IsZero() {
		merged.UpdatedAt = next.UpdatedAt.UTC()
	}

	return merged
}

type readOnlyStream struct {
	io.ReadCloser
}

func (s *readOnlyStream) Write(_ []byte) (int, error) {
	return 0, errors.New("stream is read-only")
}
