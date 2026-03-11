package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/buildinfo"
	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/diagnostics"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/pairing"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
	"github.com/loramapr/loramapr-receiver/internal/webportal"
)

const (
	maxIngestQueueDepth = 512
	maxIngestBatchTick  = 32
)

type CloudClient interface {
	cloudclient.PairingClient
	PostIngestEvent(ctx context.Context, ingestEndpoint string, apiKey string, payload map[string]any, idempotencyKey string) error
	SendReceiverHeartbeat(
		ctx context.Context,
		heartbeatEndpoint string,
		apiKey string,
		heartbeat cloudclient.ReceiverHeartbeat,
	) (cloudclient.ReceiverHeartbeatAck, error)
}

type Service struct {
	container *Container
	mode      config.RunMode
	steady    steadyState
	build     buildinfo.Info
}

type Container struct {
	Config     config.Config
	Logger     *slog.Logger
	State      *state.Store
	Status     *status.Model
	Cloud      CloudClient
	Pairing    *pairing.Manager
	Meshtastic meshtastic.Adapter
	MeshEvents <-chan meshtastic.Event
	Portal     *webportal.Server
}

type steadyState struct {
	ingestQueue       []queuedIngestEvent
	lastHeartbeatSent *time.Time
	lastHeartbeatAck  *time.Time
	lastPacketQueued  *time.Time
	lastPacketSent    *time.Time
	lastPacketAck     *time.Time
	cloudReachable    bool
}

type queuedIngestEvent struct {
	payload        map[string]any
	idempotencyKey string
	enqueuedAt     time.Time
	nextAttemptAt  time.Time
	attempts       int
}

func New(cfg config.Config, logger *slog.Logger) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}

	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		return nil, fmt.Errorf("open state store: %w", err)
	}

	current := store.Snapshot()
	mode := resolveMode(cfg.Service.Mode, current.Pairing.Phase)
	profile := resolveRuntimeProfile(cfg.Runtime.Profile, cfg.Paths.StateFile)

	if err := store.Update(func(data *state.Data) {
		data.Installation.LastStartedAt = time.Now().UTC()
		data.Cloud.EndpointURL = cfg.Cloud.BaseURL
		data.Runtime.Profile = profile
		data.Runtime.Mode = string(mode)
	}); err != nil {
		return nil, fmt.Errorf("persist startup state: %w", err)
	}

	current = store.Snapshot()
	statusModel := status.New()
	statusModel.SetInstallationID(current.Installation.ID)
	build := buildinfo.Current()
	statusModel.SetBuildInfo(build.Version, build.Channel, build.Commit)
	statusModel.SetMode(string(mode))
	statusModel.SetRuntimeProfile(profile)
	statusModel.SetPairingPhase(string(current.Pairing.Phase))
	statusModel.SetCloud(cfg.Cloud.BaseURL, "unknown")
	statusModel.SetComponent("runtime", "starting", "initializing service container")
	statusModel.SetComponent("portal", "stopped", "portal not started yet")

	svc := &Service{}
	svc.mode = mode
	svc.build = build
	svc.steady = steadyState{
		ingestQueue: make([]queuedIngestEvent, 0, 64),
	}

	cloud := cloudclient.NewHTTPClient(cfg.Cloud.BaseURL, 10*time.Second)
	mesh := meshtastic.NewAdapter(cfg.Meshtastic, logger.With("component", "meshtastic"))
	meshSnap := mesh.Snapshot()
	statusModel.SetComponent("meshtastic", string(meshSnap.State), meshtasticStatusMessage(meshSnap))
	statusModel.SetComponent("ingest", "idle", "no queued packets")

	svc.container = &Container{
		Config:     cfg,
		Logger:     logger.With("component", "runtime"),
		State:      store,
		Status:     statusModel,
		Cloud:      cloud,
		Meshtastic: mesh,
		Pairing: pairing.NewManager(
			store,
			statusModel,
			cloud,
			logger,
			pairing.ActivationIdentity{
				RuntimeVersion: build.Version,
				Metadata: map[string]any{
					"releaseChannel": build.Channel,
					"buildCommit":    build.Commit,
				},
			},
		),
	}
	svc.container.Portal = webportal.New(cfg.Portal.BindAddress, svc, svc, logger.With("component", "webportal"))
	svc.configureInitialReadiness(current.Pairing.Phase)
	return svc, nil
}

