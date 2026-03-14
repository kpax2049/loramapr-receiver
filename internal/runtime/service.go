package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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
	"github.com/loramapr/loramapr-receiver/internal/homeautosession"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/pairing"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
	"github.com/loramapr/loramapr-receiver/internal/update"
	"github.com/loramapr/loramapr-receiver/internal/webportal"
)

const (
	maxIngestQueueDepth = 512
	maxIngestBatchTick  = 32
)

var errLifecycleTransition = errors.New("receiver lifecycle transition")

const (
	homeAutoConfigApplyCloud             = "cloud_config_applied"
	homeAutoConfigApplyCloudDisabled     = "cloud_config_disabled_feature"
	homeAutoConfigApplyCloudInvalid      = "cloud_config_invalid_local_fallback"
	homeAutoConfigApplyCloudMissing      = "cloud_config_missing_local_fallback"
	homeAutoConfigApplyFetchFailed       = "cloud_config_fetch_failed_using_last_effective"
	homeAutoConfigApplyLocalStartup      = "startup_local_fallback"
	homeAutoConfigApplyLocalManualUpdate = "local_fallback_updated"
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
	StartHomeAutoSession(
		ctx context.Context,
		startEndpoint string,
		apiKey string,
		request cloudclient.HomeAutoSessionStartRequest,
	) (cloudclient.HomeAutoSessionStartResult, error)
	StopHomeAutoSession(
		ctx context.Context,
		stopEndpoint string,
		apiKey string,
		request cloudclient.HomeAutoSessionStopRequest,
	) (cloudclient.HomeAutoSessionStopResult, error)
}

type Service struct {
	container *Container
	mode      config.RunMode
	steady    steadyState
	build     buildinfo.Info
	updater   *update.Checker
}

type Container struct {
	Config          config.Config
	Logger          *slog.Logger
	State           *state.Store
	Status          *status.Model
	Cloud           CloudClient
	Pairing         *pairing.Manager
	Meshtastic      meshtastic.Adapter
	HomeAutoSession *homeautosession.Module
	MeshEvents      <-chan meshtastic.Event
	Portal          *webportal.Server
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
	installType := installTypeFromProfile(profile)
	hostName := runtimeHostName()
	localName := resolveLocalNameHint(
		cfg.Runtime.LocalName,
		current.Installation.LocalName,
		hostName,
		installType,
		current.Installation.ID,
	)

	if err := store.Update(func(data *state.Data) {
		data.Installation.LastStartedAt = time.Now().UTC()
		data.Installation.LocalName = localName
		data.Installation.Hostname = hostName
		data.Cloud.EndpointURL = cfg.Cloud.BaseURL
		data.Runtime.Profile = profile
		data.Runtime.Mode = string(mode)
		data.Runtime.InstallType = installType
	}); err != nil {
		return nil, fmt.Errorf("persist startup state: %w", err)
	}

	current = store.Snapshot()
	statusModel := status.New()
	statusModel.SetInstallationID(current.Installation.ID)
	statusModel.SetIdentity(
		current.Installation.LocalName,
		current.Installation.Hostname,
		current.Cloud.ReceiverID,
		current.Cloud.ReceiverLabel,
		current.Cloud.SiteLabel,
		current.Cloud.GroupLabel,
	)
	build := buildinfo.Current()
	statusModel.SetBuildInfo(
		build.Version,
		build.Channel,
		build.Commit,
		build.BuildDate,
		build.BuildID,
		goruntime.GOOS,
		goruntime.GOARCH,
		installType,
	)
	statusModel.SetMode(string(mode))
	statusModel.SetRuntimeProfile(profile)
	statusModel.SetPairingPhase(string(current.Pairing.Phase))
	statusModel.SetCloud(cfg.Cloud.BaseURL, "unknown")
	statusModel.SetComponent("runtime", "starting", "initializing service container")
	statusModel.SetComponent("portal", "stopped", "portal not started yet")
	statusModel.SetComponent("network", "unknown", "network status not probed yet")
	statusModel.SetComponent("update", "unknown", "update status not evaluated yet")
	statusModel.SetComponent("cloud_config", "unknown", "cloud config version not reported yet")
	statusModel.SetComponent("operations", "unknown", "operational checks not evaluated yet")
	statusModel.SetComponent("attention", "none", "no local attention required")
	statusModel.SetUpdateStatus(
		current.Update.Status,
		current.Update.Summary,
		current.Update.Hint,
		current.Update.ManifestVersion,
		current.Update.ManifestChannel,
		current.Update.RecommendedVersion,
		current.Update.LastCheckedAt,
	)

	svc := &Service{}
	svc.mode = mode
	svc.build = build
	svc.updater = update.NewChecker(update.Config{
		Enabled:             cfg.Update.Enabled,
		ManifestURL:         cfg.Update.ManifestURL,
		CheckInterval:       cfg.Update.CheckInterval.Std(),
		RequestTimeout:      cfg.Update.RequestTimeout.Std(),
		MinSupportedVersion: cfg.Update.MinSupportedVersion,
	})
	svc.steady = steadyState{
		ingestQueue: make([]queuedIngestEvent, 0, 64),
	}

	cloud := cloudclient.NewHTTPClient(cfg.Cloud.BaseURL, 10*time.Second)
	mesh := meshtastic.NewAdapter(cfg.Meshtastic, logger.With("component", "meshtastic"))
	meshSnap := mesh.Snapshot()
	statusModel.SetComponent("meshtastic", string(meshSnap.State), meshtasticStatusMessage(meshSnap))
	statusModel.SetMeshtasticConfig(mapMeshtasticConfigStatus(meshSnap))
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
				Label:          current.Installation.LocalName,
				RuntimeVersion: build.Version,
				Metadata: map[string]any{
					"releaseChannel": build.Channel,
					"buildCommit":    build.Commit,
					"buildDate":      build.BuildDate,
					"buildID":        build.BuildID,
					"installType":    installType,
					"localName":      current.Installation.LocalName,
					"hostname":       current.Installation.Hostname,
					"installationId": current.Installation.ID,
				},
			},
		),
	}
	svc.container.HomeAutoSession = homeautosession.New(
		cfg.HomeAutoSession,
		store,
		statusModel,
		logger,
		cloud,
	)
	svc.applyHomeAutoLocalFallbackConfig(homeAutoConfigApplyLocalStartup, "")
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
	if c.HomeAutoSession != nil {
		c.HomeAutoSession.Start(ctx)
	}

	portalErr := make(chan error, 1)
	go func() {
		portalErr <- c.Portal.Run(ctx)
	}()
	c.Status.SetComponent("portal", "running", "local setup portal listening on "+c.Config.Portal.BindAddress)

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

