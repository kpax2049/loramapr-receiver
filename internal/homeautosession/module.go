package homeautosession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

type ModuleState string

const (
	StateDisabled      ModuleState = "disabled"
	StateMisconfigured ModuleState = "misconfigured"
	StateObserveReady  ModuleState = "observe_ready"
	StateControlReady  ModuleState = "control_ready"
	StateStartPending  ModuleState = "start_pending"
	StateActive        ModuleState = "active"
	StateStopPending   ModuleState = "stop_pending"
	StateCooldown      ModuleState = "cooldown"
	StateDegraded      ModuleState = "degraded"
)

const (
	ConfigSourceLocalFallback = "local_fallback"
	ConfigSourceCloudManaged  = "cloud_managed"
)

type ConfigApplyStatus struct {
	EffectiveSource    string
	EffectiveVersion   string
	CloudConfigPresent bool
	LastFetchedVersion string
	LastAppliedVersion string
	LastApplyResult    string
	LastApplyError     string
	DesiredEnabled     bool
	DesiredMode        string
}

type pendingActionKind string

const (
	actionStart pendingActionKind = "start"
	actionStop  pendingActionKind = "stop"
)

const (
	reconciliationCleanIdle              = "clean_idle"
	reconciliationStartupDisabled        = "startup_reconcile_disabled"
	reconciliationActiveRecovered        = "active_recovered_unverified"
	reconciliationPendingStartRecovering = "pending_start_recovering"
	reconciliationPendingStopRecovering  = "pending_stop_recovering"
	reconciliationPendingStartResolved   = "pending_start_resolved"
	reconciliationPendingStopResolved    = "pending_stop_resolved"
	reconciliationInconsistentDegraded   = "inconsistent_degraded"
	reconciliationConflictAlreadyActive  = "conflict_already_active"
	reconciliationConflictStateMismatch  = "conflict_state_mismatch"
	reconciliationLifecycleRevoked       = "lifecycle_revoked"
	reconciliationLifecycleDisabled      = "lifecycle_disabled"
	reconciliationLifecycleReplaced      = "lifecycle_replaced"
)

const (
	gpsStatusMissing           = "missing"
	gpsStatusInvalid           = "invalid"
	gpsStatusStale             = "stale"
	gpsStatusBoundaryUncertain = "boundary_uncertain"
	gpsStatusValid             = "valid"
)

const (
	controlStateDisabled          = "disabled"
	controlStateMisconfigured     = "misconfigured"
	controlStateReady             = "ready"
	controlStatePendingStart      = "pending_start"
	controlStatePendingStop       = "pending_stop"
	controlStateActive            = "active"
	controlStateCooldown          = "cooldown"
	controlStateConflictBlocked   = "conflict_blocked"
	controlStateLifecycleBlocked  = "lifecycle_blocked"
	controlStateDegraded          = "degraded"
	activeStateSourceNone         = "none"
	activeStateSourceCloudAck     = "cloud_acknowledged"
	activeStateSourceLocalRecover = "local_recovered_unverified"
	activeStateSourceConflict     = "conflict_unresolved"
)

const (
	observationQueueDepth   = 256
	controlCallTimeout      = 8 * time.Second
	retryCooldown           = 30 * time.Second
	retryCooldownMax        = 4 * time.Minute
	stopRetryQuickFirst     = 10 * time.Second
	stopRetryQuickSecond    = 20 * time.Second
	stopRetryQuickThird     = 30 * time.Second
	stopRetryBypassMin      = 5 * time.Second
	stopRetryFreshInsideMax = 45 * time.Second
	gpsStaleFloor           = 2 * time.Minute
	gpsStaleCeiling         = 10 * time.Minute
	decisionCooldownFloor   = 1 * time.Second
	decisionCooldownCeiling = 30 * time.Second
	boundaryMarginFloorM    = 8.0
	boundaryMarginCapM      = 75.0
	boundaryMarginFraction  = 0.08
)

type retryableErrorClass string

const (
	retryClassGenericRetryable retryableErrorClass = "generic_retryable"
	retryClassTimeoutNetwork   retryableErrorClass = "timeout_network"
)

type SessionClient interface {
	StartHomeAutoSession(ctx context.Context, startEndpoint string, apiKey string, request cloudclient.HomeAutoSessionStartRequest) (cloudclient.HomeAutoSessionStartResult, error)
	StopHomeAutoSession(ctx context.Context, stopEndpoint string, apiKey string, request cloudclient.HomeAutoSessionStopRequest) (cloudclient.HomeAutoSessionStopResult, error)
}

type Module struct {
	logger *slog.Logger
	store  *state.Store
	status *status.Model
	client SessionClient

	mu             sync.RWMutex
	cfg            config.HomeAutoSessionConfig
	configHash     string
	trackedByLower map[string]string
	state          ModuleState
	summary        string

	controlState        string
	activeStateSource   string
	trackedNodeState    string
	reconciliationState string
	blockedReason       string

	effectiveConfigSource  string
	effectiveConfigVersion string
	cloudConfigPresent     bool
	lastFetchedConfigVer   string
	lastAppliedConfigVer   string
	lastConfigApplyResult  string
	lastConfigApplyError   string
	desiredConfigEnabled   bool
	desiredConfigMode      string

	activeSessionID   string
	activeTriggerNode string
	lastDecision      string
	lastError         string
	lastStartDedupe   string
	lastStopDedupe    string
	lastAction        string
	lastActionResult  string
	lastActionAt      *time.Time

	lastSuccessfulAction   string
	lastSuccessfulActionAt *time.Time
	lastEventAt            *time.Time

	pendingAction *pendingAction

	cooldownUntil         *time.Time
	decisionCooldownUntil *time.Time
	consecutiveFailures   int
	lastRetryClass        string
	observedDropped       int

	gpsStatus    string
	gpsReason    string
	gpsNodeID    string
	gpsUpdatedAt *time.Time
	gpsDistanceM *float64

	startCandidate *transitionCandidate
	stopCandidate  *transitionCandidate
	nodeFacts      map[string]nodeFact

	events     chan meshtastic.Event
	reevaluate chan struct{}
	started    bool

	lastPersistFingerprint string
	lastLoggedState        string
	lastLoggedControlState string
	lastLoggedDecision     string
	lastLoggedAction       string
	lastLoggedResult       string
	lastLoggedSummary      string
}

type pendingAction struct {
	Action    pendingActionKind
	NodeID    string
	Reason    string
	DedupeKey string
	Since     time.Time
}

type transitionCandidate struct {
	NodeID    string
	At        time.Time
	Reason    string
	DedupeKey string
}

type nodeFact struct {
	HasPosition     bool
	InsideGeofence  bool
	LastLat         float64
	LastLon         float64
	LastDistanceM   float64
	LastSeenAt      time.Time
	LastTransition  time.Time
	LastOutsideSeen time.Time
}

func New(cfg config.HomeAutoSessionConfig, store *state.Store, statusModel *status.Model, logger *slog.Logger, client SessionClient) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Module{
		logger:                 logger.With("component", "home_auto_session"),
		store:                  store,
		status:                 statusModel,
		client:                 client,
		events:                 make(chan meshtastic.Event, observationQueueDepth),
		reevaluate:             make(chan struct{}, 1),
		nodeFacts:              make(map[string]nodeFact),
		trackedByLower:         make(map[string]string),
		state:                  StateDisabled,
		summary:                "module disabled",
		controlState:           controlStateDisabled,
		activeStateSource:      activeStateSourceNone,
		trackedNodeState:       "no tracked node data observed yet",
		reconciliationState:    reconciliationCleanIdle,
		gpsStatus:              gpsStatusMissing,
		gpsReason:              "waiting for tracked-node position updates",
		effectiveConfigSource:  ConfigSourceLocalFallback,
		effectiveConfigVersion: "local-default",
		lastAppliedConfigVer:   "local-default",
		lastConfigApplyResult:  "local_config_applied",
		desiredConfigEnabled:   cfg.Enabled,
		desiredConfigMode:      string(normalizeMode(cfg.Mode)),
	}
	if err := m.ApplyConfig(cfg); err != nil {
		m.lastConfigApplyError = err.Error()
		m.lastConfigApplyResult = "local_config_invalid"
	}
	return m
}

func (m *Module) Start(ctx context.Context) {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return
	}
	m.started = true
	m.bootstrapFromStateLocked()
	m.publishLocked()
	m.persistLocked()
	m.mu.Unlock()

	go m.run(ctx)
}

func (m *Module) ObserveEvent(event meshtastic.Event) {
	select {
	case m.events <- event:
	default:
		m.mu.Lock()
		m.observedDropped++
		m.lastError = "home auto session observation queue is full"
		m.markDegradedLocked(time.Now().UTC(), "event observation queue overflow", "manual action required after event queue overflow")
		m.mu.Unlock()
	}
}

func (m *Module) ApplyConfig(cfg config.HomeAutoSessionConfig) error {
	normalized := normalizeConfig(cfg)
	if err := validateConfig(normalized); err != nil {
		return err
	}
	hash := configHash(normalized)
	m.mu.Lock()
	if hash == m.configHash {
		m.mu.Unlock()
		return nil
	}
	m.cfg = normalized
	m.configHash = hash
	m.trackedByLower = make(map[string]string, len(normalized.TrackedNodeIDs))
	for _, id := range normalized.TrackedNodeIDs {
		m.trackedByLower[strings.ToLower(strings.TrimSpace(id))] = id
	}
	m.desiredConfigEnabled = normalized.Enabled
	m.desiredConfigMode = string(normalizeMode(normalized.Mode))
	m.publishLocked()
	m.mu.Unlock()
	return nil
}

