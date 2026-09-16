package meshcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestBuildTelemetryRequestRequiresCanonicalFullKey(t *testing.T) {
	t.Parallel()
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	target, err := parseTelemetryTarget(key)
	if err != nil {
		t.Fatal(err)
	}
	frame := BuildTelemetryRequest(target)
	if len(frame) != 36 || frame[0] != CommandSendTelemetryRequest || !bytes.Equal(frame[1:4], []byte{0, 0, 0}) || !bytes.Equal(frame[4:], target[:]) {
		t.Fatalf("unexpected telemetry request frame: %x", frame)
	}
	for _, invalid := range []string{"", key[:63], "0123456789ABCDEF0123456789abcdef0123456789abcdef0123456789abcdef"} {
		if _, err := parseTelemetryTarget(invalid); !errors.Is(err, ErrInvalidTelemetryTarget) {
			t.Fatalf("target %q error=%v, want invalid target", invalid, err)
		}
	}
}

func TestBuildBinaryTelemetryRequestUsesStockTaggedContract(t *testing.T) {
	t.Parallel()
	target := mustTelemetryTarget(t, trackingKey)
	frame := BuildBinaryTelemetryRequest(target)
	if len(frame) != 34 || frame[0] != CommandSendBinaryRequest || !bytes.Equal(frame[1:33], target[:]) || frame[33] != 0x03 {
		t.Fatalf("binary telemetry request = %x", frame)
	}
}

func TestAdapterTelemetryRequestFailsClosedWhenReleased(t *testing.T) {
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.disconnectBLE = func(context.Context, BLEConfig) error { return nil }
	if err := adapter.Release(); err != nil {
		t.Fatal(err)
	}
	_, err := adapter.RequestTelemetry(context.Background(), "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if !errors.Is(err, ErrTelemetryAdapterDisconnected) {
		t.Fatalf("released telemetry error=%v, want disconnected adapter", err)
	}
	if status := adapter.DetailedSnapshot(); status.State != StateReleased || !status.ReconnectSuppressed {
		t.Fatalf("telemetry request changed released adapter state: %#v", status)
	}
}

func TestPathResetUsesExactFullKeyAndRequiresCompanionAcknowledgement(t *testing.T) {
	key := trackingKey
	target := mustTelemetryTarget(t, key)
	if frame := BuildPathResetRequest(target); len(frame) != 33 || frame[0] != CommandResetPath || !bytes.Equal(frame[1:], target[:]) {
		t.Fatalf("unexpected reset frame: %x", frame)
	}
	if err := NewAdapter(Config{}, nil, nil).ResetPath(context.Background(), "short"); !errors.Is(err, ErrInvalidTelemetryTarget) {
		t.Fatalf("malformed target error=%v", err)
	}

	newAdapter := func() (*Adapter, *telemetryTestLink) {
		link := &telemetryTestLink{writes: make(chan []byte, 1)}
		adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{Adapter: "hci0", PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
		adapter.setLink(link)
		adapter.setStatus(func(status *AdapterStatus) { status.State, status.Session.State = StateConnected, SessionReady })
		return adapter, link
	}
	t.Run("acknowledged", func(t *testing.T) {
		adapter, link := newAdapter()
		done := make(chan error, 1)
		go func() { done <- adapter.ResetPath(context.Background(), key) }()
		if frame := <-link.writes; !bytes.Equal(frame, BuildPathResetRequest(target)) {
			t.Fatalf("reset frame=%x", frame)
		}
		if _, err := adapter.RequestTelemetry(context.Background(), key); !errors.Is(err, ErrTelemetryRequestInFlight) {
			t.Fatalf("telemetry interleaved with reset: %v", err)
		}
		adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseOK, Payload: []byte{ResponseOK}})
		if err := <-done; err != nil {
			t.Fatalf("reset error=%v", err)
		}
	})
	t.Run("firmware error", func(t *testing.T) {
		adapter, link := newAdapter()
		done := make(chan error, 1)
		go func() { done <- adapter.ResetPath(context.Background(), key) }()
		<-link.writes
		adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseError, Payload: []byte{ResponseError, 2}})
		if err := <-done; !errors.Is(err, ErrPathResetFirmware) {
			t.Fatalf("reset error=%v", err)
		}
	})
	t.Run("disconnect", func(t *testing.T) {
		adapter, link := newAdapter()
		done := make(chan error, 1)
		go func() { done <- adapter.ResetPath(context.Background(), key) }()
		<-link.writes
		adapter.finishCurrentPathReset(ErrTelemetryAdapterDisconnected)
		if err := <-done; !errors.Is(err, ErrTelemetryAdapterDisconnected) {
			t.Fatalf("reset error=%v", err)
		}
	})
}