func (s *Service) ResetPairing(_ context.Context, deauthorize bool) error {
	return s.container.Pairing.ResetPairing(deauthorize)
}

func (s *Service) CurrentHomeAutoSessionConfig() config.HomeAutoSessionConfig {
	return s.container.Config.HomeAutoSession
}

func (s *Service) UpdateHomeAutoSessionConfig(_ context.Context, hasCfg config.HomeAutoSessionConfig) error {
	next := s.container.Config
	next.HomeAutoSession = hasCfg
	if err := next.Validate(); err != nil {
		return err
	}
	if path := strings.TrimSpace(next.LoadedFromConfig); path != "" {
		if err := config.Save(path, next); err != nil {
			return err
		}
		next.LoadedFromConfig = path
	}
	s.container.Config = next
	if s.container.HomeAutoSession == nil {
		return nil
	}

	current := s.container.State.Snapshot().HomeAutoSession
	if current.CloudConfigPresent && strings.TrimSpace(current.EffectiveConfigSource) == homeautosession.ConfigSourceCloudManaged {
		s.container.HomeAutoSession.SetConfigApplyStatus(homeautosession.ConfigApplyStatus{
			EffectiveSource:    homeautosession.ConfigSourceCloudManaged,
			EffectiveVersion:   strings.TrimSpace(current.EffectiveConfigVersion),
			CloudConfigPresent: true,
			LastFetchedVersion: strings.TrimSpace(current.LastFetchedConfigVer),
			LastAppliedVersion: strings.TrimSpace(current.LastAppliedConfigVer),
			LastApplyResult:    "local_fallback_saved_cloud_managed_active",
			LastApplyError:     "",
			DesiredEnabled:     next.HomeAutoSession.Enabled,
			DesiredMode:        string(next.HomeAutoSession.Mode),
		})
		return nil
	}

	s.applyHomeAutoLocalFallbackConfig(homeAutoConfigApplyLocalManualUpdate, "")
	return nil
}

func (s *Service) ReevaluateHomeAutoSession(_ context.Context) error {
	if s.container.HomeAutoSession != nil {
		s.container.HomeAutoSession.Reevaluate()
		return nil
	}
	return errors.New("home auto session module unavailable")
}