func (s *Service) Run(ctx context.Context) error {
	c := s.container
	c.Logger.Info(
		"starting loramapr-receiverd",
		"version", s.build.Version,
		"channel", s.build.Channel,
		"commit", s.build.Commit,
		"mode", s.mode,
		"profile", c.Status.Snapshot().RuntimeProfile,
		"state_file", c.State.Path(),
		"portal_bind", c.Config.Portal.BindAddress,
	)

	c.Status.SetLifecycle(status.LifecycleRunning)
	c.Status.SetComponent("runtime", "running", "runtime loop active")
	c.Status.SetComponent("portal", "starting", "local setup portal starting")

	meshEvents, err := c.Meshtastic.Start(ctx)
	if err != nil {
		return err
	}
	c.MeshEvents = meshEvents

	portalErr := make(chan error, 1)
	go func() {
		portalErr <- c.Portal.Run(ctx)
	}()

	ticker := time.NewTicker(c.Config.Service.Heartbeat.Std())
	defer ticker.Stop()
	s.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			c.Logger.Info("shutdown requested")
			c.Status.SetLifecycle(status.LifecycleStopping)
			c.Status.SetReady(false, "shutdown requested")
			c.Status.SetComponent("runtime", "stopping", "context canceled")
			c.Status.SetLifecycle(status.LifecycleStopped)
			return nil
		case err := <-portalErr:
			if err != nil {
				c.Logger.Error("portal failed", "err", err)
				c.Status.SetLastError(err.Error())
				c.Status.SetLifecycle(status.LifecycleFailed)
				c.Status.SetComponent("portal", "error", "portal terminated unexpectedly")
				c.Status.SetReady(false, "local portal failed")
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
			return errors.New("portal exited unexpectedly")
		case event, ok := <-c.MeshEvents:
			if !ok {
				c.Logger.Warn("meshtastic event stream closed")
				c.Status.SetComponent("meshtastic", "degraded", "meshtastic stream closed")
				continue
			}
			s.onMeshtasticEvent(event)
		case <-ticker.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) CurrentStatus() status.Snapshot {
	return s.container.Status.Snapshot()
}

func (s *Service) SubmitPairingCode(ctx context.Context, code string) error {
	return s.container.Pairing.SubmitPairingCode(ctx, code)
}

func (s *Service) StatusModel() *status.Model {
	return s.container.Status
}

func (s *Service) StateStore() *state.Store {
	return s.container.State
}

func (s *Service) Mode() config.RunMode {
	return s.mode
}

func (s *Service) tick(ctx context.Context) {
	c := s.container
	if err := c.Pairing.Process(ctx); err != nil {
		c.Logger.Error("pairing lifecycle tick failed", "err", err)
		c.Status.SetLastError("pairing tick failed")
	}

	snap := c.State.Snapshot()
	meshSnap := c.Meshtastic.Snapshot()

	c.Status.SetPairingPhase(string(snap.Pairing.Phase))
	c.Status.SetCloud(c.Config.Cloud.BaseURL, pairingCloudStatus(snap.Pairing.Phase))
	c.Status.SetComponent("meshtastic", string(meshSnap.State), meshtasticStatusMessage(meshSnap))

	s.processSteadyState(ctx, snap, meshSnap)

	syncReady := snap.Pairing.Phase == state.PairingSteadyState
	switch s.mode {
	case config.ModeSetup:
		c.Status.SetReady(true, "setup portal available")
		if syncReady {
			c.Status.SetComponent("service", "ready", "receiver paired and activated")
		} else {
			c.Status.SetComponent("service", "setup", "receiver in setup mode")
		}
	case config.ModeService:
		if syncReady {
			c.Status.SetReady(true, "service mode active")
		} else {
			c.Status.SetReady(false, "service mode requires paired state")
		}
		c.Status.SetComponent("pairing", string(snap.Pairing.Phase), "pairing state from persistent store")
		c.Status.SetComponent("service", "running", "service mode loop active")
	default:
		c.Status.SetReady(false, "unknown runtime mode")
		c.Status.SetLastError("unknown runtime mode")
	}

	s.updateFailureState(snap, meshSnap)
}

func (s *Service) onMeshtasticEvent(event meshtastic.Event) {
	c := s.container
	now := time.Now().UTC()

	switch event.Kind {
	case meshtastic.EventPacket:
		if event.Packet != nil {
			payload, idempotencyKey := shapeIngestPayload(*event.Packet)
			s.enqueueIngestEvent(payload, idempotencyKey, now)
			c.Status.SetComponent(
				"meshtastic",
				"connected",
				fmt.Sprintf("packet from %s port=%d queued=%d", event.Packet.SourceNodeID, event.Packet.PortNum, len(s.steady.ingestQueue)),
			)
		}
	case meshtastic.EventStatus:
		if event.Node != nil {
			c.Status.SetComponent(
				"meshtastic",
				"connected",
				fmt.Sprintf("local node %s observed=%d", event.Node.LocalNodeID, len(event.Node.ObservedNodeIDs)),
			)
		}
	}
}

func (s *Service) enqueueIngestEvent(payload map[string]any, idempotencyKey string, now time.Time) {
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("rx-%d", now.UnixNano())
	}

	if len(s.steady.ingestQueue) >= maxIngestQueueDepth {
		s.steady.ingestQueue = s.steady.ingestQueue[1:]
		s.container.Status.SetComponent("ingest", "queue_trimmed", "oldest ingest event dropped due to queue limit")
	}

	s.steady.ingestQueue = append(s.steady.ingestQueue, queuedIngestEvent{
		payload:        payload,
		idempotencyKey: idempotencyKey,
		enqueuedAt:     now,
		nextAttemptAt:  now,
	})
	s.steady.lastPacketQueued = cloneTime(now)

	s.container.Status.SetPacketTelemetry(
		s.steady.lastPacketQueued,
		s.steady.lastPacketSent,
		s.steady.lastPacketAck,
		len(s.steady.ingestQueue),
	)
	s.container.Status.SetComponent("ingest", "queued", fmt.Sprintf("queue depth %d", len(s.steady.ingestQueue)))
}