func TestParseTelemetryLPPSupportsStockWioTrackerFields(t *testing.T) {
	t.Parallel()
	payload := []byte{
		1, 120, 87, // battery percentage
		1, 116, 0x01, 0x99, // 4.09 V
		2, 103, 0x01, 0x1d, // 28.5 C
		3, 136,
	}
	payload = appendInt24(payload, 493958) // 49.3958
	payload = appendInt24(payload, 76102)  // 7.6102
	payload = appendInt24(payload, 35850)  // 358.5 m
	telemetry, err := ParseTelemetryLPP(payload)
	if err != nil {
		t.Fatal(err)
	}
	if telemetry.BatteryPercentage == nil || *telemetry.BatteryPercentage != 87 || telemetry.Voltage == nil || *telemetry.Voltage != 4.09 || telemetry.TemperatureC == nil || *telemetry.TemperatureC != 28.5 || telemetry.Latitude == nil || *telemetry.Latitude != 49.3958 || telemetry.Longitude == nil || *telemetry.Longitude != 7.6102 || telemetry.AltitudeM == nil || *telemetry.AltitudeM != 358.5 {
		t.Fatalf("unexpected parsed telemetry: %#v", telemetry)
	}
}

func TestParseTelemetryLPPSafelyPreservesUnsupportedType(t *testing.T) {
	t.Parallel()
	telemetry, err := ParseTelemetryLPP([]byte{1, 116, 0x01, 0x99, 2, 0xfe, 0xaa, 0xbb})
	if err != nil || telemetry.Voltage == nil || *telemetry.Voltage != 4.09 || len(telemetry.UnsupportedTypes) != 1 || telemetry.UnsupportedTypes[0] != 0xfe {
		t.Fatalf("unexpected safely partial parse: telemetry=%#v err=%v", telemetry, err)
	}
	if _, err := ParseTelemetryLPP([]byte{1, 136, 0, 0}); !errors.Is(err, ErrInvalidTelemetryPayload) {
		t.Fatalf("truncated known type error=%v, want invalid payload", err)
	}
}

func TestCompanionSessionRecognizesTelemetryResponseAndSent(t *testing.T) {
	t.Parallel()
	session := readySession(t)
	sent := make([]byte, 10)
	sent[0] = ResponseSent
	binary.LittleEndian.PutUint32(sent[6:], 1234)
	result, err := session.Handle(sent)
	if err != nil || result.Response == nil || result.Response.Code != ResponseSent || result.Push != nil {
		t.Fatalf("unexpected SENT handling: result=%#v err=%v", result, err)
	}
	telemetry := []byte{PushTelemetryResponse, 0, 1, 2, 3, 4, 5, 6, 1, 120, 90}
	result, err = session.Handle(telemetry)
	if err != nil || result.Push == nil || result.Push.Opcode != PushTelemetryResponse {
		t.Fatalf("unexpected telemetry push handling: result=%#v err=%v", result, err)
	}
	contact := telemetryContactFrame(t, trackingKey, 0, nil)
	result, err = session.Handle(contact)
	if err != nil || result.Response == nil || result.Response.Code != ResponseContact {
		t.Fatalf("unexpected contact response handling: result=%#v err=%v", result, err)
	}
	binaryResponse := []byte{PushBinaryResponse, 0, 0x44, 0x33, 0x22, 0x11, 1, 120, 90}
	result, err = session.Handle(binaryResponse)
	if err != nil || result.Push == nil || result.Push.Opcode != PushBinaryResponse {
		t.Fatalf("unexpected binary telemetry handling: result=%#v err=%v", result, err)
	}
}

