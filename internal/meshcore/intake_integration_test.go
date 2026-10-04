package meshcore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/outbox"
	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
	"github.com/loramapr/loramapr-receiver/internal/receiverevents"
)

type normalizedHTTPRequest struct {
	body           []byte
	idempotencyKey string
	envelopeSHA256 string
	apiKey         string
	contentType    string
}

func TestPhysicalSerialToDurableCloudRetrySurvivesRestart(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	device := existingDeviceFixture(t)
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: device}, nil, protocoladapter.NewSerialLeaseRegistry())
	adapter.openFn = func(string) (io.ReadWriteCloser, error) {
		host, radio := net.Pipe()
		go serveCompanion(t, radio, readHexFixture(t, "signed-log-rx-advert-v1.hex"))
		return host, nil
	}
	manager, err := protocoladapter.NewManager(adapter)
	if err != nil {
		t.Fatal(err)
	}
	events, err := manager.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var adapterEvent protocoladapter.Event
	select {
	case adapterEvent = <-events:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for source-derived Companion frame")
	}
	if err := manager.Close(); err != nil {
		t.Fatal(err)
	}
	typed, ok := adapterEvent.Value.(AdapterEvent)
	if !ok || !typed.Session.Trust.Trusted {
		t.Fatalf("unexpected adapter event: %#v", adapterEvent)
	}
	adapterEvent.ObservedAt = observedAt

	binding := outbox.Binding{
		OwnerID: "owner-fixture", ReceiverAgentID: "018f8f5b-8c6d-7abc-8def-0123456789ab", InstallationID: "00112233445566778899aabbccddeeff",
	}
	normalized, err := NormalizeAdapterEvent(protocoladapter.Event{
		Adapter: AdapterName, Value: typed.Frame, ObservedAt: observedAt,
	}, ReceiverBinding{
		ReceiverAgentID: binding.ReceiverAgentID,
		InstallationID:  binding.InstallationID,
		AdapterVersion:  "integration-v1",
	}, typed.Session)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := receiverevents.Prepare(normalized, observedAt)
	if err != nil {
		t.Fatal(err)
	}

	var requestsMu sync.Mutex
	requests := make([]normalizedHTTPRequest, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Errorf("read normalized request: %v", readErr)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		requestsMu.Lock()
		requests = append(requests, normalizedHTTPRequest{
			body:           append([]byte(nil), body...),
			idempotencyKey: request.Header.Get("X-Idempotency-Key"),
			envelopeSHA256: request.Header.Get("X-LoRaMapr-Envelope-SHA256"),
			apiKey:         request.Header.Get("X-API-Key"),
			contentType:    request.Header.Get("Content-Type"),
		})
		attempt := len(requests)
		requestsMu.Unlock()

		writer.Header().Set("Content-Type", "application/json")
		if attempt == 1 {
			writer.Header().Set("Retry-After", "1")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"code": "RECEIVER_EVENTS_V1_DISABLED", "message": "normalized intake disabled",
			})
			return
		}
		writer.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"accepted": true, "deliveryId": prepared.DeliveryID,
		})
	}))
	defer server.Close()

	outboxPath := filepath.Join(t.TempDir(), "ingest-outbox.db")
	store, err := outbox.Open(outbox.Config{Path: outboxPath, Now: func() time.Time { return observedAt }})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := outbox.NewEngine(store, outbox.EngineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	delivery := outbox.Delivery{
		DeliveryID: prepared.DeliveryID, Envelope: prepared.Envelope, EnvelopeSHA256: prepared.EnvelopeSHA256,
		IdempotencyKey: prepared.DeliveryID, OwnerID: binding.OwnerID,
		ReceiverAgentIDSnapshot: binding.ReceiverAgentID, InstallationID: binding.InstallationID,
		Endpoint: receiverevents.EndpointPath, EnqueuedAt: observedAt,
	}
	if err := engine.TryStage(delivery); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-engine.Results():
		if result.Err != nil {
			t.Fatalf("persist staged delivery: %v", result.Err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for durable persistence")
	}
	client := cloudclient.NewHTTPClient(server.URL, time.Second)
	dispatcher := receiverevents.Dispatcher{Outbox: engine, Client: client}
	first, err := dispatcher.DispatchOnce(context.Background(), "fixture-key", binding, observedAt)
	if err != nil {
		t.Fatal(err)
	}
	if first.Disposition.Action != receiverevents.ActionRetry {
		t.Fatalf("first dispatch = %#v, want retry", first)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	if err := engine.Close(closeCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	restartAt := observedAt.Add(2 * time.Second)
	store, err = outbox.Open(outbox.Config{Path: outboxPath, Now: func() time.Time { return restartAt }})
	if err != nil {
		t.Fatal(err)
	}
	engine, err = outbox.NewEngine(store, outbox.EngineConfig{})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher.Outbox = engine
	second, err := dispatcher.DispatchOnce(context.Background(), "fixture-key", binding, restartAt)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Acknowledged || second.Duplicate {
		t.Fatalf("second dispatch = %#v, want new acknowledgement", second)
	}
	if _, err := engine.Get(prepared.DeliveryID); !errors.Is(err, outbox.ErrDeliveryNotFound) {
		t.Fatalf("acknowledged delivery remains after restart: %v", err)
	}
	closeCtx, cancel = context.WithTimeout(context.Background(), time.Second)
	if err := engine.Close(closeCtx); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	requestsMu.Lock()
	defer requestsMu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	for index, request := range requests {
		if !bytes.Equal(request.body, prepared.Envelope) || request.idempotencyKey != prepared.DeliveryID ||
			request.envelopeSHA256 != prepared.EnvelopeSHA256 || request.apiKey != "fixture-key" || request.contentType != "application/json" {
			t.Fatalf("request %d mutated immutable delivery: %#v body=%s", index+1, request, request.body)
		}
	}
	if !bytes.Equal(requests[0].body, requests[1].body) {
		t.Fatal("restart retry changed normalized envelope bytes")
	}
}
