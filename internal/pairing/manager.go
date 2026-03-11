package pairing

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	goruntime "runtime"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

const (
	minPairingCodeLength = 8
	maxRetryDelay        = 2 * time.Minute
)

type LifecycleChange string

const (
	LifecycleCredentialRevoked LifecycleChange = "credential_revoked"
	LifecycleReceiverDisabled  LifecycleChange = "receiver_disabled"
	LifecycleReceiverReplaced  LifecycleChange = "receiver_replaced"
	LifecycleLocalReset        LifecycleChange = "local_reset"
	LifecycleLocalDeauthorized LifecycleChange = "local_deauthorized"
)

type ActivationIdentity struct {
	Label          string
	RuntimeVersion string
	Platform       string
	Arch           string
	Metadata       map[string]any
}

type Manager struct {
	store    *state.Store
	status   *status.Model
	client   cloudclient.PairingClient
	logger   *slog.Logger
	identity ActivationIdentity
	now      func() time.Time
}

func NewManager(
	store *state.Store,
	statusModel *status.Model,
	client cloudclient.PairingClient,
	logger *slog.Logger,
	identity ActivationIdentity,
) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	if identity.Platform == "" {
		identity.Platform = goruntime.GOOS
	}
	if identity.Arch == "" {
		identity.Arch = goruntime.GOARCH
	}
	if identity.RuntimeVersion == "" {
		identity.RuntimeVersion = "dev"
	}

	return &Manager{
		store:    store,
		status:   statusModel,
		client:   client,
		logger:   logger.With("component", "pairing"),
		identity: identity,
		now:      time.Now,
	}
}

func (m *Manager) SubmitPairingCode(_ context.Context, code string) error {
	normalized, err := normalizePairingCode(code)
	if err != nil {
		return err
	}

	now := m.now().UTC()
	if err := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingCodeEntered
		data.Pairing.PairingCode = normalized
		data.Pairing.InstallSessionID = ""
		data.Pairing.FlowKey = ""
		data.Pairing.ActivationToken = ""
		data.Pairing.ActivationExpires = nil
		data.Pairing.RetryCount = 0
		data.Pairing.NextRetryAt = nil
		data.Pairing.LastAttemptAt = nil
		data.Pairing.LastError = ""
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = "pairing_code_entered"
	}); err != nil {
		return err
	}

	m.status.SetPairingPhase(string(state.PairingCodeEntered))
	m.status.SetComponent("pairing", "pairing_code_entered", "pairing code accepted")
	m.status.SetLastError("")
	return nil
}

func (m *Manager) ResetPairing(deauthorize bool) error {
	change := LifecycleLocalReset
	detail := ""
	clearDurable := false
	if deauthorize {
		change = LifecycleLocalDeauthorized
		detail = "local receiver deauthorization requested"
		clearDurable = true
	}
	return m.ApplyLifecycleChange(change, detail, clearDurable)
}

func (m *Manager) ApplyLifecycleChange(change LifecycleChange, detail string, clearDurable bool) error {
	if !isLifecycleChangeValid(change) {
		return fmt.Errorf("invalid lifecycle change %q", change)
	}

	now := m.now().UTC()
	reason := sanitizeText(detail)
	if !isLocalLifecycleChange(change) && reason == "" {
		reason = lifecycleDefaultReason(change)
	}

	if err := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingUnpaired
		data.Pairing.PairingCode = ""
		data.Pairing.InstallSessionID = ""
		data.Pairing.FlowKey = ""
		data.Pairing.ActivationToken = ""
		data.Pairing.ActivationExpires = nil
		data.Pairing.RetryCount = 0
		data.Pairing.NextRetryAt = nil
		data.Pairing.LastAttemptAt = &now
		if isLocalLifecycleChange(change) {
			data.Pairing.LastError = ""
		} else {
			data.Pairing.LastError = reason
		}
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = string(change)

		if clearDurable {
			data.Cloud.OwnerID = ""
			data.Cloud.ReceiverID = ""
			data.Cloud.IngestAPIKeyID = ""
			data.Cloud.IngestAPIKey = ""
			data.Cloud.CredentialRef = ""
			data.Cloud.UpdatedAt = now
		}
	}); err != nil {
		return err
	}

	componentState, componentMessage := lifecycleStatus(change)
	m.status.SetPairingPhase(string(state.PairingUnpaired))
	m.status.SetComponent("pairing", componentState, componentMessage)
	if isLocalLifecycleChange(change) {
		m.status.SetLastError("")
	} else {
		m.status.SetLastError(lifecycleError(change))
	}

	return nil
}

