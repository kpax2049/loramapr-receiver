package status

import (
	"strings"
	"sync"
	"time"
)

type Lifecycle string

const (
	LifecycleStarting Lifecycle = "starting"
	LifecycleRunning  Lifecycle = "running"
	LifecycleStopping Lifecycle = "stopping"
	LifecycleStopped  Lifecycle = "stopped"
	LifecycleFailed   Lifecycle = "failed"
)

type ComponentStatus struct {
	State     string    `json:"state"`
	Message   string    `json:"message,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type FailureEvent struct {
	Code      string    `json:"code"`
	Summary   string    `json:"summary"`
	Hint      string    `json:"hint,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
}

type HomeAutoSessionSnapshot struct {
	Enabled               bool       `json:"enabled"`
	Mode                  string     `json:"mode,omitempty"`
	EffectiveConfigSource string     `json:"effective_config_source,omitempty"`
	EffectiveConfigVer    string     `json:"effective_config_version,omitempty"`
	CloudConfigPresent    bool       `json:"cloud_config_present,omitempty"`
	LastFetchedConfigVer  string     `json:"last_fetched_config_version,omitempty"`
	LastAppliedConfigVer  string     `json:"last_applied_config_version,omitempty"`
	LastConfigApplyResult string     `json:"last_config_apply_result,omitempty"`
	LastConfigApplyError  string     `json:"last_config_apply_error,omitempty"`
	DesiredConfigEnabled  bool       `json:"desired_config_enabled"`
	DesiredConfigMode     string     `json:"desired_config_mode,omitempty"`
	State                 string     `json:"state,omitempty"`
	ControlState          string     `json:"control_state,omitempty"`
	ActiveStateSource     string     `json:"active_state_source,omitempty"`
	Summary               string     `json:"summary,omitempty"`
	HomeSummary           string     `json:"home_summary,omitempty"`
	TrackedNodeIDs        []string   `json:"tracked_node_ids,omitempty"`
	TrackedNodeState      string     `json:"tracked_node_state,omitempty"`
	ReconciliationState   string     `json:"reconciliation_state,omitempty"`
	PendingAction         string     `json:"pending_action,omitempty"`
	PendingSince          *time.Time `json:"pending_since,omitempty"`
	ActiveSessionID       string     `json:"active_session_id,omitempty"`
	ActiveTriggerNode     string     `json:"active_trigger_node_id,omitempty"`
	LastDecisionReason    string     `json:"last_decision_reason,omitempty"`
	LastError             string     `json:"last_error,omitempty"`
	LastAction            string     `json:"last_action,omitempty"`
	LastActionResult      string     `json:"last_action_result,omitempty"`
	LastActionAt          *time.Time `json:"last_action_at,omitempty"`
	LastSuccessfulAction  string     `json:"last_successful_action,omitempty"`
	LastSuccessfulAt      *time.Time `json:"last_successful_at,omitempty"`
	BlockedReason         string     `json:"blocked_reason,omitempty"`
	ConsecutiveFailures   int        `json:"consecutive_failures,omitempty"`
	CooldownUntil         *time.Time `json:"cooldown_until,omitempty"`
	DecisionCooldownUntil *time.Time `json:"decision_cooldown_until,omitempty"`
	GPSStatus             string     `json:"gps_status,omitempty"`
	GPSReason             string     `json:"gps_reason,omitempty"`
	GPSNodeID             string     `json:"gps_node_id,omitempty"`
	GPSUpdatedAt          *time.Time `json:"gps_updated_at,omitempty"`
	GPSDistanceM          *float64   `json:"gps_distance_m,omitempty"`
	ObservedQueueDepth    int        `json:"observed_queue_depth,omitempty"`
	ObservedDropped       int        `json:"observed_dropped,omitempty"`
	UpdatedAt             time.Time  `json:"updated_at,omitempty"`
}

