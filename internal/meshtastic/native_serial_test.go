package meshtastic

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

func TestDecodeNativeFramePacketWithPosition(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 19, 10, 0, 0, 0, time.UTC)
	positionPayload := append(
		testFixed32Field(1, uint32(int32(523450000))),
		testFixed32Field(2, uint32(int32(134560000)))...,
	)
	decodedData := append(
		testVarintField(1, nativePortNumPositionApp),
		testBytesField(2, positionPayload)...,
	)
	meshPacket := append(
		testFixed32Field(1, 0x1234ABCD),
		testFixed32Field(2, 0xDEADBEEF)...,
	)
	meshPacket = append(meshPacket, testBytesField(4, decodedData)...)
	meshPacket = append(meshPacket, testFixed32Field(7, uint32(now.Unix()))...)
	fromRadio := testBytesField(2, meshPacket)

	event, handled, err := decodeNativeFrame(fromRadio, now)
	if err != nil {
		t.Fatalf("decodeNativeFrame returned error: %v", err)
	}
	if !handled {
		t.Fatal("expected handled native frame")
	}
	if event.Kind != EventPacket || event.Packet == nil {
		t.Fatalf("expected packet event, got %#v", event)
	}
	if event.Packet.SourceNodeID != "!1234abcd" {
		t.Fatalf("unexpected source node id: %q", event.Packet.SourceNodeID)
	}
	if event.Packet.DestinationNodeID != "!deadbeef" {
		t.Fatalf("unexpected destination node id: %q", event.Packet.DestinationNodeID)
	}
	if event.Packet.PortNum != nativePortNumPositionApp {
		t.Fatalf("unexpected port number: %d", event.Packet.PortNum)
	}
	if event.Packet.Position == nil {
		t.Fatal("expected position to be decoded from native payload")
	}
	if event.Packet.Position.Lat < 52.344 || event.Packet.Position.Lat > 52.346 {
		t.Fatalf("unexpected latitude: %f", event.Packet.Position.Lat)
	}
	if event.Packet.Position.Lon < 13.455 || event.Packet.Position.Lon > 13.457 {
		t.Fatalf("unexpected longitude: %f", event.Packet.Position.Lon)
	}
}

func TestDecodeNativeFrameConfigAndChannelStatus(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	loraConfig := append(
		testVarintField(7, 3), // EU_868
		testVarintField(2, 0)...,
	)
	configPayload := testBytesField(6, loraConfig)
	configFrame := testBytesField(5, configPayload)

	event, handled, err := decodeNativeFrame(configFrame, now)
	if err != nil {
		t.Fatalf("decode config frame: %v", err)
	}
	if !handled || event.Kind != EventStatus || event.Node == nil || event.Node.HomeConfig == nil {
		t.Fatalf("expected status/home config from native config frame, got %#v", event)
	}
	if event.Node.HomeConfig.Region != "EU_868" {
		t.Fatalf("unexpected region: %q", event.Node.HomeConfig.Region)
	}
	if event.Node.HomeConfig.LoRaPreset != "LONG_FAST" {
		t.Fatalf("unexpected LoRa preset: %q", event.Node.HomeConfig.LoRaPreset)
	}

	channelSettings := append(
		testBytesField(2, []byte{1, 2, 3, 4}),
		testBytesField(3, []byte("Home Mesh"))...,
	)
	channelPayload := append(
		testVarintField(1, 1),
		testBytesField(2, channelSettings)...,
	)
	channelFrame := testBytesField(10, channelPayload)
	event, handled, err = decodeNativeFrame(channelFrame, now)
	if err != nil {
		t.Fatalf("decode channel frame: %v", err)
	}
	if !handled || event.Kind != EventStatus || event.Node == nil || event.Node.HomeConfig == nil {
		t.Fatalf("expected status/home config from native channel frame, got %#v", event)
	}
	if event.Node.HomeConfig.PrimaryChannel != "Home Mesh" {
		t.Fatalf("unexpected primary channel: %q", event.Node.HomeConfig.PrimaryChannel)
	}
	if event.Node.HomeConfig.PSKState != "present" {
		t.Fatalf("unexpected psk state: %q", event.Node.HomeConfig.PSKState)
	}
}