func (m *Manager) Process(ctx context.Context) error {
	snapshot := m.store.Snapshot()
	m.status.SetPairingPhase(string(snapshot.Pairing.Phase))

	switch snapshot.Pairing.Phase {
	case state.PairingUnpaired:
		m.status.SetComponent("pairing", "unpaired", "waiting for pairing code")
		return nil
	case state.PairingCodeEntered:
		return m.exchangeBootstrap(ctx, snapshot)
	case state.PairingBootstrapExchanged:
		return m.activate(ctx, snapshot)
	case state.PairingActivated:
		return m.promoteSteadyState(snapshot)
	case state.PairingSteadyState:
		m.status.SetComponent("pairing", "steady_state", "pairing completed")
		return nil
	default:
		return m.failPairing("unknown pairing phase", snapshot.Pairing.Phase)
	}
}

func (m *Manager) exchangeBootstrap(ctx context.Context, snapshot state.Data) error {
	if m.client == nil {
		m.status.SetComponent("pairing", "disabled", "cloud client unavailable")
		return nil
	}
	if !canAttempt(snapshot.Pairing.NextRetryAt, m.now()) {
		m.status.SetComponent("pairing", "retry_wait", "waiting for next pairing retry")
		return nil
	}

	pairingCode := strings.TrimSpace(snapshot.Pairing.PairingCode)
	if pairingCode == "" {
		return m.failPairing("pairing phase has no pairing code", snapshot.Pairing.Phase)
	}

	m.status.SetComponent("pairing", "bootstrap_exchange", "redeeming pairing code")
	response, err := m.client.ExchangePairingCode(ctx, pairingCode)
	if err != nil {
		return m.handleAttemptError(snapshot, err, state.PairingCodeEntered)
	}

	now := m.now().UTC()
	if err := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingBootstrapExchanged
		data.Pairing.InstallSessionID = response.InstallSessionID
		data.Pairing.FlowKey = response.FlowKey
		data.Pairing.ActivationToken = response.ActivationToken
		expiresAt := response.ActivationExpires.UTC()
		data.Pairing.ActivationExpires = &expiresAt
		data.Pairing.RetryCount = 0
		data.Pairing.NextRetryAt = nil
		data.Pairing.LastAttemptAt = &now
		data.Pairing.LastError = ""
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = "bootstrap_exchanged"
		data.Cloud.ActivateEndpoint = response.ActivateEndpoint
		data.Cloud.HeartbeatEndpoint = response.HeartbeatEndpoint
		data.Cloud.IngestEndpoint = response.IngestEndpoint
		data.Cloud.UpdatedAt = now
	}); err != nil {
		return err
	}

	m.status.SetPairingPhase(string(state.PairingBootstrapExchanged))
	m.status.SetComponent("pairing", "bootstrap_exchanged", "pairing code redeemed")
	m.status.SetLastError("")
	return nil
}