type Snapshot struct {
	InstallationID           string                     `json:"installation_id"`
	LocalName                string                     `json:"local_name,omitempty"`
	Hostname                 string                     `json:"hostname,omitempty"`
	CloudReceiverID          string                     `json:"cloud_receiver_id,omitempty"`
	CloudReceiverLabel       string                     `json:"cloud_receiver_label,omitempty"`
	CloudSiteLabel           string                     `json:"cloud_site_label,omitempty"`
	CloudGroupLabel          string                     `json:"cloud_group_label,omitempty"`
	ReceiverVersion          string                     `json:"receiver_version,omitempty"`
	ReleaseChannel           string                     `json:"release_channel,omitempty"`
	BuildCommit              string                     `json:"build_commit,omitempty"`
	BuildDate                string                     `json:"build_date,omitempty"`
	BuildID                  string                     `json:"build_id,omitempty"`
	Platform                 string                     `json:"platform,omitempty"`
	Arch                     string                     `json:"arch,omitempty"`
	InstallType              string                     `json:"install_type,omitempty"`
	Mode                     string                     `json:"mode"`
	RuntimeProfile           string                     `json:"runtime_profile"`
	Lifecycle                Lifecycle                  `json:"lifecycle"`
	PairingPhase             string                     `json:"pairing_phase"`
	CloudEndpoint            string                     `json:"cloud_endpoint"`
	CloudStatus              string                     `json:"cloud_status"`
	CloudReachable           bool                       `json:"cloud_reachable"`
	Ready                    bool                       `json:"ready"`
	ReadyReason              string                     `json:"ready_reason,omitempty"`
	LastError                string                     `json:"last_error,omitempty"`
	LastHeartbeatSent        *time.Time                 `json:"last_heartbeat_sent,omitempty"`
	LastHeartbeatAck         *time.Time                 `json:"last_heartbeat_ack,omitempty"`
	HeartbeatFresh           bool                       `json:"heartbeat_fresh"`
	LastPacketQueued         *time.Time                 `json:"last_packet_queued,omitempty"`
	LastPacketSent           *time.Time                 `json:"last_packet_sent,omitempty"`
	LastPacketAck            *time.Time                 `json:"last_packet_ack,omitempty"`
	IngestQueueDepth         int                        `json:"ingest_queue_depth,omitempty"`
	FailureCode              string                     `json:"failure_code,omitempty"`
	FailureSummary           string                     `json:"failure_summary,omitempty"`
	FailureHint              string                     `json:"failure_hint,omitempty"`
	FailureSince             *time.Time                 `json:"failure_since,omitempty"`
	RecentFailures           []FailureEvent             `json:"recent_failures,omitempty"`
	AttentionState           string                     `json:"attention_state,omitempty"`
	AttentionCategory        string                     `json:"attention_category,omitempty"`
	AttentionCode            string                     `json:"attention_code,omitempty"`
	AttentionSummary         string                     `json:"attention_summary,omitempty"`
	AttentionHint            string                     `json:"attention_hint,omitempty"`
	AttentionActionRequired  bool                       `json:"attention_action_required,omitempty"`
	AttentionUpdatedAt       *time.Time                 `json:"attention_updated_at,omitempty"`
	UpdateStatus             string                     `json:"update_status,omitempty"`
	UpdateSummary            string                     `json:"update_summary,omitempty"`
	UpdateHint               string                     `json:"update_hint,omitempty"`
	UpdateManifestVersion    string                     `json:"update_manifest_version,omitempty"`
	UpdateManifestChannel    string                     `json:"update_manifest_channel,omitempty"`
	UpdateRecommendedVersion string                     `json:"update_recommended_version,omitempty"`
	UpdateCheckedAt          *time.Time                 `json:"update_checked_at,omitempty"`
	HomeAutoSession          HomeAutoSessionSnapshot    `json:"home_auto_session"`
	StartedAt                time.Time                  `json:"started_at"`
	UpdatedAt                time.Time                  `json:"updated_at"`
	Components               map[string]ComponentStatus `json:"components,omitempty"`
}

type Model struct {
	mu   sync.RWMutex
	now  func() time.Time
	snap Snapshot
}

func New() *Model {
	now := time.Now().UTC()
	return &Model{
		now: time.Now,
		snap: Snapshot{
			Lifecycle:   LifecycleStarting,
			StartedAt:   now,
			UpdatedAt:   now,
			CloudStatus: "unknown",
			Components:  map[string]ComponentStatus{},
		},
	}
}

func (m *Model) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := m.snap
	out.Components = make(map[string]ComponentStatus, len(m.snap.Components))
	for key, val := range m.snap.Components {
		out.Components[key] = val
	}
	out.RecentFailures = append([]FailureEvent(nil), m.snap.RecentFailures...)
	out.FailureSince = cloneTimePtr(m.snap.FailureSince)
	out.AttentionUpdatedAt = cloneTimePtr(m.snap.AttentionUpdatedAt)
	out.UpdateCheckedAt = cloneTimePtr(m.snap.UpdateCheckedAt)
	out.HomeAutoSession.TrackedNodeIDs = append([]string(nil), m.snap.HomeAutoSession.TrackedNodeIDs...)
	out.HomeAutoSession.PendingSince = cloneTimePtr(m.snap.HomeAutoSession.PendingSince)
	out.HomeAutoSession.LastActionAt = cloneTimePtr(m.snap.HomeAutoSession.LastActionAt)
	out.HomeAutoSession.LastSuccessfulAt = cloneTimePtr(m.snap.HomeAutoSession.LastSuccessfulAt)
	out.HomeAutoSession.CooldownUntil = cloneTimePtr(m.snap.HomeAutoSession.CooldownUntil)
	out.HomeAutoSession.DecisionCooldownUntil = cloneTimePtr(m.snap.HomeAutoSession.DecisionCooldownUntil)
	out.HomeAutoSession.GPSUpdatedAt = cloneTimePtr(m.snap.HomeAutoSession.GPSUpdatedAt)
	out.HomeAutoSession.GPSDistanceM = cloneFloat64Ptr(m.snap.HomeAutoSession.GPSDistanceM)
	return out
}