func TestTaggedTelemetryDiscardsExpiredResponseBeforeSameTargetReplacement(t *testing.T) {
	key := trackingKey
	link := &telemetryTestLink{writes: make(chan []byte, 8)}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{Adapter: "hci0", PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.setLink(link)
	adapter.setStatus(func(status *AdapterStatus) { status.State, status.Session.State = StateConnected, SessionReady })

	start := func(pathEncoding byte, path []byte) chan telemetryCompletion {
		done := make(chan telemetryCompletion, 1)
		go func() {
			result, err := adapter.RequestTelemetry(context.Background(), key)
			done <- telemetryCompletion{result: result, err: err}
		}()
		<-link.writes // contact lookup
		adapter.handleTelemetryContactResponse(telemetryContactFrame(t, key, pathEncoding, path))
		frame := <-link.writes
		if frame[0] != CommandSendBinaryRequest {
			t.Fatalf("request command = 0x%02x, want binary", frame[0])
		}
		return done
	}

	first := start(2, []byte{0xaa, 0xbb})
	adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseSent, Payload: []byte{ResponseSent, 0, 1, 0, 0, 0, 0xe8, 3, 0, 0}})
	adapter.finishCurrentTelemetry(TelemetryResult{}, ErrTelemetryTimeout)
	if completion := <-first; !errors.Is(completion.err, ErrTelemetryTimeout) || !completion.result.Tagged || completion.result.RequestTag == nil || *completion.result.RequestTag != 1 {
		t.Fatalf("first completion = %#v", completion)
	}

	second := start(0, nil)
	adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseSent, Payload: []byte{ResponseSent, 0, 2, 0, 0, 0, 0xe8, 3, 0, 0}})
	// This is a valid-looking response for the expired request A. It must not
	// update B's request route, telemetry, motion evidence, or completion.
	adapter.handleBinaryTelemetryResponse([]byte{PushBinaryResponse, 0, 1, 0, 0, 0, 1, 116, 0x01, 0x99}, time.Now())
	select {
	case completion := <-second:
		t.Fatalf("late A completed B: %#v", completion)
	default:
	}
	adapter.handleBinaryTelemetryResponse([]byte{PushBinaryResponse, 0, 2, 0, 0, 0, 1, 116, 0x01, 0x99}, time.Now())
	completion := <-second
	if completion.err != nil || completion.result.RequestTag == nil || *completion.result.RequestTag != 2 || completion.result.RouteAttempt.Mode != RouteModeZeroHop || completion.result.RouteAttempt.PathLength != 0 {
		t.Fatalf("B correlation or route evidence was contaminated: %#v err=%v", completion.result, completion.err)
	}
}

func TestBinaryTelemetryUnsupportedFallsBackToLegacy(t *testing.T) {
	key := trackingKey
	link := &telemetryTestLink{writes: make(chan []byte, 4)}
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: "/dev/null"}, nil, nil)
	adapter.setLink(link)
	adapter.setStatus(func(status *AdapterStatus) { status.State, status.Session.State = StateConnected, SessionReady })
	done := make(chan telemetryCompletion, 1)
	go func() {
		result, err := adapter.RequestTelemetry(context.Background(), key)
		done <- telemetryCompletion{result: result, err: err}
	}()
	<-link.writes // contact lookup
	adapter.handleTelemetryContactResponse(telemetryContactFrame(t, key, 0, nil))
	if frame := <-link.writes; frame[0] != CommandSendBinaryRequest {
		t.Fatalf("first request command = 0x%02x, want binary", frame[0])
	}
	adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseError, Payload: []byte{ResponseError, 1}})
	if frame := <-link.writes; frame[0] != CommandSendTelemetryRequest {
		t.Fatalf("fallback request command = 0x%02x, want legacy", frame[0])
	}
	adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseSent, Payload: []byte{ResponseSent, 0, 0, 0, 0, 0, 0, 0, 0, 0}})
	adapter.handleTelemetryResponse([]byte{PushTelemetryResponse, 0, 0x01, 0x23, 0x45, 0x67, 0x89, 0xab, 1, 120, 90}, time.Now())
	completion := <-done
	if completion.err != nil || completion.result.Tagged || adapter.binaryTelemetryCapability != telemetryBinaryUnsupported {
		t.Fatalf("legacy fallback = %#v capability=%v", completion, adapter.binaryTelemetryCapability)
	}
}