func (s *Service) processSteadyState(ctx context.Context, snapshot state.Data, meshSnap meshtastic.Snapshot) {
	c := s.container
	if !credentialsReady(snapshot) {
		fresh := isHeartbeatFresh(s.steady.lastHeartbeatAck, c.Config.Service.Heartbeat.Std())
		c.Status.SetHeartbeat(s.steady.lastHeartbeatSent, s.steady.lastHeartbeatAck, fresh)
		c.Status.SetPacketTelemetry(
			s.steady.lastPacketQueued,
			s.steady.lastPacketSent,
			s.steady.lastPacketAck,
			len(s.steady.ingestQueue),
		)
		c.Status.SetCloudReachable(false)
		return
	}

	heartbeatErr := s.sendHeartbeat(ctx, snapshot, meshSnap)
	if heartbeatErr != nil {
		if cloudclient.IsRetryable(heartbeatErr) {
			c.Logger.Warn("heartbeat failed", "err", heartbeatErr)
		} else {
			c.Logger.Error("heartbeat failed", "err", heartbeatErr)
		}
	}
	ingestErr := s.drainIngestQueue(ctx, snapshot)
	if ingestErr != nil {
		if cloudclient.IsRetryable(ingestErr) {
			c.Logger.Warn("ingest delivery failed", "err", ingestErr)
		} else {
			c.Logger.Error("ingest delivery failed", "err", ingestErr)
		}
	}
	if heartbeatErr == nil && ingestErr == nil {
		c.Status.SetLastError("")
	}

	fresh := isHeartbeatFresh(s.steady.lastHeartbeatAck, c.Config.Service.Heartbeat.Std())
	c.Status.SetHeartbeat(s.steady.lastHeartbeatSent, s.steady.lastHeartbeatAck, fresh)
	c.Status.SetPacketTelemetry(
		s.steady.lastPacketQueued,
		s.steady.lastPacketSent,
		s.steady.lastPacketAck,
		len(s.steady.ingestQueue),
	)
	c.Status.SetCloudReachable(s.steady.cloudReachable)
	if s.steady.cloudReachable {
		c.Status.SetCloud(snapshot.Cloud.EndpointURL, "reachable")
	} else {
		c.Status.SetCloud(snapshot.Cloud.EndpointURL, "unreachable")
	}
}

