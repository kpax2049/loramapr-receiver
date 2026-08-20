package meshcore

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
	"github.com/loramapr/loramapr-receiver/internal/receiverevents"
)

const (
	testReceiverAgentID = "018f8f5b-8c6d-7abc-8def-0123456789ab"
	testInstallationID  = "00112233445566778899aabbccddeeff"
	testDeliveryID      = "019166f0-7c00-7000-8000-000000000001"
)

func TestNormalizeSignedLogRXAdvertRetainsRawVerifiedEvidence(t *testing.T) {
	payload := readNormalizeHexFixture(t, "signed-log-rx-advert-v1.hex")
	if got := sha256Hex(payload); got != "5f761d58e5785b361f771fc259c3c94f1ec23b5926ae0d4d8e115c44b343b6af" {
		t.Fatalf("LOG_RX golden payload SHA-256 changed: %s", got)
	}
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)

	event := normalizeFixture(t, payload, observedAt, pinnedReadySnapshot())
	if got := stringField(t, event, "eventType"); got != "device_advertisement" {
		t.Fatalf("eventType = %q, want device_advertisement", got)
	}
	subject := mapField(t, event, "subject")
	if got := stringField(t, subject, "protocolNamespace"); got != "ed25519" {
		t.Fatalf("subject namespace = %q, want ed25519", got)
	}
	const expectedKey = "03a107bff3ce10be1d70dd18e74bc09967e4d6309ba50d5f1ddc8664125531b8"
	if got := stringField(t, subject, "canonicalId"); got != expectedKey {
		t.Fatalf("subject key = %q, want %q", got, expectedKey)
	}
	if got := stringField(t, subject, "displayId"); got != "Fixture Node" {
		t.Fatalf("subject displayId = %q, want Fixture Node", got)
	}

	position := mapField(t, event, "position")
	if position["lat"] != float64(49.395919) || position["lon"] != float64(11.351234) || position["verification"] != "verified" {
		t.Fatalf("unexpected verified position: %#v", position)
	}
	if got := stringField(t, event, "timeConfidence"); got != "authoritative_protocol" {
		t.Fatalf("timeConfidence = %q, want authoritative_protocol", got)
	}

	authenticity := mapField(t, event, "authenticity")
	if authenticity["method"] != "raw_ed25519" || authenticity["state"] != "verified" {
		t.Fatalf("unexpected authenticity: %#v", authenticity)
	}
	source := mapField(t, event, "source")
	if got := stringField(t, source, "raw"); got != base64.StdEncoding.EncodeToString(payload) {
		t.Fatal("source.raw does not retain the exact Companion control payload")
	}
	if got := stringField(t, source, "rawSha256"); got != sha256Hex(payload) {
		t.Fatalf("source.rawSha256 = %q, want exact control payload hash", got)
	}
	evidence := mapField(t, source, "evidence")
	onAir := payload[3:]
	if evidence["onAirAdvertBase64"] != base64.StdEncoding.EncodeToString(onAir) ||
		evidence["onAirAdvertSha256"] != sha256Hex(onAir) ||
		evidence["signatureAlgorithm"] != "ed25519" || evidence["verifier"] != "meshcore-advert-v1" {
		t.Fatalf("unexpected raw advert evidence: %#v", evidence)
	}

	radio := mapField(t, event, "radio")
	if radio["routeKind"] != "direct" {
		t.Fatalf("routeKind = %#v, want direct", radio["routeKind"])
	}
	path, ok := radio["path"].([]string)
	if !ok || len(path) != 2 || path[0] != "aa" || path[1] != "bb" {
		t.Fatalf("unexpected route path: %#v", radio["path"])
	}
	metrics, ok := radio["metrics"].([]any)
	if !ok || len(metrics) != 2 {
		t.Fatalf("unexpected radio metrics: %#v", radio["metrics"])
	}
	snr := metrics[0].(map[string]any)
	rssi := metrics[1].(map[string]any)
	if snr["value"] != float64(4.5) || snr["origin"] != "receiver_local" || rssi["value"] != -91 {
		t.Fatalf("unexpected local RF evidence: snr=%#v rssi=%#v", snr, rssi)
	}

	assertContractValid(t, event)
}