func (m *Manager) activate(ctx context.Context, snapshot state.Data) error {
	if m.client == nil {
		m.status.SetComponent("pairing", "disabled", "cloud client unavailable")
		return nil
	}
	if !canAttempt(snapshot.Pairing.NextRetryAt, m.now()) {
		m.status.SetComponent("pairing", "retry_wait", "waiting for next activation retry")
		return nil
	}

	activationToken := strings.TrimSpace(snapshot.Pairing.ActivationToken)
	if activationToken == "" {
		return m.failPairing("bootstrap exchange missing activation token", snapshot.Pairing.Phase)
	}

	now := m.now().UTC()
	if snapshot.Pairing.ActivationExpires != nil && now.After(snapshot.Pairing.ActivationExpires.UTC()) {
		return m.revertToUnpaired("activation token expired")
	}

	activateEndpoint := strings.TrimSpace(snapshot.Cloud.ActivateEndpoint)
	if activateEndpoint == "" {
		activateEndpoint = "/api/receiver/activate"
	}

	m.status.SetComponent("pairing", "activate", "activating receiver credential")
	response, err := m.client.ActivateReceiver(ctx, activateEndpoint, cloudclient.ActivationRequest{
		ActivationToken: activationToken,
		Label:           m.identity.Label,
		RuntimeVersion:  m.identity.RuntimeVersion,
		Platform:        m.identity.Platform,
		Arch:            m.identity.Arch,
		Metadata:        m.identity.Metadata,
	})
	if err != nil {
		return m.handleAttemptError(snapshot, err, state.PairingBootstrapExchanged)
	}

	if err := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingActivated
		data.Pairing.PairingCode = ""
		data.Pairing.ActivationToken = ""
		data.Pairing.ActivationExpires = nil
		data.Pairing.RetryCount = 0
		data.Pairing.NextRetryAt = nil
		data.Pairing.LastAttemptAt = &now
		data.Pairing.LastError = ""
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = "activated"
		data.Cloud.OwnerID = response.OwnerID
		data.Cloud.ReceiverID = response.ReceiverAgentID
		data.Cloud.IngestAPIKeyID = response.IngestAPIKeyID
		data.Cloud.IngestAPIKey = response.IngestAPIKey
		if response.IngestEndpoint != "" {
			data.Cloud.IngestEndpoint = response.IngestEndpoint
		}
		if response.HeartbeatEndpoint != "" {
			data.Cloud.HeartbeatEndpoint = response.HeartbeatEndpoint
		}
		if response.ReceiverAgentID != "" {
			data.Cloud.CredentialRef = response.ReceiverAgentID
		}
		data.Cloud.UpdatedAt = now
	}); err != nil {
		return err
	}

	m.status.SetPairingPhase(string(state.PairingActivated))
	m.status.SetComponent("pairing", "activated", "receiver credential activated")
	m.status.SetLastError("")
	return nil
}

func (m *Manager) promoteSteadyState(snapshot state.Data) error {
	if snapshot.Pairing.Phase != state.PairingActivated {
		return nil
	}
	if strings.TrimSpace(snapshot.Cloud.IngestAPIKey) == "" {
		m.status.SetComponent("pairing", "activated", "waiting for durable credentials")
		return nil
	}

	now := m.now().UTC()
	if err := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = "steady_state"
	}); err != nil {
		return err
	}

	m.status.SetPairingPhase(string(state.PairingSteadyState))
	m.status.SetComponent("pairing", "steady_state", "pairing completed")
	return nil
}

func (m *Manager) handleAttemptError(snapshot state.Data, err error, retryPhase state.PairingPhase) error {
	now := m.now().UTC()
	retryable := cloudclient.IsRetryable(err)
	message := sanitizeError(err)
	lastChange := pairingFailureChange(retryPhase, err, retryable)

	if retryable {
		retryCount := snapshot.Pairing.RetryCount + 1
		delay := retryDelay(retryCount)
		nextRetry := now.Add(delay)
		if updateErr := m.store.Update(func(data *state.Data) {
			data.Pairing.Phase = retryPhase
			data.Pairing.RetryCount = retryCount
			data.Pairing.NextRetryAt = &nextRetry
			data.Pairing.LastAttemptAt = &now
			data.Pairing.LastError = message
			data.Pairing.UpdatedAt = now
			data.Pairing.LastChange = lastChange
		}); updateErr != nil {
			return updateErr
		}

		m.status.SetComponent("pairing", "retrying", fmt.Sprintf("temporary failure, retrying in %s", delay))
		m.status.SetLastError("temporary cloud error")
		m.logger.Warn("pairing attempt failed, retry scheduled", "retry_in", delay, "err", message)
		return nil
	}

	if updateErr := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingUnpaired
		data.Pairing.PairingCode = ""
		data.Pairing.ActivationToken = ""
		data.Pairing.ActivationExpires = nil
		data.Pairing.RetryCount = 0
		data.Pairing.NextRetryAt = nil
		data.Pairing.LastAttemptAt = &now
		data.Pairing.LastError = message
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = lastChange
	}); updateErr != nil {
		return updateErr
	}

	m.status.SetPairingPhase(string(state.PairingUnpaired))
	m.status.SetComponent("pairing", "failed", "pairing failed, enter a new pairing code")
	m.status.SetLastError("pairing failed")
	m.logger.Warn("pairing attempt failed permanently", "err", message)
	return nil
}

func (m *Manager) failPairing(reason string, phase state.PairingPhase) error {
	m.status.SetPairingPhase(string(phase))
	m.status.SetComponent("pairing", "error", reason)
	m.status.SetLastError(reason)
	return errors.New(reason)
}

