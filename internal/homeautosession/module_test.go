package homeautosession

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

type mockSessionClient struct {
	mu sync.Mutex

	startCalls int
	stopCalls  int

	startErr error
	stopErr  error

	startResult cloudclient.HomeAutoSessionStartResult
	stopResult  cloudclient.HomeAutoSessionStopResult

	startHook func(request cloudclient.HomeAutoSessionStartRequest, call int) (cloudclient.HomeAutoSessionStartResult, error)
	stopHook  func(request cloudclient.HomeAutoSessionStopRequest, call int) (cloudclient.HomeAutoSessionStopResult, error)

	startRequests []cloudclient.HomeAutoSessionStartRequest
	stopRequests  []cloudclient.HomeAutoSessionStopRequest
}

func (m *mockSessionClient) StartHomeAutoSession(_ context.Context, _ string, _ string, request cloudclient.HomeAutoSessionStartRequest) (cloudclient.HomeAutoSessionStartResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls++
	m.startRequests = append(m.startRequests, request)
	if m.startHook != nil {
		return m.startHook(request, m.startCalls)
	}
	if m.startErr != nil {
		return cloudclient.HomeAutoSessionStartResult{}, m.startErr
	}
	if strings.TrimSpace(m.startResult.SessionID) != "" || !m.startResult.StartedAt.IsZero() {
		result := m.startResult
		if result.StartedAt.IsZero() {
			result.StartedAt = time.Now().UTC()
		}
		return result, nil
	}
	return cloudclient.HomeAutoSessionStartResult{
		SessionID: "session-1",
		StartedAt: time.Now().UTC(),
	}, nil
}

func (m *mockSessionClient) StopHomeAutoSession(_ context.Context, _ string, _ string, request cloudclient.HomeAutoSessionStopRequest) (cloudclient.HomeAutoSessionStopResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopCalls++
	m.stopRequests = append(m.stopRequests, request)
	if m.stopHook != nil {
		return m.stopHook(request, m.stopCalls)
	}
	if m.stopErr != nil {
		return cloudclient.HomeAutoSessionStopResult{}, m.stopErr
	}
	if strings.TrimSpace(m.stopResult.SessionID) != "" || !m.stopResult.StoppedAt.IsZero() || strings.TrimSpace(m.stopResult.Status) != "" {
		result := m.stopResult
		if result.StoppedAt.IsZero() {
			result.StoppedAt = time.Now().UTC()
		}
		return result, nil
	}
	return cloudclient.HomeAutoSessionStopResult{
		SessionID: "session-1",
		StoppedAt: time.Now().UTC(),
		Status:    "stopped",
	}, nil
}

func (m *mockSessionClient) calls() (int, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startCalls, m.stopCalls
}

func (m *mockSessionClient) lastStartRequest() cloudclient.HomeAutoSessionStartRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.startRequests) == 0 {
		return cloudclient.HomeAutoSessionStartRequest{}
	}
	return m.startRequests[len(m.startRequests)-1]
}

func (m *mockSessionClient) startRequestsSnapshot() []cloudclient.HomeAutoSessionStartRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]cloudclient.HomeAutoSessionStartRequest, len(m.startRequests))
	copy(out, m.startRequests)
	return out
}

func (m *mockSessionClient) lastStopRequest() cloudclient.HomeAutoSessionStopRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.stopRequests) == 0 {
		return cloudclient.HomeAutoSessionStopRequest{}
	}
	return m.stopRequests[len(m.stopRequests)-1]
}

func TestModuleDisabledState(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	statusModel := status.New()
	module := New(config.HomeAutoSessionConfig{
		Enabled: false,
		Mode:    config.HomeAutoSessionModeOff,
	}, store, statusModel, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	waitForCondition(t, 3*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateDisabled)
	})
}

func TestStartupReconciliationCleanIdle(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	statusModel := status.New()
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, &mockSessionClient{})

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	waitForCondition(t, 4*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.ReconciliationState == reconciliationCleanIdle
	})
}

