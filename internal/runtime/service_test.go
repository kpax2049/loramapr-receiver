package runtime

import (
	"context"
	"errors"
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

	payload, key := shapeIngestPayload(meshtastic.Packet{
		SourceNodeID:      "!node-1",
		DestinationNodeID: "!gateway",
		PortNum:           2,
		Payload:           []byte("hello"),
		ReceivedAt:        time.Date(2026, 3, 10, 23, 0, 0, 0, time.UTC),
	})
	if !strings.HasPrefix(key, "rx-") {
		t.Fatalf("unexpected idempotency key: %q", key)
	}
	if payload["fromId"] != "!node-1" {
		t.Fatalf("unexpected payload source: %#v", payload)
	}
	if payload["packetId"] != key {
		t.Fatalf("expected payload packetId to match idempotency key")
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
	if got := mockCloud.lastHeartbeat.Status["localName"]; got != "garage-pi-abc123" {
		t.Fatalf("expected localName in heartbeat payload, got %#v", got)
	}
	if got := mockCloud.lastHeartbeat.Status["receiverLabel"]; got != "Garage Receiver" {
		t.Fatalf("expected receiverLabel in heartbeat payload, got %#v", got)
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

func TestProcessSteadyStateBlocksOnUnsupportedCloudConfig(t *testing.T) {
	t.Parallel()

	statusModel := status.New()
	svc := &Service{
		container: &Container{
			Config: config.Default(),
			Logger: slog.Default(),
			Status: statusModel,
		},
	}

	svc.processSteadyState(context.Background(), state.Data{
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			EndpointURL:   "https://api.example.com",
			ConfigVersion: "v2.0",
		},
	}, meshtastic.Snapshot{})

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
}