func (s *Service) sendHeartbeat(ctx context.Context, snapshot state.Data, meshSnap meshtastic.Snapshot) error {
	apiKey := strings.TrimSpace(snapshot.Cloud.IngestAPIKey)
	if apiKey == "" {
		return errors.New("heartbeat skipped: ingest API key missing")
	}
	endpoint := strings.TrimSpace(snapshot.Cloud.HeartbeatEndpoint)
	if endpoint == "" {
		endpoint = "/api/receiver/heartbeat"
	}

	sentAt := time.Now().UTC()
	s.steady.lastHeartbeatSent = &sentAt

	coarseFailure := "none"
	if meshSnap.State != meshtastic.StateConnected {
		coarseFailure = "node_not_connected"
	}
	if len(s.steady.ingestQueue) > 0 {
		coarseFailure = "queue_backlog"
	}

	ack, err := s.container.Cloud.SendReceiverHeartbeat(ctx, endpoint, apiKey, cloudclient.ReceiverHeartbeat{
		RuntimeVersion:  s.build.Version,
		Platform:        goruntime.GOOS,
		Arch:            goruntime.GOARCH,
		LocalNodeID:     meshSnap.LocalNodeID,
		ObservedNodeIDs: append([]string(nil), meshSnap.ObservedNodeIDs...),
		Status: map[string]any{
			"pairingPhase":        snapshot.Pairing.Phase,
			"serviceMode":         s.mode,
			"meshtasticState":     meshSnap.State,
			"ingestQueueDepth":    len(s.steady.ingestQueue),
			"coarseFailureReason": coarseFailure,
			"releaseChannel":      s.build.Channel,
			"buildCommit":         s.build.Commit,
		},
	})
	if err != nil {
		s.steady.cloudReachable = false
		s.container.Status.SetLastError(coarseCloudError(err))
		return err
	}

	ackAt := ack.LastHeartbeatAt.UTC()
	s.steady.lastHeartbeatAck = &ackAt
	s.steady.cloudReachable = true
	return nil
}

func (s *Service) drainIngestQueue(ctx context.Context, snapshot state.Data) error {
	if len(s.steady.ingestQueue) == 0 {
		s.container.Status.SetComponent("ingest", "idle", "no queued packets")
		return nil
	}

	apiKey := strings.TrimSpace(snapshot.Cloud.IngestAPIKey)
	if apiKey == "" {
		return errors.New("ingest delivery skipped: ingest API key missing")
	}
	endpoint := strings.TrimSpace(snapshot.Cloud.IngestEndpoint)
	if endpoint == "" {
		endpoint = "/api/meshtastic/event"
	}

	for i := 0; i < maxIngestBatchTick && len(s.steady.ingestQueue) > 0; i++ {
		now := time.Now().UTC()
		item := &s.steady.ingestQueue[0]
		if now.Before(item.nextAttemptAt) {
			break
		}

		sentAt := time.Now().UTC()
		s.steady.lastPacketSent = &sentAt
		err := s.container.Cloud.PostIngestEvent(ctx, endpoint, apiKey, item.payload, item.idempotencyKey)
		if err == nil {
			ackAt := time.Now().UTC()
			s.steady.lastPacketAck = &ackAt
			s.steady.cloudReachable = true
			s.steady.ingestQueue = s.steady.ingestQueue[1:]
			s.container.Status.SetComponent("ingest", "delivered", fmt.Sprintf("queue depth %d", len(s.steady.ingestQueue)))
			continue
		}

		if cloudclient.IsRetryable(err) {
			item.attempts++
			item.nextAttemptAt = now.Add(deliveryRetryDelay(item.attempts))
			s.steady.cloudReachable = false
			s.container.Status.SetComponent("ingest", "retrying", fmt.Sprintf("retrying in %s", time.Until(item.nextAttemptAt).Round(time.Second)))
			s.container.Status.SetLastError(coarseCloudError(err))
			return err
		}

		s.container.Logger.Warn("dropping non-retryable ingest event", "err", err)
		s.steady.ingestQueue = s.steady.ingestQueue[1:]
		s.container.Status.SetComponent("ingest", "dropped", "non-retryable ingest failure")
		s.container.Status.SetLastError(coarseCloudError(err))
	}

	return nil
}