func TestStartupReconciliationInconsistentStateDegraded(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.HomeAutoSession.ActiveSessionID = ""
		data.HomeAutoSession.ActiveTriggerNode = "!nodeA"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, &mockSessionClient{})

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	waitForCondition(t, 4*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateDegraded)
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.ReconciliationState != reconciliationInconsistentDegraded {
		t.Fatalf("expected reconciliation state %q, got %q", reconciliationInconsistentDegraded, snap.ReconciliationState)
	}
	if snap.BlockedReason == "" {
		t.Fatalf("expected blocked reason for degraded startup")
	}
}

func TestPendingStartRecoveredAcrossRestartWithoutDuplicate(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
		data.HomeAutoSession.PendingAction = "start"
		data.HomeAutoSession.PendingTriggerNode = "!nodeA"
		data.HomeAutoSession.PendingReason = "tracked node moved outside home geofence"
		data.HomeAutoSession.PendingDedupeKey = "start-dedupe-1"
		now := time.Now().UTC().Add(-2 * time.Minute)
		data.HomeAutoSession.PendingSince = &now
		data.HomeAutoSession.ReconciliationState = "pending_start_recovering"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	firstClient := &mockSessionClient{}
	firstModule := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, firstClient)
	ctx1, cancel1 := context.WithCancel(context.Background())
	firstModule.Start(ctx1)

	waitForCondition(t, 5*time.Second, func() bool {
		startCalls, _ := firstClient.calls()
		return startCalls >= 1
	})
	waitForCondition(t, 5*time.Second, func() bool {
		snap := store.Snapshot().HomeAutoSession
		return snap.PendingAction == "" && snap.ActiveSessionID != ""
	})
	cancel1()
	time.Sleep(80 * time.Millisecond)

	store2, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("reopen state: %v", err)
	}
	secondClient := &mockSessionClient{}
	secondStatus := status.New()
	secondModule := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store2, secondStatus, nil, secondClient)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	secondModule.Start(ctx2)

	time.Sleep(1300 * time.Millisecond)
	startCalls, _ := secondClient.calls()
	if startCalls != 0 {
		t.Fatalf("expected no duplicate start call after restart, got %d", startCalls)
	}
	if got := secondStatus.Snapshot().HomeAutoSession.ReconciliationState; got != reconciliationActiveRecovered {
		t.Fatalf("expected reconciliation state %q after restart, got %q", reconciliationActiveRecovered, got)
	}
}

func TestHomeAutoPersistFingerprintIgnoresVolatileTimestamps(t *testing.T) {
	t.Parallel()

	firstDecision := time.Date(2026, 3, 22, 8, 0, 0, 0, time.UTC)
	secondDecision := firstDecision.Add(5 * time.Second)
	firstUpdated := firstDecision
	secondUpdated := firstUpdated.Add(10 * time.Second)
	base := state.HomeAutoSessionState{
		ModuleState:         string(StateControlReady),
		ControlState:        controlStateReady,
		ReconciliationState: reconciliationCleanIdle,
		LastDecisionReason:  "waiting for tracked node near home geofence",
	}

	stateOne := base
	stateOne.LastDecisionAt = &firstDecision
	stateOne.UpdatedAt = firstUpdated

	stateTwo := base
	stateTwo.LastDecisionAt = &secondDecision
	stateTwo.UpdatedAt = secondUpdated

	fingerprintOne := homeAutoPersistFingerprint(stateOne)
	fingerprintTwo := homeAutoPersistFingerprint(stateTwo)
	if fingerprintOne == "" || fingerprintTwo == "" {
		t.Fatalf("expected non-empty fingerprints")
	}
	if fingerprintOne != fingerprintTwo {
		t.Fatalf("expected identical fingerprints when only timestamps differ")
	}
}