func TestNormalizeInvalidSignedAdvertMutationsFailClosedAndRetainRaw(t *testing.T) {
	original := readNormalizeHexFixture(t, "signed-log-rx-advert-v1.hex")
	publicKey, err := hex.DecodeString("03a107bff3ce10be1d70dd18e74bc09967e4d6309ba50d5f1ddc8664125531b8")
	if err != nil {
		t.Fatal(err)
	}
	keyIndex := bytes.Index(original, publicKey)
	if keyIndex < 0 {
		t.Fatal("fixture public key not found")
	}
	tests := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{name: "coordinate", mutate: func(value []byte) []byte { value[keyIndex+101] ^= 1; return value }},
		{name: "public key", mutate: func(value []byte) []byte { value[keyIndex] ^= 1; return value }},
		{name: "signature", mutate: func(value []byte) []byte { value[keyIndex+36] ^= 1; return value }},
		{name: "truncation", mutate: func(value []byte) []byte { return value[:len(value)-1] }},
		{name: "payload version", mutate: func(value []byte) []byte { value[3] ^= 0x40; return value }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := test.mutate(append([]byte(nil), original...))
			event := normalizeFixture(t, payload, time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC), pinnedReadySnapshot())
			if got := stringField(t, event, "eventType"); got != "packet_observed" {
				t.Fatalf("eventType = %q, want packet_observed", got)
			}
			if _, ok := event["subject"]; ok {
				t.Fatal("invalid signed advert must not gain a subject")
			}
			if _, ok := event["position"]; ok {
				t.Fatal("invalid signed advert must not gain a position")
			}
			if got := mapField(t, event, "authenticity")["state"]; got != "unverified" {
				t.Fatalf("authenticity state = %#v, want unverified", got)
			}
			source := mapField(t, event, "source")
			if source["raw"] != base64.StdEncoding.EncodeToString(payload) || source["rawSha256"] != sha256Hex(payload) {
				t.Fatal("invalid signed advert did not retain exact raw evidence")
			}
			assertContractValid(t, event)
		})
	}
}

func TestNormalizeDelegatedAdvertRequiresExactPinnedTrustProfile(t *testing.T) {
	payload := readNormalizeHexFixture(t, "new-advert-v1.17.1.hex")
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	trusted := pinnedReadySnapshot()

	event := normalizeFixture(t, payload, observedAt, trusted)
	if got := stringField(t, event, "eventType"); got != "device_advertisement" {
		t.Fatalf("eventType = %q, want device_advertisement", got)
	}
	if _, ok := event["radio"]; ok {
		t.Fatal("delegated NEW_ADVERT must not fabricate receiver-local RF")
	}
	assertRFNotObservable(t, event)
	subject := mapField(t, event, "subject")
	if got := stringField(t, subject, "protocolNamespace"); got != "ed25519" {
		t.Fatalf("subject namespace = %q, want ed25519", got)
	}
	if got := stringField(t, subject, "displayId"); got != "Source Fixture" {
		t.Fatalf("subject displayId = %q, want Source Fixture", got)
	}
	authenticity := mapField(t, event, "authenticity")
	if authenticity["method"] != "trusted_companion_validation" || authenticity["deviceAttested"] != false {
		t.Fatalf("unexpected delegated authenticity: %#v", authenticity)
	}
	source := mapField(t, event, "source")
	evidence := mapField(t, source, "evidence")
	if evidence["kind"] != "meshcore_companion_delegated_advert_v1" ||
		evidence["controlFrameSha256"] != source["rawSha256"] ||
		evidence["sourceCommit"] != PinnedSourceCommit ||
		evidence["deviceAttestedSourceCommit"] != false ||
		evidence["deviceProtocol"] != int(ProtocolVersion) ||
		evidence["appTargetProtocol"] != int(ProtocolVersion) {
		t.Fatalf("unexpected delegated evidence: %#v", evidence)
	}
	if _, ok := event["position"]; !ok {
		t.Fatal("pinned delegated advert with nonzero coordinates must retain position")
	}
	assertContractValid(t, event)

	profiles := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{name: "disconnected", mutate: func(value *Snapshot) { value.State = SessionDisconnected }},
		{name: "untrusted", mutate: func(value *Snapshot) { value.Trust.Trusted = false }},
		{name: "wrong protocol", mutate: func(value *Snapshot) { value.Trust.ProtocolVersion = 12 }},
		{name: "wrong firmware", mutate: func(value *Snapshot) { value.Trust.FirmwareVersion = "v1.17.0" }},
		{name: "wrong build", mutate: func(value *Snapshot) { value.Trust.FirmwareBuild = "13 Aug 2026" }},
		{name: "missing model", mutate: func(value *Snapshot) { value.Trust.Model = "" }},
		{name: "wrong source allowlist", mutate: func(value *Snapshot) { value.Trust.AllowlistCommit = strings.Repeat("0", 40) }},
		{name: "claimed device attestation", mutate: func(value *Snapshot) { value.Trust.DeviceAttested = true }},
	}
	for _, testCase := range profiles {
		t.Run(testCase.name, func(t *testing.T) {
			snapshot := trusted
			testCase.mutate(&snapshot)
			actual := normalizeFixture(t, payload, observedAt, snapshot)
			if actual["eventType"] != "packet_observed" || actual["authenticity"].(map[string]any)["state"] != "unverified" {
				t.Fatalf("mismatched profile was not raw-only: %#v", actual)
			}
			if _, ok := actual["subject"]; ok {
				t.Fatal("mismatched profile must not gain a subject")
			}
			assertRFNotObservable(t, actual)
			assertContractValid(t, actual)
		})
	}
}

