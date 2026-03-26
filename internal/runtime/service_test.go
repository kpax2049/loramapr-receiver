package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/homeautosession"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/pairing"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

func TestResolveMode(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		request  config.RunMode
		phase    state.PairingPhase
		expected config.RunMode
	}{
		{name: "explicit setup", request: config.ModeSetup, phase: state.PairingSteadyState, expected: config.ModeSetup},
		{name: "explicit service", request: config.ModeService, phase: state.PairingUnpaired, expected: config.ModeService},
		{name: "auto unpaired", request: config.ModeAuto, phase: state.PairingUnpaired, expected: config.ModeSetup},
		{name: "auto steady", request: config.ModeAuto, phase: state.PairingSteadyState, expected: config.ModeService},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveMode(tc.request, tc.phase)
			if got != tc.expected {
				t.Fatalf("expected %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestDetectRuntimeProfile(t *testing.T) {
	t.Parallel()

	if got := detectRuntimeProfile("/var/lib/loramapr/state.json"); got != "linux-service" {
		t.Fatalf("expected linux-service profile, got %q", got)
	}
	if got := detectRuntimeProfile("C:/Users/test/AppData/Roaming/loramapr/state.json"); got != "windows-user" {
		t.Fatalf("expected windows-user profile, got %q", got)
	}
	if got := detectRuntimeProfile("./data/receiver-state.json"); got != "local-dev" {
		t.Fatalf("expected local-dev profile, got %q", got)
	}
}

func TestMapMeshtasticConfigStatus(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 14, 10, 0, 0, 0, time.UTC)
	mapped := mapMeshtasticConfigStatus(meshtastic.Snapshot{
		State: meshtastic.StateConnected,
		HomeConfig: &meshtastic.HomeNodeConfigSummary{
			Available:         true,
			Region:            "EU_868",
			PrimaryChannel:    "Home Mesh",
			PSKState:          "present",
			ShareURL:          "https://meshtastic.org/e/#CwgB",
			ShareURLRedacted:  "https://meshtastic.org/e/#<redacted>",
			ShareURLAvailable: true,
			Source:            "status_event",
			UpdatedAt:         now,
		},
	})
	if !mapped.Available {
		t.Fatal("expected mapped meshtastic config available")
	}
	if mapped.Region != "EU_868" {
		t.Fatalf("expected region EU_868, got %q", mapped.Region)
	}
	if !mapped.ShareURLAvailable {
		t.Fatal("expected share URL available")
	}
	if mapped.UpdatedAt == nil || !mapped.UpdatedAt.Equal(now) {
		t.Fatalf("unexpected mapped updated_at: %#v", mapped.UpdatedAt)
	}

	unavailable := mapMeshtasticConfigStatus(meshtastic.Snapshot{State: meshtastic.StateConnected})
	if unavailable.Available {
		t.Fatal("expected unavailable meshtastic config when no home config summary is present")
	}
	if unavailable.UnavailableReason == "" {
		t.Fatal("expected unavailable reason for missing config summary")
	}
}

func TestResolveRuntimeProfile(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		requested string
		stateFile string
		expected  string
	}{
		{
			name:      "auto by linux path",
			requested: "auto",
			stateFile: "/var/lib/loramapr/receiver-state.json",
			expected:  "linux-service",
		},
		{
			name:      "appliance override",
			requested: "appliance-pi",
			stateFile: "./data/receiver-state.json",
			expected:  "appliance-pi",
		},
		{
			name:      "local dev override",
			requested: "local-dev",
			stateFile: "/var/lib/loramapr/receiver-state.json",
			expected:  "local-dev",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := resolveRuntimeProfile(tc.requested, tc.stateFile)
			if got != tc.expected {
				t.Fatalf("expected profile %q, got %q", tc.expected, got)
			}
		})
	}
}

func TestNewPersistsIdentityHints(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	cfg.Paths.StateFile = filepath.Join(t.TempDir(), "receiver-state.json")
	cfg.Runtime.Profile = "linux-service"
	cfg.Runtime.LocalName = "Kitchen Receiver"
	cfg.Service.Mode = config.ModeSetup

	svc, err := New(cfg, slog.Default())
	if err != nil {
		t.Fatalf("runtime.New failed: %v", err)
	}

	snap := svc.StateStore().Snapshot()
	if snap.Installation.LocalName != "Kitchen Receiver" {
		t.Fatalf("expected persisted local name hint, got %q", snap.Installation.LocalName)
	}
	if snap.Installation.Hostname == "" {
		t.Fatal("expected hostname hint to be persisted")
	}

	statusSnap := svc.CurrentStatus()
	if statusSnap.LocalName != "Kitchen Receiver" {
		t.Fatalf("expected status local_name, got %q", statusSnap.LocalName)
	}
	if statusSnap.InstallType != "linux-package" {
		t.Fatalf("expected install_type linux-package, got %q", statusSnap.InstallType)
	}
}

type mockCloudClient struct {
	postErr      error
	postCalls    int
	lastPayload  map[string]any
	lastEventKey string

	heartbeatErr   error
	heartbeatCalls int
	lastHeartbeat  cloudclient.ReceiverHeartbeat
	ackConfigVer   string
	ackHomeAutoCfg *cloudclient.HomeAutoSessionManagedConfig

	startHomeAutoCalls int
	stopHomeAutoCalls  int
}

