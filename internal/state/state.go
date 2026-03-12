package state

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PairingPhase string

const (
	CurrentSchemaVersion = 3

	PairingUnpaired           PairingPhase = "unpaired"
	PairingCodeEntered        PairingPhase = "pairing_code_entered"
	PairingBootstrapExchanged PairingPhase = "bootstrap_exchanged"
	PairingActivated          PairingPhase = "activated"
	PairingSteadyState        PairingPhase = "steady_state"
)

type Data struct {
	SchemaVersion int               `json:"schema_version"`
	Installation  InstallationState `json:"installation"`
	Pairing       PairingState      `json:"pairing"`
	Cloud         CloudState        `json:"cloud"`
	Runtime       RuntimeState      `json:"runtime"`
	Update        UpdateState       `json:"update"`
	Metadata      MetadataState     `json:"metadata"`
}

type InstallationState struct {
	ID            string    `json:"id"`
	LocalName     string    `json:"local_name,omitempty"`
	Hostname      string    `json:"hostname,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastStartedAt time.Time `json:"last_started_at"`
}

type PairingState struct {
	Phase             PairingPhase `json:"phase"`
	PairingCode       string       `json:"pairing_code,omitempty"`
	InstallSessionID  string       `json:"install_session_id,omitempty"`
	FlowKey           string       `json:"flow_key,omitempty"`
	ActivationToken   string       `json:"activation_token,omitempty"`
	ActivationExpires *time.Time   `json:"activation_expires_at,omitempty"`
	RetryCount        int          `json:"retry_count,omitempty"`
	NextRetryAt       *time.Time   `json:"next_retry_at,omitempty"`
	LastAttemptAt     *time.Time   `json:"last_attempt_at,omitempty"`
	LastError         string       `json:"last_error,omitempty"`
	UpdatedAt         time.Time    `json:"updated_at,omitempty"`
	LastChange        string       `json:"last_change,omitempty"`
}

type CloudState struct {
	EndpointURL       string    `json:"endpoint_url"`
	ConfigVersion     string    `json:"config_version,omitempty"`
	ActivateEndpoint  string    `json:"activate_endpoint,omitempty"`
	HeartbeatEndpoint string    `json:"heartbeat_endpoint,omitempty"`
	IngestEndpoint    string    `json:"ingest_endpoint,omitempty"`
	OwnerID           string    `json:"owner_id,omitempty"`
	ReceiverID        string    `json:"receiver_id,omitempty"`
	ReceiverLabel     string    `json:"receiver_label,omitempty"`
	SiteLabel         string    `json:"site_label,omitempty"`
	GroupLabel        string    `json:"group_label,omitempty"`
	IngestAPIKeyID    string    `json:"ingest_api_key_id,omitempty"`
	IngestAPIKey      string    `json:"ingest_api_key_secret,omitempty"`
	CredentialRef     string    `json:"credential_ref,omitempty"`
	UpdatedAt         time.Time `json:"updated_at,omitempty"`
}

type RuntimeState struct {
	Profile     string `json:"profile,omitempty"`
	Mode        string `json:"mode,omitempty"`
	InstallType string `json:"install_type,omitempty"`
}

type UpdateState struct {
	Status             string     `json:"status,omitempty"`
	Summary            string     `json:"summary,omitempty"`
	Hint               string     `json:"hint,omitempty"`
	ManifestVersion    string     `json:"manifest_version,omitempty"`
	ManifestChannel    string     `json:"manifest_channel,omitempty"`
	RecommendedVersion string     `json:"recommended_version,omitempty"`
	LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
	LastError          string     `json:"last_error,omitempty"`
	UpdatedAt          time.Time  `json:"updated_at,omitempty"`
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

	migrated, err := s.migrate()
	if err != nil {
		return nil, err
	}
	changed, err := s.ensureDefaults()
	if err != nil {
		return nil, err
	}
	if changed || migrated {
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
	if s.data.SchemaVersion == 0 {
		s.data.SchemaVersion = CurrentSchemaVersion
		changed = true
	}
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
	if s.data.Runtime.InstallType == "" {
		s.data.Runtime.InstallType = installTypeFromProfile(s.data.Runtime.Profile)
		changed = true
	}
	if !isKnownUpdateStatus(s.data.Update.Status) {
		s.data.Update.Status = "unknown"
		changed = true
	}
	return changed, nil
}

func (s *Store) migrate() (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	changed := false
	version := s.data.SchemaVersion
	if version == 0 {
		version = 1
		changed = true
	}

	if version <= 1 {
		switch s.data.Pairing.Phase {
		case "paired", "ready":
			s.data.Pairing.Phase = PairingSteadyState
			changed = true
		case "pairing":
			s.data.Pairing.Phase = PairingCodeEntered
			changed = true
		case "":
			// default assignment is handled in ensureDefaults
		default:
			if !isValidPhase(s.data.Pairing.Phase) {
				s.data.Pairing.Phase = PairingUnpaired
				changed = true
			}
		}
		version = 2
		changed = true
	}
	if version <= 2 {
		if s.data.Runtime.InstallType == "" {
			s.data.Runtime.InstallType = installTypeFromProfile(s.data.Runtime.Profile)
			changed = true
		}
		if !isKnownUpdateStatus(s.data.Update.Status) {
			s.data.Update.Status = "unknown"
			changed = true
		}
		version = 3
		changed = true
	}

	if version > CurrentSchemaVersion {
		return false, errors.New("state schema version is newer than this runtime")
	}

	if s.data.SchemaVersion != version {
		s.data.SchemaVersion = version
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

func isValidPhase(phase PairingPhase) bool {
	switch phase {
	case PairingUnpaired, PairingCodeEntered, PairingBootstrapExchanged, PairingActivated, PairingSteadyState:
		return true
	default:
		return false
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

func isKnownUpdateStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "unknown", "disabled", "current", "outdated", "channel_mismatch", "unsupported", "ahead":
		return true
	default:
		return false
	}
}