func TestModuleIdleLoopDoesNotRewriteStateEverySecond(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, &mockSessionClient{})

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	waitForCondition(t, 5*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateControlReady) && snap.ReconciliationState == reconciliationCleanIdle
	})

	baseline := store.Snapshot().HomeAutoSession
	if baseline.LastDecisionAt == nil {
		t.Fatalf("expected baseline last decision timestamp to be persisted")
	}
	baselineDecision := baseline.LastDecisionAt.UTC()
	baselineUpdated := baseline.UpdatedAt

	time.Sleep(1300 * time.Millisecond)

	after := store.Snapshot().HomeAutoSession
	if after.LastDecisionAt == nil {
		t.Fatalf("expected last decision timestamp to remain populated")
	}
	if !after.LastDecisionAt.UTC().Equal(baselineDecision) {
		t.Fatalf("expected no idle churn in last decision timestamp: baseline=%s after=%s", baselineDecision, after.LastDecisionAt.UTC())
	}
	if !after.UpdatedAt.Equal(baselineUpdated) {
		t.Fatalf("expected no idle churn in updated_at: baseline=%s after=%s", baselineUpdated, after.UpdatedAt)
	}
}

func TestIdleTimeoutStopTriggersWithoutNewTraffic(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	cfg := homeAutoTestConfig(config.HomeAutoSessionModeControl)
	cfg.StartDebounce = config.Duration(20 * time.Millisecond)
	cfg.StopDebounce = config.Duration(20 * time.Millisecond)
	cfg.IdleStopTimeout = config.Duration(80 * time.Millisecond)

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(cfg, store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 260), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	module.mu.Lock()
	fact := module.nodeFacts[strings.ToLower("!nodeA")]
	fact.HasPosition = true
	fact.InsideGeofence = true
	fact.LastSeenAt = time.Now().UTC().Add(-2 * cfg.IdleStopTimeout.Std())
	module.nodeFacts[strings.ToLower("!nodeA")] = fact
	module.stopCandidate = nil
	module.mu.Unlock()
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		_, stopCalls := cloud.calls()
		return stopCalls >= 1
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.LastActionResult == "" {
		t.Fatalf("expected last action result to be populated after idle stop")
	}
	if snap.State != string(StateControlReady) {
		t.Fatalf("expected module to return to control_ready after idle stop, got %q", snap.State)
	}
}

func TestIdleTimeoutStopDoesNotTriggerWhenTriggerNodeLastSeenOutside(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	cfg := homeAutoTestConfig(config.HomeAutoSessionModeControl)
	cfg.StartDebounce = config.Duration(20 * time.Millisecond)
	cfg.StopDebounce = config.Duration(20 * time.Millisecond)
	cfg.IdleStopTimeout = config.Duration(80 * time.Millisecond)

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(cfg, store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 260), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	module.mu.Lock()
	fact := module.nodeFacts[strings.ToLower("!nodeA")]
	fact.HasPosition = true
	fact.InsideGeofence = false
	fact.LastSeenAt = time.Now().UTC().Add(-2 * cfg.IdleStopTimeout.Std())
	module.nodeFacts[strings.ToLower("!nodeA")] = fact
	module.stopCandidate = nil
	module.mu.Unlock()
	module.Reevaluate()

	time.Sleep(1300 * time.Millisecond)

	_, stopCalls := cloud.calls()
	if stopCalls != 0 {
		t.Fatalf("expected no idle stop call when trigger node last seen outside, got %d", stopCalls)
	}
	snap := statusModel.Snapshot().HomeAutoSession
	if snap.State != string(StateActive) && snap.State != string(StateCooldown) {
		t.Fatalf("expected active/cooldown state to remain, got %q", snap.State)
	}
}