func (m *mockCloudClient) ExchangePairingCode(_ context.Context, _ string) (cloudclient.BootstrapExchange, error) {
	return cloudclient.BootstrapExchange{}, nil
}

func (m *mockCloudClient) ActivateReceiver(
	_ context.Context,
	_ string,
	_ cloudclient.ActivationRequest,
) (cloudclient.ActivationResult, error) {
	return cloudclient.ActivationResult{}, nil
}

func (m *mockCloudClient) PostIngestEvent(
	_ context.Context,
	_ string,
	_ string,
	payload map[string]any,
	idempotencyKey string,
) error {
	m.postCalls++
	m.lastPayload = payload
	m.lastEventKey = idempotencyKey
	return m.postErr
}

func (m *mockCloudClient) SendReceiverHeartbeat(
	_ context.Context,
	_ string,
	_ string,
	heartbeat cloudclient.ReceiverHeartbeat,
) (cloudclient.ReceiverHeartbeatAck, error) {
	m.heartbeatCalls++
	m.lastHeartbeat = heartbeat
	if m.heartbeatErr != nil {
		return cloudclient.ReceiverHeartbeatAck{}, m.heartbeatErr
	}
	return cloudclient.ReceiverHeartbeatAck{
		ReceiverAgentID:       "agent-1",
		OwnerID:               "owner-1",
		ConfigVersion:         m.ackConfigVer,
		LastHeartbeatAt:       time.Now().UTC(),
		NodeCount:             len(heartbeat.ObservedNodeIDs),
		HomeAutoSessionConfig: m.ackHomeAutoCfg,
	}, nil
}

func (m *mockCloudClient) StartHomeAutoSession(
	_ context.Context,
	_ string,
	_ string,
	_ cloudclient.HomeAutoSessionStartRequest,
) (cloudclient.HomeAutoSessionStartResult, error) {
	m.startHomeAutoCalls++
	return cloudclient.HomeAutoSessionStartResult{
		SessionID: "session-1",
		StartedAt: time.Now().UTC(),
	}, nil
}

func (m *mockCloudClient) StopHomeAutoSession(
	_ context.Context,
	_ string,
	_ string,
	_ cloudclient.HomeAutoSessionStopRequest,
) (cloudclient.HomeAutoSessionStopResult, error) {
	m.stopHomeAutoCalls++
	return cloudclient.HomeAutoSessionStopResult{
		SessionID: "session-1",
		StoppedAt: time.Now().UTC(),
		Status:    "stopped",
	}, nil
}

func boolPtr(v bool) *bool {
	return &v
}

func TestShapeIngestPayload(t *testing.T) {
	t.Parallel()

	payload, key, capturedAt := shapeIngestPayload(meshtastic.Packet{
		SourceNodeID:      "!node-1",
		DestinationNodeID: "!gateway",
		PortNum:           3,
		Payload:           []byte("hello"),
		ReceivedAt:        time.Date(2026, 3, 10, 23, 0, 0, 0, time.UTC),
		Position: &meshtastic.Position{
			Lat: 49.3959195,
			Lon: 7.6103928,
		},
		Meta: map[string]string{
			"rssi": "-87",
			"snr":  "4.50",
		},
	})
	if !strings.HasPrefix(key, "rx-") {
		t.Fatalf("unexpected idempotency key: %q", key)
	}
	if capturedAt.IsZero() {
		t.Fatal("expected capturedAt timestamp")
	}
	if payload["fromId"] != "!node-1" {
		t.Fatalf("unexpected payload source: %#v", payload)
	}
	if payload["packetId"] != key {
		t.Fatalf("expected payload packetId to match idempotency key")
	}
	if payload["portnum"] != "POSITION_APP" {
		t.Fatalf("expected POSITION_APP port label, got %#v", payload["portnum"])
	}
	decoded, ok := payload["decoded"].(map[string]any)
	if !ok {
		t.Fatalf("expected decoded map, got %#v", payload["decoded"])
	}
	if decoded["portnum"] != "POSITION_APP" {
		t.Fatalf("expected decoded.position port label, got %#v", decoded["portnum"])
	}
	position, ok := decoded["position"].(map[string]any)
	if !ok {
		t.Fatalf("expected decoded.position map, got %#v", decoded["position"])
	}
	if position["latitudeI"] != int64(493959195) {
		t.Fatalf("unexpected latitudeI: %#v", position["latitudeI"])
	}
	if payload["rxRssi"] != -87 {
		t.Fatalf("unexpected rxRssi: %#v", payload["rxRssi"])
	}
}

func TestDrainIngestQueueRetryableFailure(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{
		postErr: &cloudclient.APIError{StatusCode: 503, Message: "outage", Retryable: true},
	}
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: status.New(),
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{{
				payload:        map[string]any{"fromId": "node-1"},
				idempotencyKey: "evt-1",
				nextAttemptAt:  time.Now().Add(-time.Second),
			}},
		},
	}

	err := svc.drainIngestQueue(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			IngestEndpoint: "/api/meshtastic/event",
			IngestAPIKey:   "secret",
		},
	})
	if err == nil || !cloudclient.IsRetryable(err) {
		t.Fatalf("expected retryable error, got %v", err)
	}
	if len(svc.steady.ingestQueue) != 1 {
		t.Fatalf("expected queue item to remain for retry")
	}
	if svc.steady.ingestQueue[0].attempts != 1 {
		t.Fatalf("expected retry attempts to increment")
	}
}