func TestNativeFrameScannerRejectsUnreadableStream(t *testing.T) {
	t.Parallel()

	scanner := &nativeFrameScanner{
		reader:       bufioFromBytes([]byte("\x1b[31mdebug output without native frames\n")),
		maxPayload:   nativeMaxFramePayloadSize,
		maxDiscarded: 8,
	}

	_, _, err := scanner.NextFrame()
	if !errors.Is(err, errNoNativeFrames) {
		t.Fatalf("expected errNoNativeFrames, got %v", err)
	}
}

func TestBuildNativeFrameAndDecodeRoundTrip(t *testing.T) {
	t.Parallel()

	myInfo := testBytesField(3, testVarintField(1, 0xAABBCCDD))
	frame := buildNativeFrame(myInfo)
	if len(frame) < 4 {
		t.Fatalf("native frame too short: %d", len(frame))
	}
	if frame[0] != nativeSerialStart1 || frame[1] != nativeSerialStart2 {
		t.Fatalf("unexpected native frame prefix: %x %x", frame[0], frame[1])
	}

	reader := bytes.NewReader(frame)
	scanner := &nativeFrameScanner{
		reader:       bufioFromReader(reader),
		maxPayload:   nativeMaxFramePayloadSize,
		maxDiscarded: nativeMaxDiscardedBytes,
	}
	payload, _, err := scanner.NextFrame()
	if err != nil {
		t.Fatalf("scanner NextFrame: %v", err)
	}
	event, handled, err := decodeNativeFrame(payload, time.Now().UTC())
	if err != nil {
		t.Fatalf("decodeNativeFrame: %v", err)
	}
	if !handled || event.Kind != EventStatus || event.Node == nil {
		t.Fatalf("expected status event from my_info frame, got %#v", event)
	}
	if !strings.EqualFold(event.Node.LocalNodeID, "!aabbccdd") {
		t.Fatalf("unexpected local node id: %q", event.Node.LocalNodeID)
	}
}

func TestNormalizedNativeRXTimeUsesNowWhenClockSkewed(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 3, 24, 10, 30, 0, 0, time.UTC)

	inRange := uint32(now.Add(-5 * time.Minute).Unix())
	if got := normalizedNativeRXTime(now, inRange); !got.Equal(time.Unix(int64(inRange), 0).UTC()) {
		t.Fatalf("expected in-range rx_time to be used, got %s", got.Format(time.RFC3339))
	}

	tooOld := uint32(now.Add(-2 * time.Hour).Unix())
	if got := normalizedNativeRXTime(now, tooOld); !got.Equal(now.UTC()) {
		t.Fatalf("expected old rx_time fallback to now, got %s", got.Format(time.RFC3339))
	}

	tooFarFuture := uint32(now.Add(2 * time.Hour).Unix())
	if got := normalizedNativeRXTime(now, tooFarFuture); !got.Equal(now.UTC()) {
		t.Fatalf("expected future rx_time fallback to now, got %s", got.Format(time.RFC3339))
	}
}

func bufioFromBytes(payload []byte) *bufio.Reader {
	return bufioFromReader(bytes.NewReader(payload))
}

func bufioFromReader(reader io.Reader) *bufio.Reader {
	return bufio.NewReader(reader)
}

func testVarintField(number int, value uint64) []byte {
	out := []byte{byte((number << 3) | protoWireVarint)}
	out = append(out, encodeProtoVarint(value)...)
	return out
}

func testBytesField(number int, value []byte) []byte {
	out := []byte{byte((number << 3) | protoWireBytes)}
	out = append(out, encodeProtoVarint(uint64(len(value)))...)
	out = append(out, value...)
	return out
}

func testFixed32Field(number int, value uint32) []byte {
	out := []byte{byte((number << 3) | protoWireFixed32)}
	var raw [4]byte
	binary.LittleEndian.PutUint32(raw[:], value)
	out = append(out, raw[:]...)
	return out
}
