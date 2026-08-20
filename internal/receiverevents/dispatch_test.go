package receiverevents

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/cloudclient"
	"github.com/loramapr/loramapr-receiver/internal/outbox"
)

type fakeDeliveryClient struct {
	result cloudclient.NormalizedDeliveryResult
	err    error
	seen   []byte
}

func (f *fakeDeliveryClient) PostNormalizedEvent(
	_ context.Context,
	_ string,
	_ string,
	envelope []byte,
	_ string,
	_ string,
) (cloudclient.NormalizedDeliveryResult, error) {
	f.seen = append([]byte(nil), envelope...)
	return f.result, f.err
}

func TestDispatcherAcknowledgesNewAndDuplicateDeliveries(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		duplicate := duplicate
		t.Run(map[bool]string{false: "new", true: "duplicate"}[duplicate], func(t *testing.T) {
			now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
			engine, store := testEngine(t, now)
			defer closeTestEngine(t, engine, store)
			delivery := testDelivery("0198c7a2-e395-7000-8000-000000000010", "agent-1", []byte(`{"exact":true}`))
			if err := store.Enqueue(delivery); err != nil {
				t.Fatal(err)
			}
			client := &fakeDeliveryClient{result: cloudclient.NormalizedDeliveryResult{
				StatusCode: 202, Duplicate: duplicate, DeliveryID: delivery.DeliveryID,
			}}
			result, err := (Dispatcher{Outbox: engine, Client: client}).DispatchOnce(context.Background(), "secret", testBinding(), now)
			if err != nil {
				t.Fatal(err)
			}
			if !result.Acknowledged || result.Duplicate != duplicate || !bytes.Equal(client.seen, delivery.Envelope) {
				t.Fatalf("unexpected dispatch result %#v bytes=%s", result, client.seen)
			}
			if _, err := engine.Get(delivery.DeliveryID); !errors.Is(err, outbox.ErrDeliveryNotFound) {
				t.Fatalf("acknowledged delivery remains: %v", err)
			}
		})
	}
}

