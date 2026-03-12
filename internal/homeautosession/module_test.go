package homeautosession

import (
	"context"
	"path/filepath"
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

	startRequests []cloudclient.HomeAutoSessionStartRequest
	stopRequests  []cloudclient.HomeAutoSessionStopRequest
}

func (m *mockSessionClient) StartHomeAutoSession(_ context.Context, _ string, _ string, request cloudclient.HomeAutoSessionStartRequest) (cloudclient.HomeAutoSessionStartResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.startCalls++
	m.startRequests = append(m.startRequests, request)
	if m.startErr != nil {
		return cloudclient.HomeAutoSessionStartResult{}, m.startErr
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
	if m.stopErr != nil {
		return cloudclient.HomeAutoSessionStopResult{}, m.stopErr
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