func (m *Module) CurrentConfig() config.HomeAutoSessionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
}

func (m *Module) SetConfigApplyStatus(status ConfigApplyStatus) {
	m.mu.Lock()
	changed := m.setConfigApplyStatusLocked(status)
	if changed {
		m.publishLocked()
		m.persistLocked()
	}
	m.mu.Unlock()
}

func (m *Module) Reevaluate() {
	select {
	case m.reevaluate <- struct{}{}:
	default:
	}
}

func (m *Module) ResetDegraded() {
	m.mu.Lock()
	m.lastError = ""
	m.blockedReason = ""
	m.cooldownUntil = nil
	m.decisionCooldownUntil = nil
	m.stopCandidate = nil
	m.startCandidate = nil
	m.pendingAction = nil
	m.consecutiveFailures = 0
	m.lastRetryClass = ""
	m.summary = "degraded state reset"
	if m.state == StateDegraded {
		m.state = StateControlReady
	}
	if m.cfg.Enabled && normalizeMode(m.cfg.Mode) == config.HomeAutoSessionModeControl {
		m.controlState = controlStateReady
	} else if m.cfg.Enabled && normalizeMode(m.cfg.Mode) == config.HomeAutoSessionModeObserve {
		m.controlState = controlStateReady
	} else {
		m.controlState = controlStateDisabled
	}
	m.persistLocked()
	m.publishLocked()
	m.mu.Unlock()
	m.Reevaluate()
}

func (m *Module) run(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	m.mu.Lock()
	m.evaluateLocked(ctx, time.Now().UTC(), "startup")
	m.mu.Unlock()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-m.events:
			m.mu.Lock()
			m.consumeEventLocked(event)
			m.evaluateLocked(ctx, eventTime(event), "event")
			m.mu.Unlock()
		case <-ticker.C:
			m.mu.Lock()
			m.evaluateLocked(ctx, time.Now().UTC(), "tick")
			m.mu.Unlock()
		case <-m.reevaluate:
			m.mu.Lock()
			m.evaluateLocked(ctx, time.Now().UTC(), "reevaluate")
			m.mu.Unlock()
		}
	}
}

func (m *Module) bootstrapFromStateLocked() {
	if m.store == nil {
		if m.reconciliationState == "" {
			m.reconciliationState = reconciliationCleanIdle
		}
		return
	}

	snap := m.store.Snapshot().HomeAutoSession
	m.lastPersistFingerprint = homeAutoPersistFingerprint(snap)
	m.activeSessionID = strings.TrimSpace(snap.ActiveSessionID)
	m.activeTriggerNode = strings.TrimSpace(snap.ActiveTriggerNode)
	m.controlState = strings.TrimSpace(snap.ControlState)
	if m.controlState == "" {
		m.controlState = controlStateDisabled
	}
	m.activeStateSource = strings.TrimSpace(snap.ActiveStateSource)
	if m.activeStateSource == "" {
		m.activeStateSource = activeStateSourceNone
	}
	m.lastDecision = strings.TrimSpace(snap.LastDecisionReason)
	m.lastStartDedupe = strings.TrimSpace(snap.LastStartDedupeKey)
	m.lastStopDedupe = strings.TrimSpace(snap.LastStopDedupeKey)
	m.lastAction = strings.TrimSpace(snap.LastAction)
	m.lastActionResult = strings.TrimSpace(snap.LastActionResult)
	m.lastActionAt = cloneTimePtr(snap.LastActionAt)
	m.lastSuccessfulAction = strings.TrimSpace(snap.LastSuccessfulAction)
	m.lastSuccessfulActionAt = cloneTimePtr(snap.LastSuccessfulActionAt)
	m.lastError = strings.TrimSpace(snap.LastError)
	m.blockedReason = strings.TrimSpace(snap.BlockedReason)
	m.consecutiveFailures = snap.ConsecutiveFailures
	m.cooldownUntil = cloneTimePtr(snap.CooldownUntil)
	m.decisionCooldownUntil = cloneTimePtr(snap.DecisionCooldownUntil)
	m.lastEventAt = cloneTimePtr(snap.LastEventAt)
	m.observedDropped = snap.ObservedDropped
	m.gpsStatus = strings.TrimSpace(snap.GPSStatus)
	m.gpsReason = strings.TrimSpace(snap.GPSReason)
	m.gpsNodeID = strings.TrimSpace(snap.GPSNodeID)
	m.gpsUpdatedAt = cloneTimePtr(snap.GPSUpdatedAt)
	m.gpsDistanceM = cloneFloat64Ptr(snap.GPSDistanceM)
	m.reconciliationState = strings.TrimSpace(snap.ReconciliationState)
	m.effectiveConfigSource = strings.TrimSpace(snap.EffectiveConfigSource)
	if m.effectiveConfigSource == "" {
		m.effectiveConfigSource = ConfigSourceLocalFallback
	}
	m.effectiveConfigVersion = strings.TrimSpace(snap.EffectiveConfigVersion)
	if m.effectiveConfigVersion == "" {
		m.effectiveConfigVersion = "local-default"
	}
	m.cloudConfigPresent = snap.CloudConfigPresent
	m.lastFetchedConfigVer = strings.TrimSpace(snap.LastFetchedConfigVer)
	m.lastAppliedConfigVer = strings.TrimSpace(snap.LastAppliedConfigVer)
	if m.lastAppliedConfigVer == "" {
		m.lastAppliedConfigVer = m.effectiveConfigVersion
	}
	m.lastConfigApplyResult = strings.TrimSpace(snap.LastConfigApplyResult)
	if m.lastConfigApplyResult == "" {
		m.lastConfigApplyResult = "local_config_applied"
	}
	m.lastConfigApplyError = strings.TrimSpace(snap.LastConfigApplyError)
	if snap.DesiredConfigEnabled != nil {
		m.desiredConfigEnabled = *snap.DesiredConfigEnabled
	}
	m.desiredConfigMode = strings.TrimSpace(snap.DesiredConfigMode)
	if m.desiredConfigMode == "" {
		m.desiredConfigMode = string(normalizeMode(m.cfg.Mode))
	}

	if strings.TrimSpace(snap.ModuleState) != "" {
		m.state = ModuleState(strings.TrimSpace(snap.ModuleState))
	}

	pendingActionCode := normalizePendingAction(strings.TrimSpace(snap.PendingAction))
	if pendingActionCode != "" {
		since := time.Now().UTC()
		if snap.PendingSince != nil {
			since = snap.PendingSince.UTC()
		}
		m.pendingAction = &pendingAction{
			Action:    pendingActionCode,
			NodeID:    strings.TrimSpace(snap.PendingTriggerNode),
			Reason:    strings.TrimSpace(snap.PendingReason),
			DedupeKey: strings.TrimSpace(snap.PendingDedupeKey),
			Since:     since,
		}
	}

	if !m.cfg.StartupReconcile {
		if m.activeSessionID != "" || m.pendingAction != nil {
			m.lastDecision = "startup reconcile disabled; cleared prior local session markers"
		}
		m.activeSessionID = ""
		m.activeTriggerNode = ""
		m.pendingAction = nil
		m.startCandidate = nil
		m.stopCandidate = nil
		m.controlState = controlStateDisabled
		m.activeStateSource = activeStateSourceNone
		m.reconciliationState = reconciliationStartupDisabled
		m.blockedReason = ""
		return
	}

	m.reconcileStartupLocked(time.Now().UTC())
}

func (m *Module) reconcileStartupLocked(now time.Time) {
	if m.pendingAction != nil {
		pending := *m.pendingAction
		if strings.TrimSpace(pending.NodeID) == "" || strings.TrimSpace(pending.DedupeKey) == "" {
			m.markStartupInconsistentLocked(now, "pending action state is incomplete")
			return
		}
		if pending.Action == actionStart {
			if pending.DedupeKey == m.lastStartDedupe {
				m.pendingAction = nil
				m.reconciliationState = reconciliationPendingStartResolved
				m.activeStateSource = activeStateSourceCloudAck
				m.lastDecision = "pending start action already completed before restart"
			} else {
				m.startCandidate = &transitionCandidate{
					NodeID:    pending.NodeID,
					At:        pending.Since,
					Reason:    pending.Reason,
					DedupeKey: pending.DedupeKey,
				}
				m.stopCandidate = nil
				m.reconciliationState = reconciliationPendingStartRecovering
				m.lastDecision = "recovering pending start action after restart"
			}
		} else {
			if m.activeSessionID == "" || pending.DedupeKey == m.lastStopDedupe {
				m.pendingAction = nil
				m.reconciliationState = reconciliationPendingStopResolved
				m.activeStateSource = activeStateSourceCloudAck
				m.lastDecision = "pending stop action already resolved before restart"
			} else {
				m.stopCandidate = &transitionCandidate{
					NodeID:    pending.NodeID,
					At:        pending.Since,
					Reason:    pending.Reason,
					DedupeKey: pending.DedupeKey,
				}
				m.startCandidate = nil
				m.reconciliationState = reconciliationPendingStopRecovering
				m.lastDecision = "recovering pending stop action after restart"
			}
		}
	}

	if m.activeSessionID != "" {
		if strings.TrimSpace(m.activeTriggerNode) == "" {
			m.markStartupInconsistentLocked(now, "active session state is missing trigger node")
			return
		}
		m.reconciliationState = reconciliationActiveRecovered
		m.activeStateSource = activeStateSourceLocalRecover
		if m.summary == "" {
			m.summary = "recovered active home auto session from local state"
		}
		m.state = StateActive
		return
	}

	if m.activeSessionID == "" && strings.TrimSpace(m.activeTriggerNode) != "" {
		m.markStartupInconsistentLocked(now, "trigger node persisted without active session")
		return
	}

	m.reconciliationState = reconciliationCleanIdle
	m.activeStateSource = activeStateSourceNone
	if m.lastDecision == "" {
		m.lastDecision = "startup reconciliation complete"
	}
}

