package meshcore

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestCompanionSessionNegotiatesPinnedProfileInOrder(t *testing.T) {
	t.Parallel()

	session := NewCompanionSession("loramapr-receiver")
	query := session.Begin()
	if !bytes.Equal(query, []byte{CommandDeviceQuery, ProtocolVersion}) {
		t.Fatalf("unexpected DEVICE_QUERY payload: %x", query)
	}

	deviceFrame := readHexFixture(t, "device-info-v1.17.1.hex")
	result, err := session.Handle(deviceFrame)
	if err != nil {
		t.Fatalf("handle DEVICE_INFO: %v", err)
	}
	if result.Ready || result.Push != nil {
		t.Fatalf("DEVICE_INFO must not complete the handshake: %#v", result)
	}
	if len(result.Outbound) < 8 || result.Outbound[0] != CommandAppStart || result.Outbound[1] != ProtocolVersion {
		t.Fatalf("unexpected APP_START payload: %x", result.Outbound)
	}
	if !bytes.Equal(result.Outbound[2:8], make([]byte, 6)) {
		t.Fatalf("APP_START reserved bytes must be zero: %x", result.Outbound[2:8])
	}
	if string(result.Outbound[8:]) != "loramapr-receiver" {
		t.Fatalf("unexpected APP_START app name: %q", result.Outbound[8:])
	}

	negotiating := session.Snapshot()
	if negotiating.State != SessionAwaitSelfInfo || negotiating.Trust.Trusted {
		t.Fatalf("unexpected negotiating snapshot: %#v", negotiating)
	}
	if negotiating.DeviceInfo == nil || negotiating.DeviceInfo.Model != "Fixture Companion Board" {
		t.Fatalf("device model was not retained: %#v", negotiating.DeviceInfo)
	}
	if negotiating.Trust.AllowlistCommit != PinnedSourceCommit || negotiating.Trust.DeviceAttested {
		t.Fatalf("unexpected provenance/attestation state: %#v", negotiating.Trust)
	}

	result, err = session.Handle(readHexFixture(t, "self-info-v1.17.1.hex"))
	if err != nil {
		t.Fatalf("handle SELF_INFO: %v", err)
	}
	if !result.Ready || result.Push != nil || len(result.Outbound) != 0 {
		t.Fatalf("unexpected SELF_INFO result: %#v", result)
	}

	ready := session.Snapshot()
	if ready.State != SessionReady || !ready.Trust.Trusted || ready.Trust.DeviceAttested {
		t.Fatalf("unexpected ready trust state: %#v", ready)
	}
	if ready.Trust.FirmwareBuild != PinnedFirmwareBuild || ready.Trust.FirmwareVersion != PinnedFirmwareVersion {
		t.Fatalf("unexpected trusted profile: %#v", ready.Trust)
	}
	if ready.SelfInfo == nil || ready.SelfInfo.NodeName != "Fixture Node" {
		t.Fatalf("unexpected self info: %#v", ready.SelfInfo)
	}
	if ready.SelfInfo.PublicKey[0] != 0 || ready.SelfInfo.PublicKey[31] != 31 {
		t.Fatalf("unexpected full public key: %x", ready.SelfInfo.PublicKey)
	}
}

func TestCompanionSessionRejectsOutOfOrderHandshake(t *testing.T) {
	t.Parallel()

	session := NewCompanionSession("")
	if _, err := session.Handle([]byte{ResponseDeviceInfo}); !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("expected not-started error, got %v", err)
	}
	session.Begin()
	if _, err := session.Handle(readHexFixture(t, "self-info-v1.17.1.hex")); !errors.Is(err, ErrUnexpectedHandshakeFrame) {
		t.Fatalf("expected out-of-order handshake error, got %v", err)
	}
	if session.Snapshot().State != SessionFailed {
		t.Fatalf("expected failed state, got %q", session.Snapshot().State)
	}
}

func TestCompanionSessionRejectsWrongProtocolAndProfile(t *testing.T) {
	t.Parallel()

	valid := readHexFixture(t, "device-info-v1.17.1.hex")
	tests := []struct {
		name string
		edit func([]byte)
		want error
	}{
		{name: "wrong protocol", edit: func(value []byte) { value[1] = ProtocolVersion - 1 }, want: ErrUnsupportedProtocol},
		{name: "wrong build", edit: func(value []byte) { copy(value[8:20], []byte("13 Aug 2026\x00")) }, want: ErrTrustProfileMismatch},
		{name: "wrong version", edit: func(value []byte) { copy(value[60:80], []byte("v1.17.0\x00")) }, want: ErrTrustProfileMismatch},
		{name: "blank model", edit: func(value []byte) { clear(value[20:60]) }, want: ErrTrustProfileMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			frame := append([]byte(nil), valid...)
			test.edit(frame)
			session := NewCompanionSession("")
			session.Begin()
			_, err := session.Handle(frame)
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
			if session.Snapshot().Trust.Trusted {
				t.Fatal("profile mismatch must never establish trust")
			}
		})
	}
}