func TestNormalizeDelegatedAdvertMalformedIsRawOnlyWithoutRF(t *testing.T) {
	payload := readNormalizeHexFixture(t, "new-advert-v1.17.1.hex")[:20]
	event := normalizeFixture(t, payload, time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC), pinnedReadySnapshot())
	if event["eventType"] != "packet_observed" || mapField(t, event, "authenticity")["state"] != "unverified" {
		t.Fatalf("malformed delegated advert was not raw-only: %#v", event)
	}
	if _, ok := event["radio"]; ok {
		t.Fatal("malformed delegated advert fabricated RF")
	}
	assertRFNotObservable(t, event)
	assertContractValid(t, event)
}

func TestNormalizeUnknownOpcodeIsRawOnly(t *testing.T) {
	payload := []byte{0x99, 0x01, 0x02}
	event := normalizeFixture(t, payload, time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC), pinnedReadySnapshot())
	if event["eventType"] != "packet_observed" || mapField(t, event, "authenticity")["state"] != "unverified" {
		t.Fatalf("unknown opcode was not raw-only: %#v", event)
	}
	if mapField(t, event, "source")["raw"] != base64.StdEncoding.EncodeToString(payload) {
		t.Fatal("unknown opcode raw payload changed")
	}
	assertRFNotObservable(t, event)
	assertContractValid(t, event)
}

func TestNormalizeProfileMismatchRetainsKnownPushesWithoutSemanticEvidence(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	mismatched := pinnedReadySnapshot()
	mismatched.Trust.Trusted = false
	mismatched.Trust.ProfileMatched = false
	mismatched.Trust.FirmwareBuild = "13 Aug 2026"
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "raw data", payload: []byte{PushRawData, 4, 0x91, 0xff, 1}},
		{name: "log rx signed advert", payload: readNormalizeHexFixture(t, "signed-log-rx-advert-v1.hex")},
		{name: "delegated advert", payload: readNormalizeHexFixture(t, "new-advert-v1.17.1.hex")},
		{name: "control data", payload: []byte{PushControlData, 8, 0x90, 0, 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := normalizeFixture(t, test.payload, observedAt, mismatched)
			if event["eventType"] != "packet_observed" || mapField(t, event, "authenticity")["state"] != "unverified" {
				t.Fatalf("profile-mismatched push was not raw-only: %#v", event)
			}
			if _, ok := event["radio"]; ok {
				t.Fatal("profile-mismatched push fabricated RF")
			}
			if _, ok := event["subject"]; ok {
				t.Fatal("profile-mismatched push gained a subject")
			}
			if _, ok := event["position"]; ok {
				t.Fatal("profile-mismatched push gained a position")
			}
			assertRFNotObservable(t, event)
			assertContractValid(t, event)
		})
	}
}

func TestNormalizeRawAndControlDataPreserveLocalRFWithoutAttribution(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	tests := []struct {
		name      string
		payload   []byte
		wantRoute string
	}{
		{name: "raw", payload: []byte{PushRawData, 0xf8, 0xa0, 0xff, 0x01, 0x02}},
		{name: "control", payload: []byte{PushControlData, 0x08, 0x9f, 0x00, 0xaa, 0xbb, 0x01}, wantRoute: "direct"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			event := normalizeFixture(t, testCase.payload, observedAt, pinnedReadySnapshot())
			if _, ok := event["subject"]; ok {
				t.Fatal("raw/control event must not fabricate sender identity")
			}
			radio := mapField(t, event, "radio")
			capabilities := mapField(t, event, "capabilities")
			if capabilities["receiver_rssi"] != "available" || capabilities["receiver_snr"] != "available" {
				t.Fatalf("validated RF frame lacks local RF capabilities: %#v", capabilities)
			}
			if testCase.wantRoute != "" && radio["routeKind"] != testCase.wantRoute {
				t.Fatalf("routeKind = %#v, want %q", radio["routeKind"], testCase.wantRoute)
			}
			assertContractValid(t, event)
		})
	}
}

