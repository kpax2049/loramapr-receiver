package runtime

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
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

type mockCloudClient struct {
	postErr      error
	postCalls    int
	lastPayload  map[string]any
	lastEventKey string

	heartbeatErr   error
	heartbeatCalls int
	lastHeartbeat  cloudclient.ReceiverHeartbeat
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
		ReceiverAgentID: "agent-1",
		OwnerID:         "owner-1",
		LastHeartbeatAt: time.Now().UTC(),
		NodeCount:       len(heartbeat.ObservedNodeIDs),
	}, nil
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
		Pairing: state.PairingState{Phase: state.PairingSteadyState},
		Cloud: state.CloudState{
			HeartbeatEndpoint: "/api/receiver/heartbeat",
			IngestAPIKey:      "secret",
		},
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
}