func TestAdapterTelemetryRequestCorrelatesOnlyInFlightFullTarget(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	link := &telemetryTestLink{writes: make(chan []byte, 1)}
	adapter := NewAdapter(Config{Transport: "ble", BLE: BLEConfig{Adapter: "hci0", PeerAddress: "AA:BB:CC:DD:EE:FF"}}, nil, nil)
	adapter.setLink(link)
	adapter.setStatus(func(status *AdapterStatus) {
		status.State = StateConnected
		status.Session.State = SessionReady
	})
	resultCh := make(chan telemetryCompletion, 1)
	go func() {
		result, err := adapter.RequestTelemetry(context.Background(), key)
		resultCh <- telemetryCompletion{result: result, err: err}
	}()
	select {
	case frame := <-link.writes:
		target := mustTelemetryTarget(t, key)
		if len(frame) != 33 || frame[0] != CommandGetContactByKey || !bytes.Equal(frame[1:], target[:]) {
			t.Fatalf("unexpected route snapshot request: %x", frame)
		}
		adapter.handleTelemetryContactResponse(telemetryContactFrame(t, key, 2, []byte{0xaa, 0xbb}))
	case <-time.After(time.Second):
		t.Fatal("route snapshot request was not written")
	}
	select {
	case frame := <-link.writes:
		target := mustTelemetryTarget(t, key)
		if len(frame) != 34 || frame[0] != CommandSendBinaryRequest || !bytes.Equal(frame[1:33], target[:]) || frame[33] != 0x03 {
			t.Fatalf("unexpected telemetry request: %x", frame)
		}
	case <-time.After(time.Second):
		t.Fatal("telemetry request was not written")
	}
	adapter.handleTelemetryCommandResponse(ResponseFrame{Code: ResponseSent, Payload: []byte{ResponseSent, 0, 0x33, 0x44, 0x55, 0x66, 0, 0, 0, 0}})
	if _, err := adapter.RequestTelemetry(context.Background(), key); !errors.Is(err, ErrTelemetryRequestInFlight) {
		t.Fatalf("second request error=%v, want in-flight", err)
	}
	frame := []byte{PushBinaryResponse, 0, 0x33, 0x44, 0x55, 0x66, 1, 116, 0x01, 0x99}
	adapter.handleBinaryTelemetryResponse(frame, time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC))
	select {
	case completion := <-resultCh:
		if completion.err != nil || !completion.result.Tagged || completion.result.RequestTag == nil || *completion.result.RequestTag != 0x66554433 || completion.result.TargetPublicKey != key || completion.result.SourcePrefix != key[:12] || completion.result.Telemetry.Voltage == nil || *completion.result.Telemetry.Voltage != 4.09 || completion.result.RouteAttempt.Mode != RouteModeExplicitPath || completion.result.RouteAttempt.Source != "contact_out_path+response_sent" || !completion.result.ResponseRouteUnknown || !bytes.Equal(completion.result.RawFrame, frame) {
			t.Fatalf("unexpected completion: %#v err=%v", completion.result, completion.err)
		}
	case <-time.After(time.Second):
		t.Fatal("telemetry response did not complete request")
	}
}