func (s *Service) ResetHomeAutoSession(_ context.Context) error {
	if s.container.HomeAutoSession != nil {
		s.container.HomeAutoSession.ResetDegraded()
		return nil
	}
	return errors.New("home auto session module unavailable")
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
	networkProbe := diagnostics.ProbeLocalNetwork()
	networkState, networkMessage := diagnostics.NetworkComponentState(networkProbe)
	c.Status.SetComponent("network", networkState, networkMessage)

	if err := c.Pairing.Process(ctx); err != nil {
		c.Logger.Error("pairing lifecycle tick failed", "err", err)
		c.Status.SetLastError("pairing tick failed")
	}

	snap := c.State.Snapshot()
	meshSnap := c.Meshtastic.Snapshot()

	c.Status.SetIdentity(
		snap.Installation.LocalName,
		snap.Installation.Hostname,
		snap.Cloud.ReceiverID,
		snap.Cloud.ReceiverLabel,
		snap.Cloud.SiteLabel,
		snap.Cloud.GroupLabel,
	)
	c.Status.SetPairingPhase(string(snap.Pairing.Phase))
	c.Status.SetCloud(c.Config.Cloud.BaseURL, pairingCloudStatus(snap.Pairing))
	c.Status.SetComponent("meshtastic", string(meshSnap.State), meshtasticStatusMessage(meshSnap))
	c.Status.SetMeshtasticConfig(mapMeshtasticConfigStatus(meshSnap))

	s.processSteadyState(ctx, snap, meshSnap)
	s.refreshUpdateStatus(ctx, snap)
	snap = c.State.Snapshot()

	syncReady := snap.Pairing.Phase == state.PairingSteadyState
	cloudConfigIssue := cloudConfigCompatibilityIssue(snap.Cloud.ConfigVersion)
	switch s.mode {
	case config.ModeSetup:
		c.Status.SetReady(true, "setup portal available")
		if syncReady {
			if cloudConfigIssue != "" {
				c.Status.SetComponent("service", "blocked", cloudConfigIssue)
			} else {
				c.Status.SetComponent("service", "ready", "receiver paired and activated")
			}
		} else {
			c.Status.SetComponent("service", "setup", "receiver in setup mode")
		}
	case config.ModeService:
		if syncReady && cloudConfigIssue == "" {
			c.Status.SetReady(true, "service mode active")
		} else {
			switch {
			case !syncReady:
				c.Status.SetReady(false, "service mode requires paired state")
			default:
				c.Status.SetReady(false, "service mode blocked by incompatible cloud config")
				c.Status.SetComponent("service", "blocked", cloudConfigIssue)
			}
		}
		c.Status.SetComponent("pairing", string(snap.Pairing.Phase), "pairing state from persistent store")
		if cloudConfigIssue == "" {
			c.Status.SetComponent("service", "running", "service mode loop active")
		}
	default:
		c.Status.SetReady(false, "unknown runtime mode")
		c.Status.SetLastError("unknown runtime mode")
	}

	s.updateFailureState(snap, meshSnap, networkProbe)
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
			c.Status.SetMeshtasticConfig(mapMeshtasticConfigStatus(c.Meshtastic.Snapshot()))
		}
	}
	if c.HomeAutoSession != nil {
		c.HomeAutoSession.ObserveEvent(event)
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
	if issue := cloudConfigCompatibilityIssue(snapshot.Cloud.ConfigVersion); issue != "" {
		c.Status.SetComponent("cloud_config", "unsupported", issue)
		c.Status.SetLastError("cloud config version unsupported")
		c.Status.SetCloud(snapshot.Cloud.EndpointURL, "config_incompatible")
		c.Status.SetCloudReachable(false)
		return
	}
	c.Status.SetComponent("cloud_config", "compatible", cloudConfigStatusMessage(snapshot.Cloud.ConfigVersion))

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
	if errors.Is(heartbeatErr, errLifecycleTransition) {
		fresh := isHeartbeatFresh(s.steady.lastHeartbeatAck, c.Config.Service.Heartbeat.Std())
		c.Status.SetHeartbeat(s.steady.lastHeartbeatSent, s.steady.lastHeartbeatAck, fresh)
		c.Status.SetPacketTelemetry(
			s.steady.lastPacketQueued,
			s.steady.lastPacketSent,
			s.steady.lastPacketAck,
			len(s.steady.ingestQueue),
		)
		c.Status.SetCloudReachable(false)
		c.Status.SetCloud(snapshot.Cloud.EndpointURL, "lifecycle_blocked")
		return
	}
	if heartbeatErr != nil {
		if cloudclient.IsRetryable(heartbeatErr) {
			c.Logger.Warn("heartbeat failed", "err", heartbeatErr)
		} else {
			c.Logger.Error("heartbeat failed", "err", heartbeatErr)
		}
	}
	ingestErr := s.drainIngestQueue(ctx, snapshot)
	if errors.Is(ingestErr, errLifecycleTransition) {
		fresh := isHeartbeatFresh(s.steady.lastHeartbeatAck, c.Config.Service.Heartbeat.Std())
		c.Status.SetHeartbeat(s.steady.lastHeartbeatSent, s.steady.lastHeartbeatAck, fresh)
		c.Status.SetPacketTelemetry(
			s.steady.lastPacketQueued,
			s.steady.lastPacketSent,
			s.steady.lastPacketAck,
			len(s.steady.ingestQueue),
		)
		c.Status.SetCloudReachable(false)
		c.Status.SetCloud(snapshot.Cloud.EndpointURL, "lifecycle_blocked")
		return
	}
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
	updateSnap := s.container.Status.Snapshot()
	ops := s.deriveOperational(updateSnap, snapshot, meshSnap)

	ack, err := s.container.Cloud.SendReceiverHeartbeat(ctx, endpoint, apiKey, cloudclient.ReceiverHeartbeat{
		RuntimeVersion:  s.build.Version,
		Platform:        goruntime.GOOS,
		Arch:            goruntime.GOARCH,
		LocalNodeID:     meshSnap.LocalNodeID,
		ObservedNodeIDs: append([]string(nil), meshSnap.ObservedNodeIDs...),
		Status: map[string]any{
			"installationId":         snapshot.Installation.ID,
			"localName":              snapshot.Installation.LocalName,
			"hostname":               snapshot.Installation.Hostname,
			"receiverId":             snapshot.Cloud.ReceiverID,
			"receiverLabel":          snapshot.Cloud.ReceiverLabel,
			"siteLabel":              snapshot.Cloud.SiteLabel,
			"groupLabel":             snapshot.Cloud.GroupLabel,
			"pairingPhase":           snapshot.Pairing.Phase,
			"serviceMode":            s.mode,
			"meshtasticState":        meshSnap.State,
			"ingestQueueDepth":       len(s.steady.ingestQueue),
			"coarseFailureReason":    coarseFailure,
			"releaseChannel":         s.build.Channel,
			"buildCommit":            s.build.Commit,
			"buildDate":              s.build.BuildDate,
			"buildID":                s.build.BuildID,
			"installType":            snapshot.Runtime.InstallType,
			"updateStatus":           updateSnap.UpdateStatus,
			"updateVersion":          updateSnap.UpdateRecommendedVersion,
			"failureCode":            updateSnap.FailureCode,
			"failureSummary":         updateSnap.FailureSummary,
			"failureHint":            updateSnap.FailureHint,
			"attentionState":         updateSnap.AttentionState,
			"attentionCategory":      updateSnap.AttentionCategory,
			"attentionCode":          updateSnap.AttentionCode,
			"attentionSummary":       updateSnap.AttentionSummary,
			"attentionHint":          updateSnap.AttentionHint,
			"attentionRequired":      updateSnap.AttentionActionRequired,
			"operationalStatus":      ops.Overall,
			"operationalSummary":     ops.Summary,
			"homeAutoSessionEnabled": updateSnap.HomeAutoSession.Enabled,
			"homeAutoSessionMode":    updateSnap.HomeAutoSession.Mode,
			"homeAutoConfigSource":   updateSnap.HomeAutoSession.EffectiveConfigSource,
			"homeAutoConfigVersion":  updateSnap.HomeAutoSession.EffectiveConfigVer,
			"homeAutoCloudConfig":    updateSnap.HomeAutoSession.CloudConfigPresent,
			"homeAutoConfigResult":   updateSnap.HomeAutoSession.LastConfigApplyResult,
			"homeAutoConfigError":    updateSnap.HomeAutoSession.LastConfigApplyError,
			"homeAutoSessionState":   updateSnap.HomeAutoSession.State,
			"homeAutoControlState":   updateSnap.HomeAutoSession.ControlState,
			"homeAutoActiveSource":   updateSnap.HomeAutoSession.ActiveStateSource,
			"homeAutoReconciliation": updateSnap.HomeAutoSession.ReconciliationState,
			"homeAutoPendingAction":  updateSnap.HomeAutoSession.PendingAction,
			"homeAutoSessionSummary": updateSnap.HomeAutoSession.Summary,
			"homeAutoTrackedNode":    updateSnap.HomeAutoSession.TrackedNodeState,
			"homeAutoDecision":       updateSnap.HomeAutoSession.LastDecisionReason,
			"homeAutoLastAction":     updateSnap.HomeAutoSession.LastAction,
			"homeAutoLastResult":     updateSnap.HomeAutoSession.LastActionResult,
			"homeAutoLastError":      updateSnap.HomeAutoSession.LastError,
			"homeAutoBlockedReason":  updateSnap.HomeAutoSession.BlockedReason,
			"homeAutoGPSStatus":      updateSnap.HomeAutoSession.GPSStatus,
			"homeAutoGPSReason":      updateSnap.HomeAutoSession.GPSReason,
			"meshConfigAvailable":    updateSnap.MeshtasticConfig.Available,
			"meshConfigRegion":       updateSnap.MeshtasticConfig.Region,
			"meshConfigChannel":      updateSnap.MeshtasticConfig.PrimaryChannel,
			"meshConfigPSKState":     updateSnap.MeshtasticConfig.PSKState,
			"meshConfigShareReady":   updateSnap.MeshtasticConfig.ShareURLAvailable,
			"meshConfigShareHint":    updateSnap.MeshtasticConfig.ShareURLRedacted,
		},
	})
	if err != nil {
		if change, ok := lifecycleChangeFromCloudError(err); ok {
			if applyErr := s.handleLifecycleCloudError(change, err); applyErr != nil {
				return applyErr
			}
			return errLifecycleTransition
		}
		s.markHomeAutoCloudConfigFetchFailed(snapshot, err)
		s.steady.cloudReachable = false
		s.container.Status.SetLastError(coarseCloudError(err))
		return err
	}

	ackAt := ack.LastHeartbeatAt.UTC()
	s.steady.lastHeartbeatAck = &ackAt
	if s.container.State != nil {
		if configVersion := strings.TrimSpace(ack.ConfigVersion); configVersion != "" && configVersion != strings.TrimSpace(snapshot.Cloud.ConfigVersion) {
			if err := s.container.State.Update(func(data *state.Data) {
				data.Cloud.ConfigVersion = configVersion
				if value := strings.TrimSpace(ack.ReceiverAgentID); value != "" {
					data.Cloud.ReceiverID = value
				}
				if value := strings.TrimSpace(ack.OwnerID); value != "" {
					data.Cloud.OwnerID = value
				}
				if value := strings.TrimSpace(ack.ReceiverLabel); value != "" {
					data.Cloud.ReceiverLabel = value
				}
				if value := strings.TrimSpace(ack.SiteLabel); value != "" {
					data.Cloud.SiteLabel = value
				}
				if value := strings.TrimSpace(ack.GroupLabel); value != "" {
					data.Cloud.GroupLabel = value
				}
				data.Cloud.UpdatedAt = time.Now().UTC()
			}); err != nil {
				s.container.Logger.Warn("persist cloud heartbeat identity/config failed", "err", err)
			}
		}
		if strings.TrimSpace(ack.ConfigVersion) == strings.TrimSpace(snapshot.Cloud.ConfigVersion) &&
			(strings.TrimSpace(ack.ReceiverAgentID) != "" || strings.TrimSpace(ack.ReceiverLabel) != "" ||
				strings.TrimSpace(ack.SiteLabel) != "" || strings.TrimSpace(ack.GroupLabel) != "") {
			if err := s.container.State.Update(func(data *state.Data) {
				if value := strings.TrimSpace(ack.ReceiverAgentID); value != "" {
					data.Cloud.ReceiverID = value
				}
				if value := strings.TrimSpace(ack.OwnerID); value != "" {
					data.Cloud.OwnerID = value
				}
				if value := strings.TrimSpace(ack.ReceiverLabel); value != "" {
					data.Cloud.ReceiverLabel = value
				}
				if value := strings.TrimSpace(ack.SiteLabel); value != "" {
					data.Cloud.SiteLabel = value
				}
				if value := strings.TrimSpace(ack.GroupLabel); value != "" {
					data.Cloud.GroupLabel = value
				}
				data.Cloud.UpdatedAt = time.Now().UTC()
			}); err != nil {
				s.container.Logger.Warn("persist cloud heartbeat identity failed", "err", err)
			}
		}
	}
	latest := snapshot
	if s.container.State != nil {
		latest = s.container.State.Snapshot()
	}
	s.container.Status.SetIdentity(
		latest.Installation.LocalName,
		latest.Installation.Hostname,
		latest.Cloud.ReceiverID,
		latest.Cloud.ReceiverLabel,
		latest.Cloud.SiteLabel,
		latest.Cloud.GroupLabel,
	)
	s.applyHomeAutoCloudConfigFromAck(ack)
	s.steady.cloudReachable = true
	return nil
}