func TestRetryableStartFailurePersistsPendingAndCooldown(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{startErr: &cloudclient.APIError{StatusCode: 503, Message: "temporary", Retryable: true}}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateCooldown)
	})

	persisted := store.Snapshot().HomeAutoSession
	if persisted.PendingAction != "start" {
		t.Fatalf("expected pending start action persisted, got %q", persisted.PendingAction)
	}
	if persisted.PendingDedupeKey == "" {
		t.Fatalf("expected persisted pending dedupe key")
	}
	if persisted.ConsecutiveFailures == 0 {
		t.Fatalf("expected failure counter incremented")
	}
	startCalls, _ := cloud.calls()
	if startCalls == 0 {
		t.Fatalf("expected start call attempt")
	}
}

func TestStopTimeoutRetryDelaySchedule(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{failures: 1, want: 10 * time.Second},
		{failures: 2, want: 20 * time.Second},
		{failures: 3, want: 30 * time.Second},
		{failures: 4, want: 1 * time.Minute},
		{failures: 5, want: 2 * time.Minute},
		{failures: 6, want: 4 * time.Minute},
		{failures: 9, want: 4 * time.Minute},
	}
	for _, tc := range cases {
		got := stopTimeoutRetryDelay(tc.failures)
		if got != tc.want {
			t.Fatalf("failures=%d expected %s, got %s", tc.failures, tc.want, got)
		}
	}
}

func TestJitterRetryDelayDeterministicAndCapped(t *testing.T) {
	base := retryCooldownMax
	first := jitterRetryDelay(base, actionStop, 7, "seed-1")
	second := jitterRetryDelay(base, actionStop, 7, "seed-1")
	if first != second {
		t.Fatalf("expected deterministic jitter for same seed, got %s and %s", first, second)
	}
	if first > retryCooldownMax {
		t.Fatalf("expected jittered delay to stay capped at %s, got %s", retryCooldownMax, first)
	}
	other := jitterRetryDelay(base, actionStop, 7, "seed-2")
	if other > retryCooldownMax {
		t.Fatalf("expected jittered delay for second seed to stay capped at %s, got %s", retryCooldownMax, other)
	}
}

func TestStopTimeoutRetryUsesFastEarlyCadence(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	cloud.mu.Lock()
	cloud.stopErr = context.DeadlineExceeded
	cloud.mu.Unlock()

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateCooldown) &&
			snap.LastAction == "stop" &&
			snap.LastActionResult == "retry_scheduled"
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.CooldownUntil == nil || snap.LastActionAt == nil {
		t.Fatalf("expected cooldown and last action time")
	}
	delay := snap.CooldownUntil.Sub(*snap.LastActionAt)
	if delay < 9*time.Second || delay > 11*time.Second {
		t.Fatalf("expected first stop-timeout retry delay near 10s, got %s", delay)
	}
	if !strings.Contains(snap.Summary, "stop pending; cloud unreachable/slow") {
		t.Fatalf("expected stop-timeout summary, got %q", snap.Summary)
	}
	if !strings.Contains(snap.LastDecisionReason, "attempt 1") {
		t.Fatalf("expected attempt count in last decision, got %q", snap.LastDecisionReason)
	}
}

func TestStopTimeoutFreshInsideBypassesCooldownAndCanRecover(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	cloud.mu.Lock()
	cloud.stopErr = context.DeadlineExceeded
	cloud.mu.Unlock()

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateCooldown) && snap.CooldownUntil != nil
	})
	firstCooldown := statusModel.Snapshot().HomeAutoSession.CooldownUntil.UTC()

	cloud.mu.Lock()
	cloud.stopErr = nil
	cloud.mu.Unlock()

	time.Sleep(stopRetryBypassMin + 200*time.Millisecond)
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, time.Now().UTC()))
	module.Reevaluate()

	waitForCondition(t, 3*time.Second, func() bool {
		_, stopCalls := cloud.calls()
		return stopCalls >= 2
	})
	waitForCondition(t, 3*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateControlReady) && snap.LastActionResult == "stopped"
	})

	if !time.Now().UTC().Before(firstCooldown.Add(-1 * time.Second)) {
		t.Fatalf("expected stop retry before scheduled cooldown expiry (%s)", firstCooldown.Format(time.RFC3339))
	}
}