func credentialsReady(snapshot state.Data) bool {
	if snapshot.Pairing.Phase != state.PairingSteadyState {
		return false
	}
	return strings.TrimSpace(snapshot.Cloud.IngestAPIKey) != ""
}

func shapeIngestPayload(packet meshtastic.Packet) (map[string]any, string) {
	receivedAt := packet.ReceivedAt.UTC()
	if receivedAt.IsZero() {
		receivedAt = time.Now().UTC()
	}
	idempotencyKey := ingestIdempotencyKey(packet, receivedAt)
	portLabel := fmt.Sprintf("PORT_%d", packet.PortNum)

	payload := map[string]any{
		"fromId":     packet.SourceNodeID,
		"to":         packet.DestinationNodeID,
		"packetId":   idempotencyKey,
		"portnum":    portLabel,
		"receivedAt": receivedAt.Format(time.RFC3339Nano),
		"decoded": map[string]any{
			"portnum": portLabel,
		},
		"payload": map[string]any{
			"rawBase64": base64.StdEncoding.EncodeToString(packet.Payload),
			"size":      len(packet.Payload),
		},
		"_receiver": map[string]any{
			"adapter":      "meshtastic",
			"sourceNodeId": packet.SourceNodeID,
			"receivedAt":   receivedAt.Format(time.RFC3339Nano),
		},
	}
	if len(packet.Meta) > 0 {
		payload["radio"] = packet.Meta
	}
	return payload, idempotencyKey
}

func ingestIdempotencyKey(packet meshtastic.Packet, receivedAt time.Time) string {
	hasher := sha256.New()
	_, _ = hasher.Write([]byte(packet.SourceNodeID))
	_, _ = hasher.Write([]byte("|"))
	_, _ = hasher.Write([]byte(packet.DestinationNodeID))
	_, _ = hasher.Write([]byte("|"))
	_, _ = hasher.Write([]byte(strconv.Itoa(packet.PortNum)))
	_, _ = hasher.Write([]byte("|"))
	_, _ = hasher.Write([]byte(receivedAt.Format(time.RFC3339Nano)))
	_, _ = hasher.Write([]byte("|"))
	_, _ = hasher.Write(packet.Payload)
	sum := hasher.Sum(nil)
	return "rx-" + hex.EncodeToString(sum[:12])
}

func deliveryRetryDelay(attempt int) time.Duration {
	if attempt <= 0 {
		return 0
	}
	delay := time.Second << minInt(attempt+1, 7)
	if delay > 2*time.Minute {
		return 2 * time.Minute
	}
	return delay
}

func isHeartbeatFresh(lastAck *time.Time, interval time.Duration) bool {
	if lastAck == nil {
		return false
	}
	if interval <= 0 {
		interval = 30 * time.Second
	}
	maxAge := interval * 2
	if maxAge < 30*time.Second {
		maxAge = 30 * time.Second
	}
	return time.Since(lastAck.UTC()) <= maxAge
}

func cloneTime(value time.Time) *time.Time {
	v := value.UTC()
	return &v
}