func (m *Module) markStartupInconsistentLocked(now time.Time, reason string) {
	m.reconciliationState = reconciliationInconsistentDegraded
	m.controlState = controlStateConflictBlocked
	m.activeStateSource = activeStateSourceConflict
	m.lastError = strings.TrimSpace(reason)
	m.markDegradedLocked(now, "local persisted state is inconsistent", "manual reset required before control actions")
}

func (m *Module) consumeEventLocked(event meshtastic.Event) {
	cfg := m.cfg
	if !cfg.Enabled || strings.EqualFold(string(cfg.Mode), string(config.HomeAutoSessionModeOff)) {
		return
	}
	if event.Packet == nil {
		return
	}
	nodeID := strings.TrimSpace(event.Packet.SourceNodeID)
	if nodeID == "" {
		return
	}
	nodeKey := strings.ToLower(nodeID)
	if _, ok := m.trackedByLower[nodeKey]; !ok {
		return
	}

	now := eventTime(event)
	m.lastEventAt = cloneTime(now)

	fact := m.nodeFacts[nodeKey]
	fact.LastSeenAt = now

	if event.Packet.Position == nil {
		m.trackedNodeState = fmt.Sprintf("node %s seen without position fix", nodeID)
		m.setGPSStatusLocked(gpsStatusMissing, "waiting for tracked-node position updates", nodeID, nil, now)
		m.nodeFacts[nodeKey] = fact
		return
	}

	lat := event.Packet.Position.Lat
	lon := event.Packet.Position.Lon
	if !coordinatesValid(lat, lon) {
		m.trackedNodeState = fmt.Sprintf("node %s reported invalid coordinates", nodeID)
		m.setGPSStatusLocked(gpsStatusInvalid, "ignored invalid GPS coordinates from tracked node", nodeID, nil, now)
		m.nodeFacts[nodeKey] = fact
		return
	}

	if positionIsStale(now, stalePositionThreshold(cfg)) {
		reason := fmt.Sprintf("position sample is stale (older than %s)", stalePositionThreshold(cfg).Round(time.Second))
		m.trackedNodeState = fmt.Sprintf("node %s position stale", nodeID)
		m.setGPSStatusLocked(gpsStatusStale, reason, nodeID, nil, time.Now().UTC())
		m.nodeFacts[nodeKey] = fact
		return
	}

	distance := haversineMeters(cfg.Home.Lat, cfg.Home.Lon, lat, lon)
	margin := geofenceBoundaryMargin(cfg.Home.RadiusM)
	if math.Abs(distance-cfg.Home.RadiusM) <= margin {
		reason := fmt.Sprintf("position is near boundary (±%.0fm uncertainty)", margin)
		m.trackedNodeState = fmt.Sprintf("node %s near geofence boundary", nodeID)
		m.setGPSStatusLocked(gpsStatusBoundaryUncertain, reason, nodeID, &distance, now)
		fact.LastLat = lat
		fact.LastLon = lon
		fact.LastDistanceM = distance
		m.nodeFacts[nodeKey] = fact
		return
	}

	inside := distance <= cfg.Home.RadiusM
	if !inside {
		fact.LastOutsideSeen = now
	}
	if fact.HasPosition {
		if fact.InsideGeofence && !inside {
			m.startCandidate = &transitionCandidate{
				NodeID: nodeID,
				At:     now,
				Reason: "tracked node moved outside home geofence",
			}
			m.stopCandidate = nil
		}
		if !fact.InsideGeofence && inside {
			m.stopCandidate = &transitionCandidate{
				NodeID: nodeID,
				At:     now,
				Reason: "tracked node returned inside home geofence",
			}
			m.startCandidate = nil
		}
		if fact.InsideGeofence != inside {
			fact.LastTransition = now
		}
	}

	fact.HasPosition = true
	fact.InsideGeofence = inside
	fact.LastLat = lat
	fact.LastLon = lon
	fact.LastDistanceM = distance
	m.nodeFacts[nodeKey] = fact
	if inside {
		m.trackedNodeState = fmt.Sprintf("node %s is inside home geofence", nodeID)
	} else {
		m.trackedNodeState = fmt.Sprintf("node %s is outside home geofence", nodeID)
	}
	m.setGPSStatusLocked(gpsStatusValid, "tracked node position is valid for geofence decisions", nodeID, &distance, now)
}