func TestGPSValidityAndBoundaryDoNotTriggerStart(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()

	module.ObserveEvent(meshtastic.Event{
		Kind: meshtastic.EventPacket,
		Packet: &meshtastic.Packet{
			SourceNodeID: "!nodeA",
			ReceivedAt:   now,
		},
		Received: now,
	})
	module.Reevaluate()
	waitForCondition(t, 4*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.GPSStatus == gpsStatusMissing
	})

	module.ObserveEvent(testPacket("!nodeA", 999, -122.0090, now.Add(200*time.Millisecond)))
	module.Reevaluate()
	waitForCondition(t, 4*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.GPSStatus == gpsStatusInvalid
	})

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(-3*time.Minute)))
	module.Reevaluate()
	waitForCondition(t, 4*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.GPSStatus == gpsStatusStale
	})

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(time.Second)))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 155), -122.0090, now.Add(2*time.Second)))
	module.Reevaluate()
	time.Sleep(150 * time.Millisecond)
	startCalls, _ := cloud.calls()
	if startCalls != 0 {
		t.Fatalf("expected no start call from boundary jitter, got %d", startCalls)
	}

	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		calls, _ := cloud.calls()
		return calls >= 1
	})
}

func TestStopAlreadyResolvedErrorClearsActiveSession(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return store.Snapshot().HomeAutoSession.ActiveSessionID != ""
	})

	cloud.mu.Lock()
	cloud.stopErr = &cloudclient.APIError{StatusCode: 404, Message: "session not found", Retryable: false}
	cloud.mu.Unlock()

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()
	waitForCondition(t, 6*time.Second, func() bool {
		snap := store.Snapshot().HomeAutoSession
		return snap.ActiveSessionID == "" && snap.LastSuccessfulAction == "stop"
	})
}

func TestStopFallbackWithoutSessionIDOnRetryableError(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		stopHook: func(request cloudclient.HomeAutoSessionStopRequest, call int) (cloudclient.HomeAutoSessionStopResult, error) {
			if call == 1 {
				if strings.TrimSpace(request.SessionID) == "" {
					t.Fatalf("expected first stop request to include session ID")
				}
				return cloudclient.HomeAutoSessionStopResult{}, &cloudclient.APIError{
					StatusCode: 500,
					Message:    "Internal server error",
					Retryable:  true,
					RequestID:  "req-stop-1",
				}
			}
			if strings.TrimSpace(request.SessionID) != "" {
				t.Fatalf("expected fallback stop request to omit session ID")
			}
			return cloudclient.HomeAutoSessionStopResult{
				SessionID:      "session-1",
				StoppedAt:      time.Now().UTC(),
				Status:         "stopped",
				StatusCode:     200,
				CloudRequestID: "req-stop-2",
			}, nil
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateControlReady) && snap.LastActionResult == "stopped"
	})

	_, stopCalls := cloud.calls()
	if stopCalls != 2 {
		t.Fatalf("expected fallback to perform two stop calls, got %d", stopCalls)
	}
	if snap := store.Snapshot().HomeAutoSession; snap.ActiveSessionID != "" || snap.PendingAction != "" {
		t.Fatalf("expected active session and pending action cleared after fallback stop, got %#v", snap)
	}
}