func (s *Service) applyHomeAutoCloudConfigFromAck(ack cloudclient.ReceiverHeartbeatAck) {
	if s.container.HomeAutoSession == nil {
		return
	}
	if ack.HomeAutoSessionConfig == nil {
		s.applyHomeAutoLocalFallbackConfig(homeAutoConfigApplyCloudMissing, "")
		return
	}

	fallbackCfg := s.container.Config.HomeAutoSession
	candidate, fetchedVersion, mapErr := mapManagedHomeAutoConfig(fallbackCfg, ack.HomeAutoSessionConfig)
	if fetchedVersion == "" {
		fetchedVersion = "cloud-unversioned"
	}
	if mapErr != nil {
		s.applyHomeAutoLocalFallbackConfigWithStatus(homeautosession.ConfigApplyStatus{
			EffectiveSource:    homeautosession.ConfigSourceLocalFallback,
			EffectiveVersion:   localHomeAutoConfigVersion(fallbackCfg),
			CloudConfigPresent: true,
			LastFetchedVersion: fetchedVersion,
			LastAppliedVersion: localHomeAutoConfigVersion(fallbackCfg),
			LastApplyResult:    homeAutoConfigApplyCloudInvalid,
			LastApplyError:     mapErr.Error(),
			DesiredEnabled:     candidate.Enabled,
			DesiredMode:        string(candidate.Mode),
		})
		return
	}
	if err := s.container.HomeAutoSession.ApplyConfig(candidate); err != nil {
		s.applyHomeAutoLocalFallbackConfigWithStatus(homeautosession.ConfigApplyStatus{
			EffectiveSource:    homeautosession.ConfigSourceLocalFallback,
			EffectiveVersion:   localHomeAutoConfigVersion(fallbackCfg),
			CloudConfigPresent: true,
			LastFetchedVersion: fetchedVersion,
			LastAppliedVersion: localHomeAutoConfigVersion(fallbackCfg),
			LastApplyResult:    homeAutoConfigApplyCloudInvalid,
			LastApplyError:     err.Error(),
			DesiredEnabled:     candidate.Enabled,
			DesiredMode:        string(candidate.Mode),
		})
		return
	}

	applyResult := homeAutoConfigApplyCloud
	if !candidate.Enabled || candidate.Mode == config.HomeAutoSessionModeOff {
		applyResult = homeAutoConfigApplyCloudDisabled
	}
	s.container.HomeAutoSession.SetConfigApplyStatus(homeautosession.ConfigApplyStatus{
		EffectiveSource:    homeautosession.ConfigSourceCloudManaged,
		EffectiveVersion:   fetchedVersion,
		CloudConfigPresent: true,
		LastFetchedVersion: fetchedVersion,
		LastAppliedVersion: fetchedVersion,
		LastApplyResult:    applyResult,
		LastApplyError:     "",
		DesiredEnabled:     candidate.Enabled,
		DesiredMode:        string(candidate.Mode),
	})
}

