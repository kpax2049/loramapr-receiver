package receiverevents

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"
)

func TestNewUUIDv7EncodesTimeVersionAndVariant(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 34, 56, 789_000_000, time.UTC)
	id, err := NewUUIDv7(now, bytes.NewReader(bytes.Repeat([]byte{0xff}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if id != "01a01f2a-d895-7fff-bfff-ffffffffffff" {
		t.Fatalf("UUIDv7 = %q", id)
	}
}

func TestPrepareCanonicalizesValidatesAndDoesNotMutateInput(t *testing.T) {
	id := "0198c7a2-e395-7000-8000-000000000001"
	event := validEvent()
	prepared, err := PrepareWithDeliveryID(event, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := event["deliveryId"]; exists {
		t.Fatal("Prepare mutated caller-owned event")
	}
	if prepared.DeliveryID != id {
		t.Fatalf("delivery id = %q", prepared.DeliveryID)
	}
	var decoded map[string]any
	if err := json.Unmarshal(prepared.Envelope, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["deliveryId"] != id {
		t.Fatalf("canonical envelope delivery id = %#v", decoded["deliveryId"])
	}
	if bytes.Contains(prepared.Envelope, []byte(" ")) || prepared.Envelope[0] != '{' {
		t.Fatalf("envelope is not compact canonical JSON: %s", prepared.Envelope)
	}
	if !bytes.HasPrefix(prepared.Envelope, []byte(`{"capabilities"`)) {
		t.Fatalf("JCS key order not applied: %s", prepared.Envelope)
	}
	digest := sha256.Sum256(prepared.Envelope)
	if prepared.EnvelopeSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("envelope hash = %q", prepared.EnvelopeSHA256)
	}

	event["eventType"] = "changed_after_prepare"
	if bytes.Contains(prepared.Envelope, []byte("changed_after_prepare")) {
		t.Fatal("prepared bytes changed with caller input")
	}
}

func TestPrepareRejectsInvalidEnvelopeAndDeliveryMismatch(t *testing.T) {
	id := "0198c7a2-e395-7000-8000-000000000002"
	invalid := validEvent()
	delete(invalid, "source")
	if _, err := PrepareWithDeliveryID(invalid, id); err == nil {
		t.Fatal("invalid contract envelope was accepted")
	}

	mismatch := validEvent()
	mismatch["deliveryId"] = "0198c7a2-e395-7000-8000-000000000003"
	if _, err := PrepareWithDeliveryID(mismatch, id); err == nil {
		t.Fatal("delivery ID mismatch was accepted")
	}
	if _, err := PrepareWithDeliveryID(validEvent(), "not-v7"); err == nil {
		t.Fatal("invalid UUIDv7 was accepted")
	}
}

func validEvent() map[string]any {
	return map[string]any{
		"contractVersion": "1.0",
		"eventType":       "packet_observed",
		"protocol":        "meshcore",
		"receiver": map[string]any{
			"receiverAgentId": "11111111-1111-4111-8111-111111111111",
			"installationId":  "0123456789abcdef0123456789abcdef",
			"adapter":         "meshcore-companion",
			"adapterVersion":  "1.0.0",
			"observedAt":      "2026-08-20T12:34:56.789Z",
		},
		"capabilities": map[string]any{
			"receiver_rssi": "available",
		},
		"source": map[string]any{
			"protocolVersion":   "13",
			"nativeMessageType": "LOG_RX",
			"rawEncoding":       "base64",
			"raw":               "iMQY",
			"rawSha256":         "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
	}
}