func TestStopFallbackWithoutSessionIDOnStaleSessionSemanticError(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		stopHook: func(request cloudclient.HomeAutoSessionStopRequest, call int) (cloudclient.HomeAutoSessionStopResult, error) {
			if call == 1 {
				return cloudclient.HomeAutoSessionStopResult{}, &cloudclient.APIError{
					StatusCode: 400,
					Message:    "property sessionId should not exist",
					Retryable:  false,
					RequestID:  "req-stop-stale-1",
				}
			}
			if strings.TrimSpace(request.SessionID) != "" {
				t.Fatalf("expected fallback stop request to omit session ID")
			}
			return cloudclient.HomeAutoSessionStopResult{
				SessionID: "session-1",
				StoppedAt: time.Now().UTC(),
				Status:    "stopped",
			}, nil
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()
	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateControlReady) && snap.LastActionResult == "stopped"
	})

	_, stopCalls := cloud.calls()
	if stopCalls != 2 {
		t.Fatalf("expected stale-session fallback to perform two stop calls, got %d", stopCalls)
	}
}

func TestStopSuccessLikeAlreadyClosedClearsActiveSession(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		stopResult: cloudclient.HomeAutoSessionStopResult{
			SessionID: "session-1",
			StoppedAt: time.Now().UTC(),
			Status:    "already_closed",
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()
	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateControlReady) && snap.LastActionResult == "already_closed_resolved"
	})

	if snap := store.Snapshot().HomeAutoSession; snap.ActiveSessionID != "" || snap.PendingAction != "" {
		t.Fatalf("expected active session and pending action cleared after already_closed stop, got %#v", snap)
	}
}

func TestStartAlreadyActiveWithSessionIDSyncsLocalState(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		startErr: &cloudclient.APIError{
			StatusCode: 409,
			Message:    "session already active for receiver",
			Retryable:  false,
			SessionID:  "session-existing-1",
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateActive) && snap.ActiveSessionID == "session-existing-1"
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.LastActionResult != "already_active_synced" {
		t.Fatalf("expected already_active_synced result, got %q", snap.LastActionResult)
	}
	if snap.ControlState != controlStateActive {
		t.Fatalf("expected control_state active, got %q", snap.ControlState)
	}
}

func TestStartConflictAlreadyActiveEntersConflictBlocked(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		startErr: &cloudclient.APIError{
			StatusCode: 409,
			Message:    "session already active for receiver",
			Retryable:  false,
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateDegraded) && snap.ReconciliationState == reconciliationConflictAlreadyActive
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.ControlState != controlStateConflictBlocked {
		t.Fatalf("expected control state %q, got %q", controlStateConflictBlocked, snap.ControlState)
	}
	if snap.ActiveStateSource != activeStateSourceConflict {
		t.Fatalf("expected active source %q, got %q", activeStateSourceConflict, snap.ActiveStateSource)
	}
	if snap.LastActionResult != "already_active_conflict" {
		t.Fatalf("expected last action result already_active_conflict, got %q", snap.LastActionResult)
	}
	time.Sleep(250 * time.Millisecond)
	startCalls, _ := cloud.calls()
	if startCalls != 1 {
		t.Fatalf("expected one start call under conflict block, got %d", startCalls)
	}
}