func TestDrainIngestQueueSuccess(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{}
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: status.New(),
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{{
				payload:        map[string]any{"fromId": "node-2"},
				idempotencyKey: "evt-2",
				nextAttemptAt:  time.Now().Add(-time.Second),
			}},
		},
	}

	err := svc.drainIngestQueue(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			IngestEndpoint: "/api/meshtastic/event",
			IngestAPIKey:   "secret",
		},
	})
	if err != nil {
		t.Fatalf("expected successful drain, got %v", err)
	}
	if len(svc.steady.ingestQueue) != 0 {
		t.Fatalf("expected queue to be emptied")
	}
	if svc.steady.lastPacketAck == nil {
		t.Fatalf("expected last packet ack timestamp")
	}
}

func TestOnMeshtasticEventSignalsIngestDispatch(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: status.New(),
		},
		ingestWake: make(chan struct{}, 1),
		steady: steadyState{
			ingestQueue: make([]queuedIngestEvent, 0, 4),
		},
	}

	svc.onMeshtasticEvent(meshtastic.Event{
		Kind: meshtastic.EventPacket,
		Packet: &meshtastic.Packet{
			SourceNodeID:      "!nodeA",
			DestinationNodeID: "!nodeB",
			PortNum:           3,
			Payload:           []byte("hello"),
			ReceivedAt:        now,
		},
		Received: now,
	})

	if len(svc.steady.ingestQueue) != 1 {
		t.Fatalf("expected one queued ingest event, got %d", len(svc.steady.ingestQueue))
	}
	select {
	case <-svc.ingestWake:
	default:
		t.Fatal("expected ingest wake signal after packet enqueue")
	}
}

func TestProcessIngestDispatchDeliversImmediatelyWhenReachable(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.EndpointURL = "https://api.example.com"
		data.Cloud.IngestEndpoint = "/api/meshtastic/event"
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	mockCloud := &mockCloudClient{}
	statusModel := status.New()
	now := time.Now().UTC()
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: statusModel,
			State:  store,
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{
					payload:        map[string]any{"fromId": "node-1"},
					idempotencyKey: "evt-1",
					capturedAt:     now.Add(-200 * time.Millisecond),
					enqueuedAt:     now,
					nextAttemptAt:  now.Add(-10 * time.Millisecond),
				},
			},
		},
	}

	svc.processIngestDispatch(context.Background(), "test")

	if mockCloud.postCalls != 1 {
		t.Fatalf("expected immediate ingest post call, got %d", mockCloud.postCalls)
	}
	if len(svc.steady.ingestQueue) != 0 {
		t.Fatalf("expected queue to be drained, got %d", len(svc.steady.ingestQueue))
	}
	if svc.steady.lastPacketAck == nil || svc.steady.lastPacketSent == nil {
		t.Fatal("expected packet send/ack telemetry after dispatch")
	}
	capturedAt := now.Add(-200 * time.Millisecond)
	lagToAck := svc.steady.lastPacketAck.Sub(capturedAt)
	if lagToAck < 0 || lagToAck > 2*time.Second {
		t.Fatalf("expected near-real-time ack lag, got %s", lagToAck)
	}
	lagSendToAck := svc.steady.lastPacketAck.Sub(*svc.steady.lastPacketSent)
	if lagSendToAck < 0 || lagSendToAck > time.Second {
		t.Fatalf("expected quick send->ack lag, got %s", lagSendToAck)
	}
	t.Logf("ingest lag captured->ack=%s send->ack=%s", lagToAck, lagSendToAck)
	if statusModel.Snapshot().CloudStatus != "reachable" {
		t.Fatalf("expected cloud status reachable, got %q", statusModel.Snapshot().CloudStatus)
	}
}

func TestProcessIngestDispatchRecoversAfterRetryableOutage(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.EndpointURL = "https://api.example.com"
		data.Cloud.IngestEndpoint = "/api/meshtastic/event"
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	mockCloud := &mockCloudClient{
		postErr: &cloudclient.APIError{StatusCode: 503, Message: "outage", Retryable: true},
	}
	statusModel := status.New()
	now := time.Now().UTC()
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: statusModel,
			State:  store,
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{
					payload:        map[string]any{"fromId": "node-2"},
					idempotencyKey: "evt-2",
					capturedAt:     now.Add(-time.Second),
					enqueuedAt:     now.Add(-500 * time.Millisecond),
					nextAttemptAt:  now.Add(-10 * time.Millisecond),
				},
			},
		},
	}

	svc.processIngestDispatch(context.Background(), "test")
	if mockCloud.postCalls != 1 {
		t.Fatalf("expected first post attempt, got %d", mockCloud.postCalls)
	}
	if len(svc.steady.ingestQueue) != 1 {
		t.Fatalf("expected queue item to remain after retryable outage")
	}
	if svc.steady.ingestQueue[0].attempts != 1 {
		t.Fatalf("expected retry attempts=1, got %d", svc.steady.ingestQueue[0].attempts)
	}

	mockCloud.postErr = nil
	svc.processIngestDispatch(context.Background(), "test")
	if mockCloud.postCalls != 1 {
		t.Fatalf("expected no immediate reattempt before retry window, got %d calls", mockCloud.postCalls)
	}

	svc.steady.ingestQueue[0].nextAttemptAt = time.Now().UTC().Add(-10 * time.Millisecond)
	svc.processIngestDispatch(context.Background(), "test")
	if mockCloud.postCalls != 2 {
		t.Fatalf("expected retry attempt after retry window, got %d", mockCloud.postCalls)
	}
	if len(svc.steady.ingestQueue) != 0 {
		t.Fatalf("expected queue drained after recovery, got %d", len(svc.steady.ingestQueue))
	}
}