func (s *Service) markHomeAutoCloudConfigFetchFailed(snapshot state.Data, err error) {
	if s.container.HomeAutoSession == nil {
		return
	}
	home := snapshot.HomeAutoSession
	effectiveSource := strings.TrimSpace(home.EffectiveConfigSource)
	if effectiveSource == "" {
		effectiveSource = homeautosession.ConfigSourceLocalFallback
	}
	effectiveVersion := strings.TrimSpace(home.EffectiveConfigVersion)
	if effectiveVersion == "" && effectiveSource == homeautosession.ConfigSourceLocalFallback {
		effectiveVersion = localHomeAutoConfigVersion(s.container.Config.HomeAutoSession)
	}
	lastApplied := strings.TrimSpace(home.LastAppliedConfigVer)
	if lastApplied == "" {
		lastApplied = effectiveVersion
	}
	desiredEnabled := s.container.Config.HomeAutoSession.Enabled
	if home.DesiredConfigEnabled != nil {
		desiredEnabled = *home.DesiredConfigEnabled
	}
	desiredMode := strings.TrimSpace(home.DesiredConfigMode)
	if desiredMode == "" {
		desiredMode = string(s.container.Config.HomeAutoSession.Mode)
	}
	s.container.HomeAutoSession.SetConfigApplyStatus(homeautosession.ConfigApplyStatus{
		EffectiveSource:    effectiveSource,
		EffectiveVersion:   effectiveVersion,
		CloudConfigPresent: home.CloudConfigPresent,
		LastFetchedVersion: strings.TrimSpace(home.LastFetchedConfigVer),
		LastAppliedVersion: lastApplied,
		LastApplyResult:    homeAutoConfigApplyFetchFailed,
		LastApplyError:     coarseCloudError(err),
		DesiredEnabled:     desiredEnabled,
		DesiredMode:        desiredMode,
	})
}