func (s *Service) configureInitialReadiness(phase state.PairingPhase) {
	switch s.mode {
	case config.ModeSetup:
		s.container.Status.SetReady(true, "setup mode selected")
	case config.ModeService:
		if phase == state.PairingSteadyState {
			s.container.Status.SetReady(true, "paired state present")
			return
		}
		s.container.Status.SetReady(false, "service mode requested without steady pairing state")
	default:
		s.container.Status.SetReady(false, "runtime mode unresolved")
	}
}

func resolveMode(requested config.RunMode, pairingPhase state.PairingPhase) config.RunMode {
	switch requested {
	case config.ModeSetup:
		return config.ModeSetup
	case config.ModeService:
		return config.ModeService
	default:
		if pairingPhase == state.PairingSteadyState {
			return config.ModeService
		}
		return config.ModeSetup
	}
}

func detectRuntimeProfile(stateFile string) string {
	path := strings.ToLower(filepath.Clean(stateFile))
	switch {
	case strings.HasPrefix(path, "/var/lib/loramapr"):
		return "linux-service"
	case strings.Contains(path, "appdata"):
		return "windows-user"
	default:
		return "local-dev"
	}
}

func resolveRuntimeProfile(requestedProfile string, stateFile string) string {
	switch strings.ToLower(strings.TrimSpace(requestedProfile)) {
	case "", "auto":
		return detectRuntimeProfile(stateFile)
	case "local-dev", "linux-service", "windows-user", "appliance-pi":
		return strings.ToLower(strings.TrimSpace(requestedProfile))
	default:
		return detectRuntimeProfile(stateFile)
	}
}

func pairingCloudStatus(phase state.PairingPhase) string {
	switch phase {
	case state.PairingSteadyState, state.PairingActivated:
		return "credential_ready"
	case state.PairingBootstrapExchanged:
		return "activating"
	case state.PairingCodeEntered:
		return "pairing"
	default:
		return "unknown"
	}
}

func meshtasticStatusMessage(snapshot meshtastic.Snapshot) string {
	device := snapshot.DetectedDevice
	if device == "" {
		device = snapshot.Device
	}
	if device == "" {
		device = "none"
	}
	message := fmt.Sprintf("device=%s packets=%d observed_nodes=%d", device, snapshot.PacketsSeen, len(snapshot.ObservedNodeIDs))
	if snapshot.LocalNodeID != "" {
		message += " local_node=" + snapshot.LocalNodeID
	}
	if snapshot.LastError != "" {
		message += " error=" + snapshot.LastError
	}
	return message
}

func (s *Service) updateFailureState(snapshot state.Data, meshSnap meshtastic.Snapshot) {
	now := time.Now().UTC()
	current := s.container.Status.Snapshot()
	finding := diagnostics.Evaluate(diagnostics.Input{
		PairingPhase:      string(snapshot.Pairing.Phase),
		PairingLastChange: snapshot.Pairing.LastChange,
		PairingLastError:  snapshot.Pairing.LastError,
		RuntimeLastError:  current.LastError,
		CloudReachable:    s.steady.cloudReachable,
		MeshtasticState:   string(meshSnap.State),
		IngestQueueDepth:  len(s.steady.ingestQueue),
		LastPacketQueued:  s.steady.lastPacketQueued,
		LastPacketAck:     s.steady.lastPacketAck,
		Now:               now,
	})

	if finding.Code == diagnostics.FailureNone {
		s.container.Status.SetFailure("", "", "")
		return
	}
	s.container.Status.SetFailure(string(finding.Code), finding.Summary, finding.Hint)
}

func coarseCloudError(err error) string {
	if err == nil {
		return ""
	}
	var apiErr *cloudclient.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401, 403:
			return "cloud authentication rejected"
		case 408, 429:
			return "cloud request throttled or timed out"
		default:
			if apiErr.StatusCode >= 500 {
				return "cloud service unavailable"
			}
			return "cloud request failed"
		}
	}
	if cloudclient.IsRetryable(err) {
		return "cloud endpoint unreachable"
	}
	return "cloud request failed"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