func (m *Module) setGPSStatusLocked(code, reason, nodeID string, distanceM *float64, at time.Time) {
	m.gpsStatus = strings.TrimSpace(code)
	m.gpsReason = strings.TrimSpace(reason)
	m.gpsNodeID = strings.TrimSpace(nodeID)
	if m.gpsNodeID == "" {
		m.gpsNodeID = ""
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	m.gpsUpdatedAt = cloneTime(at)
	m.gpsDistanceM = cloneFloat64Ptr(distanceM)
}

func (m *Module) evaluateLocked(ctx context.Context, now time.Time, trigger string) {
	cfg := m.cfg
	mode := normalizeMode(cfg.Mode)

	if !cfg.Enabled || mode == config.HomeAutoSessionModeOff {
		m.state = StateDisabled
		m.controlState = controlStateDisabled
		m.activeStateSource = activeStateSourceNone
		m.summary = "home auto session is disabled"
		m.lastDecision = "module disabled"
		m.publishLocked()
		m.persistLocked()
		return
	}

	if err := validateConfig(cfg); err != nil {
		m.state = StateMisconfigured
		m.controlState = controlStateMisconfigured
		m.activeStateSource = activeStateSourceNone
		m.summary = err.Error()
		m.lastDecision = "configuration invalid"
		m.publishLocked()
		m.persistLocked()
		return
	}

	if m.state == StateDegraded && strings.TrimSpace(m.blockedReason) != "" {
		m.summary = "home auto session is degraded"
		if m.lastDecision == "" {
			m.lastDecision = "manual action required to recover control mode"
		}
		if m.controlState == "" {
			m.controlState = controlStateDegraded
		}
		m.publishLocked()
		m.persistLocked()
		return
	}

	if m.cooldownUntil != nil && now.Before(m.cooldownUntil.UTC()) {
		if m.shouldBypassStopRetryCooldownLocked(now, trigger) {
			m.cooldownUntil = nil
			m.lastDecision = "fresh inside-geofence update received; retrying stop during cooldown"
		} else {
			cooldownETA := m.cooldownUntil.UTC()
			m.state = StateCooldown
			m.controlState = controlStateCooldown
			if m.pendingAction != nil &&
				m.pendingAction.Action == actionStop &&
				m.lastActionResult == "retry_scheduled" &&
				m.lastRetryClass == string(retryClassTimeoutNetwork) {
				m.summary = stopRetryPendingSummary(m.consecutiveFailures, cooldownETA)
				m.lastDecision = fmt.Sprintf(
					"waiting for stop retry cooldown (attempt %d, next retry %s)",
					m.consecutiveFailures,
					cooldownETA.Format(time.RFC3339),
				)
			} else {
				m.summary = "cooldown after prior cloud/session error"
				m.lastDecision = "waiting for cooldown window before retry"
			}
			m.publishLocked()
			m.persistLocked()
			return
		}
	}
	if m.decisionCooldownUntil != nil && now.Before(m.decisionCooldownUntil.UTC()) {
		m.state = StateCooldown
		m.controlState = controlStateCooldown
		m.summary = "stabilization cooldown after prior home-auto action"
		m.lastDecision = "waiting for geofence stabilization window"
		m.publishLocked()
		m.persistLocked()
		return
	}

	m.promotePendingActionLocked()

	if m.activeSessionID != "" {
		if m.stopCandidate == nil && m.shouldStopByIdleLocked(now, cfg) {
			nodeID := m.activeTriggerNode
			if strings.TrimSpace(nodeID) == "" {
				nodeID = m.gpsNodeID
			}
			if strings.TrimSpace(nodeID) == "" {
				nodeID = "unknown"
			}
			m.stopCandidate = &transitionCandidate{
				NodeID: nodeID,
				At:     now,
				Reason: "idle stop timeout elapsed without tracked-node updates",
			}
		}
		if m.stopCandidate != nil {
			if now.Sub(m.stopCandidate.At) < cfg.StopDebounce.Std() {
				m.state = StateStopPending
				m.controlState = controlStatePendingStop
				m.summary = "waiting for stop debounce window"
				m.lastDecision = fmt.Sprintf("stop candidate pending for node %s", m.stopCandidate.NodeID)
				m.publishLocked()
				m.persistLocked()
				return
			}
			if mode == config.HomeAutoSessionModeObserve {
				m.state = StateActive
				m.controlState = controlStateActive
				m.summary = "would stop active session now, but observe mode is enabled"
				m.lastDecision = "observe mode: stop call suppressed"
				m.publishLocked()
				m.persistLocked()
				return
			}
			m.attemptStopLocked(ctx, now, cfg, trigger)
			return
		}
		m.state = StateActive
		m.controlState = controlStateActive
		if strings.TrimSpace(m.activeStateSource) == "" || m.activeStateSource == activeStateSourceNone {
			m.activeStateSource = activeStateSourceLocalRecover
		}
		m.summary = "home auto session is active"
		m.publishLocked()
		m.persistLocked()
		return
	}

	if m.startCandidate != nil {
		if now.Sub(m.startCandidate.At) < cfg.StartDebounce.Std() {
			m.state = StateStartPending
			m.controlState = controlStatePendingStart
			m.summary = "waiting for start debounce window"
			m.lastDecision = fmt.Sprintf("start candidate pending for node %s", m.startCandidate.NodeID)
			m.publishLocked()
			m.persistLocked()
			return
		}

		if mode == config.HomeAutoSessionModeObserve {
			m.state = StateObserveReady
			m.controlState = controlStateReady
			m.summary = "would start session now, but observe mode is enabled"
			m.lastDecision = "observe mode: start call suppressed"
			m.publishLocked()
			m.persistLocked()
			return
		}
		m.attemptStartLocked(ctx, now, cfg, trigger)
		return
	}

	if mode == config.HomeAutoSessionModeControl {
		m.state = StateControlReady
		m.controlState = controlStateReady
		m.activeStateSource = activeStateSourceNone
		m.summary = readySummaryForGPS(m.gpsStatus, m.gpsReason, "waiting for tracked-node geofence transition")
		m.lastDecision = "control mode ready"
	} else {
		m.state = StateObserveReady
		m.controlState = controlStateReady
		m.activeStateSource = activeStateSourceNone
		m.summary = readySummaryForGPS(m.gpsStatus, m.gpsReason, "waiting for tracked-node geofence transition")
		m.lastDecision = "observe mode ready"
	}
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) promotePendingActionLocked() {
	if m.pendingAction == nil {
		return
	}
	pending := *m.pendingAction
	candidate := &transitionCandidate{
		NodeID:    pending.NodeID,
		At:        pending.Since,
		Reason:    pending.Reason,
		DedupeKey: pending.DedupeKey,
	}
	if pending.Action == actionStart {
		m.startCandidate = candidate
		m.stopCandidate = nil
	} else if pending.Action == actionStop {
		m.stopCandidate = candidate
		m.startCandidate = nil
	}
}

func (m *Module) attemptStartLocked(ctx context.Context, now time.Time, cfg config.HomeAutoSessionConfig, trigger string) {
	if m.startCandidate == nil {
		return
	}

	candidate := *m.startCandidate
	if candidate.DedupeKey == "" {
		candidate.DedupeKey = dedupeKey(string(actionStart), candidate.NodeID, candidate.At, candidate.Reason)
	}
	if strings.TrimSpace(candidate.DedupeKey) == strings.TrimSpace(m.lastStartDedupe) {
		m.startCandidate = nil
		m.pendingAction = nil
		m.lastAction = string(actionStart)
		m.lastActionAt = cloneTime(now)
		m.lastActionResult = "dedupe_suppressed"
		m.state = StateControlReady
		m.controlState = controlStateReady
		m.summary = "duplicate start decision suppressed"
		m.lastDecision = "start dedupe key already executed"
		m.publishLocked()
		m.persistLocked()
		return
	}
	apiKey, startEndpoint, ok := m.resolveControlAuthLocked(cfg, actionStart)
	if !ok {
		m.lastAction = string(actionStart)
		m.lastActionAt = cloneTime(now)
		m.lastActionResult = "deferred_not_paired"
		m.state = StateControlReady
		m.controlState = controlStateReady
		m.summary = "waiting for paired receiver credentials before control actions"
		m.lastDecision = "control action deferred: receiver not fully paired"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.client == nil {
		m.lastAction = string(actionStart)
		m.lastActionAt = cloneTime(now)
		m.lastActionResult = "failed_client_unavailable"
		m.lastError = "home auto session cloud client is unavailable"
		m.markDegradedLocked(now, "cloud session client is unavailable", "manual action required to recover control mode")
		return
	}

	m.lastAction = string(actionStart)
	m.lastActionAt = cloneTime(now)
	m.lastActionResult = "attempting"
	m.setPendingActionLocked(actionStart, candidate, now)
	m.state = StateStartPending
	m.controlState = controlStatePendingStart
	m.summary = "executing start request"
	m.lastDecision = fmt.Sprintf("starting session for node %s", candidate.NodeID)
	m.publishLocked()
	m.persistLocked()

	name, notes := renderSessionText(cfg, candidate.NodeID)
	request := cloudclient.HomeAutoSessionStartRequest{
		TriggerNodeID: candidate.NodeID,
		DedupeKey:     candidate.DedupeKey,
		Reason:        candidate.Reason,
		SessionName:   name,
		SessionNotes:  notes,
		StartedAt:     now.Format(time.RFC3339),
		Home: map[string]any{
			"lat":      cfg.Home.Lat,
			"lon":      cfg.Home.Lon,
			"radius_m": cfg.Home.RadiusM,
		},
	}

	callCtx, cancel := context.WithTimeout(ctx, controlCallTimeout)
	defer cancel()
	result, err := m.client.StartHomeAutoSession(callCtx, startEndpoint, apiKey, request)
	if err != nil {
		m.handleCloudErrorLocked(now, actionStart, err)
		return
	}

	startedAt := now
	if !result.StartedAt.IsZero() {
		startedAt = result.StartedAt.UTC()
	}
	m.pendingAction = nil
	m.startCandidate = nil
	m.stopCandidate = nil
	m.lastError = ""
	m.blockedReason = ""
	m.cooldownUntil = nil
	m.consecutiveFailures = 0
	m.lastRetryClass = ""
	m.lastStartDedupe = candidate.DedupeKey
	m.activeSessionID = strings.TrimSpace(result.SessionID)
	m.activeTriggerNode = candidate.NodeID
	m.activeStateSource = activeStateSourceCloudAck
	m.lastActionResult = "started"
	m.lastSuccessfulAction = string(actionStart)
	m.lastSuccessfulActionAt = cloneTime(startedAt)
	decisionCooldown := now.Add(decisionCooldownDuration(cfg))
	m.decisionCooldownUntil = &decisionCooldown
	m.state = StateActive
	m.controlState = controlStateActive
	m.summary = "home auto session started"
	m.lastDecision = fmt.Sprintf("session start executed via trigger %s", candidate.NodeID)
	m.logger.Info("home auto session started", "trigger_node", candidate.NodeID, "session_id", m.activeSessionID, "trigger", trigger)
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) attemptStopLocked(ctx context.Context, now time.Time, cfg config.HomeAutoSessionConfig, trigger string) {
	if m.stopCandidate == nil {
		return
	}
	candidate := *m.stopCandidate
	if candidate.DedupeKey == "" {
		candidate.DedupeKey = dedupeKey(string(actionStop), candidate.NodeID, candidate.At, candidate.Reason)
	}
	if strings.TrimSpace(candidate.DedupeKey) == strings.TrimSpace(m.lastStopDedupe) {
		m.stopCandidate = nil
		m.pendingAction = nil
		m.lastAction = string(actionStop)
		m.lastActionAt = cloneTime(now)
		m.lastActionResult = "dedupe_suppressed"
		m.state = StateControlReady
		m.controlState = controlStateReady
		m.summary = "duplicate stop decision suppressed"
		m.lastDecision = "stop dedupe key already executed"
		m.publishLocked()
		m.persistLocked()
		return
	}
	apiKey, stopEndpoint, ok := m.resolveControlAuthLocked(cfg, actionStop)
	if !ok {
		m.lastAction = string(actionStop)
		m.lastActionAt = cloneTime(now)
		m.lastActionResult = "deferred_not_paired"
		m.state = StateControlReady
		m.controlState = controlStateReady
		m.summary = "waiting for paired receiver credentials before control actions"
		m.lastDecision = "control action deferred: receiver not fully paired"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.client == nil {
		m.lastAction = string(actionStop)
		m.lastActionAt = cloneTime(now)
		m.lastActionResult = "failed_client_unavailable"
		m.lastError = "home auto session cloud client is unavailable"
		m.markDegradedLocked(now, "cloud session client is unavailable", "manual action required to recover control mode")
		return
	}

	m.lastAction = string(actionStop)
	m.lastActionAt = cloneTime(now)
	m.lastActionResult = "attempting"
	m.setPendingActionLocked(actionStop, candidate, now)
	m.state = StateStopPending
	m.controlState = controlStatePendingStop
	m.summary = "executing stop request"
	m.lastDecision = fmt.Sprintf("stopping session for node %s", candidate.NodeID)
	m.publishLocked()
	m.persistLocked()

	request := cloudclient.HomeAutoSessionStopRequest{
		SessionID:     m.activeSessionID,
		TriggerNodeID: candidate.NodeID,
		DedupeKey:     candidate.DedupeKey,
		Reason:        candidate.Reason,
		StoppedAt:     now.Format(time.RFC3339),
	}
	callCtx, cancel := context.WithTimeout(ctx, controlCallTimeout)
	defer cancel()
	result, err := m.client.StopHomeAutoSession(callCtx, stopEndpoint, apiKey, request)
	if err != nil {
		if stopAlreadyResolvedError(err) {
			m.completeStopLocked(candidate, now, trigger, cloudclient.HomeAutoSessionStopResult{SessionID: m.activeSessionID, StoppedAt: now, Status: "already_stopped"})
			return
		}
		m.handleCloudErrorLocked(now, actionStop, err)
		return
	}

	m.completeStopLocked(candidate, now, trigger, result)
}

func (m *Module) completeStopLocked(candidate transitionCandidate, now time.Time, trigger string, result cloudclient.HomeAutoSessionStopResult) {
	stoppedAt := now
	if !result.StoppedAt.IsZero() {
		stoppedAt = result.StoppedAt.UTC()
	}
	m.pendingAction = nil
	m.stopCandidate = nil
	m.startCandidate = nil
	m.lastError = ""
	m.blockedReason = ""
	m.cooldownUntil = nil
	m.consecutiveFailures = 0
	m.lastRetryClass = ""
	m.lastStopDedupe = candidate.DedupeKey
	m.activeSessionID = ""
	m.activeTriggerNode = ""
	m.activeStateSource = activeStateSourceNone
	m.lastAction = string(actionStop)
	m.lastActionAt = cloneTime(now)
	if strings.EqualFold(strings.TrimSpace(result.Status), "already_stopped") {
		m.lastActionResult = "already_closed_resolved"
	} else {
		m.lastActionResult = "stopped"
	}
	m.lastSuccessfulAction = string(actionStop)
	m.lastSuccessfulActionAt = cloneTime(stoppedAt)
	decisionCooldown := now.Add(decisionCooldownDuration(m.cfg))
	m.decisionCooldownUntil = &decisionCooldown
	m.state = StateControlReady
	m.controlState = controlStateReady
	m.summary = "home auto session stopped"
	m.lastDecision = "session stop executed"
	m.logger.Info("home auto session stopped", "trigger_node", candidate.NodeID, "trigger", trigger, "status", strings.TrimSpace(result.Status))
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) setPendingActionLocked(action pendingActionKind, candidate transitionCandidate, now time.Time) {
	since := candidate.At.UTC()
	if since.IsZero() || since.After(now) {
		since = now.UTC()
	}
	m.pendingAction = &pendingAction{
		Action:    action,
		NodeID:    strings.TrimSpace(candidate.NodeID),
		Reason:    strings.TrimSpace(candidate.Reason),
		DedupeKey: strings.TrimSpace(candidate.DedupeKey),
		Since:     since,
	}
}

func (m *Module) setConfigApplyStatusLocked(next ConfigApplyStatus) bool {
	source := strings.TrimSpace(next.EffectiveSource)
	switch source {
	case ConfigSourceCloudManaged, ConfigSourceLocalFallback:
	default:
		source = ConfigSourceLocalFallback
	}

	effectiveVersion := strings.TrimSpace(next.EffectiveVersion)
	if effectiveVersion == "" && source == ConfigSourceLocalFallback {
		effectiveVersion = "local-default"
	}
	lastFetched := strings.TrimSpace(next.LastFetchedVersion)
	lastApplied := strings.TrimSpace(next.LastAppliedVersion)
	if lastApplied == "" {
		lastApplied = effectiveVersion
	}
	lastResult := strings.TrimSpace(next.LastApplyResult)
	if lastResult == "" {
		lastResult = "config_apply_unknown"
	}
	lastErr := strings.TrimSpace(next.LastApplyError)
	desiredMode := strings.TrimSpace(next.DesiredMode)
	if desiredMode == "" {
		desiredMode = string(normalizeMode(m.cfg.Mode))
	} else {
		desiredMode = string(normalizeMode(config.HomeAutoSessionMode(desiredMode)))
	}
	desiredEnabled := next.DesiredEnabled

	changed := false
	if m.effectiveConfigSource != source {
		m.effectiveConfigSource = source
		changed = true
	}
	if m.effectiveConfigVersion != effectiveVersion {
		m.effectiveConfigVersion = effectiveVersion
		changed = true
	}
	if m.cloudConfigPresent != next.CloudConfigPresent {
		m.cloudConfigPresent = next.CloudConfigPresent
		changed = true
	}
	if m.lastFetchedConfigVer != lastFetched {
		m.lastFetchedConfigVer = lastFetched
		changed = true
	}
	if m.lastAppliedConfigVer != lastApplied {
		m.lastAppliedConfigVer = lastApplied
		changed = true
	}
	if m.lastConfigApplyResult != lastResult {
		m.lastConfigApplyResult = lastResult
		changed = true
	}
	if m.lastConfigApplyError != lastErr {
		m.lastConfigApplyError = lastErr
		changed = true
	}
	if m.desiredConfigEnabled != desiredEnabled {
		m.desiredConfigEnabled = desiredEnabled
		changed = true
	}
	if m.desiredConfigMode != desiredMode {
		m.desiredConfigMode = desiredMode
		changed = true
	}
	return changed
}

func (m *Module) resolveControlAuthLocked(cfg config.HomeAutoSessionConfig, action pendingActionKind) (string, string, bool) {
	if m.store == nil {
		return "", "", false
	}
	snap := m.store.Snapshot()
	if snap.Pairing.Phase != state.PairingSteadyState {
		return "", "", false
	}
	apiKey := strings.TrimSpace(snap.Cloud.IngestAPIKey)
	if apiKey == "" {
		return "", "", false
	}
	if action == actionStop {
		stopEndpoint := strings.TrimSpace(cfg.Cloud.StopEndpoint)
		if stopEndpoint == "" {
			stopEndpoint = "/api/receiver/home-auto-session/stop"
		}
		return apiKey, stopEndpoint, true
	}
	endpoint := strings.TrimSpace(cfg.Cloud.StartEndpoint)
	if endpoint == "" {
		endpoint = "/api/receiver/home-auto-session/start"
	}
	return apiKey, endpoint, true
}

func (m *Module) shouldStopByIdleLocked(now time.Time, cfg config.HomeAutoSessionConfig) bool {
	if m.activeTriggerNode == "" {
		return false
	}
	timeout := cfg.IdleStopTimeout.Std()
	if timeout <= 0 {
		return false
	}
	fact, ok := m.nodeFacts[strings.ToLower(strings.TrimSpace(m.activeTriggerNode))]
	if !ok || !fact.HasPosition {
		// Keep active session running when trigger-node position state is unknown.
		return false
	}
	if !fact.InsideGeofence {
		// Do not auto-stop just because trigger-node updates are temporarily missing
		// while it was last seen outside home.
		return false
	}
	if fact.LastSeenAt.IsZero() {
		return false
	}
	return now.Sub(fact.LastSeenAt) >= timeout
}

func (m *Module) handleCloudErrorLocked(now time.Time, action pendingActionKind, err error) {
	actionName := string(action)
	errText := strings.TrimSpace(err.Error())
	if errText == "" {
		errText = "cloud/session API error"
	}
	m.lastError = errText
	m.lastAction = actionName
	m.lastActionAt = cloneTime(now)
	m.consecutiveFailures++
	m.logger.Warn("home auto session cloud action failed", "action", actionName, "err", err)

	if lifecycle, ok := lifecycleConflictFromError(err); ok {
		m.lastRetryClass = ""
		m.lastActionResult = "rejected_" + lifecycle
		m.pendingAction = nil
		m.startCandidate = nil
		m.stopCandidate = nil
		m.cooldownUntil = nil
		m.activeSessionID = ""
		m.activeTriggerNode = ""
		m.activeStateSource = activeStateSourceConflict
		switch lifecycle {
		case "revoked":
			m.reconciliationState = reconciliationLifecycleRevoked
			m.markLifecycleBlockedLocked(now, "receiver credential revoked by cloud", "home auto control blocked: receiver credential revoked")
		case "disabled":
			m.reconciliationState = reconciliationLifecycleDisabled
			m.markLifecycleBlockedLocked(now, "receiver disabled by cloud", "home auto control blocked: receiver disabled")
		case "replaced":
			m.reconciliationState = reconciliationLifecycleReplaced
			m.markLifecycleBlockedLocked(now, "receiver replaced by another install", "home auto control blocked: receiver replaced")
		default:
			m.reconciliationState = reconciliationConflictStateMismatch
			m.markConflictBlockedLocked(now, "lifecycle conflict", "home auto control blocked by lifecycle conflict")
		}
		m.logger.Error(
			"home auto session action blocked by lifecycle conflict",
			"action",
			actionName,
			"lifecycle",
			lifecycle,
			"blocked_reason",
			m.blockedReason,
		)
		return
	}

	if action == actionStart && startAlreadyActiveConflict(err) {
		m.lastRetryClass = ""
		m.lastActionResult = "already_active_conflict"
		m.pendingAction = nil
		m.startCandidate = nil
		m.stopCandidate = nil
		m.reconciliationState = reconciliationConflictAlreadyActive
		m.activeStateSource = activeStateSourceConflict
		m.markConflictBlockedLocked(now, "cloud reports an active session already exists", "start blocked: cloud/local state disagreement")
		m.logger.Warn(
			"home auto session start blocked by cloud/local conflict",
			"action",
			actionName,
			"blocked_reason",
			m.blockedReason,
		)
		return
	}

	if action == actionStop && stopStateMismatchConflict(err) {
		m.lastRetryClass = ""
		m.lastActionResult = "state_mismatch_conflict"
		m.pendingAction = nil
		m.startCandidate = nil
		m.stopCandidate = nil
		m.reconciliationState = reconciliationConflictStateMismatch
		m.activeStateSource = activeStateSourceConflict
		m.markConflictBlockedLocked(now, "cloud rejected stop because local and cloud session state disagree", "stop blocked: cloud/local state disagreement")
		m.logger.Warn(
			"home auto session stop blocked by cloud/local conflict",
			"action",
			actionName,
			"blocked_reason",
			m.blockedReason,
		)
		return
	}

	if cloudclient.IsRetryable(err) {
		retryClass := classifyRetryableCloudError(err)
		delay := retryDelayFor(action, retryClass, m.consecutiveFailures)
		cooldown := now.Add(delay)
		m.cooldownUntil = &cooldown
		m.blockedReason = ""
		m.state = StateCooldown
		m.controlState = controlStateCooldown
		m.lastRetryClass = string(retryClass)
		m.lastActionResult = "retry_scheduled"
		if action == actionStop && retryClass == retryClassTimeoutNetwork {
			m.summary = stopRetryPendingSummary(m.consecutiveFailures, cooldown.UTC())
			m.lastDecision = fmt.Sprintf(
				"stop retry attempt %d scheduled in %s (next retry %s)",
				m.consecutiveFailures,
				delay.Round(time.Second),
				cooldown.UTC().Format(time.RFC3339),
			)
		} else {
			m.summary = "cloud/session API unavailable, retrying after cooldown"
			m.lastDecision = fmt.Sprintf("%s action retry scheduled after %s", actionName, delay.Round(time.Second))
		}
		m.logger.Warn(
			"home auto session retry scheduled",
			"action",
			actionName,
			"attempt",
			m.consecutiveFailures,
			"retry_in",
			delay.Round(time.Second),
			"retry_class",
			string(retryClass),
			"cooldown_until",
			cooldown.Format(time.RFC3339Nano),
		)
		m.publishLocked()
		m.persistLocked()
		return
	}

	m.lastRetryClass = ""
	blocked := nonRetryableBlockedReason(action, err)
	decision := fmt.Sprintf("%s action blocked by non-retryable cloud error", actionName)
	m.lastActionResult = "failed_non_retryable"
	m.markDegradedLocked(now, blocked, decision)
	m.logger.Error(
		"home auto session action blocked by non-retryable cloud error",
		"action",
		actionName,
		"blocked_reason",
		m.blockedReason,
		"err",
		err,
	)
}

func (m *Module) markDegradedLocked(now time.Time, blockedReason, decision string) {
	m.blockedReason = strings.TrimSpace(blockedReason)
	if m.blockedReason == "" {
		m.blockedReason = "home auto session control path is blocked"
	}
	m.state = StateDegraded
	if strings.TrimSpace(m.controlState) == "" || m.controlState == controlStateReady || m.controlState == controlStateCooldown {
		m.controlState = controlStateDegraded
	}
	m.summary = "home auto session is degraded"
	if strings.TrimSpace(decision) == "" {
		decision = "manual action required to recover control mode"
	}
	m.lastDecision = strings.TrimSpace(decision)
	if m.lastError == "" {
		m.lastError = m.blockedReason
	}
	m.cooldownUntil = nil
	m.lastRetryClass = ""
	m.activeStateSource = activeStateSourceConflict
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) markConflictBlockedLocked(now time.Time, blockedReason, decision string) {
	m.controlState = controlStateConflictBlocked
	m.markDegradedLocked(now, blockedReason, decision)
}

func (m *Module) markLifecycleBlockedLocked(now time.Time, blockedReason, decision string) {
	m.controlState = controlStateLifecycleBlocked
	m.markDegradedLocked(now, blockedReason, decision)
}

func (m *Module) persistLocked() {
	if m.store == nil {
		return
	}
	now := time.Now().UTC()
	stateCode := strings.TrimSpace(string(m.state))
	controlState := strings.TrimSpace(m.controlState)
	if controlState == "" {
		controlState = deriveControlState(m.state, m.blockedReason)
	}
	activeStateSource := strings.TrimSpace(m.activeStateSource)
	if activeStateSource == "" {
		activeStateSource = activeStateSourceNone
	}
	decision := strings.TrimSpace(m.lastDecision)
	lastErr := strings.TrimSpace(m.lastError)
	activeID := strings.TrimSpace(m.activeSessionID)
	activeNode := strings.TrimSpace(m.activeTriggerNode)
	lastStart := strings.TrimSpace(m.lastStartDedupe)
	lastStop := strings.TrimSpace(m.lastStopDedupe)
	cooldown := cloneTimePtr(m.cooldownUntil)
	decisionCooldown := cloneTimePtr(m.decisionCooldownUntil)
	lastAction := strings.TrimSpace(m.lastAction)
	lastActionResult := strings.TrimSpace(m.lastActionResult)
	lastActionAt := cloneTimePtr(m.lastActionAt)
	lastSuccessAction := strings.TrimSpace(m.lastSuccessfulAction)
	lastSuccessAt := cloneTimePtr(m.lastSuccessfulActionAt)
	blockedReason := strings.TrimSpace(m.blockedReason)
	reconciliation := strings.TrimSpace(m.reconciliationState)
	if reconciliation == "" {
		reconciliation = reconciliationCleanIdle
	}
	effectiveConfigSource := strings.TrimSpace(m.effectiveConfigSource)
	if effectiveConfigSource == "" {
		effectiveConfigSource = ConfigSourceLocalFallback
	}
	effectiveConfigVersion := strings.TrimSpace(m.effectiveConfigVersion)
	if effectiveConfigVersion == "" && effectiveConfigSource == ConfigSourceLocalFallback {
		effectiveConfigVersion = "local-default"
	}
	cloudConfigPresent := m.cloudConfigPresent
	lastFetchedConfigVer := strings.TrimSpace(m.lastFetchedConfigVer)
	lastAppliedConfigVer := strings.TrimSpace(m.lastAppliedConfigVer)
	if lastAppliedConfigVer == "" {
		lastAppliedConfigVer = effectiveConfigVersion
	}
	lastConfigApplyResult := strings.TrimSpace(m.lastConfigApplyResult)
	if lastConfigApplyResult == "" {
		lastConfigApplyResult = "config_apply_unknown"
	}
	lastConfigApplyError := strings.TrimSpace(m.lastConfigApplyError)
	desiredConfigEnabled := m.desiredConfigEnabled
	desiredConfigMode := strings.TrimSpace(m.desiredConfigMode)
	if desiredConfigMode == "" {
		desiredConfigMode = string(normalizeMode(m.cfg.Mode))
	}
	lastEventAt := cloneTimePtr(m.lastEventAt)
	gpsStatus := strings.TrimSpace(m.gpsStatus)
	gpsReason := strings.TrimSpace(m.gpsReason)
	gpsNode := strings.TrimSpace(m.gpsNodeID)
	gpsUpdatedAt := cloneTimePtr(m.gpsUpdatedAt)
	gpsDistance := cloneFloat64Ptr(m.gpsDistanceM)
	observedDropped := m.observedDropped
	consecutiveFailures := m.consecutiveFailures

	pendingActionCode := ""
	pendingNode := ""
	pendingReason := ""
	pendingDedupe := ""
	var pendingSince *time.Time
	if m.pendingAction != nil {
		pendingActionCode = string(m.pendingAction.Action)
		pendingNode = strings.TrimSpace(m.pendingAction.NodeID)
		pendingReason = strings.TrimSpace(m.pendingAction.Reason)
		pendingDedupe = strings.TrimSpace(m.pendingAction.DedupeKey)
		pendingSince = cloneTime(m.pendingAction.Since)
	}

	nextState := state.HomeAutoSessionState{
		ModuleState:            stateCode,
		ControlState:           controlState,
		ActiveStateSource:      activeStateSource,
		ReconciliationState:    reconciliation,
		EffectiveConfigSource:  effectiveConfigSource,
		EffectiveConfigVersion: effectiveConfigVersion,
		CloudConfigPresent:     cloudConfigPresent,
		LastFetchedConfigVer:   lastFetchedConfigVer,
		LastAppliedConfigVer:   lastAppliedConfigVer,
		LastConfigApplyResult:  lastConfigApplyResult,
		LastConfigApplyError:   lastConfigApplyError,
		DesiredConfigEnabled:   &desiredConfigEnabled,
		DesiredConfigMode:      desiredConfigMode,
		ActiveSessionID:        activeID,
		ActiveTriggerNode:      activeNode,
		PendingAction:          pendingActionCode,
		PendingTriggerNode:     pendingNode,
		PendingReason:          pendingReason,
		PendingDedupeKey:       pendingDedupe,
		PendingSince:           pendingSince,
		LastDecisionReason:     decision,
		LastStartDedupeKey:     lastStart,
		LastStopDedupeKey:      lastStop,
		LastAction:             lastAction,
		LastActionResult:       lastActionResult,
		LastActionAt:           lastActionAt,
		LastSuccessfulAction:   lastSuccessAction,
		LastSuccessfulActionAt: lastSuccessAt,
		LastError:              lastErr,
		BlockedReason:          blockedReason,
		ConsecutiveFailures:    consecutiveFailures,
		LastEventAt:            lastEventAt,
		CooldownUntil:          cooldown,
		DecisionCooldownUntil:  decisionCooldown,
		GPSStatus:              gpsStatus,
		GPSReason:              gpsReason,
		GPSNodeID:              gpsNode,
		GPSUpdatedAt:           gpsUpdatedAt,
		GPSDistanceM:           gpsDistance,
		ObservedDropped:        observedDropped,
	}

	fingerprint := homeAutoPersistFingerprint(nextState)
	if fingerprint != "" && fingerprint == m.lastPersistFingerprint {
		return
	}

	nextState.LastDecisionAt = cloneTime(now)
	if !now.IsZero() {
		nextState.UpdatedAt = now
	}
	if err := m.store.Update(func(data *state.Data) {
		data.HomeAutoSession = nextState
	}); err != nil {
		m.logger.Warn("persist home auto session state failed", "err", err)
		return
	}
	if fingerprint != "" {
		m.lastPersistFingerprint = fingerprint
	}
}

func (m *Module) publishLocked() {
	if m.status == nil {
		return
	}
	tracked := make([]string, 0, len(m.cfg.TrackedNodeIDs))
	tracked = append(tracked, m.cfg.TrackedNodeIDs...)
	summary := strings.TrimSpace(m.summary)
	if summary == "" {
		summary = "home auto session status unavailable"
	}

	pendingActionCode := ""
	var pendingSince *time.Time
	if m.pendingAction != nil {
		pendingActionCode = string(m.pendingAction.Action)
		pendingSince = cloneTime(m.pendingAction.Since)
	}
	controlState := strings.TrimSpace(m.controlState)
	if controlState == "" {
		controlState = deriveControlState(m.state, m.blockedReason)
	}
	activeSource := strings.TrimSpace(m.activeStateSource)
	if activeSource == "" {
		activeSource = activeStateSourceNone
	}
	effectiveConfigSource := strings.TrimSpace(m.effectiveConfigSource)
	if effectiveConfigSource == "" {
		effectiveConfigSource = ConfigSourceLocalFallback
	}
	effectiveConfigVersion := strings.TrimSpace(m.effectiveConfigVersion)
	if effectiveConfigVersion == "" && effectiveConfigSource == ConfigSourceLocalFallback {
		effectiveConfigVersion = "local-default"
	}
	lastAppliedConfigVer := strings.TrimSpace(m.lastAppliedConfigVer)
	if lastAppliedConfigVer == "" {
		lastAppliedConfigVer = effectiveConfigVersion
	}
	lastConfigApplyResult := strings.TrimSpace(m.lastConfigApplyResult)
	if lastConfigApplyResult == "" {
		lastConfigApplyResult = "config_apply_unknown"
	}
	desiredConfigMode := strings.TrimSpace(m.desiredConfigMode)
	if desiredConfigMode == "" {
		desiredConfigMode = string(normalizeMode(m.cfg.Mode))
	}
	trackedNodeState := strings.TrimSpace(m.trackedNodeState)
	if trackedNodeState == "" {
		trackedNodeState = "no tracked node data observed yet"
	}

	m.status.SetHomeAutoSession(status.HomeAutoSessionSnapshot{
		Enabled:               m.cfg.Enabled,
		Mode:                  string(normalizeMode(m.cfg.Mode)),
		EffectiveConfigSource: effectiveConfigSource,
		EffectiveConfigVer:    effectiveConfigVersion,
		CloudConfigPresent:    m.cloudConfigPresent,
		LastFetchedConfigVer:  strings.TrimSpace(m.lastFetchedConfigVer),
		LastAppliedConfigVer:  lastAppliedConfigVer,
		LastConfigApplyResult: lastConfigApplyResult,
		LastConfigApplyError:  strings.TrimSpace(m.lastConfigApplyError),
		DesiredConfigEnabled:  m.desiredConfigEnabled,
		DesiredConfigMode:     desiredConfigMode,
		State:                 string(m.state),
		ControlState:          controlState,
		ActiveStateSource:     activeSource,
		Summary:               summary,
		HomeSummary:           formatHomeSummary(m.cfg.Home),
		TrackedNodeIDs:        tracked,
		TrackedNodeState:      trackedNodeState,
		ReconciliationState:   m.reconciliationState,
		PendingAction:         pendingActionCode,
		PendingSince:          pendingSince,
		ActiveSessionID:       m.activeSessionID,
		ActiveTriggerNode:     m.activeTriggerNode,
		LastDecisionReason:    m.lastDecision,
		LastError:             m.lastError,
		LastAction:            m.lastAction,
		LastActionResult:      m.lastActionResult,
		LastActionAt:          cloneTimePtr(m.lastActionAt),
		LastSuccessfulAction:  m.lastSuccessfulAction,
		LastSuccessfulAt:      cloneTimePtr(m.lastSuccessfulActionAt),
		BlockedReason:         m.blockedReason,
		ConsecutiveFailures:   m.consecutiveFailures,
		CooldownUntil:         cloneTimePtr(m.cooldownUntil),
		DecisionCooldownUntil: cloneTimePtr(m.decisionCooldownUntil),
		GPSStatus:             m.gpsStatus,
		GPSReason:             m.gpsReason,
		GPSNodeID:             m.gpsNodeID,
		GPSUpdatedAt:          cloneTimePtr(m.gpsUpdatedAt),
		GPSDistanceM:          cloneFloat64Ptr(m.gpsDistanceM),
		ObservedQueueDepth:    len(m.events),
		ObservedDropped:       m.observedDropped,
	})
	m.status.SetComponent("home_auto_session", string(m.state), summary)

	currentState := string(m.state)
	currentDecision := strings.TrimSpace(m.lastDecision)
	currentAction := strings.TrimSpace(m.lastAction)
	currentResult := strings.TrimSpace(m.lastActionResult)
	if m.lastLoggedState != currentState ||
		m.lastLoggedControlState != controlState ||
		m.lastLoggedDecision != currentDecision ||
		m.lastLoggedAction != currentAction ||
		m.lastLoggedResult != currentResult ||
		m.lastLoggedSummary != summary {
		m.logger.Info(
			"home auto session decision update",
			"state", currentState,
			"control_state", controlState,
			"decision", currentDecision,
			"action", currentAction,
			"result", currentResult,
			"summary", summary,
		)
		m.lastLoggedState = currentState
		m.lastLoggedControlState = controlState
		m.lastLoggedDecision = currentDecision
		m.lastLoggedAction = currentAction
		m.lastLoggedResult = currentResult
		m.lastLoggedSummary = summary
	}
}

func normalizeConfig(cfg config.HomeAutoSessionConfig) config.HomeAutoSessionConfig {
	cfg.Mode = normalizeMode(cfg.Mode)
	ids := make([]string, 0, len(cfg.TrackedNodeIDs))
	seen := map[string]struct{}{}
	for _, raw := range cfg.TrackedNodeIDs {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if _, ok := seen[lower]; ok {
			continue
		}
		seen[lower] = struct{}{}
		ids = append(ids, value)
	}
	cfg.TrackedNodeIDs = ids
	cfg.SessionNameTemplate = strings.TrimSpace(cfg.SessionNameTemplate)
	cfg.SessionNotesTemplate = strings.TrimSpace(cfg.SessionNotesTemplate)
	cfg.Cloud.StartEndpoint = strings.TrimSpace(cfg.Cloud.StartEndpoint)
	cfg.Cloud.StopEndpoint = strings.TrimSpace(cfg.Cloud.StopEndpoint)
	return cfg
}

func configHash(cfg config.HomeAutoSessionConfig) string {
	payload, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12])
}

func homeAutoPersistFingerprint(value state.HomeAutoSessionState) string {
	value.LastDecisionAt = nil
	value.UpdatedAt = time.Time{}
	payload, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:12])
}