func TestStartMissingSessionIDConflictSchedulesFreshRetry(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		startHook: func(request cloudclient.HomeAutoSessionStartRequest, call int) (cloudclient.HomeAutoSessionStartResult, error) {
			if call == 1 {
				return cloudclient.HomeAutoSessionStartResult{}, &cloudclient.APIError{
					StatusCode: 409,
					Message:    "home auto session start is missing sessionId",
					Retryable:  false,
					RequestID:  "req-start-missing-1",
				}
			}
			return cloudclient.HomeAutoSessionStartResult{
				SessionID:      "session-2",
				StartedAt:      time.Now().UTC(),
				StatusCode:     201,
				CloudRequestID: "req-start-ok-2",
			}, nil
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateCooldown) &&
			snap.ControlState == controlStateConflictBlocked &&
			strings.Contains(strings.ToLower(snap.Summary), "start pending; cloud start conflict")
	})

	initialReqs := cloud.startRequestsSnapshot()
	if len(initialReqs) != 1 {
		t.Fatalf("expected one initial start call, got %d", len(initialReqs))
	}
	firstDedupe := strings.TrimSpace(initialReqs[0].DedupeKey)
	if firstDedupe == "" {
		t.Fatalf("expected first start call to include dedupe key")
	}

	time.Sleep(300 * time.Millisecond)
	if calls, _ := cloud.calls(); calls != 1 {
		t.Fatalf("expected no retry storm before cooldown expiry, got %d start calls", calls)
	}

	module.mu.Lock()
	if module.cooldownUntil == nil {
		t.Fatalf("expected cooldown to be set")
	}
	soon := time.Now().UTC().Add(30 * time.Millisecond)
	module.cooldownUntil = &soon
	if module.startCandidate != nil {
		module.startCandidate.At = time.Now().UTC().Add(-module.cfg.StartDebounce.Std() - 20*time.Millisecond)
	}
	module.mu.Unlock()
	module.Reevaluate()

	waitForCondition(t, 5*time.Second, func() bool {
		if calls, _ := cloud.calls(); calls < 2 {
			return false
		}
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateActive) && snap.ActiveSessionID == "session-2"
	})

	reqs := cloud.startRequestsSnapshot()
	if len(reqs) < 2 {
		t.Fatalf("expected second start call after cooldown expiry")
	}
	secondDedupe := strings.TrimSpace(reqs[1].DedupeKey)
	if secondDedupe == "" {
		t.Fatalf("expected second start call to include dedupe key")
	}
	if secondDedupe == firstDedupe {
		t.Fatalf("expected fresh dedupe key on retry, got same value %q", secondDedupe)
	}

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.BlockedReason != "" {
		t.Fatalf("expected blocked reason cleared on success, got %q", snap.BlockedReason)
	}
	if snap.LastError != "" {
		t.Fatalf("expected last error cleared on success, got %q", snap.LastError)
	}
}

func TestStartLifecycleRevokedEntersLifecycleBlocked(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{
		startErr: &cloudclient.APIError{
			StatusCode: 401,
			Message:    "receiver credential revoked",
			Retryable:  false,
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateDegraded) && snap.ReconciliationState == reconciliationLifecycleRevoked
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.ControlState != controlStateLifecycleBlocked {
		t.Fatalf("expected control state %q, got %q", controlStateLifecycleBlocked, snap.ControlState)
	}
	if snap.LastActionResult != "rejected_revoked" {
		t.Fatalf("expected last action result rejected_revoked, got %q", snap.LastActionResult)
	}
	if snap.BlockedReason == "" {
		t.Fatalf("expected blocked reason under lifecycle block")
	}
}

func TestStopStateMismatchConflictBlocked(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	cloud := &mockSessionClient{}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, nil, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	cloud.mu.Lock()
	cloud.stopErr = &cloudclient.APIError{
		StatusCode: 409,
		Message:    "state mismatch: cannot stop from current state",
		Retryable:  false,
	}
	cloud.mu.Unlock()

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateDegraded) && snap.ReconciliationState == reconciliationConflictStateMismatch
	})

	snap := statusModel.Snapshot().HomeAutoSession
	if snap.ControlState != controlStateConflictBlocked {
		t.Fatalf("expected control state %q, got %q", controlStateConflictBlocked, snap.ControlState)
	}
	if snap.LastActionResult != "state_mismatch_conflict" {
		t.Fatalf("expected last action result state_mismatch_conflict, got %q", snap.LastActionResult)
	}
	if snap.CooldownUntil != nil {
		t.Fatalf("expected no retry cooldown for non-retryable stop conflict")
	}
	if strings.Contains(strings.ToLower(snap.Summary), "cloud unreachable/slow") {
		t.Fatalf("expected non-retryable stop conflict summary, got %q", snap.Summary)
	}
}

