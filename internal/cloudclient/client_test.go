package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestExchangePairingCode(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/receiver/bootstrap/exchange" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if req.Method != http.MethodPost {
				t.Fatalf("unexpected method: %s", req.Method)
			}

			var payload struct {
				PairingCode string `json:"pairingCode"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload.PairingCode != "LMR-ABC12345" {
				t.Fatalf("unexpected pairing code: %q", payload.PairingCode)
			}

			body := `{
				"installSessionId":"session-1",
				"flowKey":"meshtastic_first_run",
				"activationToken":"rx_act_token",
				"activationExpiresAt":"2026-03-10T18:00:00Z",
				"configVersion":"v1.2",
				"receiverLabel":"Home Receiver",
				"siteLabel":"Home",
				"groupLabel":"Outdoor",
				"activateEndpoint":"/api/receiver/activate",
				"heartbeatEndpoint":"/api/receiver/heartbeat",
				"ingestEndpoint":"/api/meshtastic/event"
			}`
			return jsonResponse(http.StatusCreated, body), nil
		})},
	}

	response, err := client.ExchangePairingCode(context.Background(), "LMR-ABC12345")
	if err != nil {
		t.Fatalf("ExchangePairingCode returned error: %v", err)
	}

	if response.InstallSessionID != "session-1" {
		t.Fatalf("unexpected install session: %q", response.InstallSessionID)
	}
	if response.ActivationToken != "rx_act_token" {
		t.Fatalf("unexpected activation token")
	}
	if response.ActivationExpires.IsZero() {
		t.Fatal("expected activation expiry to be parsed")
	}
	if response.ConfigVersion != "v1.2" {
		t.Fatalf("unexpected config version %q", response.ConfigVersion)
	}
	if response.ReceiverLabel != "Home Receiver" {
		t.Fatalf("unexpected receiver label %q", response.ReceiverLabel)
	}
	if response.SiteLabel != "Home" || response.GroupLabel != "Outdoor" {
		t.Fatalf("unexpected site/group labels: %q/%q", response.SiteLabel, response.GroupLabel)
	}
}

func TestActivateReceiver(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/runtime/activate" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			var payload struct {
				ActivationToken string `json:"activationToken"`
				RuntimeVersion  string `json:"runtimeVersion"`
				Platform        string `json:"platform"`
				Arch            string `json:"arch"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload.ActivationToken != "rx_act_123" {
				t.Fatalf("unexpected activation token")
			}

			body := `{
				"receiverAgentId":"agent-1",
				"ownerId":"owner-1",
				"receiverLabel":"Garage Receiver",
				"siteLabel":"Home",
				"groupLabel":"Indoor",
				"ingestApiKeyId":"api-key-id",
				"ingestApiKeySecret":"api-secret",
				"configVersion":"v1.2",
				"ingestEndpoint":"/api/meshtastic/event",
				"heartbeatEndpoint":"/api/receiver/heartbeat",
				"activatedAt":"2026-03-10T18:10:00Z"
			}`
			return jsonResponse(http.StatusCreated, body), nil
		})},
	}

	response, err := client.ActivateReceiver(context.Background(), "/runtime/activate", ActivationRequest{
		ActivationToken: "rx_act_123",
		RuntimeVersion:  "1.0.0",
		Platform:        "linux",
		Arch:            "amd64",
	})
	if err != nil {
		t.Fatalf("ActivateReceiver returned error: %v", err)
	}

	if response.ReceiverAgentID != "agent-1" {
		t.Fatalf("unexpected receiver id: %q", response.ReceiverAgentID)
	}
	if response.IngestAPIKey != "api-secret" {
		t.Fatalf("unexpected ingest API key")
	}
	if response.ActivatedAt.IsZero() {
		t.Fatal("expected activated_at to be parsed")
	}
	if response.ConfigVersion != "v1.2" {
		t.Fatalf("unexpected config version %q", response.ConfigVersion)
	}
	if response.ReceiverLabel != "Garage Receiver" {
		t.Fatalf("unexpected receiver label: %q", response.ReceiverLabel)
	}
}

func TestExchangePairingCodeRetryableError(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusServiceUnavailable, `{"message":"temporary outage"}`), nil
		})},
	}

	_, err := client.ExchangePairingCode(context.Background(), "LMR-ERR00001")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsRetryable(err) {
		t.Fatal("expected error to be retryable")
	}
}

func TestPostIngestEventSendsHeaders(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/meshtastic/event" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if req.Header.Get("x-api-key") != "ingest-secret" {
				t.Fatalf("missing x-api-key header")
			}
			if req.Header.Get("x-idempotency-key") != "event-1" {
				t.Fatalf("missing x-idempotency-key header")
			}
			return jsonResponse(http.StatusOK, `{"status":"ok"}`), nil
		})},
	}

	err := client.PostIngestEvent(context.Background(), "/api/meshtastic/event", "ingest-secret", map[string]any{
		"fromId": "node-1",
	}, "event-1")
	if err != nil {
		t.Fatalf("PostIngestEvent returned error: %v", err)
	}
}