func TestDrainIngestQueueBypassesRetryWaitingHead(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{}
	now := time.Now().UTC()
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: status.New(),
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{
					payload:        map[string]any{"fromId": "head"},
					idempotencyKey: "evt-head",
					nextAttemptAt:  now.Add(2 * time.Minute),
					attempts:       4,
				},
				{
					payload:        map[string]any{"fromId": "due"},
					idempotencyKey: "evt-due",
					nextAttemptAt:  now.Add(-10 * time.Millisecond),
				},
			},
		},
	}

	err := svc.drainIngestQueue(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			IngestEndpoint: "/api/meshtastic/event",
			IngestAPIKey:   "secret",
		},
	})
	if err != nil {
		t.Fatalf("expected successful drain, got %v", err)
	}
	if mockCloud.postCalls != 1 {
		t.Fatalf("expected one post call for due item, got %d", mockCloud.postCalls)
	}
	if mockCloud.lastEventKey != "evt-due" {
		t.Fatalf("expected due item to be delivered first, got %q", mockCloud.lastEventKey)
	}
	if len(svc.steady.ingestQueue) != 1 {
		t.Fatalf("expected waiting head to remain queued, got %d items", len(svc.steady.ingestQueue))
	}
	if svc.steady.ingestQueue[0].idempotencyKey != "evt-head" {
		t.Fatalf("expected remaining queue head evt-head, got %q", svc.steady.ingestQueue[0].idempotencyKey)
	}
}

func TestNextDispatchableIngestIndex(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	idx, next := nextDispatchableIngestIndex([]queuedIngestEvent{
		{nextAttemptAt: now.Add(90 * time.Second)},
		{nextAttemptAt: now.Add(-time.Second)},
	}, now)
	if idx != 1 {
		t.Fatalf("expected index 1, got %d", idx)
	}
	if next.IsZero() {
		t.Fatal("expected next attempt timestamp for due item")
	}

	idx, next = nextDispatchableIngestIndex([]queuedIngestEvent{
		{nextAttemptAt: now.Add(30 * time.Second)},
		{nextAttemptAt: now.Add(10 * time.Second)},
	}, now)
	if idx != -1 {
		t.Fatalf("expected no dispatchable index, got %d", idx)
	}
	if next.IsZero() {
		t.Fatal("expected earliest retry timestamp when all items are waiting")
	}
}

func TestProcessSteadyStateDoesNotMaskIngestRetryState(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{}
	statusModel := status.New()
	statusModel.SetLastError("cloud endpoint unreachable")
	statusModel.SetComponent("ingest", "retrying", "retrying in 30s")

	svc := &Service{
		container: &Container{
			Config: config.Default(),
			Logger: slog.Default(),
			Status: statusModel,
			Cloud:  mockCloud,
		},
		mode: config.ModeService,
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{
					payload:        map[string]any{"fromId": "node-3"},
					idempotencyKey: "evt-3",
					nextAttemptAt:  time.Now().UTC().Add(2 * time.Minute),
				},
			},
		},
	}

	svc.processSteadyState(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			HeartbeatEndpoint: "/api/receiver/heartbeat",
			IngestEndpoint:    "/api/meshtastic/event",
			IngestAPIKey:      "secret",
		},
	}, meshtastic.Snapshot{State: meshtastic.StateConnected})

	if got := statusModel.Snapshot().LastError; strings.TrimSpace(got) == "" {
		t.Fatal("expected ingest retry failure context to remain visible")
	}
}

func TestProcessIngestDispatchDrainsBacklogWhenCloudRecovered(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.EndpointURL = "https://api.example.com"
		data.Cloud.IngestEndpoint = "/api/meshtastic/event"
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	mockCloud := &mockCloudClient{}
	statusModel := status.New()
	now := time.Now().UTC()
	queue := make([]queuedIngestEvent, 0, 3)
	for i := 0; i < 3; i++ {
		queue = append(queue, queuedIngestEvent{
			payload:        map[string]any{"fromId": fmt.Sprintf("node-%d", i+1)},
			idempotencyKey: fmt.Sprintf("evt-%d", i+1),
			capturedAt:     now.Add(-time.Duration(i+1) * time.Second),
			enqueuedAt:     now.Add(-time.Duration(i+1) * 400 * time.Millisecond),
			nextAttemptAt:  now.Add(-10 * time.Millisecond),
			attempts:       1,
		})
	}

	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: statusModel,
			State:  store,
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: queue,
		},
	}

	svc.processIngestDispatch(context.Background(), "interval")

	if mockCloud.postCalls != 3 {
		t.Fatalf("expected all queued events to flush after recovery, got %d calls", mockCloud.postCalls)
	}
	if len(svc.steady.ingestQueue) != 0 {
		t.Fatalf("expected backlog drained, got %d remaining", len(svc.steady.ingestQueue))
	}
	if svc.steady.lastPacketAck == nil {
		t.Fatal("expected ack timestamp after backlog drain")
	}
}

