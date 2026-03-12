package state

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenInitializesDefaults(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	snapshot := store.Snapshot()
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.Installation.ID == "" {
		t.Fatal("expected installation ID to be generated")
	}
	if snapshot.Pairing.Phase != PairingUnpaired {
		t.Fatalf("expected default pairing phase %q, got %q", PairingUnpaired, snapshot.Pairing.Phase)
	}
	if snapshot.Installation.CreatedAt.IsZero() {
		t.Fatal("expected CreatedAt to be populated")
	}
	if snapshot.Update.Status != "unknown" {
		t.Fatalf("expected default update status unknown, got %q", snapshot.Update.Status)
	}
	if snapshot.HomeAutoSession.ReconciliationState != "clean_idle" {
		t.Fatalf("expected default home_auto_session reconciliation clean_idle, got %q", snapshot.HomeAutoSession.ReconciliationState)
	}
	if snapshot.HomeAutoSession.ControlState != "disabled" {
		t.Fatalf("expected default home_auto_session control_state disabled, got %q", snapshot.HomeAutoSession.ControlState)
	}
	if snapshot.HomeAutoSession.ActiveStateSource != "none" {
		t.Fatalf("expected default home_auto_session active_state_source none, got %q", snapshot.HomeAutoSession.ActiveStateSource)
	}
	if snapshot.HomeAutoSession.EffectiveConfigSource != "local_fallback" {
		t.Fatalf("expected default home_auto_session effective_config_source local_fallback, got %q", snapshot.HomeAutoSession.EffectiveConfigSource)
	}
	if snapshot.HomeAutoSession.EffectiveConfigVersion == "" {
		t.Fatal("expected default home_auto_session effective_config_version")
	}
	if snapshot.HomeAutoSession.LastConfigApplyResult == "" {
		t.Fatal("expected default home_auto_session last_config_apply_result")
	}
	if snapshot.HomeAutoSession.DesiredConfigEnabled == nil {
		t.Fatal("expected default home_auto_session desired_config_enabled")
	}
	if snapshot.HomeAutoSession.DesiredConfigMode == "" {
		t.Fatal("expected default home_auto_session desired_config_mode")
	}
}

func TestUpdatePersistsAcrossRestart(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	err = store.Update(func(data *Data) {
		data.Pairing.Phase = PairingActivated
		data.Cloud.EndpointURL = "https://api.example.com"
		data.Cloud.ReceiverID = "rx-123"
		data.Runtime.Profile = "linux-service"
		data.Runtime.Mode = "service"
	})
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}

	store2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}

	snapshot := store2.Snapshot()
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.Pairing.Phase != PairingActivated {
		t.Fatalf("expected phase %q, got %q", PairingActivated, snapshot.Pairing.Phase)
	}
	if snapshot.Cloud.EndpointURL != "https://api.example.com" {
		t.Fatalf("unexpected endpoint: %q", snapshot.Cloud.EndpointURL)
	}
	if snapshot.Cloud.ReceiverID != "rx-123" {
		t.Fatalf("unexpected receiver id: %q", snapshot.Cloud.ReceiverID)
	}
	if snapshot.Runtime.Profile != "linux-service" {
		t.Fatalf("unexpected profile: %q", snapshot.Runtime.Profile)
	}
	if snapshot.Runtime.Mode != "service" {
		t.Fatalf("unexpected mode: %q", snapshot.Runtime.Mode)
	}
	if snapshot.Runtime.InstallType == "" {
		t.Fatal("expected install_type to be derived")
	}
}

func TestOpenMigratesLegacyState(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	legacy := `{
  "installation": {"id":"legacy-install"},
  "pairing": {"phase":"paired"},
  "cloud": {"endpoint_url":"https://api.example.com"},
  "runtime": {"profile":"linux-service","mode":"service"},
  "metadata": {}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated state: %v", err)
	}
	snapshot := store.Snapshot()
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.Pairing.Phase != PairingSteadyState {
		t.Fatalf("expected migrated pairing phase %q, got %q", PairingSteadyState, snapshot.Pairing.Phase)
	}
	if snapshot.Runtime.InstallType != "linux-package" {
		t.Fatalf("expected migrated install_type linux-package, got %q", snapshot.Runtime.InstallType)
	}
	if snapshot.Update.Status != "unknown" {
		t.Fatalf("expected migrated update status unknown, got %q", snapshot.Update.Status)
	}
}

func TestOpenMigratesSchemaV2ToCurrent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	legacy := `{
  "schema_version": 2,
  "installation": {"id":"legacy-install","created_at":"2026-03-11T00:00:00Z","last_started_at":"2026-03-11T00:00:00Z"},
  "pairing": {"phase":"steady_state"},
  "cloud": {"endpoint_url":"https://api.example.com"},
  "runtime": {"profile":"appliance-pi","mode":"service"},
  "metadata": {}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated state: %v", err)
	}
	snapshot := store.Snapshot()
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.Runtime.InstallType != "pi-appliance" {
		t.Fatalf("expected install_type pi-appliance, got %q", snapshot.Runtime.InstallType)
	}
	if snapshot.Update.Status != "unknown" {
		t.Fatalf("expected update status unknown, got %q", snapshot.Update.Status)
	}
	if snapshot.HomeAutoSession.ModuleState != "disabled" {
		t.Fatalf("expected home_auto_session module_state disabled, got %q", snapshot.HomeAutoSession.ModuleState)
	}
	if snapshot.HomeAutoSession.ReconciliationState != "clean_idle" {
		t.Fatalf("expected home_auto_session reconciliation_state clean_idle, got %q", snapshot.HomeAutoSession.ReconciliationState)
	}
	if snapshot.HomeAutoSession.ControlState != "disabled" {
		t.Fatalf("expected home_auto_session control_state disabled, got %q", snapshot.HomeAutoSession.ControlState)
	}
	if snapshot.HomeAutoSession.ActiveStateSource != "none" {
		t.Fatalf("expected home_auto_session active_state_source none, got %q", snapshot.HomeAutoSession.ActiveStateSource)
	}
	if snapshot.HomeAutoSession.EffectiveConfigSource != "local_fallback" {
		t.Fatalf("expected home_auto_session effective_config_source local_fallback, got %q", snapshot.HomeAutoSession.EffectiveConfigSource)
	}
	if snapshot.HomeAutoSession.LastConfigApplyResult == "" {
		t.Fatal("expected home_auto_session last_config_apply_result")
	}
}