func TestSendReceiverHeartbeat(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/receiver/heartbeat" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if req.Header.Get("x-api-key") != "ingest-secret" {
				t.Fatalf("missing x-api-key header")
			}

			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload["runtimeVersion"] != "1.0.0" {
				t.Fatalf("unexpected runtime version payload: %#v", payload)
			}
			return jsonResponse(http.StatusCreated, `{
				"receiverAgentId":"agent-1",
				"ownerId":"owner-1",
				"receiverLabel":"Garage Receiver",
				"siteLabel":"Home",
				"groupLabel":"Outdoor",
				"configVersion":"v1.3",
				"lastHeartbeatAt":"2026-03-10T22:00:00Z",
				"nodeCount":2,
				"homeAutoSessionConfig":{
					"version":"has-v2",
					"enabled":true,
					"mode":"control",
					"home":{"lat":37.3349,"lon":-122.0090,"radiusM":150},
					"trackedNodeIds":["!node-1"],
					"startDebounce":"30s",
					"stopDebounce":"30s",
					"idleStopTimeout":"15m"
				}
			}`), nil
		})},
	}

	ack, err := client.SendReceiverHeartbeat(context.Background(), "/api/receiver/heartbeat", "ingest-secret", ReceiverHeartbeat{
		RuntimeVersion:  "1.0.0",
		Platform:        "linux",
		Arch:            "arm64",
		LocalNodeID:     "!home",
		ObservedNodeIDs: []string{"!node-1", "!node-2"},
		Status:          map[string]any{"queueDepth": 1},
	})
	if err != nil {
		t.Fatalf("SendReceiverHeartbeat returned error: %v", err)
	}
	if ack.ReceiverAgentID != "agent-1" {
		t.Fatalf("unexpected agent id: %q", ack.ReceiverAgentID)
	}
	if ack.NodeCount != 2 {
		t.Fatalf("unexpected node count: %d", ack.NodeCount)
	}
	if ack.ConfigVersion != "v1.3" {
		t.Fatalf("unexpected config version: %q", ack.ConfigVersion)
	}
	if ack.ReceiverLabel != "Garage Receiver" || ack.SiteLabel != "Home" || ack.GroupLabel != "Outdoor" {
		t.Fatalf("unexpected receiver/site/group labels: %q/%q/%q", ack.ReceiverLabel, ack.SiteLabel, ack.GroupLabel)
	}
	if ack.HomeAutoSessionConfig == nil {
		t.Fatal("expected heartbeat ack homeAutoSessionConfig")
	}
	if ack.HomeAutoSessionConfig.Version != "has-v2" {
		t.Fatalf("unexpected heartbeat ack home auto config version: %q", ack.HomeAutoSessionConfig.Version)
	}
	if ack.HomeAutoSessionConfig.Mode != "control" {
		t.Fatalf("unexpected heartbeat ack home auto mode: %q", ack.HomeAutoSessionConfig.Mode)
	}
}

func TestIsRetryableCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if IsRetryable(ctx.Err()) {
		t.Fatal("context canceled should not be retryable")
	}
}

func jsonResponse(statusCode int, payload string) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewBufferString(strings.TrimSpace(payload))),
	}
}

func TestNewHTTPClientDefaultTimeout(t *testing.T) {
	t.Parallel()

	client := NewHTTPClient("https://api.example.com", 0)
	if client.client.Timeout != 10*time.Second {
		t.Fatalf("expected default timeout 10s, got %s", client.client.Timeout)
	}
}

func TestStartHomeAutoSession(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/receiver/home-auto-session/start" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if req.Header.Get("x-api-key") != "ingest-secret" {
				t.Fatalf("missing x-api-key header")
			}
			if req.Header.Get("x-idempotency-key") != "start-1" {
				t.Fatalf("missing x-idempotency-key header")
			}
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload["triggerNodeId"] != "!nodeA" {
				t.Fatalf("unexpected trigger node payload: %#v", payload)
			}
			return jsonResponse(http.StatusCreated, `{"sessionId":"session-1","startedAt":"2026-03-12T10:00:00Z"}`), nil
		})},
	}

	result, err := client.StartHomeAutoSession(context.Background(), "/api/receiver/home-auto-session/start", "ingest-secret", HomeAutoSessionStartRequest{
		TriggerNodeID: "!nodeA",
		DedupeKey:     "start-1",
		Reason:        "tracked node exited geofence",
	})
	if err != nil {
		t.Fatalf("StartHomeAutoSession returned error: %v", err)
	}
	if result.SessionID != "session-1" {
		t.Fatalf("unexpected session ID: %q", result.SessionID)
	}
	if result.StartedAt.IsZero() {
		t.Fatal("expected started_at to be parsed")
	}
}

func TestStopHomeAutoSession(t *testing.T) {
	t.Parallel()

	client := &HTTPClient{
		baseURL: "https://api.example.com",
		client: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Path != "/api/receiver/home-auto-session/stop" {
				t.Fatalf("unexpected path: %s", req.URL.Path)
			}
			if req.Header.Get("x-api-key") != "ingest-secret" {
				t.Fatalf("missing x-api-key header")
			}
			if req.Header.Get("x-idempotency-key") != "stop-1" {
				t.Fatalf("missing x-idempotency-key header")
			}
			return jsonResponse(http.StatusOK, `{"sessionId":"session-1","stoppedAt":"2026-03-12T10:15:00Z","status":"stopped"}`), nil
		})},
	}

	result, err := client.StopHomeAutoSession(context.Background(), "/api/receiver/home-auto-session/stop", "ingest-secret", HomeAutoSessionStopRequest{
		SessionID: "session-1",
		DedupeKey: "stop-1",
		Reason:    "returned home",
	})
	if err != nil {
		t.Fatalf("StopHomeAutoSession returned error: %v", err)
	}
	if result.SessionID != "session-1" {
		t.Fatalf("unexpected session ID: %q", result.SessionID)
	}
	if result.Status != "stopped" {
		t.Fatalf("unexpected status: %q", result.Status)
	}
	if result.StoppedAt.IsZero() {
		t.Fatal("expected stopped_at to be parsed")
	}
}
