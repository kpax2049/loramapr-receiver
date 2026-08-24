package meshcore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
	"github.com/loramapr/loramapr-receiver/internal/receiverevents"
)

func TestM5SourceDerivedRawAdvertPreparedEnvelope(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	payload := readNormalizeHexFixture(t, "signed-log-rx-advert-v1.hex")
	event, err := NormalizeAdapterEvent(protocoladapter.Event{
		Adapter: AdapterName, ObservedAt: observedAt, Value: PushFrame{Opcode: payload[0], Payload: payload},
	}, testBinding(), pinnedReadySnapshot())
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := receiverevents.PrepareWithDeliveryID(event, testDeliveryID)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		DeliveryID     string `json:"deliveryId"`
		EnvelopeSHA256 string `json:"envelopeSha256"`
		Envelope       string `json:"envelope"`
	}
	contents, err := os.ReadFile(filepath.Join("..", "contracts", "protocolevents", "v1", "m5-source-derived-raw-advert.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(contents, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.DeliveryID != prepared.DeliveryID || fixture.EnvelopeSHA256 != prepared.EnvelopeSHA256 || fixture.Envelope != string(prepared.Envelope) {
		t.Fatalf("receiver-generated M5 envelope drifted: fixture=%#v prepared=%#v", fixture, prepared)
	}
}
