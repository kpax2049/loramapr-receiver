package homeautosession

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const (
	observationQueueDepth = 256
	controlCallTimeout    = 8 * time.Second
	retryCooldown         = 30 * time.Second
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

	activeSessionID   string
	activeTriggerNode string
	lastDecision      string
	lastError         string
	lastStartDedupe   string
	lastStopDedupe    string
	cooldownUntil     *time.Time
	observedDropped   int

	startCandidate *transitionCandidate
	stopCandidate  *transitionCandidate
	nodeFacts      map[string]nodeFact

	events     chan meshtastic.Event
	reevaluate chan struct{}
	started    bool
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
	LastSeenAt      time.Time
	LastTransition  time.Time
	LastOutsideSeen time.Time
}

func New(cfg config.HomeAutoSessionConfig, store *state.Store, statusModel *status.Model, logger *slog.Logger, client SessionClient) *Module {
	if logger == nil {
		logger = slog.Default()
	}
	m := &Module{
		logger:          logger.With("component", "home_auto_session"),
		store:           store,
		status:          statusModel,
		client:          client,
		events:          make(chan meshtastic.Event, observationQueueDepth),
		reevaluate:      make(chan struct{}, 1),
		nodeFacts:       make(map[string]nodeFact),
		trackedByLower:  make(map[string]string),
		state:           StateDisabled,
		summary:         "module disabled",
		startCandidate:  nil,
		stopCandidate:   nil,
		cooldownUntil:   nil,
		observedDropped: 0,
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
		m.state = StateDegraded
		m.summary = "event observation queue overflow"
		m.persistLocked()
		m.publishLocked()
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
	m.cooldownUntil = nil
	m.stopCandidate = nil
	m.startCandidate = nil
	m.summary = "degraded state reset"
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
		return
	}
	snap := m.store.Snapshot().HomeAutoSession
	m.activeSessionID = strings.TrimSpace(snap.ActiveSessionID)
	m.activeTriggerNode = strings.TrimSpace(snap.ActiveTriggerNode)
	m.lastDecision = strings.TrimSpace(snap.LastDecisionReason)
	m.lastStartDedupe = strings.TrimSpace(snap.LastStartDedupeKey)
	m.lastStopDedupe = strings.TrimSpace(snap.LastStopDedupeKey)
	m.lastError = strings.TrimSpace(snap.LastError)
	if snap.CooldownUntil != nil {
		value := snap.CooldownUntil.UTC()
		m.cooldownUntil = &value
	}
	m.observedDropped = snap.ObservedDropped
	if strings.TrimSpace(snap.ModuleState) != "" {
		m.state = ModuleState(strings.TrimSpace(snap.ModuleState))
	}
	if !m.cfg.StartupReconcile && m.activeSessionID != "" {
		m.activeSessionID = ""
		m.activeTriggerNode = ""
		m.lastDecision = "startup reconcile disabled; cleared prior active local session marker"
	}
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
	if _, ok := m.trackedByLower[strings.ToLower(nodeID)]; !ok {
		return
	}

	now := eventTime(event)
	fact := m.nodeFacts[strings.ToLower(nodeID)]
	fact.LastSeenAt = now

	if event.Packet.Position == nil {
		m.nodeFacts[strings.ToLower(nodeID)] = fact
		return
	}
	inside := pointInsideHome(cfg.Home, event.Packet.Position.Lat, event.Packet.Position.Lon)
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
	fact.LastLat = event.Packet.Position.Lat
	fact.LastLon = event.Packet.Position.Lon
	m.nodeFacts[strings.ToLower(nodeID)] = fact
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

	if m.cooldownUntil != nil && now.Before(m.cooldownUntil.UTC()) {
		m.state = StateCooldown
		m.summary = "cooldown after prior cloud/session error"
		m.lastDecision = "waiting for cooldown window before retry"
		m.publishLocked()
		m.persistLocked()
		return
	}

	if m.activeSessionID != "" {
		if m.stopCandidate == nil && m.shouldStopByIdleLocked(now, cfg) {
			m.stopCandidate = &transitionCandidate{
				NodeID: m.activeTriggerNode,
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
		m.summary = "waiting for tracked-node geofence transition"
		m.lastDecision = "control mode ready"
	} else {
		m.state = StateObserveReady
		m.summary = "waiting for tracked-node geofence transition"
		m.lastDecision = "observe mode ready"
	}
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) attemptStartLocked(ctx context.Context, now time.Time, cfg config.HomeAutoSessionConfig, trigger string) {
	if m.startCandidate == nil {
		return
	}

	candidate := *m.startCandidate
	if candidate.DedupeKey == "" {
		candidate.DedupeKey = dedupeKey("start", candidate.NodeID, candidate.At, candidate.Reason)
	}
	if strings.TrimSpace(candidate.DedupeKey) == strings.TrimSpace(m.lastStartDedupe) {
		m.startCandidate = nil
		m.state = StateControlReady
		m.summary = "duplicate start decision suppressed"
		m.lastDecision = "start dedupe key already executed"
		m.publishLocked()
		m.persistLocked()
		return
	}
	apiKey, startEndpoint, ok := m.resolveControlAuthLocked(cfg)
	if !ok {
		m.state = StateControlReady
		m.summary = "waiting for paired receiver credentials before control actions"
		m.lastDecision = "control action deferred: receiver not fully paired"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.client == nil {
		m.markDegradedLocked(now, "home auto session cloud client is unavailable")
		return
	}

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
		m.handleCloudErrorLocked(now, "start", err)
		return
	}

	m.startCandidate = nil
	m.stopCandidate = nil
	m.lastError = ""
	m.cooldownUntil = nil
	m.lastStartDedupe = candidate.DedupeKey
	m.activeSessionID = strings.TrimSpace(result.SessionID)
	m.activeTriggerNode = candidate.NodeID
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
		candidate.DedupeKey = dedupeKey("stop", candidate.NodeID, candidate.At, candidate.Reason)
	}
	if strings.TrimSpace(candidate.DedupeKey) == strings.TrimSpace(m.lastStopDedupe) {
		m.stopCandidate = nil
		m.state = StateControlReady
		m.summary = "duplicate stop decision suppressed"
		m.lastDecision = "stop dedupe key already executed"
		m.publishLocked()
		m.persistLocked()
		return
	}
	apiKey, stopEndpoint, ok := m.resolveControlAuthLocked(cfg)
	if !ok {
		m.state = StateControlReady
		m.summary = "waiting for paired receiver credentials before control actions"
		m.lastDecision = "control action deferred: receiver not fully paired"
		m.publishLocked()
		m.persistLocked()
		return
	}
	if m.client == nil {
		m.markDegradedLocked(now, "home auto session cloud client is unavailable")
		return
	}

	request := cloudclient.HomeAutoSessionStopRequest{
		SessionID:     m.activeSessionID,
		TriggerNodeID: candidate.NodeID,
		DedupeKey:     candidate.DedupeKey,
		Reason:        candidate.Reason,
		StoppedAt:     now.Format(time.RFC3339),
	}
	callCtx, cancel := context.WithTimeout(ctx, controlCallTimeout)
	defer cancel()
	_, err := m.client.StopHomeAutoSession(callCtx, stopEndpoint, apiKey, request)
	if err != nil {
		m.handleCloudErrorLocked(now, "stop", err)
		return
	}

	m.stopCandidate = nil
	m.startCandidate = nil
	m.lastError = ""
	m.cooldownUntil = nil
	m.lastStopDedupe = candidate.DedupeKey
	m.activeSessionID = ""
	m.activeTriggerNode = ""
	m.state = StateControlReady
	m.summary = "home auto session stopped"
	m.lastDecision = "session stop executed"
	m.logger.Info("home auto session stopped", "trigger_node", candidate.NodeID, "trigger", trigger)
	m.publishLocked()
	m.persistLocked()
}

func (m *Module) resolveControlAuthLocked(cfg config.HomeAutoSessionConfig) (string, string, bool) {
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
	endpoint := strings.TrimSpace(cfg.Cloud.StartEndpoint)
	if endpoint == "" {
		endpoint = "/api/receiver/home-auto-session/start"
	}
	if m.stopCandidate != nil {
		stopEndpoint := strings.TrimSpace(cfg.Cloud.StopEndpoint)
		if stopEndpoint == "" {
			stopEndpoint = "/api/receiver/home-auto-session/stop"
		}
		return apiKey, stopEndpoint, true
	}
	return apiKey, endpoint, true
}

func (m *Module) shouldStopByIdleLocked(now time.Time, cfg config.HomeAutoSessionConfig) bool {
	if m.activeTriggerNode == "" {
		return false
	}
	fact, ok := m.nodeFacts[strings.ToLower(m.activeTriggerNode)]
	if !ok || fact.LastSeenAt.IsZero() {
		return false
	}
	return now.Sub(fact.LastSeenAt) >= cfg.IdleStopTimeout.Std()
}

func (m *Module) handleCloudErrorLocked(now time.Time, action string, err error) {
	m.logger.Warn("home auto session cloud action failed", "action", action, "err", err)
	if cloudclient.IsRetryable(err) {
		cooldown := now.Add(retryCooldown)
		m.cooldownUntil = &cooldown
		m.state = StateCooldown
		m.summary = "cloud/session API unavailable, retrying after cooldown"
		m.lastDecision = "temporary cloud/session error"
		m.lastError = err.Error()
		m.publishLocked()
		m.persistLocked()
		return
	}
	m.markDegradedLocked(now, err.Error())
}

func (m *Module) markDegradedLocked(now time.Time, reason string) {
	m.lastError = strings.TrimSpace(reason)
	m.state = StateDegraded
	m.summary = "home auto session is degraded"
	m.lastDecision = "manual action required to recover control mode"
	cooldown := now.Add(retryCooldown)
	m.cooldownUntil = &cooldown
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
	observedDropped := m.observedDropped
	_ = m.store.Update(func(data *state.Data) {
		data.HomeAutoSession.ModuleState = stateCode
		data.HomeAutoSession.ActiveSessionID = activeID
		data.HomeAutoSession.ActiveTriggerNode = activeNode
		data.HomeAutoSession.LastDecisionReason = decision
		data.HomeAutoSession.LastStartDedupeKey = lastStart
		data.HomeAutoSession.LastStopDedupeKey = lastStop
		data.HomeAutoSession.LastError = lastErr
		data.HomeAutoSession.CooldownUntil = cooldown
		data.HomeAutoSession.ObservedDropped = observedDropped
		data.HomeAutoSession.LastDecisionAt = cloneTime(now)
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
	m.status.SetHomeAutoSession(status.HomeAutoSessionSnapshot{
		Enabled:            m.cfg.Enabled,
		Mode:               string(normalizeMode(m.cfg.Mode)),
		State:              string(m.state),
		Summary:            summary,
		HomeSummary:        formatHomeSummary(m.cfg.Home),
		TrackedNodeIDs:     tracked,
		ActiveSessionID:    m.activeSessionID,
		ActiveTriggerNode:  m.activeTriggerNode,
		LastDecisionReason: m.lastDecision,
		LastError:          m.lastError,
		ObservedQueueDepth: len(m.events),
		ObservedDropped:    m.observedDropped,
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

func pointInsideHome(home config.HomeGeofenceConfig, lat, lon float64) bool {
	return haversineMeters(home.Lat, home.Lon, lat, lon) <= home.RadiusM
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