func (s *Service) applyHomeAutoLocalFallbackConfig(resultCode string, applyErr string) {
	cfg := s.container.Config.HomeAutoSession
	status := homeautosession.ConfigApplyStatus{
		EffectiveSource:    homeautosession.ConfigSourceLocalFallback,
		EffectiveVersion:   localHomeAutoConfigVersion(cfg),
		CloudConfigPresent: false,
		LastFetchedVersion: "",
		LastAppliedVersion: localHomeAutoConfigVersion(cfg),
		LastApplyResult:    resultCode,
		LastApplyError:     strings.TrimSpace(applyErr),
		DesiredEnabled:     cfg.Enabled,
		DesiredMode:        string(cfg.Mode),
	}
	s.applyHomeAutoLocalFallbackConfigWithStatus(status)
}

func (s *Service) applyHomeAutoLocalFallbackConfigWithStatus(statusPayload homeautosession.ConfigApplyStatus) {
	if s.container.HomeAutoSession == nil {
		return
	}
	if err := s.container.HomeAutoSession.ApplyConfig(s.container.Config.HomeAutoSession); err != nil {
		statusPayload.LastApplyError = err.Error()
		if strings.TrimSpace(statusPayload.LastApplyResult) == "" {
			statusPayload.LastApplyResult = homeAutoConfigApplyCloudInvalid
		}
	}
	if strings.TrimSpace(statusPayload.LastApplyResult) == "" {
		statusPayload.LastApplyResult = homeAutoConfigApplyLocalManualUpdate
	}
	s.container.HomeAutoSession.SetConfigApplyStatus(statusPayload)
}

func mapManagedHomeAutoConfig(base config.HomeAutoSessionConfig, managed *cloudclient.HomeAutoSessionManagedConfig) (config.HomeAutoSessionConfig, string, error) {
	if managed == nil {
		return base, "", errors.New("cloud config payload is missing")
	}

	cfg := base
	version := strings.TrimSpace(managed.Version)
	if managed.Enabled != nil {
		cfg.Enabled = *managed.Enabled
	}
	if mode := strings.TrimSpace(managed.Mode); mode != "" {
		cfg.Mode = config.HomeAutoSessionMode(strings.ToLower(mode))
	}
	if !cfg.Enabled {
		cfg.Mode = config.HomeAutoSessionModeOff
	}

	cfg.Home = config.HomeGeofenceConfig{
		Lat:     managed.Home.Lat,
		Lon:     managed.Home.Lon,
		RadiusM: managed.Home.RadiusM,
	}
	cfg.TrackedNodeIDs = append([]string(nil), managed.TrackedNodeIDs...)

	if value := strings.TrimSpace(managed.StartDebounce); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return cfg, version, fmt.Errorf("cloud home_auto_session.start_debounce is invalid: %w", err)
		}
		cfg.StartDebounce = config.Duration(parsed)
	}
	if value := strings.TrimSpace(managed.StopDebounce); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return cfg, version, fmt.Errorf("cloud home_auto_session.stop_debounce is invalid: %w", err)
		}
		cfg.StopDebounce = config.Duration(parsed)
	}
	if value := strings.TrimSpace(managed.IdleStopTimeout); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return cfg, version, fmt.Errorf("cloud home_auto_session.idle_stop_timeout is invalid: %w", err)
		}
		cfg.IdleStopTimeout = config.Duration(parsed)
	}
	if managed.StartupReconcile != nil {
		cfg.StartupReconcile = *managed.StartupReconcile
	}
	if value := strings.TrimSpace(managed.SessionNameTemplate); value != "" {
		cfg.SessionNameTemplate = value
	}
	if value := strings.TrimSpace(managed.SessionNotesTemplate); value != "" {
		cfg.SessionNotesTemplate = value
	}
	if value := strings.TrimSpace(managed.Cloud.StartEndpoint); value != "" {
		cfg.Cloud.StartEndpoint = value
	}
	if value := strings.TrimSpace(managed.Cloud.StopEndpoint); value != "" {
		cfg.Cloud.StopEndpoint = value
	}

	validator := config.Default()
	validator.HomeAutoSession = cfg
	if err := validator.Validate(); err != nil {
		return cfg, version, fmt.Errorf("cloud home_auto_session config rejected: %w", err)
	}
	return cfg, version, nil
}

func localHomeAutoConfigVersion(cfg config.HomeAutoSessionConfig) string {
	normalized := strings.TrimSpace(string(cfg.Mode))
	if normalized == "" {
		normalized = "off"
	}
	payload := map[string]any{
		"enabled":              cfg.Enabled,
		"mode":                 normalized,
		"home":                 cfg.Home,
		"trackedNodeIDs":       cfg.TrackedNodeIDs,
		"startDebounce":        cfg.StartDebounce.Std().String(),
		"stopDebounce":         cfg.StopDebounce.Std().String(),
		"idleStopTimeout":      cfg.IdleStopTimeout.Std().String(),
		"startupReconcile":     cfg.StartupReconcile,
		"sessionNameTemplate":  cfg.SessionNameTemplate,
		"sessionNotesTemplate": cfg.SessionNotesTemplate,
		"cloudStartEndpoint":   cfg.Cloud.StartEndpoint,
		"cloudStopEndpoint":    cfg.Cloud.StopEndpoint,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return "local-default"
	}
	sum := sha256.Sum256(data)
	return "local-" + hex.EncodeToString(sum[:8])
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

		if change, ok := lifecycleChangeFromCloudError(err); ok {
			if applyErr := s.handleLifecycleCloudError(change, err); applyErr != nil {
				return applyErr
			}
			return errLifecycleTransition
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

func installTypeFromProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "appliance-pi":
		return "pi-appliance"
	case "linux-service":
		return "linux-package"
	case "windows-user":
		return "windows-user"
	default:
		return "manual"
	}
}

