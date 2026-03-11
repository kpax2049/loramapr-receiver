package pairing

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

type mockCloudClient struct {
	exchangeCalls int
	activateCalls int

	exchangeResult cloudclient.BootstrapExchange
	exchangeErr    error

	activateResult cloudclient.ActivationResult
	activateErr    error
}

func (m *mockCloudClient) ExchangePairingCode(_ context.Context, _ string) (cloudclient.BootstrapExchange, error) {
	m.exchangeCalls++
	if m.exchangeErr != nil {
		return cloudclient.BootstrapExchange{}, m.exchangeErr
	}
	return m.exchangeResult, nil
}

func (m *mockCloudClient) ActivateReceiver(
	_ context.Context,
	_ string,
	_ cloudclient.ActivationRequest,
) (cloudclient.ActivationResult, error) {
	m.activateCalls++
	if m.activateErr != nil {
		return cloudclient.ActivationResult{}, m.activateErr
	}
	return m.activateResult, nil
}

func TestPairingLifecycleProgression(t *testing.T) {
	t.Parallel()

	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}

	now := time.Date(2026, 3, 10, 17, 0, 0, 0, time.UTC)
	cloud := &mockCloudClient{
		exchangeResult: cloudclient.BootstrapExchange{
			InstallSessionID:  "session-1",
			FlowKey:           "meshtastic_first_run",
			ActivationToken:   "rx_act_abc123",
			ActivationExpires: now.Add(10 * time.Minute),
			ConfigVersion:     "v1.2",
			ActivateEndpoint:  "https://api.example.com/api/receiver/activate",
			HeartbeatEndpoint: "https://api.example.com/api/receiver/heartbeat",
			IngestEndpoint:    "https://api.example.com/api/meshtastic/event",
		},
		activateResult: cloudclient.ActivationResult{
			ReceiverAgentID:   "agent-1",
			OwnerID:           "owner-1",
			IngestAPIKeyID:    "key-id-1",
			IngestAPIKey:      "secret-1",
			ConfigVersion:     "v1.3",
			IngestEndpoint:    "https://api.example.com/api/meshtastic/event",
			HeartbeatEndpoint: "https://api.example.com/api/receiver/heartbeat",
			ActivatedAt:       now,
		},
	}

	manager := NewManager(store, status.New(), cloud, nil, ActivationIdentity{})
	manager.now = func() time.Time { return now }

	if err := manager.SubmitPairingCode(context.Background(), "LMR-ABCDEF12"); err != nil {
		t.Fatalf("submit pairing code: %v", err)
	}

	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("process exchange: %v", err)
	}
	if phase := store.Snapshot().Pairing.Phase; phase != state.PairingBootstrapExchanged {
		t.Fatalf("expected phase %q, got %q", state.PairingBootstrapExchanged, phase)
	}

	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("process activation: %v", err)
	}
	snap := store.Snapshot()
	if snap.Pairing.Phase != state.PairingActivated {
		t.Fatalf("expected phase %q, got %q", state.PairingActivated, snap.Pairing.Phase)
	}
	if snap.Cloud.IngestAPIKey != "secret-1" {
		t.Fatalf("expected durable ingest key to be stored")
	}
	if snap.Cloud.ConfigVersion != "v1.3" {
		t.Fatalf("expected cloud config version to be persisted, got %q", snap.Cloud.ConfigVersion)
	}

	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("process steady state: %v", err)
	}
	if phase := store.Snapshot().Pairing.Phase; phase != state.PairingSteadyState {
		t.Fatalf("expected phase %q, got %q", state.PairingSteadyState, phase)
	}

	if cloud.exchangeCalls != 1 {
		t.Fatalf("expected one exchange call, got %d", cloud.exchangeCalls)
	}
	if cloud.activateCalls != 1 {
		t.Fatalf("expected one activate call, got %d", cloud.activateCalls)
	}
}