func (m *Model) Update(fn func(*Snapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(&m.snap)
	m.snap.UpdatedAt = m.now().UTC()
}

func (m *Model) SetLifecycle(l Lifecycle) {
	m.Update(func(s *Snapshot) {
		s.Lifecycle = l
	})
}

func (m *Model) SetMode(mode string) {
	m.Update(func(s *Snapshot) {
		s.Mode = mode
	})
}

func (m *Model) SetRuntimeProfile(profile string) {
	m.Update(func(s *Snapshot) {
		s.RuntimeProfile = profile
	})
}

func (m *Model) SetInstallationID(id string) {
	m.Update(func(s *Snapshot) {
		s.InstallationID = id
	})
}

func (m *Model) SetIdentity(localName, hostname, cloudReceiverID, cloudReceiverLabel, cloudSiteLabel, cloudGroupLabel string) {
	m.Update(func(s *Snapshot) {
		s.LocalName = normalize(localName)
		s.Hostname = normalize(hostname)
		s.CloudReceiverID = normalize(cloudReceiverID)
		s.CloudReceiverLabel = normalize(cloudReceiverLabel)
		s.CloudSiteLabel = normalize(cloudSiteLabel)
		s.CloudGroupLabel = normalize(cloudGroupLabel)
	})
}

func (m *Model) SetBuildInfo(version, channel, commit, buildDate, buildID, platform, arch, installType string) {
	m.Update(func(s *Snapshot) {
		s.ReceiverVersion = normalize(version)
		s.ReleaseChannel = normalize(channel)
		s.BuildCommit = normalize(commit)
		s.BuildDate = normalize(buildDate)
		s.BuildID = normalize(buildID)
		s.Platform = normalize(platform)
		s.Arch = normalize(arch)
		s.InstallType = normalize(installType)
	})
}

func (m *Model) SetPairingPhase(phase string) {
	m.Update(func(s *Snapshot) {
		s.PairingPhase = phase
	})
}

func (m *Model) SetCloud(endpoint, cloudStatus string) {
	m.Update(func(s *Snapshot) {
		s.CloudEndpoint = endpoint
		s.CloudStatus = cloudStatus
	})
}

func (m *Model) SetCloudReachable(reachable bool) {
	m.Update(func(s *Snapshot) {
		s.CloudReachable = reachable
	})
}

func (m *Model) SetReady(ready bool, reason string) {
	m.Update(func(s *Snapshot) {
		s.Ready = ready
		s.ReadyReason = reason
	})
}

func (m *Model) SetLastError(msg string) {
	m.Update(func(s *Snapshot) {
		s.LastError = msg
	})
}

func (m *Model) SetHeartbeat(sentAt, ackAt *time.Time, fresh bool) {
	m.Update(func(s *Snapshot) {
		s.LastHeartbeatSent = cloneTimePtr(sentAt)
		s.LastHeartbeatAck = cloneTimePtr(ackAt)
		s.HeartbeatFresh = fresh
	})
}

func (m *Model) SetPacketTelemetry(queuedAt, sentAt, ackAt *time.Time, queueDepth int) {
	m.Update(func(s *Snapshot) {
		s.LastPacketQueued = cloneTimePtr(queuedAt)
		s.LastPacketSent = cloneTimePtr(sentAt)
		s.LastPacketAck = cloneTimePtr(ackAt)
		s.IngestQueueDepth = queueDepth
	})
}

func (m *Model) SetComponent(name, state, message string) {
	m.Update(func(s *Snapshot) {
		if s.Components == nil {
			s.Components = map[string]ComponentStatus{}
		}
		s.Components[name] = ComponentStatus{
			State:     state,
			Message:   message,
			UpdatedAt: m.now().UTC(),
		}
	})
}

func (m *Model) SetFailure(code, summary, hint string) {
	m.Update(func(s *Snapshot) {
		now := m.now().UTC()
		normalizedCode := normalize(code)
		normalizedSummary := normalize(summary)
		normalizedHint := normalize(hint)

		if normalizedCode == "" {
			s.FailureCode = ""
			s.FailureSummary = ""
			s.FailureHint = ""
			s.FailureSince = nil
			return
		}

		if s.FailureCode != normalizedCode || s.FailureSummary != normalizedSummary || s.FailureHint != normalizedHint {
			updatedAt := now
			s.FailureSince = &updatedAt
			s.RecentFailures = append(s.RecentFailures, FailureEvent{
				Code:      normalizedCode,
				Summary:   normalizedSummary,
				Hint:      normalizedHint,
				UpdatedAt: now,
			})
			if len(s.RecentFailures) > 8 {
				s.RecentFailures = append([]FailureEvent(nil), s.RecentFailures[len(s.RecentFailures)-8:]...)
			}
		}

		s.FailureCode = normalizedCode
		s.FailureSummary = normalizedSummary
		s.FailureHint = normalizedHint
	})
}

func (m *Model) SetUpdateStatus(statusCode, summary, hint, manifestVersion, manifestChannel, recommendedVersion string, checkedAt *time.Time) {
	m.Update(func(s *Snapshot) {
		s.UpdateStatus = normalize(statusCode)
		s.UpdateSummary = normalize(summary)
		s.UpdateHint = normalize(hint)
		s.UpdateManifestVersion = normalize(manifestVersion)
		s.UpdateManifestChannel = normalize(manifestChannel)
		s.UpdateRecommendedVersion = normalize(recommendedVersion)
		s.UpdateCheckedAt = cloneTimePtr(checkedAt)
	})
}

func (m *Model) SetAttention(state, category, code, summary, hint string, actionRequired bool) {
	m.Update(func(s *Snapshot) {
		s.AttentionState = normalize(state)
		s.AttentionCategory = normalize(category)
		s.AttentionCode = normalize(code)
		s.AttentionSummary = normalize(summary)
		s.AttentionHint = normalize(hint)
		s.AttentionActionRequired = actionRequired
		if s.AttentionState == "" || s.AttentionState == "none" {
			s.AttentionUpdatedAt = nil
			return
		}
		updatedAt := m.now().UTC()
		s.AttentionUpdatedAt = &updatedAt
	})
}

func (m *Model) SetHomeAutoSession(module HomeAutoSessionSnapshot) {
	m.Update(func(s *Snapshot) {
		s.HomeAutoSession = HomeAutoSessionSnapshot{
			Enabled:               module.Enabled,
			Mode:                  normalize(module.Mode),
			EffectiveConfigSource: normalize(module.EffectiveConfigSource),
			EffectiveConfigVer:    normalize(module.EffectiveConfigVer),
			CloudConfigPresent:    module.CloudConfigPresent,
			LastFetchedConfigVer:  normalize(module.LastFetchedConfigVer),
			LastAppliedConfigVer:  normalize(module.LastAppliedConfigVer),
			LastConfigApplyResult: normalize(module.LastConfigApplyResult),
			LastConfigApplyError:  normalize(module.LastConfigApplyError),
			DesiredConfigEnabled:  module.DesiredConfigEnabled,
			DesiredConfigMode:     normalize(module.DesiredConfigMode),
			State:                 normalize(module.State),
			ControlState:          normalize(module.ControlState),
			ActiveStateSource:     normalize(module.ActiveStateSource),
			Summary:               normalize(module.Summary),
			HomeSummary:           normalize(module.HomeSummary),
			TrackedNodeIDs:        append([]string(nil), module.TrackedNodeIDs...),
			TrackedNodeState:      normalize(module.TrackedNodeState),
			ReconciliationState:   normalize(module.ReconciliationState),
			PendingAction:         normalize(module.PendingAction),
			PendingSince:          cloneTimePtr(module.PendingSince),
			ActiveSessionID:       normalize(module.ActiveSessionID),
			ActiveTriggerNode:     normalize(module.ActiveTriggerNode),
			LastDecisionReason:    normalize(module.LastDecisionReason),
			LastError:             normalize(module.LastError),
			LastAction:            normalize(module.LastAction),
			LastActionResult:      normalize(module.LastActionResult),
			LastActionAt:          cloneTimePtr(module.LastActionAt),
			LastSuccessfulAction:  normalize(module.LastSuccessfulAction),
			LastSuccessfulAt:      cloneTimePtr(module.LastSuccessfulAt),
			BlockedReason:         normalize(module.BlockedReason),
			ConsecutiveFailures:   module.ConsecutiveFailures,
			CooldownUntil:         cloneTimePtr(module.CooldownUntil),
			DecisionCooldownUntil: cloneTimePtr(module.DecisionCooldownUntil),
			GPSStatus:             normalize(module.GPSStatus),
			GPSReason:             normalize(module.GPSReason),
			GPSNodeID:             normalize(module.GPSNodeID),
			GPSUpdatedAt:          cloneTimePtr(module.GPSUpdatedAt),
			GPSDistanceM:          cloneFloat64Ptr(module.GPSDistanceM),
			ObservedQueueDepth:    module.ObservedQueueDepth,
			ObservedDropped:       module.ObservedDropped,
			UpdatedAt:             m.now().UTC(),
		}
	})
}

func normalize(value string) string {
	return strings.TrimSpace(value)
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
