package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type PairingStatus string

const (
	PairingUnpaired PairingStatus = "unpaired"
	PairingPending  PairingStatus = "pending"
	PairingPaired   PairingStatus = "paired"
)

type LocalState struct {
	PairingStatus PairingStatus `json:"pairing_status"`
	LastSeenMesh  *time.Time    `json:"last_seen_mesh,omitempty"`
	LastHeartbeat *time.Time    `json:"last_heartbeat,omitempty"`
	LastError     string        `json:"last_error,omitempty"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data LocalState
}

func NewStore(path string) (*Store, error) {
	s := &Store{
		path: path,
		data: LocalState{
			PairingStatus: PairingUnpaired,
		},
	}
	if err := s.Load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.data)
}

func (s *Store) Save() error {
	s.mu.RLock()
	snapshot := s.data
	s.mu.RUnlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, data, 0o600)
}

func (s *Store) Snapshot() LocalState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *Store) Update(fn func(*LocalState)) error {
	s.mu.Lock()
	fn(&s.data)
	s.mu.Unlock()
	return s.Save()
}