func TestCloudFailureLogIncludesRequestIDAndSessionFlag(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	statusModel := status.New()
	cloud := &mockSessionClient{
		stopErr: &cloudclient.APIError{
			StatusCode: 409,
			Message:    "state mismatch: cannot stop from current state",
			Retryable:  false,
			RequestID:  "req-stop-log-1",
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, logger, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()
	waitForCondition(t, 5*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateActive)
	})

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(3*time.Second)))
	module.Reevaluate()
	waitForCondition(t, 6*time.Second, func() bool {
		return statusModel.Snapshot().HomeAutoSession.State == string(StateDegraded)
	})

	logText := out.String()
	if !strings.Contains(logText, `"cloud_request_id":"req-stop-log-1"`) {
		t.Fatalf("expected cloud request id in logs, got: %s", logText)
	}
	if !strings.Contains(logText, `"session_id_included":true`) {
		t.Fatalf("expected session_id_included flag in logs, got: %s", logText)
	}
}

func TestStartMissingSessionIDConflictLogIncludesClassAndRetryMetadata(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	var out bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&out, &slog.HandlerOptions{Level: slog.LevelDebug}))

	statusModel := status.New()
	cloud := &mockSessionClient{
		startErr: &cloudclient.APIError{
			StatusCode: 409,
			Message:    "home auto session start is missing sessionId",
			Retryable:  false,
			RequestID:  "req-start-missing-log-1",
		},
	}
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeControl), store, statusModel, logger, cloud)

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		time.Sleep(80 * time.Millisecond)
	}()
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", latOffsetMeters(37.3349, 250), -122.0090, now.Add(time.Second)))
	module.Reevaluate()

	waitForCondition(t, 6*time.Second, func() bool {
		snap := statusModel.Snapshot().HomeAutoSession
		return snap.State == string(StateCooldown) && snap.ControlState == controlStateConflictBlocked
	})

	logText := out.String()
	if !strings.Contains(logText, `"error_class":"has_start_missing_session_id_conflict"`) {
		t.Fatalf("expected missing-session conflict error class in logs, got: %s", logText)
	}
	if !strings.Contains(logText, `"cloud_request_id":"req-start-missing-log-1"`) {
		t.Fatalf("expected cloud request id in logs, got: %s", logText)
	}
	if !strings.Contains(logText, `"dedupe_key_hash":"`) {
		t.Fatalf("expected dedupe key hash in logs, got: %s", logText)
	}
	if !strings.Contains(logText, `"next_retry_at":"`) {
		t.Fatalf("expected next_retry_at in logs, got: %s", logText)
	}
}

func homeAutoTestConfig(mode config.HomeAutoSessionMode) config.HomeAutoSessionConfig {
	return config.HomeAutoSessionConfig{
		Enabled: true,
		Mode:    mode,
		Home: config.HomeGeofenceConfig{
			Lat:     37.3349,
			Lon:     -122.0090,
			RadiusM: 150,
		},
		TrackedNodeIDs:      []string{"!nodeA"},
		StartDebounce:       config.Duration(25 * time.Millisecond),
		StopDebounce:        config.Duration(25 * time.Millisecond),
		IdleStopTimeout:     config.Duration(45 * time.Second),
		StartupReconcile:    true,
		SessionNameTemplate: "Home Auto {{.NodeID}}",
		Cloud: config.HomeAutoSessionCloudCfg{
			StartEndpoint: "/api/receiver/home-auto-session/start",
			StopEndpoint:  "/api/receiver/home-auto-session/stop",
		},
	}
}

func testPacket(nodeID string, lat, lon float64, at time.Time) meshtastic.Event {
	return meshtastic.Event{
		Kind: meshtastic.EventPacket,
		Packet: &meshtastic.Packet{
			SourceNodeID: nodeID,
			ReceivedAt:   at,
			Position: &meshtastic.Position{
				Lat: lat,
				Lon: lon,
			},
		},
		Received: at,
	}
}

func waitForCondition(t *testing.T, timeout time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for condition")
}

func latOffsetMeters(baseLat float64, meters float64) float64 {
	return baseLat + meters/111111.0
}