func pairingCloudStatus(pairingState state.PairingState) string {
	switch pairingState.Phase {
	case state.PairingSteadyState, state.PairingActivated:
		return "credential_ready"
	case state.PairingBootstrapExchanged:
		return "activating"
	case state.PairingCodeEntered:
		return "pairing"
	default:
		switch strings.TrimSpace(pairingState.LastChange) {
		case string(pairing.LifecycleCredentialRevoked):
			return "credential_revoked"
		case string(pairing.LifecycleReceiverDisabled):
			return "receiver_disabled"
		case string(pairing.LifecycleReceiverReplaced):
			return "receiver_replaced"
		}
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

func mapMeshtasticConfigStatus(snapshot meshtastic.Snapshot) status.MeshtasticConfigSnapshot {
	home := snapshot.HomeConfig
	if home == nil {
		reason := ""
		switch snapshot.State {
		case meshtastic.StateNotPresent:
			reason = "no connected Meshtastic node detected"
		case meshtastic.StateConnected:
			reason = "connected node has not reported channel/config summary"
		case meshtastic.StateConnecting, meshtastic.StateDetected:
			reason = "waiting for node status/config event"
		case meshtastic.StateDegraded:
			reason = "adapter degraded; config summary unavailable"
		default:
			reason = "config summary unavailable"
		}
		return status.MeshtasticConfigSnapshot{
			Available:         false,
			UnavailableReason: reason,
			PSKState:          "unknown",
		}
	}

	var updatedPtr *time.Time
	if !home.UpdatedAt.IsZero() {
		updated := home.UpdatedAt.UTC()
		updatedPtr = &updated
	}
	return status.MeshtasticConfigSnapshot{
		Available:         home.Available,
		UnavailableReason: home.UnavailableReason,
		Region:            home.Region,
		PrimaryChannel:    home.PrimaryChannel,
		PrimaryChannelIdx: home.PrimaryChannelIdx,
		PSKState:          home.PSKState,
		LoRaPreset:        home.LoRaPreset,
		LoRaBandwidth:     home.LoRaBandwidth,
		LoRaSpreading:     home.LoRaSpreading,
		LoRaCodingRate:    home.LoRaCodingRate,
		ShareURL:          home.ShareURL,
		ShareURLRedacted:  home.ShareURLRedacted,
		ShareURLAvailable: home.ShareURLAvailable,
		ShareQRText:       home.ShareQRText,
		Source:            home.Source,
		UpdatedAt:         updatedPtr,
	}
}

func cloudConfigCompatibilityIssue(configVersion string) string {
	value := strings.TrimSpace(configVersion)
	if value == "" {
		return ""
	}
	normalized := strings.TrimPrefix(strings.ToLower(value), "v")
	major := normalized
	if idx := strings.Index(major, "."); idx >= 0 {
		major = major[:idx]
	}
	if major == "1" {
		return ""
	}
	return "cloud config version " + value + " is unsupported by this receiver"
}

func cloudConfigStatusMessage(configVersion string) string {
	value := strings.TrimSpace(configVersion)
	if value == "" {
		return "cloud config version not reported"
	}
	return "cloud config version " + value + " is compatible"
}

func (s *Service) refreshUpdateStatus(ctx context.Context, snapshot state.Data) {
	if s.updater == nil {
		return
	}

	if !s.updater.ShouldCheck(snapshot.Update.LastCheckedAt) {
		s.container.Status.SetUpdateStatus(
			snapshot.Update.Status,
			snapshot.Update.Summary,
			snapshot.Update.Hint,
			snapshot.Update.ManifestVersion,
			snapshot.Update.ManifestChannel,
			snapshot.Update.RecommendedVersion,
			snapshot.Update.LastCheckedAt,
		)
		stateCode := strings.TrimSpace(snapshot.Update.Status)
		if stateCode == "" {
			stateCode = "unknown"
		}
		s.container.Status.SetComponent("update", stateCode, strings.TrimSpace(snapshot.Update.Summary))
		return
	}

	result := s.updater.Check(ctx, update.Installed{
		Version:     s.build.Version,
		Channel:     s.build.Channel,
		Platform:    goruntime.GOOS,
		Arch:        goruntime.GOARCH,
		InstallType: snapshot.Runtime.InstallType,
	})

	checkedAt := result.CheckedAt
	s.container.Status.SetUpdateStatus(
		string(result.Status),
		result.Summary,
		result.Hint,
		result.ManifestVersion,
		result.ManifestChannel,
		result.RecommendedVersion,
		&checkedAt,
	)
	s.container.Status.SetComponent("update", string(result.Status), result.Summary)

	if err := s.container.State.Update(func(data *state.Data) {
		data.Update.Status = string(result.Status)
		data.Update.Summary = result.Summary
		data.Update.Hint = result.Hint
		data.Update.ManifestVersion = result.ManifestVersion
		data.Update.ManifestChannel = result.ManifestChannel
		data.Update.RecommendedVersion = result.RecommendedVersion
		data.Update.LastError = result.LastError
		data.Update.LastCheckedAt = cloneTime(result.CheckedAt)
		data.Update.UpdatedAt = result.CheckedAt
	}); err != nil {
		s.container.Logger.Warn("persist update status failed", "err", err)
	}
}

func (s *Service) updateFailureState(snapshot state.Data, meshSnap meshtastic.Snapshot, networkProbe diagnostics.NetworkProbe) {
	now := time.Now().UTC()
	current := s.container.Status.Snapshot()
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	portalState := "unknown"
	if component, ok := current.Components["portal"]; ok {
		portalState = component.State
	}
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        current.RuntimeProfile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		RuntimeLastError:      current.LastError,
		PortalState:           portalState,
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        s.steady.cloudReachable,
		MeshtasticState:       string(meshSnap.State),
		UpdateStatus:          current.UpdateStatus,
		IngestQueueDepth:      len(s.steady.ingestQueue),
		LastPacketQueued:      s.steady.lastPacketQueued,
		LastPacketAck:         s.steady.lastPacketAck,
		Now:                   now,
	})
	ops := s.deriveOperational(current, snapshot, meshSnap)
	s.container.Status.SetComponent("operations", ops.Overall, ops.Summary)
	attention := diagnostics.DeriveAttention(finding, ops)
	s.container.Status.SetAttention(
		string(attention.State),
		string(attention.Category),
		attention.Code,
		attention.Summary,
		attention.Hint,
		attention.ActionRequired,
	)
	s.container.Status.SetComponent("attention", string(attention.State), attention.Summary)

	if finding.Code == diagnostics.FailureNone {
		s.container.Status.SetFailure("", "", "")
		return
	}
	s.container.Status.SetFailure(string(finding.Code), finding.Summary, finding.Hint)
}

