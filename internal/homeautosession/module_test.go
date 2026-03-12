package homeautosession

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

type mockSessionClient struct {
	startCalls int
	stopCalls  int

	startErr error
	stopErr  error
}

func (m *mockSessionClient) StartHomeAutoSession(_ context.Context, _ string, _ string, _ cloudclient.HomeAutoSessionStartRequest) (cloudclient.HomeAutoSessionStartResult, error) {
	m.startCalls++
	if m.startErr != nil {
		return cloudclient.HomeAutoSessionStartResult{}, m.startErr
	}
	return cloudclient.HomeAutoSessionStartResult{
		SessionID: "session-1",
		StartedAt: time.Now().UTC(),
	}, nil
}

func (m *mockSessionClient) StopHomeAutoSession(_ context.Context, _ string, _ string, _ cloudclient.HomeAutoSessionStopRequest) (cloudclient.HomeAutoSessionStopResult, error) {
	m.stopCalls++
	if m.stopErr != nil {
		return cloudclient.HomeAutoSessionStopResult{}, m.stopErr
	}
	return cloudclient.HomeAutoSessionStopResult{
		SessionID: "session-1",
		StoppedAt: time.Now().UTC(),
		Status:    "stopped",
	}, nil
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
	module.Start(ctx)

	waitForState(t, statusModel, string(StateDisabled))
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestObserveModeWouldStartDecision(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	statusModel := status.New()
	module := New(homeAutoTestConfig(config.HomeAutoSessionModeObserve), store, statusModel, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	module.Start(ctx)

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, time.Now().UTC()))
	module.ObserveEvent(testPacket("!nodeA", 37.3410, -122.0010, time.Now().UTC().Add(time.Second)))
	module.Reevaluate()

	waitForState(t, statusModel, string(StateObserveReady))
	snap := statusModel.Snapshot().HomeAutoSession
	if snap.ActiveSessionID != "" {
		t.Fatalf("expected no active session in observe mode")
	}
	if snap.Summary == "" {
		t.Fatalf("expected observe summary")
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestControlModeStartAndStop(t *testing.T) {
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
	module.Start(ctx)

	now := time.Now().UTC()
	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now))
	module.ObserveEvent(testPacket("!nodeA", 37.3410, -122.0010, now.Add(time.Second)))
	module.Reevaluate()
	waitForState(t, statusModel, string(StateActive))

	module.ObserveEvent(testPacket("!nodeA", 37.3349, -122.0090, now.Add(2*time.Second)))
	module.Reevaluate()
	waitForState(t, statusModel, string(StateControlReady))

	if cloud.startCalls == 0 {
		t.Fatalf("expected start cloud call")
	}
	if cloud.stopCalls == 0 {
		t.Fatalf("expected stop cloud call")
	}
	snap := store.Snapshot()
	if snap.HomeAutoSession.LastStartDedupeKey == "" {
		t.Fatalf("expected persisted last_start_dedupe_key")
	}
	if snap.HomeAutoSession.LastStopDedupeKey == "" {
		t.Fatalf("expected persisted last_stop_dedupe_key")
	}
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestMisconfiguredState(t *testing.T) {
	store, err := state.Open(filepath.Join(t.TempDir(), "receiver-state.json"))
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	statusModel := status.New()
	module := New(config.HomeAutoSessionConfig{
		Enabled: true,
		Mode:    config.HomeAutoSessionModeObserve,
		Home: config.HomeGeofenceConfig{
			Lat:     37.3349,
			Lon:     -122.0090,
			RadiusM: 150,
		},
		StartDebounce:   config.Duration(25 * time.Millisecond),
		StopDebounce:    config.Duration(25 * time.Millisecond),
		IdleStopTimeout: config.Duration(time.Minute),
	}, store, statusModel, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	module.Start(ctx)

	waitForState(t, statusModel, string(StateMisconfigured))
	cancel()
	time.Sleep(50 * time.Millisecond)
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

func waitForState(t *testing.T, model *status.Model, stateCode string) {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if model.Snapshot().HomeAutoSession.State == stateCode {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for home_auto_session state %q, got %q", stateCode, model.Snapshot().HomeAutoSession.State)
}