func TestRetryableExchangeFailureSchedulesRetry(t *testing.T) {
	t.Parallel()

	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	cloud := &mockCloudClient{
		exchangeErr: &cloudclient.APIError{StatusCode: 503, Message: "temporary", Retryable: true},
	}
	manager := NewManager(store, status.New(), cloud, nil, ActivationIdentity{})

	now := time.Date(2026, 3, 10, 18, 0, 0, 0, time.UTC)
	manager.now = func() time.Time { return now }

	if err := manager.SubmitPairingCode(context.Background(), "LMR-RETRY0001"); err != nil {
		t.Fatalf("submit pairing code: %v", err)
	}
	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("process should not fail hard: %v", err)
	}

	snap := store.Snapshot()
	if snap.Pairing.Phase != state.PairingCodeEntered {
		t.Fatalf("expected phase to remain %q, got %q", state.PairingCodeEntered, snap.Pairing.Phase)
	}
	if snap.Pairing.RetryCount != 1 {
		t.Fatalf("expected retry_count=1, got %d", snap.Pairing.RetryCount)
	}
	if snap.Pairing.NextRetryAt == nil {
		t.Fatal("expected next_retry_at to be set")
	}

	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("retry-wait pass should not fail: %v", err)
	}
	if cloud.exchangeCalls != 1 {
		t.Fatalf("expected no second call before retry window, got %d", cloud.exchangeCalls)
	}
}

func TestPermanentExchangeFailureResetsToUnpaired(t *testing.T) {
	t.Parallel()

	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	cloud := &mockCloudClient{
		exchangeErr: &cloudclient.APIError{StatusCode: 401, Message: "invalid pairing code", Retryable: false},
	}
	manager := NewManager(store, status.New(), cloud, nil, ActivationIdentity{})

	if err := manager.SubmitPairingCode(context.Background(), "LMR-BADCODE1"); err != nil {
		t.Fatalf("submit pairing code: %v", err)
	}
	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("process should not fail hard: %v", err)
	}

	snap := store.Snapshot()
	if snap.Pairing.Phase != state.PairingUnpaired {
		t.Fatalf("expected phase %q, got %q", state.PairingUnpaired, snap.Pairing.Phase)
	}
	if snap.Pairing.PairingCode != "" {
		t.Fatal("expected pairing code to be cleared")
	}
	if snap.Pairing.LastChange != "pairing_code_invalid" {
		t.Fatalf("expected last_change pairing_code_invalid, got %q", snap.Pairing.LastChange)
	}
}

func TestExpiredPairingCodeMapsFailureChange(t *testing.T) {
	t.Parallel()

	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	cloud := &mockCloudClient{
		exchangeErr: &cloudclient.APIError{StatusCode: 410, Message: "pairing code expired", Retryable: false},
	}
	manager := NewManager(store, status.New(), cloud, nil, ActivationIdentity{})

	if err := manager.SubmitPairingCode(context.Background(), "LMR-EXPIRED1"); err != nil {
		t.Fatalf("submit pairing code: %v", err)
	}
	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("process should not fail hard: %v", err)
	}

	snap := store.Snapshot()
	if snap.Pairing.LastChange != "pairing_code_expired" {
		t.Fatalf("expected last_change pairing_code_expired, got %q", snap.Pairing.LastChange)
	}
}