func normalizeMode(mode config.HomeAutoSessionMode) config.HomeAutoSessionMode {
	switch config.HomeAutoSessionMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case config.HomeAutoSessionModeObserve:
		return config.HomeAutoSessionModeObserve
	case config.HomeAutoSessionModeControl:
		return config.HomeAutoSessionModeControl
	default:
		return config.HomeAutoSessionModeOff
	}
}

func normalizePendingAction(value string) pendingActionKind {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(actionStart):
		return actionStart
	case string(actionStop):
		return actionStop
	default:
		return ""
	}
}

func validateConfig(cfg config.HomeAutoSessionConfig) error {
	if !cfg.Enabled || normalizeMode(cfg.Mode) == config.HomeAutoSessionModeOff {
		return nil
	}
	if cfg.Home.Lat < -90 || cfg.Home.Lat > 90 {
		return fmt.Errorf("home geofence latitude is invalid")
	}
	if cfg.Home.Lon < -180 || cfg.Home.Lon > 180 {
		return fmt.Errorf("home geofence longitude is invalid")
	}
	if cfg.Home.RadiusM <= 0 {
		return fmt.Errorf("home geofence radius must be greater than zero")
	}
	if len(cfg.TrackedNodeIDs) == 0 {
		return fmt.Errorf("tracked node IDs are required")
	}
	if cfg.StartDebounce.Std() <= 0 {
		return fmt.Errorf("start debounce must be greater than zero")
	}
	if cfg.StopDebounce.Std() <= 0 {
		return fmt.Errorf("stop debounce must be greater than zero")
	}
	if cfg.IdleStopTimeout.Std() <= 0 {
		return fmt.Errorf("idle stop timeout must be greater than zero")
	}
	return nil
}