func TestNormalizeTelemetryResultPreservesCorrelationWithoutPositionTrust(t *testing.T) {
	t.Parallel()
	voltage, latitude, longitude, altitude, temperature := 4.04, 49.3958, 7.6103, 360.2, 26.2
	key := "5bed5393ba6f5bba71ed2f4df6ed54048467dbb728de5fa3acd10c8a04dd0c2b"
	frame := []byte{PushTelemetryResponse, 0, 0x5b, 0xed, 0x53, 0x93, 0xba, 0x6f, 1, 116, 0x01, 0x94}
	normalized, err := NormalizeTelemetryResult(TelemetryResult{
		TargetPublicKey: key,
		SourcePrefix:    key[:12],
		ReceivedAt:      time.Date(2026, 9, 12, 19, 1, 5, 0, time.UTC),
		RawFrame:        frame,
		Telemetry: Telemetry{
			Voltage: &voltage, Latitude: &latitude, Longitude: &longitude, AltitudeM: &altitude, TemperatureC: &temperature,
		},
	}, ReceiverBinding{InstallationID: "00112233445566778899aabbccddeeff", AdapterVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if normalized["eventType"] != "meshcore_solicited_telemetry" || normalized["position"] != nil {
		t.Fatalf("unexpected telemetry normalization: %#v", normalized)
	}
	subject := normalized["subject"].(map[string]any)
	if subject["canonicalId"] != key || subject["verification"] != "unverified" {
		t.Fatalf("telemetry subject lost canonical correlation: %#v", subject)
	}
	evidence := normalized["solicitedTelemetry"].(map[string]any)
	if evidence["sourcePrefix"] != key[:12] || evidence["correlation"] != "request_correlated" || evidence["authenticity"] != "not_independently_signed" {
		t.Fatalf("telemetry provenance was not retained: %#v", evidence)
	}
}

func TestNormalizeTaggedBinaryTelemetryPreservesTagAndRawOpcode(t *testing.T) {
	t.Parallel()
	key := trackingKey
	tag := uint32(0x66554433)
	voltage := 4.09
	normalized, err := NormalizeTelemetryResult(TelemetryResult{
		TargetPublicKey: key, SourcePrefix: key[:12], RequestTag: &tag, Tagged: true,
		ResponseOpcode: PushBinaryResponse, ReceivedAt: time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC),
		RawFrame: []byte{PushBinaryResponse, 0, 0x33, 0x44, 0x55, 0x66, 1, 116, 1, 0x99}, Telemetry: Telemetry{Voltage: &voltage},
	}, ReceiverBinding{InstallationID: "00112233445566778899aabbccddeeff", AdapterVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	source := normalized["source"].(map[string]any)
	if source["nativeOpcodeName"] != "BINARY_RESPONSE" {
		t.Fatalf("raw opcode was rewritten: %#v", source)
	}
	telemetry := normalized["solicitedTelemetry"].(map[string]any)
	if telemetry["requestTag"] != tag {
		t.Fatalf("request tag was not normalized: %#v", telemetry)
	}
}

func TestNormalizeTelemetryResultPreservesRawRequestRouteEvidence(t *testing.T) {
	t.Parallel()
	key := "5bed5393ba6f5bba71ed2f4df6ed54048467dbb728de5fa3acd10c8a04dd0c2b"
	voltage := 4.04
	for _, test := range []struct {
		name  string
		route RouteEvidence
		want  []string
	}{
		{name: "zero hop", route: RouteEvidence{Mode: RouteModeZeroHop, PathLength: 0, Source: "contact_out_path+response_sent"}},
		{name: "flood", route: RouteEvidence{Mode: RouteModeFlood, PathLength: 0, Source: "response_sent"}},
		{name: "explicit multi-width", route: RouteEvidence{Mode: RouteModeExplicitPath, Path: []string{"18", "186d", "186d73"}, PathLength: 3, Source: "contact_out_path+response_sent"}, want: []string{"18", "186d", "186d73"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := NormalizeTelemetryResult(TelemetryResult{
				TargetPublicKey:    key,
				SourcePrefix:       key[:12],
				ReceivedAt:         time.Date(2026, 9, 12, 19, 1, 5, 0, time.UTC),
				RawFrame:           []byte{PushTelemetryResponse, 0, 0x5b, 0xed, 0x53, 0x93, 0xba, 0x6f, 1, 116, 0x01, 0x94},
				Telemetry:          Telemetry{Voltage: &voltage},
				RouteAttempt:       test.route,
				RouteRecovery:      "path_reset_acknowledged",
				PathUpdateObserved: true,
			}, ReceiverBinding{InstallationID: "00112233445566778899aabbccddeeff", AdapterVersion: "test"})
			if err != nil {
				t.Fatal(err)
			}
			route := normalized["requestRoute"].(map[string]any)
			if route["mode"] != test.route.Mode || route["pathLength"] != test.route.PathLength || route["source"] != test.route.Source || route["recoveryEvent"] != "path_reset_acknowledged" || route["pathUpdateObserved"] != true {
				t.Fatalf("request route evidence was not retained: %#v", route)
			}
			if len(test.want) > 0 {
				path := route["path"].([]string)
				if len(path) != len(test.want) {
					t.Fatalf("path length=%d, want %d", len(path), len(test.want))
				}
				for index := range path {
					if path[index] != test.want[index] {
						t.Fatalf("path[%d]=%q, want %q", index, path[index], test.want[index])
					}
				}
			}
			response := normalized["responseRoute"].(map[string]any)
			if response["known"] != false {
				t.Fatalf("response route must remain unknown: %#v", response)
			}
		})
	}
}

func TestAdapterTelemetryRequestFailsClosedOnMismatchedPrefix(t *testing.T) {
	key := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	link := &telemetryTestLink{writes: make(chan []byte, 1)}
	adapter := NewAdapter(Config{Transport: "physical_serial", Device: "/dev/null"}, nil, nil)
	adapter.binaryTelemetryCapability = telemetryBinaryUnsupported
	adapter.setLink(link)
	adapter.setStatus(func(status *AdapterStatus) { status.State, status.Session.State = StateConnected, SessionReady })
	resultCh := make(chan error, 1)
	go func() { _, err := adapter.RequestTelemetry(context.Background(), key); resultCh <- err }()
	<-link.writes
	adapter.handleTelemetryContactResponse(telemetryContactFrame(t, key, 0, nil))
	<-link.writes
	adapter.handleTelemetryResponse([]byte{PushTelemetryResponse, 0, 9, 9, 9, 9, 9, 9, 1, 120, 90}, time.Now())
	select {
	case err := <-resultCh:
		if !errors.Is(err, ErrTelemetryMismatchedResponse) {
			t.Fatalf("mismatched prefix error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("mismatched response did not fail request")
	}
}

func telemetryContactFrame(t *testing.T, key string, pathEncoding byte, path []byte) []byte {
	t.Helper()
	target := mustTelemetryTarget(t, key)
	frame := make([]byte, newAdvertLength)
	frame[0] = ResponseContact
	copy(frame[1:], target[:])
	frame[35] = pathEncoding
	copy(frame[36:100], path)
	return frame
}

func appendInt24(payload []byte, value int32) []byte {
	return append(payload, byte(value>>16), byte(value>>8), byte(value))
}

func mustTelemetryTarget(t *testing.T, value string) [telemetryPublicKeyLength]byte {
	t.Helper()
	target, err := parseTelemetryTarget(value)
	if err != nil {
		t.Fatal(err)
	}
	return target
}

type telemetryTestLink struct {
	writes chan []byte
	mu     sync.Mutex
}

func (l *telemetryTestLink) ReadFrame(context.Context) ([]byte, error) {
	return nil, errors.New("not used")
}
func (l *telemetryTestLink) WriteFrame(_ context.Context, frame []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.writes <- append([]byte(nil), frame...)
	return nil
}
func (l *telemetryTestLink) Metadata() TransportMetadata { return TransportMetadata{Kind: "test"} }
func (l *telemetryTestLink) Close() error                { return nil }
