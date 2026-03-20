package state

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PairingPhase string

const (
	CurrentSchemaVersion = 7

	PairingUnpaired           PairingPhase = "unpaired"
	PairingCodeEntered        PairingPhase = "pairing_code_entered"
	PairingBootstrapExchanged PairingPhase = "bootstrap_exchanged"
	PairingActivated          PairingPhase = "activated"
	PairingSteadyState        PairingPhase = "steady_state"
)

type Data struct {
	SchemaVersion   int                  `json:"schema_version"`
	Installation    InstallationState    `json:"installation"`
	Pairing         PairingState         `json:"pairing"`
	Cloud           CloudState           `json:"cloud"`
	Runtime         RuntimeState         `json:"runtime"`
	Update          UpdateState          `json:"update"`
	HomeAutoSession HomeAutoSessionState `json:"home_auto_session,omitempty"`
	Metadata        MetadataState        `json:"metadata"`
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

type HomeAutoSessionState struct {
	ModuleState            string     `json:"module_state,omitempty"`
	ControlState           string     `json:"control_state,omitempty"`
	ActiveStateSource      string     `json:"active_state_source,omitempty"`
	ReconciliationState    string     `json:"reconciliation_state,omitempty"`
	EffectiveConfigSource  string     `json:"effective_config_source,omitempty"`
	EffectiveConfigVersion string     `json:"effective_config_version,omitempty"`
	CloudConfigPresent     bool       `json:"cloud_config_present,omitempty"`
	LastFetchedConfigVer   string     `json:"last_fetched_config_version,omitempty"`
	LastAppliedConfigVer   string     `json:"last_applied_config_version,omitempty"`
	LastConfigApplyResult  string     `json:"last_config_apply_result,omitempty"`
	LastConfigApplyError   string     `json:"last_config_apply_error,omitempty"`
	DesiredConfigEnabled   *bool      `json:"desired_config_enabled,omitempty"`
	DesiredConfigMode      string     `json:"desired_config_mode,omitempty"`
	ActiveSessionID        string     `json:"active_session_id,omitempty"`
	ActiveTriggerNode      string     `json:"active_trigger_node_id,omitempty"`
	PendingAction          string     `json:"pending_action,omitempty"`
	PendingTriggerNode     string     `json:"pending_trigger_node_id,omitempty"`
	PendingReason          string     `json:"pending_reason,omitempty"`
	PendingDedupeKey       string     `json:"pending_dedupe_key,omitempty"`
	PendingSince           *time.Time `json:"pending_since,omitempty"`
	LastDecisionReason     string     `json:"last_decision_reason,omitempty"`
	LastStartDedupeKey     string     `json:"last_start_dedupe_key,omitempty"`
	LastStopDedupeKey      string     `json:"last_stop_dedupe_key,omitempty"`
	LastAction             string     `json:"last_action,omitempty"`
	LastActionResult       string     `json:"last_action_result,omitempty"`
	LastActionAt           *time.Time `json:"last_action_at,omitempty"`
	LastSuccessfulAction   string     `json:"last_successful_action,omitempty"`
	LastSuccessfulActionAt *time.Time `json:"last_successful_action_at,omitempty"`
	LastError              string     `json:"last_error,omitempty"`
	BlockedReason          string     `json:"blocked_reason,omitempty"`
	ConsecutiveFailures    int        `json:"consecutive_failures,omitempty"`
	LastDecisionAt         *time.Time `json:"last_decision_at,omitempty"`
	LastEventAt            *time.Time `json:"last_event_at,omitempty"`
	CooldownUntil          *time.Time `json:"cooldown_until,omitempty"`
	DecisionCooldownUntil  *time.Time `json:"decision_cooldown_until,omitempty"`
	GPSStatus              string     `json:"gps_status,omitempty"`
	GPSReason              string     `json:"gps_reason,omitempty"`
	GPSNodeID              string     `json:"gps_node_id,omitempty"`
	GPSUpdatedAt           *time.Time `json:"gps_updated_at,omitempty"`
	GPSDistanceM           *float64   `json:"gps_distance_m,omitempty"`
	ObservedDropped        int        `json:"observed_dropped,omitempty"`
	UpdatedAt              time.Time  `json:"updated_at,omitempty"`
}

type MetadataState struct {
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	path            string
	mu              sync.RWMutex
	data            Data
	now             func() time.Time
	recoveredOnLoad bool
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
	if changed || migrated || s.recoveredOnLoad {
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
	decoded, recovered, err := decodeStateBytes(data)
	if err != nil {
		backupPath, backupErr := backupCorruptStateFile(s.path, data)
		if backupErr != nil {
			return fmt.Errorf("%w (backup failed: %v)", err, backupErr)
		}
		return fmt.Errorf("state file is corrupt; backed up to %s: %w", backupPath, os.ErrNotExist)
	}

	s.recoveredOnLoad = recovered
	s.data = decoded
	return nil
}

func decodeStateBytes(raw []byte) (Data, bool, error) {
	var decoded Data
	if err := json.Unmarshal(raw, &decoded); err == nil {
		return decoded, false, nil
	}

	trimmed := bytes.Trim(raw, "\x00\r\n\t ")
	if len(trimmed) > 0 && !bytes.Equal(trimmed, raw) {
		if err := json.Unmarshal(trimmed, &decoded); err == nil {
			return decoded, true, nil
		}
	}

	if len(trimmed) > 0 {
		start := bytes.IndexByte(trimmed, '{')
		end := bytes.LastIndexByte(trimmed, '}')
		if start >= 0 && end > start {
			candidate := trimmed[start : end+1]
			if err := json.Unmarshal(candidate, &decoded); err == nil {
				return decoded, true, nil
			}
		}
	}

	dense := bytes.ReplaceAll(raw, []byte{0}, nil)
	if len(dense) > 0 && !bytes.Equal(dense, raw) {
		if err := json.Unmarshal(dense, &decoded); err == nil {
			return decoded, true, nil
		}
	}

	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Data{}, false, err
	}
	return decoded, false, nil
}

func backupCorruptStateFile(path string, payload []byte) (string, error) {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	backupName := fmt.Sprintf("%s.corrupt-%s", base, time.Now().UTC().Format("20060102T150405Z"))
	backupPath := filepath.Join(dir, backupName)
	if err := os.Rename(path, backupPath); err == nil {
		return backupPath, nil
	}

	if err := os.WriteFile(backupPath, payload, 0o600); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return backupPath, nil
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
	if strings.TrimSpace(s.data.HomeAutoSession.ModuleState) == "" {
		s.data.HomeAutoSession.ModuleState = "disabled"
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.ControlState) == "" {
		s.data.HomeAutoSession.ControlState = "disabled"
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.ActiveStateSource) == "" {
		s.data.HomeAutoSession.ActiveStateSource = "none"
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.ReconciliationState) == "" {
		s.data.HomeAutoSession.ReconciliationState = "clean_idle"
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.EffectiveConfigSource) == "" {
		s.data.HomeAutoSession.EffectiveConfigSource = "local_fallback"
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.EffectiveConfigVersion) == "" {
		s.data.HomeAutoSession.EffectiveConfigVersion = "local-default"
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.LastAppliedConfigVer) == "" {
		s.data.HomeAutoSession.LastAppliedConfigVer = s.data.HomeAutoSession.EffectiveConfigVersion
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.LastConfigApplyResult) == "" {
		s.data.HomeAutoSession.LastConfigApplyResult = "local_config_applied"
		changed = true
	}
	if s.data.HomeAutoSession.DesiredConfigEnabled == nil {
		defaultValue := false
		s.data.HomeAutoSession.DesiredConfigEnabled = &defaultValue
		changed = true
	}
	if strings.TrimSpace(s.data.HomeAutoSession.DesiredConfigMode) == "" {
		s.data.HomeAutoSession.DesiredConfigMode = "off"
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
	if version <= 3 {
		if strings.TrimSpace(s.data.HomeAutoSession.ModuleState) == "" {
			s.data.HomeAutoSession.ModuleState = "disabled"
			changed = true
		}
		version = 4
		changed = true
	}
	if version <= 4 {
		if strings.TrimSpace(s.data.HomeAutoSession.ReconciliationState) == "" {
			s.data.HomeAutoSession.ReconciliationState = "clean_idle"
			changed = true
		}
		version = 5
		changed = true
	}
	if version <= 5 {
		if strings.TrimSpace(s.data.HomeAutoSession.ControlState) == "" {
			s.data.HomeAutoSession.ControlState = "disabled"
			changed = true
		}
		if strings.TrimSpace(s.data.HomeAutoSession.ActiveStateSource) == "" {
			s.data.HomeAutoSession.ActiveStateSource = "none"
			changed = true
		}
		version = 6
		changed = true
	}
	if version <= 6 {
		if strings.TrimSpace(s.data.HomeAutoSession.EffectiveConfigSource) == "" {
			s.data.HomeAutoSession.EffectiveConfigSource = "local_fallback"
			changed = true
		}
		if strings.TrimSpace(s.data.HomeAutoSession.EffectiveConfigVersion) == "" {
			s.data.HomeAutoSession.EffectiveConfigVersion = "local-default"
			changed = true
		}
		if strings.TrimSpace(s.data.HomeAutoSession.LastAppliedConfigVer) == "" {
			s.data.HomeAutoSession.LastAppliedConfigVer = s.data.HomeAutoSession.EffectiveConfigVersion
			changed = true
		}
		if strings.TrimSpace(s.data.HomeAutoSession.LastConfigApplyResult) == "" {
			s.data.HomeAutoSession.LastConfigApplyResult = "local_config_applied"
			changed = true
		}
		if s.data.HomeAutoSession.DesiredConfigEnabled == nil {
			defaultValue := false
			s.data.HomeAutoSession.DesiredConfigEnabled = &defaultValue
			changed = true
		}
		if strings.TrimSpace(s.data.HomeAutoSession.DesiredConfigMode) == "" {
			s.data.HomeAutoSession.DesiredConfigMode = "off"
			changed = true
		}
		version = 7
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
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		return err
	}
	if err := syncDirectory(dir); err != nil {
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

func syncDirectory(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	defer dir.Close()
	if err := dir.Sync(); err != nil {
		// Some filesystems may not support directory sync.
		if errors.Is(err, os.ErrInvalid) {
			return nil
		}
		return err
	}
	return nil
}