func TestOpenMigratesSchemaV3ToCurrent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	legacy := `{
  "schema_version": 3,
  "installation": {"id":"legacy-install","created_at":"2026-03-11T00:00:00Z","last_started_at":"2026-03-11T00:00:00Z"},
  "pairing": {"phase":"steady_state"},
  "cloud": {"endpoint_url":"https://api.example.com"},
  "runtime": {"profile":"appliance-pi","mode":"service","install_type":"pi-appliance"},
  "update": {"status":"unknown"},
  "metadata": {}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated state: %v", err)
	}
	snapshot := store.Snapshot()
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.HomeAutoSession.ModuleState != "disabled" {
		t.Fatalf("expected home_auto_session module_state disabled, got %q", snapshot.HomeAutoSession.ModuleState)
	}
	if snapshot.HomeAutoSession.ReconciliationState != "clean_idle" {
		t.Fatalf("expected home_auto_session reconciliation_state clean_idle, got %q", snapshot.HomeAutoSession.ReconciliationState)
	}
	if snapshot.HomeAutoSession.ControlState != "disabled" {
		t.Fatalf("expected home_auto_session control_state disabled, got %q", snapshot.HomeAutoSession.ControlState)
	}
	if snapshot.HomeAutoSession.ActiveStateSource != "none" {
		t.Fatalf("expected home_auto_session active_state_source none, got %q", snapshot.HomeAutoSession.ActiveStateSource)
	}
	if snapshot.HomeAutoSession.EffectiveConfigSource != "local_fallback" {
		t.Fatalf("expected home_auto_session effective_config_source local_fallback, got %q", snapshot.HomeAutoSession.EffectiveConfigSource)
	}
	if snapshot.HomeAutoSession.LastConfigApplyResult == "" {
		t.Fatal("expected home_auto_session last_config_apply_result")
	}
}

func TestOpenMigratesSchemaV6ToCurrent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "receiver-state.json")
	legacy := `{
  "schema_version": 6,
  "installation": {"id":"legacy-install","created_at":"2026-03-11T00:00:00Z","last_started_at":"2026-03-11T00:00:00Z"},
  "pairing": {"phase":"steady_state"},
  "cloud": {"endpoint_url":"https://api.example.com"},
  "runtime": {"profile":"linux-service","mode":"service","install_type":"linux-package"},
  "update": {"status":"unknown"},
  "home_auto_session": {"module_state":"disabled","control_state":"disabled","active_state_source":"none","reconciliation_state":"clean_idle"},
  "metadata": {}
}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	store, err := Open(path)
	if err != nil {
		t.Fatalf("open migrated state: %v", err)
	}
	snapshot := store.Snapshot()
	if snapshot.SchemaVersion != CurrentSchemaVersion {
		t.Fatalf("expected schema version %d, got %d", CurrentSchemaVersion, snapshot.SchemaVersion)
	}
	if snapshot.HomeAutoSession.EffectiveConfigSource != "local_fallback" {
		t.Fatalf("expected migrated effective_config_source local_fallback, got %q", snapshot.HomeAutoSession.EffectiveConfigSource)
	}
	if snapshot.HomeAutoSession.LastAppliedConfigVer == "" {
		t.Fatal("expected migrated last_applied_config_version")
	}
	if snapshot.HomeAutoSession.LastConfigApplyResult == "" {
		t.Fatal("expected migrated last_config_apply_result")
	}
	if snapshot.HomeAutoSession.DesiredConfigEnabled == nil {
		t.Fatal("expected migrated desired_config_enabled")
	}
	if snapshot.HomeAutoSession.DesiredConfigMode == "" {
		t.Fatal("expected migrated desired_config_mode")
	}
}