func (s *Service) deriveOperational(current status.Snapshot, snapshot state.Data, meshSnap meshtastic.Snapshot) diagnostics.OperationalSummary {
	return diagnostics.EvaluateOperational(diagnostics.OperationalInput{
		Now:                 time.Now().UTC(),
		Lifecycle:           string(current.Lifecycle),
		Ready:               current.Ready,
		ReadyReason:         current.ReadyReason,
		PairingPhase:        string(snapshot.Pairing.Phase),
		HasIngestCredential: credentialsReady(snapshot),
		CloudReachable:      s.steady.cloudReachable,
		CloudProbeStatus:    current.CloudStatus,
		MeshtasticState:     string(meshSnap.State),
		IngestQueueDepth:    len(s.steady.ingestQueue),
		LastPacketQueued:    s.steady.lastPacketQueued,
		LastPacketAck:       s.steady.lastPacketAck,
		UpdateStatus:        current.UpdateStatus,
	})
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

func (s *Service) handleLifecycleCloudError(change pairing.LifecycleChange, err error) error {
	c := s.container
	reason := lifecycleChangeReason(err)
	if applyErr := c.Pairing.ApplyLifecycleChange(change, reason, true); applyErr != nil {
		return applyErr
	}

	s.steady.cloudReachable = false
	s.steady.ingestQueue = nil
	c.Status.SetCloud(c.Config.Cloud.BaseURL, "lifecycle_blocked")
	c.Status.SetCloudReachable(false)
	c.Status.SetComponent("service", "blocked", lifecycleServiceMessage(change))
	c.Status.SetComponent("ingest", "blocked", "ingest disabled until receiver is re-paired")
	return nil
}

func lifecycleChangeFromCloudError(err error) (pairing.LifecycleChange, bool) {
	var apiErr *cloudclient.APIError
	if !errors.As(err, &apiErr) {
		return "", false
	}

	statusCode := apiErr.StatusCode
	message := strings.ToLower(strings.TrimSpace(apiErr.Message))

	if statusCode == 423 || strings.Contains(message, "disabled") {
		return pairing.LifecycleReceiverDisabled, true
	}
	if strings.Contains(message, "replaced") || strings.Contains(message, "superseded") || strings.Contains(message, "replacement") {
		return pairing.LifecycleReceiverReplaced, true
	}
	if statusCode == 401 {
		return pairing.LifecycleCredentialRevoked, true
	}
	if statusCode == 403 {
		if strings.Contains(message, "revoked") ||
			strings.Contains(message, "invalid") ||
			strings.Contains(message, "unauthorized") ||
			strings.Contains(message, "forbidden") ||
			strings.Contains(message, "auth") ||
			message == "" {
			return pairing.LifecycleCredentialRevoked, true
		}
	}
	if statusCode == 409 || statusCode == 410 {
		if strings.Contains(message, "replaced") || strings.Contains(message, "superseded") {
			return pairing.LifecycleReceiverReplaced, true
		}
		if strings.Contains(message, "revoked") || strings.Contains(message, "invalid") {
			return pairing.LifecycleCredentialRevoked, true
		}
	}
	return "", false
}

func lifecycleChangeReason(err error) string {
	var apiErr *cloudclient.APIError
	if errors.As(err, &apiErr) {
		message := strings.TrimSpace(apiErr.Message)
		if message == "" {
			return fmt.Sprintf("cloud lifecycle response status=%d", apiErr.StatusCode)
		}
		return message
	}
	return strings.TrimSpace(err.Error())
}

func lifecycleServiceMessage(change pairing.LifecycleChange) string {
	switch change {
	case pairing.LifecycleCredentialRevoked:
		return "receiver credential revoked; re-pair required"
	case pairing.LifecycleReceiverDisabled:
		return "receiver disabled by cloud; resolve policy and re-pair"
	case pairing.LifecycleReceiverReplaced:
		return "receiver replaced by newer install; re-pair this host if needed"
	default:
		return "receiver lifecycle changed; re-pair required"
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