func renderSessionText(cfg config.HomeAutoSessionConfig, nodeID string) (string, string) {
	name := renderTemplateText(cfg.SessionNameTemplate, nodeID)
	notes := renderTemplateText(cfg.SessionNotesTemplate, nodeID)
	return name, notes
}

func renderTemplateText(templateText, nodeID string) string {
	value := strings.TrimSpace(templateText)
	if value == "" {
		return ""
	}
	value = strings.ReplaceAll(value, "{{.NodeID}}", nodeID)
	value = strings.ReplaceAll(value, "{{ .NodeID }}", nodeID)
	return strings.TrimSpace(value)
}

func coordinatesValid(lat, lon float64) bool {
	return lat >= -90 && lat <= 90 && lon >= -180 && lon <= 180
}

func positionIsStale(sampleTime time.Time, threshold time.Duration) bool {
	if sampleTime.IsZero() {
		return true
	}
	age := time.Since(sampleTime.UTC())
	return age > threshold
}

func stalePositionThreshold(cfg config.HomeAutoSessionConfig) time.Duration {
	threshold := cfg.IdleStopTimeout.Std() / 3
	if threshold < gpsStaleFloor {
		threshold = gpsStaleFloor
	}
	if threshold > gpsStaleCeiling {
		threshold = gpsStaleCeiling
	}
	return threshold
}