func TestRestartSafeBootstrapExchange(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}

	now := time.Date(2026, 3, 10, 19, 0, 0, 0, time.UTC)
	cloud := &mockCloudClient{
		exchangeResult: cloudclient.BootstrapExchange{
			InstallSessionID:  "session-2",
			FlowKey:           "meshtastic_first_run",
			ActivationToken:   "rx_act_def456",
			ActivationExpires: now.Add(10 * time.Minute),
			ActivateEndpoint:  "https://api.example.com/api/receiver/activate",
		},
		activateResult: cloudclient.ActivationResult{
			ReceiverAgentID: "agent-2",
			OwnerID:         "owner-2",
			IngestAPIKeyID:  "key-id-2",
			IngestAPIKey:    "secret-2",
			ActivatedAt:     now,
		},
	}
	manager := NewManager(store, status.New(), cloud, nil, ActivationIdentity{})
	manager.now = func() time.Time { return now }

	if err := manager.SubmitPairingCode(context.Background(), "LMR-RESTART1"); err != nil {
		t.Fatalf("submit pairing code: %v", err)
	}
	if err := manager.Process(context.Background()); err != nil {
		t.Fatalf("exchange process: %v", err)
	}

	storeAfterRestart, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("reopen state: %v", err)
	}
	managerAfterRestart := NewManager(storeAfterRestart, status.New(), cloud, nil, ActivationIdentity{})
	managerAfterRestart.now = func() time.Time { return now }

	if err := managerAfterRestart.Process(context.Background()); err != nil {
		t.Fatalf("activation process after restart: %v", err)
	}

	snap := storeAfterRestart.Snapshot()
	if snap.Pairing.Phase != state.PairingActivated {
		t.Fatalf("expected phase %q after restart activation, got %q", state.PairingActivated, snap.Pairing.Phase)
	}
	if snap.Cloud.IngestAPIKey != "secret-2" {
		t.Fatal("expected durable credentials after restart")
	}
}

func TestNormalizePairingCodeValidation(t *testing.T) {
	t.Parallel()

	if _, err := normalizePairingCode(" "); err == nil {
		t.Fatal("expected validation error for empty pairing code")
	}
	code, err := normalizePairingCode("  lmr-abc12345  ")
	if err != nil {
		t.Fatalf("normalizePairingCode returned error: %v", err)
	}
	if code != "LMR-ABC12345" {
		t.Fatalf("expected normalized code, got %q", code)
	}
}

func TestApplyLifecycleChangeClearsDurableCredentials(t *testing.T) {
	t.Parallel()

	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	err = store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.OwnerID = "owner-1"
		data.Cloud.ReceiverID = "receiver-1"
		data.Cloud.IngestAPIKeyID = "key-1"
		data.Cloud.IngestAPIKey = "secret"
		data.Cloud.CredentialRef = "receiver-1"
	})
	if err != nil {
		t.Fatalf("seed state: %v", err)
	}

	manager := NewManager(store, status.New(), nil, nil, ActivationIdentity{})
	if err := manager.ApplyLifecycleChange(LifecycleReceiverDisabled, "disabled by policy", true); err != nil {
		t.Fatalf("ApplyLifecycleChange: %v", err)
	}

	snap := store.Snapshot()
	if snap.Pairing.Phase != state.PairingUnpaired {
		t.Fatalf("expected phase %q, got %q", state.PairingUnpaired, snap.Pairing.Phase)
	}
	if snap.Pairing.LastChange != string(LifecycleReceiverDisabled) {
		t.Fatalf("expected last_change %q, got %q", LifecycleReceiverDisabled, snap.Pairing.LastChange)
	}
	if snap.Cloud.IngestAPIKey != "" || snap.Cloud.IngestAPIKeyID != "" || snap.Cloud.CredentialRef != "" {
		t.Fatalf("expected durable cloud credentials to be cleared")
	}
}

func TestResetPairingPreservesInstallationID(t *testing.T) {
	t.Parallel()

	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	initialInstallID := store.Snapshot().Installation.ID

	err = store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
		data.Cloud.IngestAPIKeyID = "key-1"
	})
	if err != nil {
		t.Fatalf("seed state: %v", err)
	}

	manager := NewManager(store, status.New(), nil, nil, ActivationIdentity{})
	if err := manager.ResetPairing(true); err != nil {
		t.Fatalf("ResetPairing: %v", err)
	}

	snap := store.Snapshot()
	if snap.Installation.ID != initialInstallID {
		t.Fatalf("expected installation ID to be preserved")
	}
	if snap.Pairing.LastChange != string(LifecycleLocalDeauthorized) {
		t.Fatalf("expected last_change %q, got %q", LifecycleLocalDeauthorized, snap.Pairing.LastChange)
	}
	if snap.Cloud.IngestAPIKey != "" {
		t.Fatalf("expected ingest API key to be cleared")
	}
}