func TestSendHeartbeatPayloadShaping(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{}
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: status.New(),
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{payload: map[string]any{"fromId": "node-1"}},
				{payload: map[string]any{"fromId": "node-2"}},
			},
		},
		mode: config.ModeService,
	}

	err := svc.sendHeartbeat(context.Background(), state.Data{
		Installation: state.InstallationState{
			ID:        "install-123",
			LocalName: "garage-pi-abc123",
			Hostname:  "garage-pi",
		},
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			HeartbeatEndpoint: "/api/receiver/heartbeat",
			IngestAPIKey:      "secret",
			ReceiverID:        "rx-123",
			ReceiverLabel:     "Garage Receiver",
			SiteLabel:         "Home",
			GroupLabel:        "Outdoor",
		},
		Runtime: state.RuntimeState{InstallType: "pi-appliance"},
	}, meshtastic.Snapshot{
		State:           meshtastic.StateConnected,
		LocalNodeID:     "!home",
		ObservedNodeIDs: []string{"!a", "!b"},
	})
	if err != nil {
		t.Fatalf("sendHeartbeat returned error: %v", err)
	}
	if mockCloud.heartbeatCalls != 1 {
		t.Fatalf("expected heartbeat call, got %d", mockCloud.heartbeatCalls)
	}
	if mockCloud.lastHeartbeat.LocalNodeID != "!home" {
		t.Fatalf("unexpected heartbeat local node id")
	}
	if len(mockCloud.lastHeartbeat.ObservedNodeIDs) != 2 {
		t.Fatalf("unexpected observed nodes in heartbeat payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["buildDate"]; !ok {
		t.Fatalf("expected buildDate in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["buildID"]; !ok {
		t.Fatalf("expected buildID in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["updateStatus"]; !ok {
		t.Fatalf("expected updateStatus in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["failureCode"]; !ok {
		t.Fatalf("expected failureCode in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["attentionState"]; !ok {
		t.Fatalf("expected attentionState in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["attentionHint"]; !ok {
		t.Fatalf("expected attentionHint in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["operationalStatus"]; !ok {
		t.Fatalf("expected operationalStatus in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["homeAutoControlState"]; !ok {
		t.Fatalf("expected homeAutoControlState in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["homeAutoConfigSource"]; !ok {
		t.Fatalf("expected homeAutoConfigSource in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["homeAutoConfigVersion"]; !ok {
		t.Fatalf("expected homeAutoConfigVersion in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["homeAutoConfigResult"]; !ok {
		t.Fatalf("expected homeAutoConfigResult in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["homeAutoActiveSource"]; !ok {
		t.Fatalf("expected homeAutoActiveSource in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["homeAutoLastResult"]; !ok {
		t.Fatalf("expected homeAutoLastResult in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["meshConfigAvailable"]; !ok {
		t.Fatalf("expected meshConfigAvailable in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["meshConfigPSKState"]; !ok {
		t.Fatalf("expected meshConfigPSKState in heartbeat status payload")
	}
	if _, ok := mockCloud.lastHeartbeat.Status["meshConfigShareReady"]; !ok {
		t.Fatalf("expected meshConfigShareReady in heartbeat status payload")
	}
	if got := mockCloud.lastHeartbeat.Status["localName"]; got != "garage-pi-abc123" {
		t.Fatalf("expected localName in heartbeat payload, got %#v", got)
	}
	if got := mockCloud.lastHeartbeat.Status["receiverLabel"]; got != "Garage Receiver" {
		t.Fatalf("expected receiverLabel in heartbeat payload, got %#v", got)
	}
}

func TestProcessSteadyStateSendsHeartbeatDuringIdle(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{}
	statusModel := status.New()
	svc := &Service{
		container: &Container{
			Config: config.Default(),
			Logger: slog.Default(),
			Status: statusModel,
			Cloud:  mockCloud,
		},
		mode: config.ModeService,
	}

	svc.processSteadyState(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			HeartbeatEndpoint: "/api/receiver/heartbeat",
			IngestAPIKey:      "secret",
		},
	}, meshtastic.Snapshot{State: meshtastic.StateConnected})

	if mockCloud.heartbeatCalls != 1 {
		t.Fatalf("expected heartbeat call during idle tick, got %d", mockCloud.heartbeatCalls)
	}
	if svc.steady.lastHeartbeatSent == nil || svc.steady.lastHeartbeatAck == nil {
		t.Fatalf("expected heartbeat send+ack timestamps to be tracked")
	}
	component, ok := statusModel.Snapshot().Components["heartbeat"]
	if !ok {
		t.Fatalf("expected heartbeat component status")
	}
	if component.State != "sent" {
		t.Fatalf("expected heartbeat component state sent, got %q", component.State)
	}
}

func TestProcessSteadyStateHeartbeatRecoversAfterRetryableFailure(t *testing.T) {
	t.Parallel()

	mockCloud := &mockCloudClient{
		heartbeatErr: &cloudclient.APIError{StatusCode: 503, Message: "outage", Retryable: true},
	}
	statusModel := status.New()
	svc := &Service{
		container: &Container{
			Config: config.Default(),
			Logger: slog.Default(),
			Status: statusModel,
			Cloud:  mockCloud,
		},
		mode: config.ModeService,
	}

	snapshot := state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			HeartbeatEndpoint: "/api/receiver/heartbeat",
			IngestAPIKey:      "secret",
		},
	}
	meshSnap := meshtastic.Snapshot{State: meshtastic.StateConnected}

	svc.processSteadyState(context.Background(), snapshot, meshSnap)
	firstHeartbeatStatus, ok := statusModel.Snapshot().Components["heartbeat"]
	if !ok {
		t.Fatalf("expected heartbeat component after failed tick")
	}
	if firstHeartbeatStatus.State != "failed" {
		t.Fatalf("expected heartbeat component state failed, got %q", firstHeartbeatStatus.State)
	}
	if svc.steady.lastHeartbeatAck != nil {
		t.Fatalf("expected no heartbeat ack after retryable failure")
	}

	mockCloud.heartbeatErr = nil
	svc.processSteadyState(context.Background(), snapshot, meshSnap)
	if mockCloud.heartbeatCalls != 2 {
		t.Fatalf("expected two heartbeat attempts across recovery, got %d", mockCloud.heartbeatCalls)
	}
	if svc.steady.lastHeartbeatAck == nil {
		t.Fatalf("expected heartbeat ack after recovery")
	}
	secondHeartbeatStatus := statusModel.Snapshot().Components["heartbeat"]
	if secondHeartbeatStatus.State != "sent" {
		t.Fatalf("expected heartbeat component state sent after recovery, got %q", secondHeartbeatStatus.State)
	}
}

func TestSendHeartbeatAppliesCloudManagedHomeAutoConfig(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	cfg.Paths.StateFile = statePath
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
		data.Cloud.HeartbeatEndpoint = "/api/receiver/heartbeat"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	mockCloud := &mockCloudClient{
		ackHomeAutoCfg: &cloudclient.HomeAutoSessionManagedConfig{
			Version:          "has-v1",
			Enabled:          boolPtr(true),
			Mode:             "observe",
			Home:             cloudclient.HomeAutoSessionManagedGeofence{Lat: 37.3349, Lon: -122.0090, RadiusM: 150},
			TrackedNodeIDs:   []string{"!nodeA"},
			StartDebounce:    "30s",
			StopDebounce:     "30s",
			IdleStopTimeout:  "15m",
			StartupReconcile: boolPtr(true),
		},
	}
	module := homeautosession.New(cfg.HomeAutoSession, store, statusModel, slog.Default(), mockCloud)
	svc := &Service{
		container: &Container{
			Config:          cfg,
			Logger:          slog.Default(),
			State:           store,
			Status:          statusModel,
			Cloud:           mockCloud,
			HomeAutoSession: module,
		},
		mode: config.ModeService,
	}

	err = svc.sendHeartbeat(context.Background(), store.Snapshot(), meshtastic.Snapshot{State: meshtastic.StateConnected})
	if err != nil {
		t.Fatalf("sendHeartbeat returned error: %v", err)
	}

	snap := store.Snapshot()
	if snap.HomeAutoSession.EffectiveConfigSource != homeautosession.ConfigSourceCloudManaged {
		t.Fatalf("expected cloud managed config source, got %q", snap.HomeAutoSession.EffectiveConfigSource)
	}
	if snap.HomeAutoSession.EffectiveConfigVersion != "has-v1" {
		t.Fatalf("expected effective config version has-v1, got %q", snap.HomeAutoSession.EffectiveConfigVersion)
	}
	if snap.HomeAutoSession.LastConfigApplyResult != homeAutoConfigApplyCloud {
		t.Fatalf("expected config apply result %q, got %q", homeAutoConfigApplyCloud, snap.HomeAutoSession.LastConfigApplyResult)
	}
}

func TestSendHeartbeatInvalidCloudManagedConfigFallsBackToLocal(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	cfg.Paths.StateFile = statePath
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
		data.Cloud.HeartbeatEndpoint = "/api/receiver/heartbeat"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	mockCloud := &mockCloudClient{
		ackHomeAutoCfg: &cloudclient.HomeAutoSessionManagedConfig{
			Version:         "has-v2",
			Enabled:         boolPtr(true),
			Mode:            "control",
			Home:            cloudclient.HomeAutoSessionManagedGeofence{Lat: 37.3349, Lon: -122.0090, RadiusM: 150},
			TrackedNodeIDs:  []string{"!nodeA"},
			StartDebounce:   "not-a-duration",
			StopDebounce:    "30s",
			IdleStopTimeout: "15m",
		},
	}
	module := homeautosession.New(cfg.HomeAutoSession, store, statusModel, slog.Default(), mockCloud)
	svc := &Service{
		container: &Container{
			Config:          cfg,
			Logger:          slog.Default(),
			State:           store,
			Status:          statusModel,
			Cloud:           mockCloud,
			HomeAutoSession: module,
		},
		mode: config.ModeService,
	}

	err = svc.sendHeartbeat(context.Background(), store.Snapshot(), meshtastic.Snapshot{State: meshtastic.StateConnected})
	if err != nil {
		t.Fatalf("sendHeartbeat returned error: %v", err)
	}

	snap := store.Snapshot()
	if snap.HomeAutoSession.EffectiveConfigSource != homeautosession.ConfigSourceLocalFallback {
		t.Fatalf("expected local fallback config source, got %q", snap.HomeAutoSession.EffectiveConfigSource)
	}
	if snap.HomeAutoSession.LastConfigApplyResult != homeAutoConfigApplyCloudInvalid {
		t.Fatalf("expected config apply result %q, got %q", homeAutoConfigApplyCloudInvalid, snap.HomeAutoSession.LastConfigApplyResult)
	}
	if strings.TrimSpace(snap.HomeAutoSession.LastConfigApplyError) == "" {
		t.Fatal("expected cloud config apply error to be persisted")
	}
}

func TestSendHeartbeatCloudManagedConfigCanDisableModule(t *testing.T) {
	t.Parallel()

	cfg := config.Default()
	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	cfg.Paths.StateFile = statePath
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret"
		data.Cloud.HeartbeatEndpoint = "/api/receiver/heartbeat"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	mockCloud := &mockCloudClient{
		ackHomeAutoCfg: &cloudclient.HomeAutoSessionManagedConfig{
			Version: "has-v3",
			Enabled: boolPtr(false),
			Mode:    "off",
			Home:    cloudclient.HomeAutoSessionManagedGeofence{Lat: 0, Lon: 0, RadiusM: 100},
		},
	}
	module := homeautosession.New(cfg.HomeAutoSession, store, statusModel, slog.Default(), mockCloud)
	svc := &Service{
		container: &Container{
			Config:          cfg,
			Logger:          slog.Default(),
			State:           store,
			Status:          statusModel,
			Cloud:           mockCloud,
			HomeAutoSession: module,
		},
		mode: config.ModeService,
	}

	err = svc.sendHeartbeat(context.Background(), store.Snapshot(), meshtastic.Snapshot{State: meshtastic.StateConnected})
	if err != nil {
		t.Fatalf("sendHeartbeat returned error: %v", err)
	}

	snap := store.Snapshot()
	if snap.HomeAutoSession.LastConfigApplyResult != homeAutoConfigApplyCloudDisabled {
		t.Fatalf("expected config apply result %q, got %q", homeAutoConfigApplyCloudDisabled, snap.HomeAutoSession.LastConfigApplyResult)
	}
	if snap.HomeAutoSession.DesiredConfigEnabled == nil || *snap.HomeAutoSession.DesiredConfigEnabled {
		t.Fatal("expected desired config enabled=false after cloud disable")
	}
}

func TestLifecycleChangeFromCloudError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		err    error
		want   pairing.LifecycleChange
		wantOK bool
	}{
		{
			name:   "revoked by status",
			err:    &cloudclient.APIError{StatusCode: 401, Message: "unauthorized", Retryable: false},
			want:   pairing.LifecycleCredentialRevoked,
			wantOK: true,
		},
		{
			name:   "disabled by message",
			err:    &cloudclient.APIError{StatusCode: 403, Message: "receiver disabled", Retryable: false},
			want:   pairing.LifecycleReceiverDisabled,
			wantOK: true,
		},
		{
			name:   "replaced by message",
			err:    &cloudclient.APIError{StatusCode: 409, Message: "receiver superseded by replacement", Retryable: false},
			want:   pairing.LifecycleReceiverReplaced,
			wantOK: true,
		},
		{
			name:   "retryable transport error",
			err:    errors.New("dial tcp timeout"),
			want:   "",
			wantOK: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := lifecycleChangeFromCloudError(tc.err)
			if ok != tc.wantOK {
				t.Fatalf("expected ok=%v got=%v", tc.wantOK, ok)
			}
			if got != tc.want {
				t.Fatalf("expected change %q got %q", tc.want, got)
			}
		})
	}
}

func TestPairingCloudStatusLifecycleState(t *testing.T) {
	t.Parallel()

	if got := pairingCloudStatus(state.PairingState{
		Phase:      state.PairingUnpaired,
		LastChange: string(pairing.LifecycleReceiverDisabled),
	}); got != "receiver_disabled" {
		t.Fatalf("expected receiver_disabled, got %q", got)
	}
	if got := pairingCloudStatus(state.PairingState{
		Phase:      state.PairingUnpaired,
		LastChange: string(pairing.LifecycleCredentialRevoked),
	}); got != "credential_revoked" {
		t.Fatalf("expected credential_revoked, got %q", got)
	}
}

func TestSendHeartbeatLifecycleTransitionRevoked(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	err = store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.IngestAPIKey = "secret-1"
		data.Cloud.IngestAPIKeyID = "key-1"
		data.Cloud.CredentialRef = "receiver-1"
		data.Cloud.HeartbeatEndpoint = "/api/receiver/heartbeat"
	})
	if err != nil {
		t.Fatalf("seed state: %v", err)
	}

	statusModel := status.New()
	pairingManager := pairing.NewManager(store, statusModel, nil, nil, pairing.ActivationIdentity{})
	mockCloud := &mockCloudClient{
		heartbeatErr: &cloudclient.APIError{StatusCode: 401, Message: "credential revoked", Retryable: false},
	}
	svc := &Service{
		container: &Container{
			Config:  config.Default(),
			Logger:  slog.Default(),
			State:   store,
			Status:  statusModel,
			Cloud:   mockCloud,
			Pairing: pairingManager,
		},
		mode: config.ModeService,
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{payload: map[string]any{"fromId": "node-1"}},
			},
		},
	}

	err = svc.sendHeartbeat(context.Background(), store.Snapshot(), meshtastic.Snapshot{State: meshtastic.StateConnected})
	if !errors.Is(err, errLifecycleTransition) {
		t.Fatalf("expected lifecycle transition error, got %v", err)
	}

	snap := store.Snapshot()
	if snap.Pairing.Phase != state.PairingUnpaired {
		t.Fatalf("expected phase %q, got %q", state.PairingUnpaired, snap.Pairing.Phase)
	}
	if snap.Pairing.LastChange != string(pairing.LifecycleCredentialRevoked) {
		t.Fatalf("expected last_change %q, got %q", pairing.LifecycleCredentialRevoked, snap.Pairing.LastChange)
	}
	if snap.Cloud.IngestAPIKey != "" || snap.Cloud.IngestAPIKeyID != "" || snap.Cloud.CredentialRef != "" {
		t.Fatalf("expected durable credentials cleared after lifecycle transition")
	}
	if len(svc.steady.ingestQueue) != 0 {
		t.Fatalf("expected ingest queue to be cleared after lifecycle transition")
	}
}

func TestProcessSteadyStateContinuesHeartbeatOnUnsupportedCloudConfig(t *testing.T) {
	t.Parallel()

	statusModel := status.New()
	mockCloud := &mockCloudClient{}
	svc := &Service{
		container: &Container{
			Config: config.Default(),
			Logger: slog.Default(),
			Status: statusModel,
			Cloud:  mockCloud,
		},
		mode: config.ModeService,
	}

	svc.processSteadyState(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			EndpointURL:       "https://api.example.com",
			ConfigVersion:     "v2.0",
			HeartbeatEndpoint: "/api/receiver/heartbeat",
			IngestAPIKey:      "secret",
		},
	}, meshtastic.Snapshot{State: meshtastic.StateConnected})

	snap := statusModel.Snapshot()
	if snap.LastError != "cloud config version unsupported" {
		t.Fatalf("expected cloud config compatibility error, got %q", snap.LastError)
	}
	component, ok := snap.Components["cloud_config"]
	if !ok {
		t.Fatalf("expected cloud_config component status")
	}
	if component.State != "unsupported" {
		t.Fatalf("expected cloud_config state unsupported, got %q", component.State)
	}
	if mockCloud.heartbeatCalls != 1 {
		t.Fatalf("expected heartbeat to continue despite unsupported config version, got %d calls", mockCloud.heartbeatCalls)
	}
}

func TestProcessIngestDispatchContinuesOnUnsupportedCloudConfig(t *testing.T) {
	t.Parallel()

	statePath := filepath.Join(t.TempDir(), "receiver-state.json")
	store, err := state.Open(statePath)
	if err != nil {
		t.Fatalf("open state: %v", err)
	}
	if err := store.Update(func(data *state.Data) {
		data.Pairing.Phase = state.PairingSteadyState
		data.Cloud.EndpointURL = "https://api.example.com"
		data.Cloud.ConfigVersion = "v2.0"
		data.Cloud.IngestEndpoint = "/api/meshtastic/event"
		data.Cloud.IngestAPIKey = "secret"
	}); err != nil {
		t.Fatalf("seed state: %v", err)
	}

	mockCloud := &mockCloudClient{}
	statusModel := status.New()
	now := time.Now().UTC()
	svc := &Service{
		container: &Container{
			Logger: slog.Default(),
			Status: statusModel,
			State:  store,
			Cloud:  mockCloud,
		},
		steady: steadyState{
			ingestQueue: []queuedIngestEvent{
				{
					payload:        map[string]any{"fromId": "node-compat"},
					idempotencyKey: "evt-compat",
					capturedAt:     now.Add(-250 * time.Millisecond),
					enqueuedAt:     now,
					nextAttemptAt:  now.Add(-10 * time.Millisecond),
				},
			},
		},
	}

	svc.processIngestDispatch(context.Background(), "compat-test")
	if mockCloud.postCalls != 1 {
		t.Fatalf("expected ingest dispatch to run despite unsupported config version, got %d calls", mockCloud.postCalls)
	}
	if len(svc.steady.ingestQueue) != 0 {
		t.Fatalf("expected queue drained, got %d remaining", len(svc.steady.ingestQueue))
	}
}