func TestCompanionSessionRequiresExactDeviceInfoAndCompleteSelfInfo(t *testing.T) {
	t.Parallel()

	device := readHexFixture(t, "device-info-v1.17.1.hex")
	session := NewCompanionSession("")
	session.Begin()
	if _, err := session.Handle(device[:len(device)-1]); !errors.Is(err, ErrInvalidDeviceInfo) {
		t.Fatalf("expected exact DEVICE_INFO length error, got %v", err)
	}

	session = NewCompanionSession("")
	session.Begin()
	if _, err := session.Handle(device); err != nil {
		t.Fatalf("handle DEVICE_INFO: %v", err)
	}
	self := readHexFixture(t, "self-info-v1.17.1.hex")
	if _, err := session.Handle(self[:selfInfoMinLength-1]); !errors.Is(err, ErrInvalidSelfInfo) {
		t.Fatalf("expected incomplete SELF_INFO error, got %v", err)
	}
}

func TestCompanionSessionCopiesOnlySupportedPushFrames(t *testing.T) {
	t.Parallel()

	session := readySession(t)
	frames := [][]byte{
		{PushRawData, 4, 0xD0, 0, 0xAA},
		{PushLogRXData, 4, 0xD0, 0xBB},
		readHexFixture(t, "new-advert-v1.17.1.hex"),
		{PushControlData, 4, 0xD0, 0, 0xCC},
	}
	for _, frame := range frames {
		original := append([]byte(nil), frame...)
		result, err := session.Handle(frame)
		if err != nil {
			t.Fatalf("handle supported opcode 0x%02x: %v", frame[0], err)
		}
		if result.Push == nil || result.Push.Opcode != frame[0] || !bytes.Equal(result.Push.Payload, original) {
			t.Fatalf("unexpected copied push: %#v", result.Push)
		}
		frame[0] = 0
		if result.Push.Payload[0] != original[0] {
			t.Fatal("push payload aliases caller memory")
		}
	}
}

func TestCompanionSessionRejectsUnsupportedAndMalformedPushFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		frame []byte
		want  error
	}{
		{name: "unsupported", frame: []byte{0x80}, want: ErrUnsupportedPush},
		{name: "raw too short", frame: []byte{PushRawData, 0, 0}, want: ErrInvalidPush},
		{name: "log missing raw packet", frame: []byte{PushLogRXData, 0, 0}, want: ErrInvalidPush},
		{name: "advert too short", frame: []byte{PushNewAdvert, 0}, want: ErrInvalidPush},
		{name: "control too short", frame: []byte{PushControlData, 0, 0}, want: ErrInvalidPush},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			session := readySession(t)
			_, err := session.Handle(test.frame)
			if !errors.Is(err, test.want) {
				t.Fatalf("expected %v, got %v", test.want, err)
			}
			if !session.Snapshot().Trust.Trusted {
				t.Fatal("unsupported post-handshake data must not mutate negotiated trust")
			}
		})
	}
}

func TestCompanionSessionDisconnectInvalidatesTrustAndRenegotiates(t *testing.T) {
	t.Parallel()

	session := readySession(t)
	session.Disconnect()
	snapshot := session.Snapshot()
	if snapshot.State != SessionDisconnected || snapshot.Trust.Trusted {
		t.Fatalf("disconnect did not invalidate trust: %#v", snapshot)
	}
	if !strings.Contains(snapshot.Trust.Reason, "reconnect") {
		t.Fatalf("unexpected disconnect reason: %q", snapshot.Trust.Reason)
	}
	if _, err := session.Handle([]byte{PushRawData, 0, 0, 0}); !errors.Is(err, ErrSessionNotStarted) {
		t.Fatalf("expected disconnected session rejection, got %v", err)
	}
	if got := session.Begin(); !bytes.Equal(got, []byte{CommandDeviceQuery, ProtocolVersion}) {
		t.Fatalf("reconnect did not restart with DEVICE_QUERY: %x", got)
	}
}

func readySession(t *testing.T) *CompanionSession {
	t.Helper()
	session := NewCompanionSession("test")
	session.Begin()
	if _, err := session.Handle(readHexFixture(t, "device-info-v1.17.1.hex")); err != nil {
		t.Fatalf("handle DEVICE_INFO: %v", err)
	}
	if _, err := session.Handle(readHexFixture(t, "self-info-v1.17.1.hex")); err != nil {
		t.Fatalf("handle SELF_INFO: %v", err)
	}
	return session
}

func readHexFixture(t *testing.T, name string) []byte {
	t.Helper()
	value, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	decoded, err := hex.DecodeString(strings.TrimSpace(string(value)))
	if err != nil {
		t.Fatalf("decode fixture %s: %v", name, err)
	}
	wantHashes := map[string]string{
		"device-info-v1.17.1.hex": "ea432a9f78717d42348bb0127c5b4b4bed69d3250f1451ae1269b94141ea3beb",
		"new-advert-v1.17.1.hex":  "38dcdca8bb70ebc2e05641fff38f4b4d4609262b816bf4a95329850c34ee363d",
		"self-info-v1.17.1.hex":   "e39872688d1cde81c892f31717985d44d197f76f8a2c6d4a843dcb817aa554d9",
	}
	if want := wantHashes[name]; want != "" {
		actual := sha256.Sum256(decoded)
		if hex.EncodeToString(actual[:]) != want {
			t.Fatalf("fixture %s decoded SHA-256 changed", name)
		}
	}
	return decoded
}