func TestNormalizeRejectsMalformedAdapterBoundary(t *testing.T) {
	observedAt := time.Date(2026, 8, 20, 12, 5, 0, 0, time.UTC)
	binding := testBinding()
	tests := []protocoladapter.Event{
		{Adapter: "meshcore", ObservedAt: observedAt, Value: PushFrame{}},
		{Adapter: "meshcore", ObservedAt: observedAt, Value: PushFrame{Opcode: PushRawData, Payload: []byte{PushControlData, 0, 0, 0}}},
		{Adapter: "meshcore", ObservedAt: observedAt, Value: PushFrame{Opcode: PushRawData, Payload: append([]byte{PushRawData, 0, 0, 0}, make([]byte, MaxPayloadSize)...)}},
		{Adapter: "meshcore", ObservedAt: observedAt, Value: "wrong"},
	}
	for _, event := range tests {
		if _, err := NormalizeAdapterEvent(event, binding, Snapshot{}); err == nil {
			t.Fatalf("malformed adapter event %#v unexpectedly normalized", event.Value)
		}
	}
	if _, err := NormalizeAdapterEvent(protocoladapter.Event{Adapter: "meshcore", ObservedAt: observedAt, Value: PushFrame{Opcode: PushRawData, Payload: []byte{PushRawData, 0, 0, 0}}}, ReceiverBinding{}, Snapshot{}); err == nil {
		t.Fatal("missing receiver binding unexpectedly normalized")
	}
}

func assertRFNotObservable(t *testing.T, event map[string]any) {
	t.Helper()
	capabilities := mapField(t, event, "capabilities")
	if capabilities["receiver_rssi"] != "not_observable" || capabilities["receiver_snr"] != "not_observable" {
		t.Fatalf("RF capabilities must be not_observable: %#v", capabilities)
	}
}

func normalizeFixture(t *testing.T, payload []byte, observedAt time.Time, snapshot Snapshot) map[string]any {
	t.Helper()
	event, err := NormalizeAdapterEvent(protocoladapter.Event{
		Adapter:    "meshcore",
		ObservedAt: observedAt,
		Value:      PushFrame{Opcode: payload[0], Payload: payload},
	}, testBinding(), snapshot)
	if err != nil {
		t.Fatalf("normalize fixture: %v", err)
	}
	return event
}

func testBinding() ReceiverBinding {
	return ReceiverBinding{
		ReceiverAgentID: testReceiverAgentID,
		InstallationID:  testInstallationID,
		AdapterVersion:  "test-v1",
	}
}

func pinnedReadySnapshot() Snapshot {
	return Snapshot{
		State: SessionReady,
		Trust: TrustProfile{
			Trusted:            true,
			ProtocolCompatible: true,
			ProfileMatched:     true,
			ProtocolVersion:    ProtocolVersion,
			FirmwareBuild:      PinnedFirmwareBuild,
			FirmwareVersion:    PinnedFirmwareVersion,
			Model:              "Fixture Companion Board",
			AllowlistCommit:    PinnedSourceCommit,
			DeviceAttested:     false,
		},
	}
}

func assertContractValid(t *testing.T, event map[string]any) {
	t.Helper()
	if _, err := receiverevents.PrepareWithDeliveryID(event, testDeliveryID); err != nil {
		t.Fatalf("normalized event rejected by vendored contract: %v", err)
	}
}

func mapField(t *testing.T, value map[string]any, key string) map[string]any {
	t.Helper()
	result, ok := value[key].(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want object", key, value[key])
	}
	return result
}

func stringField(t *testing.T, value map[string]any, key string) string {
	t.Helper()
	result, ok := value[key].(string)
	if !ok {
		t.Fatalf("%s = %#v, want string", key, value[key])
	}
	return result
}

func readNormalizeHexFixture(t *testing.T, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(contents)))
	if err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	return decoded
}