func geofenceBoundaryMargin(radiusM float64) float64 {
	margin := radiusM * boundaryMarginFraction
	if margin < boundaryMarginFloorM {
		margin = boundaryMarginFloorM
	}
	if margin > boundaryMarginCapM {
		margin = boundaryMarginCapM
	}
	return margin
}

func decisionCooldownDuration(cfg config.HomeAutoSessionConfig) time.Duration {
	duration := cfg.StartDebounce.Std() / 2
	if stop := cfg.StopDebounce.Std() / 2; stop > duration {
		duration = stop
	}
	if duration < decisionCooldownFloor {
		duration = decisionCooldownFloor
	}
	if duration > decisionCooldownCeiling {
		duration = decisionCooldownCeiling
	}
	return duration
}

func retryDelay(failures int) time.Duration {
	if failures <= 1 {
		return retryCooldown
	}
	delay := retryCooldown
	for i := 1; i < failures && i < 4; i++ {
		delay *= 2
	}
	if delay > retryCooldownMax {
		return retryCooldownMax
	}
	return delay
}

func retryDelayFor(action pendingActionKind, retryClass retryableErrorClass, failures int) time.Duration {
	if action == actionStop && retryClass == retryClassTimeoutNetwork {
		return stopTimeoutRetryDelay(failures)
	}
	return retryDelay(failures)
}