func (m *Manager) revertToUnpaired(reason string) error {
	now := m.now().UTC()
	if err := m.store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingUnpaired
		data.Pairing.PairingCode = ""
		data.Pairing.ActivationToken = ""
		data.Pairing.ActivationExpires = nil
		data.Pairing.RetryCount = 0
		data.Pairing.NextRetryAt = nil
		data.Pairing.LastAttemptAt = &now
		data.Pairing.LastError = reason
		data.Pairing.UpdatedAt = now
		data.Pairing.LastChange = "reverted_unpaired"
	}); err != nil {
		return err
	}
	m.status.SetPairingPhase(string(state.PairingUnpaired))
	m.status.SetComponent("pairing", "unpaired", reason)
	m.status.SetLastError(reason)
	return nil
}

func normalizePairingCode(value string) (string, error) {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if len(trimmed) < minPairingCodeLength {
		return "", fmt.Errorf("pairing code must be at least %d characters", minPairingCodeLength)
	}
	return trimmed, nil
}

func retryDelay(retryCount int) time.Duration {
	if retryCount <= 0 {
		return 0
	}
	delay := time.Second << minInt(retryCount+1, 7)
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func canAttempt(nextRetryAt *time.Time, now time.Time) bool {
	if nextRetryAt == nil {
		return true
	}
	return !now.UTC().Before(nextRetryAt.UTC())
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	text := err.Error()
	if len(text) > 256 {
		return text[:256]
	}
	return text
}

func sanitizeText(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) > 256 {
		return trimmed[:256]
	}
	return trimmed
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func pairingFailureChange(phase state.PairingPhase, err error, retryable bool) string {
	switch phase {
	case state.PairingCodeEntered:
		if retryable {
			return "pairing_retry_scheduled"
		}
		if isPairingCodeExpired(err) {
			return "pairing_code_expired"
		}
		return "pairing_code_invalid"
	case state.PairingBootstrapExchanged:
		if retryable {
			return "activation_retry_scheduled"
		}
		return "activation_failed"
	default:
		if retryable {
			return "retry_scheduled"
		}
		return "failed_permanent"
	}
}

func isPairingCodeExpired(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "expired") {
		return true
	}
	var apiErr *cloudclient.APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == 410
	}
	return false
}

func isLifecycleChangeValid(change LifecycleChange) bool {
	switch change {
	case LifecycleCredentialRevoked,
		LifecycleReceiverDisabled,
		LifecycleReceiverReplaced,
		LifecycleLocalReset,
		LifecycleLocalDeauthorized:
		return true
	default:
		return false
	}
}

func isLocalLifecycleChange(change LifecycleChange) bool {
	return change == LifecycleLocalReset || change == LifecycleLocalDeauthorized
}

func lifecycleDefaultReason(change LifecycleChange) string {
	switch change {
	case LifecycleCredentialRevoked:
		return "receiver credential was revoked by cloud"
	case LifecycleReceiverDisabled:
		return "receiver is disabled by cloud policy"
	case LifecycleReceiverReplaced:
		return "receiver was replaced by a newer installation"
	default:
		return "receiver lifecycle changed"
	}
}

func lifecycleStatus(change LifecycleChange) (string, string) {
	switch change {
	case LifecycleCredentialRevoked:
		return "credential_revoked", "receiver credential was revoked; re-pair required"
	case LifecycleReceiverDisabled:
		return "receiver_disabled", "receiver is disabled in cloud; resolve and re-pair"
	case LifecycleReceiverReplaced:
		return "receiver_replaced", "receiver was replaced by another installation"
	case LifecycleLocalDeauthorized:
		return "deauthorized", "local credentials cleared; enter a new pairing code"
	case LifecycleLocalReset:
		return "reset", "pairing state reset; enter a new pairing code"
	default:
		return "lifecycle_changed", "receiver lifecycle changed"
	}
}

func lifecycleError(change LifecycleChange) string {
	switch change {
	case LifecycleCredentialRevoked:
		return "receiver credential revoked"
	case LifecycleReceiverDisabled:
		return "receiver disabled by cloud"
	case LifecycleReceiverReplaced:
		return "receiver replaced by another install"
	default:
		return "receiver lifecycle changed"
	}
}
