package status

import (
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

type Snapshot struct {
	InstallationID string                     `json:"installation_id"`
	Mode           string                     `json:"mode"`
	RuntimeProfile string                     `json:"runtime_profile"`
	Lifecycle      Lifecycle                  `json:"lifecycle"`
	PairingPhase   string                     `json:"pairing_phase"`
	CloudEndpoint  string                     `json:"cloud_endpoint"`
	CloudStatus    string                     `json:"cloud_status"`
	Ready          bool                       `json:"ready"`
	ReadyReason    string                     `json:"ready_reason,omitempty"`
	LastError      string                     `json:"last_error,omitempty"`
	StartedAt      time.Time                  `json:"started_at"`
	UpdatedAt      time.Time                  `json:"updated_at"`
	Components     map[string]ComponentStatus `json:"components,omitempty"`
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
			Lifecycle:  LifecycleStarting,
			StartedAt:  now,
			UpdatedAt:  now,
			CloudStatus: "unknown",
			Components: map[string]ComponentStatus{},
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