func TestDispatcherRetriesWithoutMutatingEnvelope(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	engine, store := testEngine(t, now)
	defer closeTestEngine(t, engine, store)
	delivery := testDelivery("0198c7a2-e395-7000-8000-000000000011", "agent-1", []byte(`{"exact":true}`))
	if err := store.Enqueue(delivery); err != nil {
		t.Fatal(err)
	}
	client := &fakeDeliveryClient{err: &cloudclient.APIError{
		StatusCode: 503, Code: "RECEIVER_EVENTS_V1_DISABLED", Retryable: true, RetryAfter: 45 * time.Second,
	}}
	result, err := (Dispatcher{Outbox: engine, Client: client}).DispatchOnce(context.Background(), "secret", testBinding(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition.Action != ActionRetry {
		t.Fatalf("disposition = %#v", result.Disposition)
	}
	got, err := engine.Get(delivery.DeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != outbox.StatePending || got.Attempts != 1 || !got.NextAttemptAt.Equal(now.Add(45*time.Second)) {
		t.Fatalf("retry state = %#v", got)
	}
	if !bytes.Equal(got.Envelope, delivery.Envelope) {
		t.Fatal("retry mutated exact envelope bytes")
	}
}

func TestDispatcherQuarantinesConflictsAndTerminalAgentDeliveries(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	t.Run("one conflict", func(t *testing.T) {
		engine, store := testEngine(t, now)
		defer closeTestEngine(t, engine, store)
		delivery := testDelivery("0198c7a2-e395-7000-8000-000000000012", "agent-1", []byte(`{}`))
		if err := store.Enqueue(delivery); err != nil {
			t.Fatal(err)
		}
		client := &fakeDeliveryClient{err: &cloudclient.APIError{StatusCode: 409, Code: "DELIVERY_ID_PAYLOAD_MISMATCH"}}
		result, err := (Dispatcher{Outbox: engine, Client: client}).DispatchOnce(context.Background(), "secret", testBinding(), now)
		if err != nil || result.Quarantined != 1 {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		got, err := engine.Get(delivery.DeliveryID)
		if err != nil || got.State != outbox.StateQuarantined {
			t.Fatalf("quarantined delivery=%#v err=%v", got, err)
		}
	})

	t.Run("all terminal agent deliveries", func(t *testing.T) {
		engine, store := testEngine(t, now)
		defer closeTestEngine(t, engine, store)
		for _, delivery := range []outbox.Delivery{
			testDelivery("0198c7a2-e395-7000-8000-000000000013", "agent-old", []byte(`{"n":1}`)),
			testDelivery("0198c7a2-e395-7000-8000-000000000014", "agent-old", []byte(`{"n":2}`)),
		} {
			if err := store.Enqueue(delivery); err != nil {
				t.Fatal(err)
			}
		}
		client := &fakeDeliveryClient{err: &cloudclient.APIError{StatusCode: 403, Code: "RECEIVER_REVOKED"}}
		binding := testBinding()
		binding.ReceiverAgentID = "agent-old"
		result, err := (Dispatcher{Outbox: engine, Client: client}).DispatchOnce(context.Background(), "secret", binding, now)
		if err != nil {
			t.Fatal(err)
		}
		if result.Quarantined != 2 || !result.Disposition.ClearBinding || !result.Disposition.PairingRequired {
			t.Fatalf("terminal result = %#v", result)
		}
	})
}

func TestDispatcherQuarantinesStaleBindingBeforeNetworkDelivery(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	engine, store := testEngine(t, now)
	defer closeTestEngine(t, engine, store)
	for _, delivery := range []outbox.Delivery{
		testDelivery("0198c7a2-e395-7000-8000-000000000020", "agent-old", []byte(`{"oldAgent":true}`)),
		testDelivery("0198c7a2-e395-7000-8000-000000000021", "agent-1", []byte(`{"current":true}`)),
		func() outbox.Delivery {
			value := testDelivery("0198c7a2-e395-7000-8000-000000000022", "agent-1", []byte(`{"oldInstallation":true}`))
			value.InstallationID = "ffffffffffffffffffffffffffffffff"
			return value
		}(),
	} {
		if err := store.Enqueue(delivery); err != nil {
			t.Fatal(err)
		}
	}
	client := &fakeDeliveryClient{result: cloudclient.NormalizedDeliveryResult{
		StatusCode: 202, DeliveryID: "0198c7a2-e395-7000-8000-000000000021",
	}}
	result, err := (Dispatcher{Outbox: engine, Client: client}).DispatchOnce(context.Background(), "rotated-key", testBinding(), now)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Acknowledged || result.Reconciled.CredentialRebindQuarantined != 1 || result.Reconciled.InstallationResetQuarantined != 1 {
		t.Fatalf("unexpected binding reconciliation result: %#v", result)
	}
	for id, reason := range map[string]string{
		"0198c7a2-e395-7000-8000-000000000020": "credential_rebind_required",
		"0198c7a2-e395-7000-8000-000000000022": "installation_reset",
	} {
		record, err := engine.Get(id)
		if err != nil || record.State != outbox.StateQuarantined || record.QuarantineReason != reason {
			t.Fatalf("delivery %s = %#v err=%v, want quarantine %q", id, record, err, reason)
		}
	}
}

func TestClassifyPauseAndCredentialActions(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want Disposition
	}{
		{name: "network", err: errors.New("connection reset"), want: Disposition{Action: ActionRetry}},
		{name: "binding", err: &cloudclient.APIError{StatusCode: 403, Code: "RECEIVER_BINDING_MISMATCH"}, want: Disposition{Action: ActionPause, Quarantine: true}},
		{name: "scope", err: &cloudclient.APIError{StatusCode: 403, Code: "MISSING_SCOPE"}, want: Disposition{Action: ActionPause}},
		{name: "key", err: &cloudclient.APIError{StatusCode: 401}, want: Disposition{Action: ActionRequirePairing, PairingRequired: true}},
		{name: "invalid", err: &cloudclient.APIError{StatusCode: 422, Code: "INVALID_EVENT"}, want: Disposition{Action: ActionQuarantine, Quarantine: true}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Classify(test.err)
			if got.Action != test.want.Action || got.Quarantine != test.want.Quarantine || got.PairingRequired != test.want.PairingRequired {
				t.Fatalf("Classify() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func testEngine(t *testing.T, now time.Time) (*outbox.Engine, *outbox.Store) {
	t.Helper()
	store, err := outbox.Open(outbox.Config{Path: filepath.Join(t.TempDir(), "outbox.db"), Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := outbox.NewEngine(store, outbox.EngineConfig{})
	if err != nil {
		store.Close()
		t.Fatal(err)
	}
	return engine, store
}

func closeTestEngine(t *testing.T, engine *outbox.Engine, store *outbox.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := engine.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func testDelivery(id, agent string, body []byte) outbox.Delivery {
	return outbox.Delivery{
		DeliveryID:              id,
		Envelope:                append([]byte(nil), body...),
		EnvelopeSHA256:          "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IdempotencyKey:          id,
		OwnerID:                 "owner-1",
		ReceiverAgentIDSnapshot: agent,
		InstallationID:          "0123456789abcdef0123456789abcdef",
		Endpoint:                EndpointPath,
	}
}

func testBinding() outbox.Binding {
	return outbox.Binding{
		OwnerID: "owner-1", ReceiverAgentID: "agent-1", InstallationID: "0123456789abcdef0123456789abcdef",
	}
}
