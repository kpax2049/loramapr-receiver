package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type PairingPhase string

const (
	PairingUnpaired           PairingPhase = "unpaired"
	PairingCodeEntered        PairingPhase = "pairing_code_entered"
	PairingBootstrapExchanged PairingPhase = "bootstrap_exchanged"
	PairingActivated          PairingPhase = "activated"
	PairingSteadyState        PairingPhase = "steady_state"
)

type Data struct {
	Installation InstallationState `json:"installation"`
	Pairing      PairingState      `json:"pairing"`
	Cloud        CloudState        `json:"cloud"`
	Runtime      RuntimeState      `json:"runtime"`
	Metadata     MetadataState     `json:"metadata"`
}

type InstallationState struct {
	ID            string    `json:"id"`
	CreatedAt     time.Time `json:"created_at"`
	LastStartedAt time.Time `json:"last_started_at"`
}

type PairingState struct {
	Phase      PairingPhase `json:"phase"`
	LastError  string       `json:"last_error,omitempty"`
	UpdatedAt  time.Time    `json:"updated_at,omitempty"`
	LastChange string       `json:"last_change,omitempty"`
}

type CloudState struct {
	EndpointURL   string    `json:"endpoint_url"`
	ReceiverID    string    `json:"receiver_id,omitempty"`
	CredentialRef string    `json:"credential_ref,omitempty"`
	UpdatedAt     time.Time `json:"updated_at,omitempty"`
}

type RuntimeState struct {
	Profile string `json:"profile,omitempty"`
	Mode    string `json:"mode,omitempty"`
}

type MetadataState struct {
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	data Data
	now  func() time.Time
}

func Open(path string) (*Store, error) {
	s := &Store{
		path: path,
		now:  time.Now,
	}
	if err := s.load(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			_, err := s.ensureDefaults()
			if err != nil {
				return nil, err
			}
			if err := s.Save(); err != nil {
				return nil, err
			}
			return s, nil
		}
		return nil, err
	}

	changed, err := s.ensureDefaults()
	if err != nil {
		return nil, err
	}
	if changed {
		if err := s.Save(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) Path() string {
	return s.path
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &s.data); err != nil {
		return err
	}
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	snapshot := s.data
	s.mu.RUnlock()

	snapshot.Metadata.UpdatedAt = s.now().UTC()
	return s.write(snapshot)
}

func (s *Store) Snapshot() Data {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.data
}

func (s *Store) Update(fn func(*Data)) error {
	s.mu.Lock()
	fn(&s.data)
	snapshot := s.data
	s.mu.Unlock()

	snapshot.Metadata.UpdatedAt = s.now().UTC()
	return s.write(snapshot)
}

func (s *Store) ensureDefaults() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	if s.data.Installation.ID == "" {
		id, err := newInstallID()
		if err != nil {
			return false, err
		}
		s.data.Installation.ID = id
		changed = true
	}

	now := s.now().UTC()
	if s.data.Installation.CreatedAt.IsZero() {
		s.data.Installation.CreatedAt = now
		changed = true
	}
	if s.data.Installation.LastStartedAt.IsZero() {
		s.data.Installation.LastStartedAt = now
		changed = true
	}

	if s.data.Pairing.Phase == "" {
		s.data.Pairing.Phase = PairingUnpaired
		changed = true
	}
	return changed, nil
}

func (s *Store) write(snapshot Data) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(snapshot, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".receiver-state-*.tmp")
	if err != nil {
		return err
	}

	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write([]byte("\n")); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}

	s.mu.Lock()
	s.data = snapshot
	s.mu.Unlock()
	return nil
}

func newInstallID() (string, error) {
	buf := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
