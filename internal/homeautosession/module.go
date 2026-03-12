package homeautosession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
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
)

const (
	gpsStatusMissing           = "missing"
	gpsStatusInvalid           = "invalid"
	gpsStatusStale             = "stale"
	gpsStatusBoundaryUncertain = "boundary_uncertain"
	gpsStatusValid             = "valid"
)

const (
	observationQueueDepth   = 256
	controlCallTimeout      = 8 * time.Second
	retryCooldown           = 30 * time.Second
	retryCooldownMax        = 4 * time.Minute
	gpsStaleFloor           = 2 * time.Minute
	gpsStaleCeiling         = 10 * time.Minute
	decisionCooldownFloor   = 1 * time.Second
	decisionCooldownCeiling = 30 * time.Second
	boundaryMarginFloorM    = 8.0
	boundaryMarginCapM      = 75.0
	boundaryMarginFraction  = 0.08
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
	trackedByLower map[string]string
	state          ModuleState
	summary        string

	reconciliationState string
	blockedReason       string

	activeSessionID   string
	activeTriggerNode string
	lastDecision      string
	lastError         string
	lastStartDedupe   string
	lastStopDedupe    string

	lastSuccessfulAction   string
	lastSuccessfulActionAt *time.Time
	lastEventAt            *time.Time

	pendingAction *pendingAction

	cooldownUntil         *time.Time
	decisionCooldownUntil *time.Time
	consecutiveFailures   int
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
		logger:              logger.With("component", "home_auto_session"),
		store:               store,
		status:              statusModel,
		client:              client,
		events:              make(chan meshtastic.Event, observationQueueDepth),
		reevaluate:          make(chan struct{}, 1),
		nodeFacts:           make(map[string]nodeFact),
		trackedByLower:      make(map[string]string),
		state:               StateDisabled,
		summary:             "module disabled",
		reconciliationState: reconciliationCleanIdle,
		gpsStatus:           gpsStatusMissing,
		gpsReason:           "waiting for tracked-node position updates",
	}
	_ = m.ApplyConfig(cfg)
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
	m.mu.Lock()
	m.cfg = normalized
	m.trackedByLower = make(map[string]string, len(normalized.TrackedNodeIDs))
	for _, id := range normalized.TrackedNodeIDs {
		m.trackedByLower[strings.ToLower(strings.TrimSpace(id))] = id
	}
	m.publishLocked()
	m.mu.Unlock()
	return validateConfig(normalized)
}