func stopTimeoutRetryDelay(failures int) time.Duration {
	switch {
	case failures <= 1:
		return stopRetryQuickFirst
	case failures == 2:
		return stopRetryQuickSecond
	case failures == 3:
		return stopRetryQuickThird
	default:
		// Fall back to the existing bounded retry profile after quick early attempts.
		return retryDelay(failures - 2)
	}
}

func stopRetryPendingSummary(attempt int, eta time.Time) string {
	return fmt.Sprintf("stop pending; cloud unreachable/slow (attempt %d, next retry %s)", attempt, eta.Format(time.RFC3339))
}

func classifyRetryableCloudError(err error) retryableErrorClass {
	if err == nil {
		return retryClassGenericRetryable
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return retryClassTimeoutNetwork
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return retryClassTimeoutNetwork
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "timed out") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "dial tcp") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "no such host") {
		return retryClassTimeoutNetwork
	}
	return retryClassGenericRetryable
}

func (m *Module) shouldBypassStopRetryCooldownLocked(now time.Time, trigger string) bool {
	if trigger != "event" || m.pendingAction == nil || m.pendingAction.Action != actionStop {
		return false
	}
	if m.lastActionResult != "retry_scheduled" || m.lastRetryClass != string(retryClassTimeoutNetwork) {
		return false
	}
	if m.lastActionAt == nil {
		return false
	}
	lastAttempt := m.lastActionAt.UTC()
	if now.Sub(lastAttempt) < stopRetryBypassMin {
		return false
	}
	nodeID := strings.TrimSpace(m.activeTriggerNode)
	if nodeID == "" {
		nodeID = strings.TrimSpace(m.pendingAction.NodeID)
	}
	if nodeID == "" {
		return false
	}
	fact, ok := m.nodeFacts[strings.ToLower(nodeID)]
	if !ok || !fact.HasPosition || !fact.InsideGeofence || fact.LastSeenAt.IsZero() {
		return false
	}
	if !fact.LastSeenAt.After(lastAttempt) {
		return false
	}
	return now.Sub(fact.LastSeenAt) <= stopRetryFreshInsideMax
}

func nonRetryableBlockedReason(action pendingActionKind, err error) string {
	var apiErr *cloudclient.APIError
	if errors.As(err, &apiErr) {
		message := strings.ToLower(strings.TrimSpace(apiErr.Message))
		if strings.Contains(message, "replaced") || strings.Contains(message, "superseded") {
			return "receiver was replaced by another install"
		}
		if strings.Contains(message, "disabled") {
			return "receiver is disabled in cloud"
		}
		if strings.Contains(message, "revoked") || strings.Contains(message, "invalid credential") {
			return "receiver credentials were revoked by cloud"
		}
		switch apiErr.StatusCode {
		case 401, 403:
			return "receiver credentials were rejected by cloud"
		case 404:
			if action == actionStop {
				return "cloud session was not found during stop"
			}
			return "cloud session endpoint was not found"
		case 409:
			return "cloud reported conflicting session state"
		case 400, 422:
			return "cloud rejected Home Auto Session request"
		default:
			if text := strings.TrimSpace(apiErr.Message); text != "" {
				return text
			}
		}
	}
	return "home auto session control request failed"
}

func lifecycleConflictFromError(err error) (string, bool) {
	var apiErr *cloudclient.APIError
	if !errors.As(err, &apiErr) {
		return "", false
	}
	message := strings.ToLower(strings.TrimSpace(apiErr.Message))
	if apiErr.StatusCode == 423 || strings.Contains(message, "disabled") {
		return "disabled", true
	}
	if strings.Contains(message, "replaced") || strings.Contains(message, "superseded") || strings.Contains(message, "replacement") {
		return "replaced", true
	}
	if apiErr.StatusCode == 401 || strings.Contains(message, "revoked") || strings.Contains(message, "invalid credential") {
		return "revoked", true
	}
	return "", false
}

func startAlreadyActiveConflict(err error) bool {
	var apiErr *cloudclient.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != 409 && apiErr.StatusCode != 422 {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(apiErr.Message))
	if message == "" {
		return false
	}
	if strings.Contains(message, "already active") || strings.Contains(message, "already running") {
		return true
	}
	if strings.Contains(message, "already") && strings.Contains(message, "session") && strings.Contains(message, "active") {
		return true
	}
	return false
}

func stopStateMismatchConflict(err error) bool {
	var apiErr *cloudclient.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != 409 && apiErr.StatusCode != 422 {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(apiErr.Message))
	if message == "" {
		return false
	}
	if strings.Contains(message, "state mismatch") || strings.Contains(message, "does not match") {
		return true
	}
	if strings.Contains(message, "cannot stop") && strings.Contains(message, "current state") {
		return true
	}
	return false
}

func stopAlreadyResolvedError(err error) bool {
	var apiErr *cloudclient.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode != 404 && apiErr.StatusCode != 409 {
		return false
	}
	msg := strings.ToLower(strings.TrimSpace(apiErr.Message))
	if msg == "" {
		return apiErr.StatusCode == 404
	}
	if strings.Contains(msg, "not found") {
		return true
	}
	if strings.Contains(msg, "already") && (strings.Contains(msg, "stopped") || strings.Contains(msg, "closed") || strings.Contains(msg, "ended")) {
		return true
	}
	return false
}

func readySummaryForGPS(gpsStatus, gpsReason, fallback string) string {
	switch strings.TrimSpace(gpsStatus) {
	case gpsStatusMissing, gpsStatusInvalid, gpsStatusStale, gpsStatusBoundaryUncertain:
		if strings.TrimSpace(gpsReason) != "" {
			return gpsReason
		}
		return "waiting for valid tracked-node position updates"
	default:
		return fallback
	}
}

func deriveControlState(moduleState ModuleState, blockedReason string) string {
	if strings.TrimSpace(blockedReason) != "" {
		blocked := strings.ToLower(strings.TrimSpace(blockedReason))
		if strings.Contains(blocked, "revoked") || strings.Contains(blocked, "replaced") || strings.Contains(blocked, "disabled") {
			return controlStateLifecycleBlocked
		}
		if strings.Contains(blocked, "conflict") || strings.Contains(blocked, "disagree") || strings.Contains(blocked, "mismatch") {
			return controlStateConflictBlocked
		}
	}

	switch moduleState {
	case StateDisabled:
		return controlStateDisabled
	case StateMisconfigured:
		return controlStateMisconfigured
	case StateObserveReady, StateControlReady:
		return controlStateReady
	case StateStartPending:
		return controlStatePendingStart
	case StateStopPending:
		return controlStatePendingStop
	case StateActive:
		return controlStateActive
	case StateCooldown:
		return controlStateCooldown
	case StateDegraded:
		return controlStateDegraded
	default:
		return controlStateDegraded
	}
}

func haversineMeters(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusMeters = 6371000.0
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat := rad(lat2 - lat1)
	dLon := rad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	c := 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
	return earthRadiusMeters * c
}

func formatHomeSummary(home config.HomeGeofenceConfig) string {
	if home.RadiusM <= 0 {
		return "home geofence not configured"
	}
	return fmt.Sprintf("%.5f, %.5f (radius %.0fm)", home.Lat, home.Lon, home.RadiusM)
}

func dedupeKey(prefix, nodeID string, at time.Time, reason string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		prefix,
		strings.ToLower(strings.TrimSpace(nodeID)),
		at.UTC().Format(time.RFC3339Nano),
		strings.ToLower(strings.TrimSpace(reason)),
	}, "|")))
	return prefix + "-" + hex.EncodeToString(sum[:10])
}

func eventTime(event meshtastic.Event) time.Time {
	if event.Packet != nil && !event.Packet.ReceivedAt.IsZero() {
		return event.Packet.ReceivedAt.UTC()
	}
	if !event.Received.IsZero() {
		return event.Received.UTC()
	}
	return time.Now().UTC()
}

func cloneTime(input time.Time) *time.Time {
	out := input.UTC()
	return &out
}

func cloneTimePtr(input *time.Time) *time.Time {
	if input == nil {
		return nil
	}
	value := input.UTC()
	return &value
}

func cloneFloat64Ptr(input *float64) *float64 {
	if input == nil {
		return nil
	}
	value := *input
	return &value
}