func (m *Module) CurrentConfig() config.HomeAutoSessionConfig {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg
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
	m.summary = "degraded state reset"
	if m.state == StateDegraded {
		m.state = StateControlReady
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
	m.activeSessionID = strings.TrimSpace(snap.ActiveSessionID)
	m.activeTriggerNode = strings.TrimSpace(snap.ActiveTriggerNode)
	m.lastDecision = strings.TrimSpace(snap.LastDecisionReason)
	m.lastStartDedupe = strings.TrimSpace(snap.LastStartDedupeKey)
	m.lastStopDedupe = strings.TrimSpace(snap.LastStopDedupeKey)
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
	if m.lastDecision == "" {
		m.lastDecision = "startup reconciliation complete"
	}
}

func (m *Module) markStartupInconsistentLocked(now time.Time, reason string) {
	m.reconciliationState = reconciliationInconsistentDegraded
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
		m.setGPSStatusLocked(gpsStatusMissing, "waiting for tracked-node position updates", nodeID, nil, now)
		m.nodeFacts[nodeKey] = fact
		return
	}

	lat := event.Packet.Position.Lat
	lon := event.Packet.Position.Lon
	if !coordinatesValid(lat, lon) {
		m.setGPSStatusLocked(gpsStatusInvalid, "ignored invalid GPS coordinates from tracked node", nodeID, nil, now)
		m.nodeFacts[nodeKey] = fact
		return
	}

	if positionIsStale(now, stalePositionThreshold(cfg)) {
		reason := fmt.Sprintf("position sample is stale (older than %s)", stalePositionThreshold(cfg).Round(time.Second))
		m.setGPSStatusLocked(gpsStatusStale, reason, nodeID, nil, time.Now().UTC())
		m.nodeFacts[nodeKey] = fact
		return
	}

	distance := haversineMeters(cfg.Home.Lat, cfg.Home.Lon, lat, lon)
	margin := geofenceBoundaryMargin(cfg.Home.RadiusM)
	if math.Abs(distance-cfg.Home.RadiusM) <= margin {
		reason := fmt.Sprintf("position is near boundary (±%.0fm uncertainty)", margin)
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
		m.summary = "home auto session is disabled"
		m.lastDecision = "module disabled"
		m.publishLocked()
		m.persistLocked()
		return
	}

	if err := validateConfig(cfg); err != nil {
		m.state = StateMisconfigured
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
		m.publishLocked()
		m.persistLocked()
		return
	}

	if m.cooldownUntil != nil && now.Before(m.cooldownUntil.UTC()) {
		m.state = StateCooldown
		m.summary = "cooldown after prior cloud/session error"
		m.lastDecision = "waiting for cooldown window before retry"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.decisionCooldownUntil != nil && now.Before(m.decisionCooldownUntil.UTC()) {
		m.state = StateCooldown
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
				m.summary = "waiting for stop debounce window"
				m.lastDecision = fmt.Sprintf("stop candidate pending for node %s", m.stopCandidate.NodeID)
				m.publishLocked()
				m.persistLocked()
				return
			}
			if mode == config.HomeAutoSessionModeObserve {
				m.state = StateActive
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
		m.summary = "home auto session is active"
		m.publishLocked()
		m.persistLocked()
		return
	}

	if m.startCandidate != nil {
		if now.Sub(m.startCandidate.At) < cfg.StartDebounce.Std() {
			m.state = StateStartPending
			m.summary = "waiting for start debounce window"
			m.lastDecision = fmt.Sprintf("start candidate pending for node %s", m.startCandidate.NodeID)
			m.publishLocked()
			m.persistLocked()
			return
		}

		if mode == config.HomeAutoSessionModeObserve {
			m.state = StateObserveReady
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
		m.summary = readySummaryForGPS(m.gpsStatus, m.gpsReason, "waiting for tracked-node geofence transition")
		m.lastDecision = "control mode ready"
	} else {
		m.state = StateObserveReady
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
		m.state = StateControlReady
		m.summary = "duplicate start decision suppressed"
		m.lastDecision = "start dedupe key already executed"
		m.publishLocked()
		m.persistLocked()
		return
	}
	apiKey, startEndpoint, ok := m.resolveControlAuthLocked(cfg, actionStart)
	if !ok {
		m.state = StateControlReady
		m.summary = "waiting for paired receiver credentials before control actions"
		m.lastDecision = "control action deferred: receiver not fully paired"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.client == nil {
		m.lastError = "home auto session cloud client is unavailable"
		m.markDegradedLocked(now, "cloud session client is unavailable", "manual action required to recover control mode")
		return
	}

	m.setPendingActionLocked(actionStart, candidate, now)
	m.state = StateStartPending
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
	m.lastStartDedupe = candidate.DedupeKey
	m.activeSessionID = strings.TrimSpace(result.SessionID)
	m.activeTriggerNode = candidate.NodeID
	m.lastSuccessfulAction = string(actionStart)
	m.lastSuccessfulActionAt = cloneTime(startedAt)
	decisionCooldown := now.Add(decisionCooldownDuration(cfg))
	m.decisionCooldownUntil = &decisionCooldown
	m.state = StateActive
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
		m.state = StateControlReady
		m.summary = "duplicate stop decision suppressed"
		m.lastDecision = "stop dedupe key already executed"
		m.publishLocked()
		m.persistLocked()
		return
	}
	apiKey, stopEndpoint, ok := m.resolveControlAuthLocked(cfg, actionStop)
	if !ok {
		m.state = StateControlReady
		m.summary = "waiting for paired receiver credentials before control actions"
		m.lastDecision = "control action deferred: receiver not fully paired"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.client == nil {
		m.lastError = "home auto session cloud client is unavailable"
		m.markDegradedLocked(now, "cloud session client is unavailable", "manual action required to recover control mode")
		return
	}

	m.setPendingActionLocked(actionStop, candidate, now)
	m.state = StateStopPending
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
	m.lastStopDedupe = candidate.DedupeKey
	m.activeSessionID = ""
	m.activeTriggerNode = ""
	m.lastSuccessfulAction = string(actionStop)
	m.lastSuccessfulActionAt = cloneTime(stoppedAt)
	decisionCooldown := now.Add(decisionCooldownDuration(m.cfg))
	m.decisionCooldownUntil = &decisionCooldown
	m.state = StateControlReady
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
	fact, ok := m.nodeFacts[strings.ToLower(m.activeTriggerNode)]
	if ok && !fact.LastSeenAt.IsZero() {
		return now.Sub(fact.LastSeenAt) >= cfg.IdleStopTimeout.Std()
	}
	if m.lastEventAt == nil {
		return false
	}
	return now.Sub(m.lastEventAt.UTC()) >= cfg.IdleStopTimeout.Std()
}

func (m *Module) handleCloudErrorLocked(now time.Time, action pendingActionKind, err error) {
	actionName := string(action)
	errText := strings.TrimSpace(err.Error())
	if errText == "" {
		errText = "cloud/session API error"
	}
	m.lastError = errText
	m.consecutiveFailures++
	m.logger.Warn("home auto session cloud action failed", "action", actionName, "err", err)

	if cloudclient.IsRetryable(err) {
		delay := retryDelay(m.consecutiveFailures)
		cooldown := now.Add(delay)
		m.cooldownUntil = &cooldown
		m.blockedReason = ""
		m.state = StateCooldown
		m.summary = "cloud/session API unavailable, retrying after cooldown"
		m.lastDecision = fmt.Sprintf("%s action retry scheduled after %s", actionName, delay.Round(time.Second))
		m.publishLocked()
		m.persistLocked()
		return
	}

	blocked := nonRetryableBlockedReason(action, err)
	decision := fmt.Sprintf("%s action blocked by non-retryable cloud error", actionName)
	m.markDegradedLocked(now, blocked, decision)
}

func (m *Module) markDegradedLocked(now time.Time, blockedReason, decision string) {
	m.blockedReason = strings.TrimSpace(blockedReason)
	if m.blockedReason == "" {
		m.blockedReason = "home auto session control path is blocked"
	}
	m.state = StateDegraded
	m.summary = "home auto session is degraded"
	if strings.TrimSpace(decision) == "" {
		decision = "manual action required to recover control mode"
	}
	m.lastDecision = strings.TrimSpace(decision)
	if m.lastError == "" {
		m.lastError = m.blockedReason
	}
	m.cooldownUntil = nil
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) persistLocked() {
	if m.store == nil {
		return
	}
	now := time.Now().UTC()
	stateCode := strings.TrimSpace(string(m.state))
	decision := strings.TrimSpace(m.lastDecision)
	lastErr := strings.TrimSpace(m.lastError)
	activeID := strings.TrimSpace(m.activeSessionID)
	activeNode := strings.TrimSpace(m.activeTriggerNode)
	lastStart := strings.TrimSpace(m.lastStartDedupe)
	lastStop := strings.TrimSpace(m.lastStopDedupe)
	cooldown := cloneTimePtr(m.cooldownUntil)
	decisionCooldown := cloneTimePtr(m.decisionCooldownUntil)
	lastSuccessAction := strings.TrimSpace(m.lastSuccessfulAction)
	lastSuccessAt := cloneTimePtr(m.lastSuccessfulActionAt)
	blockedReason := strings.TrimSpace(m.blockedReason)
	reconciliation := strings.TrimSpace(m.reconciliationState)
	if reconciliation == "" {
		reconciliation = reconciliationCleanIdle
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

	_ = m.store.Update(func(data *state.Data) {
		data.HomeAutoSession.ModuleState = stateCode
		data.HomeAutoSession.ReconciliationState = reconciliation
		data.HomeAutoSession.ActiveSessionID = activeID
		data.HomeAutoSession.ActiveTriggerNode = activeNode
		data.HomeAutoSession.PendingAction = pendingActionCode
		data.HomeAutoSession.PendingTriggerNode = pendingNode
		data.HomeAutoSession.PendingReason = pendingReason
		data.HomeAutoSession.PendingDedupeKey = pendingDedupe
		data.HomeAutoSession.PendingSince = pendingSince
		data.HomeAutoSession.LastDecisionReason = decision
		data.HomeAutoSession.LastStartDedupeKey = lastStart
		data.HomeAutoSession.LastStopDedupeKey = lastStop
		data.HomeAutoSession.LastSuccessfulAction = lastSuccessAction
		data.HomeAutoSession.LastSuccessfulActionAt = lastSuccessAt
		data.HomeAutoSession.LastError = lastErr
		data.HomeAutoSession.BlockedReason = blockedReason
		data.HomeAutoSession.ConsecutiveFailures = consecutiveFailures
		data.HomeAutoSession.LastDecisionAt = cloneTime(now)
		data.HomeAutoSession.LastEventAt = lastEventAt
		data.HomeAutoSession.CooldownUntil = cooldown
		data.HomeAutoSession.DecisionCooldownUntil = decisionCooldown
		data.HomeAutoSession.GPSStatus = gpsStatus
		data.HomeAutoSession.GPSReason = gpsReason
		data.HomeAutoSession.GPSNodeID = gpsNode
		data.HomeAutoSession.GPSUpdatedAt = gpsUpdatedAt
		data.HomeAutoSession.GPSDistanceM = gpsDistance
		data.HomeAutoSession.ObservedDropped = observedDropped
		if !now.IsZero() {
			data.HomeAutoSession.UpdatedAt = now
		}
	})
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

	m.status.SetHomeAutoSession(status.HomeAutoSessionSnapshot{
		Enabled:               m.cfg.Enabled,
		Mode:                  string(normalizeMode(m.cfg.Mode)),
		State:                 string(m.state),
		Summary:               summary,
		HomeSummary:           formatHomeSummary(m.cfg.Home),
		TrackedNodeIDs:        tracked,
		ReconciliationState:   m.reconciliationState,
		PendingAction:         pendingActionCode,
		PendingSince:          pendingSince,
		ActiveSessionID:       m.activeSessionID,
		ActiveTriggerNode:     m.activeTriggerNode,
		LastDecisionReason:    m.lastDecision,
		LastError:             m.lastError,
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

func nonRetryableBlockedReason(action pendingActionKind, err error) string {
	var apiErr *cloudclient.APIError
	if errors.As(err, &apiErr) {
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
