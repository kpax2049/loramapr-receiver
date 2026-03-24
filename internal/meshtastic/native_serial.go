package meshtastic

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	nativeSerialStart1        byte = 0x94
	nativeSerialStart2        byte = 0xC3
	nativeSerialWantConfigID       = 69420
	nativeMaxFramePayloadSize      = 4096
	nativeMaxDiscardedBytes        = 256 * 1024
	nativePortNumPositionApp       = 3
	nativeNoFrameHoldDelay         = 10 * time.Second
	nativeDecodeHoldDelay          = 5 * time.Second
	nativeMaxAcceptedRXSkew        = 15 * time.Minute
)

var errNoNativeFrames = errors.New("no Meshtastic native serial frames detected")

func (s *Service) bootstrapNativeSession(stream io.Writer) error {
	frame := buildNativeFrame(buildToRadioWantConfigPayload(nativeSerialWantConfigID))
	if err := writeAll(stream, frame); err != nil {
		return fmt.Errorf("native serial bootstrap write: %w", err)
	}
	return nil
}

func (s *Service) consumeNativeSerial(ctx context.Context, device string, reader io.Reader, out chan<- Event) error {
	scanner := &nativeFrameScanner{
		reader:       bufio.NewReader(reader),
		maxPayload:   nativeMaxFramePayloadSize,
		maxDiscarded: nativeMaxDiscardedBytes,
	}

	decodeFailures := 0
	noFrameSignals := 0
	for {
		payload, discarded, err := scanner.NextFrame()
		if err != nil {
			if errors.Is(err, errNoNativeFrames) {
				noFrameSignals++
				s.setSnapshot(func(snap *Snapshot) {
					snap.State = StateDegraded
					snap.DetectedDevice = device
					snap.LastError = "native serial frames not detected yet; keeping device connection open"
				})
				s.logger.Warn(
					"native serial frames not detected; keeping connection open",
					"device", device,
					"signals", noFrameSignals,
				)
				scanner.resetDiscarded()
				if !waitOrDone(ctx, nativeNoFrameHoldDelay) {
					return ctx.Err()
				}
				continue
			}
			return err
		}
		noFrameSignals = 0
		if discarded > 0 {
			s.logger.Debug("discarded non-frame bytes before native serial frame", "device", device, "discarded_bytes", discarded)
		}

		event, handled, err := decodeNativeFrame(payload, time.Now().UTC())
		if err != nil {
			decodeFailures++
			s.logger.Warn("failed to decode native Meshtastic frame", "device", device, "err", err, "consecutive_failures", decodeFailures)
			if decodeFailures >= 5 {
				s.setSnapshot(func(snap *Snapshot) {
					snap.State = StateDegraded
					snap.DetectedDevice = device
					snap.LastError = "native serial decode failed repeatedly; holding connection for recovery"
				})
				decodeFailures = 0
				if !waitOrDone(ctx, nativeDecodeHoldDelay) {
					return ctx.Err()
				}
			}
			continue
		}
		if !handled {
			decodeFailures = 0
			continue
		}
		decodeFailures = 0

		if event.Received.IsZero() {
			event.Received = time.Now().UTC()
		}
		event.RawLine = fmt.Sprintf("native_frame_len=%d", len(payload))
		s.applyEvent(device, event)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- event:
		}
	}
}

type nativeFrameScanner struct {
	reader       *bufio.Reader
	maxPayload   int
	maxDiscarded int
	discarded    int
	seenFrames   int
}

func (s *nativeFrameScanner) resetDiscarded() {
	s.discarded = 0
}

func (s *nativeFrameScanner) NextFrame() ([]byte, int, error) {
	if s.maxPayload <= 0 {
		s.maxPayload = nativeMaxFramePayloadSize
	}
	if s.maxDiscarded <= 0 {
		s.maxDiscarded = nativeMaxDiscardedBytes
	}

	discardedThisFrame := 0
	haveStart1 := false

	for {
		value, err := s.reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) && s.seenFrames == 0 && s.discarded+discardedThisFrame > 0 {
				return nil, discardedThisFrame, errNoNativeFrames
			}
			return nil, discardedThisFrame, err
		}

		if !haveStart1 {
			if value == nativeSerialStart1 {
				haveStart1 = true
				continue
			}
			discardedThisFrame++
			s.discarded++
			if s.discarded > s.maxDiscarded {
				return nil, discardedThisFrame, errNoNativeFrames
			}
			continue
		}

		if value != nativeSerialStart2 {
			discardedThisFrame++
			s.discarded++
			if s.discarded > s.maxDiscarded {
				return nil, discardedThisFrame, errNoNativeFrames
			}
			haveStart1 = (value == nativeSerialStart1)
			continue
		}

		msb, err := s.reader.ReadByte()
		if err != nil {
			return nil, discardedThisFrame, err
		}
		lsb, err := s.reader.ReadByte()
		if err != nil {
			return nil, discardedThisFrame, err
		}
		length := int(msb)<<8 | int(lsb)
		if length <= 0 || length > s.maxPayload {
			discardedThisFrame += 4
			s.discarded += 4
			if s.discarded > s.maxDiscarded {
				return nil, discardedThisFrame, errNoNativeFrames
			}
			haveStart1 = false
			continue
		}

		payload := make([]byte, length)
		if _, err := io.ReadFull(s.reader, payload); err != nil {
			return nil, discardedThisFrame, err
		}

		s.discarded = 0
		s.seenFrames++
		return payload, discardedThisFrame, nil
	}
}

func buildNativeFrame(payload []byte) []byte {
	frame := make([]byte, 0, len(payload)+4)
	frame = append(frame, nativeSerialStart1, nativeSerialStart2)
	frame = append(frame, byte(len(payload)>>8), byte(len(payload)&0xff))
	frame = append(frame, payload...)
	return frame
}

func buildToRadioWantConfigPayload(id uint32) []byte {
	// meshtastic.ToRadio.want_config_id = field #3 (varint)
	payload := []byte{byte((3 << 3) | protoWireVarint)}
	payload = append(payload, encodeProtoVarint(uint64(id))...)
	return payload
}

func writeAll(writer io.Writer, payload []byte) error {
	remaining := payload
	for len(remaining) > 0 {
		written, err := writer.Write(remaining)
		if err != nil {
			return err
		}
		if written <= 0 {
			return io.ErrShortWrite
		}
		remaining = remaining[written:]
	}
	return nil
}

func decodeNativeFrame(payload []byte, now time.Time) (Event, bool, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return Event{}, false, fmt.Errorf("decode FromRadio frame: %w", err)
	}

	status := NodeStatus{}
	hasStatus := false

	for _, field := range fields {
		switch field.Number {
		case 2: // FromRadio.packet
			if field.WireType != protoWireBytes {
				continue
			}
			packet, err := decodeNativePacket(field.Bytes, now)
			if err != nil {
				return Event{}, false, err
			}
			receivedAt := packet.ReceivedAt
			if receivedAt.IsZero() {
				receivedAt = now.UTC()
			}
			return Event{Kind: EventPacket, Packet: &packet, Received: receivedAt}, true, nil
		case 3: // FromRadio.my_info
			if field.WireType != protoWireBytes {
				continue
			}
			nodeNum, ok, err := decodeNativeMyNodeNum(field.Bytes)
			if err != nil {
				return Event{}, false, err
			}
			if ok {
				status.LocalNodeID = formatNodeID(nodeNum)
				hasStatus = true
			}
		case 4: // FromRadio.node_info
			if field.WireType != protoWireBytes {
				continue
			}
			nodeID, err := decodeNativeObservedNodeID(field.Bytes)
			if err != nil {
				return Event{}, false, err
			}
			if nodeID != "" {
				status.ObservedNodeIDs = mergeNodeIDs(status.ObservedNodeIDs, []string{nodeID})
				hasStatus = true
			}
		case 5: // FromRadio.config
			if field.WireType != protoWireBytes {
				continue
			}
			cfg, err := decodeNativeConfigSummary(field.Bytes)
			if err != nil {
				return Event{}, false, err
			}
			if cfg != nil {
				status.HomeConfig = mergeHomeNodeConfig(status.HomeConfig, cfg)
				hasStatus = true
			}
		case 10: // FromRadio.channel
			if field.WireType != protoWireBytes {
				continue
			}
			cfg, err := decodeNativeChannelSummary(field.Bytes)
			if err != nil {
				return Event{}, false, err
			}
			if cfg != nil {
				status.HomeConfig = mergeHomeNodeConfig(status.HomeConfig, cfg)
				hasStatus = true
			}
		}
	}

	if !hasStatus {
		return Event{}, false, nil
	}
	return Event{
		Kind:     EventStatus,
		Node:     &status,
		Received: now.UTC(),
	}, true, nil
}

func decodeNativeMyNodeNum(payload []byte) (uint32, bool, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return 0, false, fmt.Errorf("decode MyNodeInfo: %w", err)
	}
	for _, field := range fields {
		if field.Number != 1 {
			continue
		}
		if value, ok := fieldUint32(field); ok && value > 0 {
			return value, true, nil
		}
	}
	return 0, false, nil
}

func decodeNativeObservedNodeID(payload []byte) (string, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return "", fmt.Errorf("decode NodeInfo: %w", err)
	}

	var nodeNum uint32
	var userID string
	for _, field := range fields {
		switch field.Number {
		case 1:
			if value, ok := fieldUint32(field); ok {
				nodeNum = value
			}
		case 2:
			if field.WireType != protoWireBytes {
				continue
			}
			id, err := decodeNativeUserID(field.Bytes)
			if err != nil {
				return "", err
			}
			userID = strings.TrimSpace(id)
		}
	}

	if userID != "" {
		return userID, nil
	}
	return formatNodeID(nodeNum), nil
}

func decodeNativeUserID(payload []byte) (string, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return "", fmt.Errorf("decode User: %w", err)
	}
	for _, field := range fields {
		if field.Number == 1 && field.WireType == protoWireBytes {
			return string(field.Bytes), nil
		}
	}
	return "", nil
}

func decodeNativePacket(payload []byte, now time.Time) (Packet, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return Packet{}, fmt.Errorf("decode MeshPacket: %w", err)
	}

	var sourceNum uint32
	var destNum uint32
	var channelNum uint64
	var packetID uint32
	var hopLimit uint64
	var rssiValue int32
	var snrBits uint32
	var rxTime uint32
	var decoded *nativeDecodedData
	var encryptedPayload []byte

	meta := map[string]string{}
	for _, field := range fields {
		switch field.Number {
		case 1: // from
			if value, ok := fieldUint32(field); ok {
				sourceNum = value
			}
		case 2: // to
			if value, ok := fieldUint32(field); ok {
				destNum = value
			}
		case 3: // channel
			if field.WireType == protoWireVarint {
				channelNum = field.Varint
			}
		case 4: // decoded
			if field.WireType != protoWireBytes {
				continue
			}
			data, err := decodeNativeDecodedData(field.Bytes)
			if err != nil {
				return Packet{}, err
			}
			decoded = data
		case 5: // encrypted
			if field.WireType == protoWireBytes {
				encryptedPayload = append([]byte(nil), field.Bytes...)
			}
		case 6: // id
			if value, ok := fieldUint32(field); ok {
				packetID = value
			}
		case 7: // rx_time
			if value, ok := fieldUint32(field); ok {
				rxTime = value
			}
		case 8: // rx_snr
			if field.WireType == protoWireFixed32 {
				snrBits = field.Fixed32
			}
		case 9: // hop_limit
			if field.WireType == protoWireVarint {
				hopLimit = field.Varint
			}
		case 12: // rx_rssi
			if field.WireType == protoWireVarint {
				rssiValue = int32(uint32(field.Varint))
			}
		}
	}

	if decoded != nil {
		if decoded.SourceNum > 0 {
			sourceNum = decoded.SourceNum
		}
		if decoded.DestinationNum > 0 {
			destNum = decoded.DestinationNum
		}
	}

	sourceNodeID := formatNodeID(sourceNum)
	if sourceNodeID == "" {
		sourceNodeID = "!unknown"
	}
	destNodeID := formatNodeID(destNum)

	payloadBytes := encryptedPayload
	portNum := 0
	if decoded != nil {
		portNum = decoded.PortNum
		if len(decoded.Payload) > 0 {
			payloadBytes = decoded.Payload
		}
	}

	receivedAt := normalizedNativeRXTime(now.UTC(), rxTime)
	if channelNum > 0 {
		meta["channel"] = strconv.FormatUint(channelNum, 10)
	}
	if rxTime > 0 {
		meta["rx_time_unix"] = strconv.FormatUint(uint64(rxTime), 10)
		candidate := time.Unix(int64(rxTime), 0).UTC()
		if !candidate.Equal(receivedAt) {
			meta["rx_time_rejected"] = "true"
			meta["rx_time_rejected_reason"] = "clock_skew"
		}
	}
	if packetID > 0 {
		meta["packet_id"] = strconv.FormatUint(uint64(packetID), 10)
	}
	if hopLimit > 0 {
		meta["hop_limit"] = strconv.FormatUint(hopLimit, 10)
	}
	if snrBits != 0 {
		snr := math.Float32frombits(snrBits)
		meta["snr"] = strconv.FormatFloat(float64(snr), 'f', 2, 32)
	}
	if rssiValue != 0 {
		meta["rssi"] = strconv.FormatInt(int64(rssiValue), 10)
	}
	if decoded == nil || len(decoded.Payload) == 0 {
		meta["encrypted"] = "true"
	}

	packet := Packet{
		SourceNodeID:      sourceNodeID,
		DestinationNodeID: destNodeID,
		PortNum:           portNum,
		Payload:           payloadBytes,
		ReceivedAt:        receivedAt,
		Meta:              meta,
	}

	if decoded != nil && portNum == nativePortNumPositionApp {
		packet.Position = decodeNativePositionPayload(decoded.Payload)
	}
	return packet, nil
}

func normalizedNativeRXTime(now time.Time, rxTime uint32) time.Time {
	ref := now.UTC()
	if rxTime == 0 {
		return ref
	}
	candidate := time.Unix(int64(rxTime), 0).UTC()
	if candidate.Before(ref.Add(-nativeMaxAcceptedRXSkew)) || candidate.After(ref.Add(nativeMaxAcceptedRXSkew)) {
		return ref
	}
	return candidate
}

type nativeDecodedData struct {
	PortNum        int
	Payload        []byte
	DestinationNum uint32
	SourceNum      uint32
}

func decodeNativeDecodedData(payload []byte) (*nativeDecodedData, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return nil, fmt.Errorf("decode Data payload: %w", err)
	}

	out := &nativeDecodedData{}
	for _, field := range fields {
		switch field.Number {
		case 1: // portnum
			if field.WireType == protoWireVarint {
				out.PortNum = int(field.Varint)
			}
		case 2: // payload
			if field.WireType == protoWireBytes {
				out.Payload = append([]byte(nil), field.Bytes...)
			}
		case 4: // dest
			if value, ok := fieldUint32(field); ok {
				out.DestinationNum = value
			}
		case 5: // source
			if value, ok := fieldUint32(field); ok {
				out.SourceNum = value
			}
		}
	}
	return out, nil
}

func decodeNativePositionPayload(payload []byte) *Position {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return nil
	}

	var lat, lon int32
	haveLat := false
	haveLon := false
	for _, field := range fields {
		switch field.Number {
		case 1:
			if field.WireType == protoWireFixed32 {
				lat = int32(field.Fixed32)
				haveLat = true
			}
		case 2:
			if field.WireType == protoWireFixed32 {
				lon = int32(field.Fixed32)
				haveLon = true
			}
		}
	}
	if !haveLat || !haveLon {
		return nil
	}

	latF := float64(lat) / 1e7
	lonF := float64(lon) / 1e7
	if latF < -90 || latF > 90 || lonF < -180 || lonF > 180 {
		return nil
	}
	return &Position{Lat: latF, Lon: lonF}
}

func decodeNativeConfigSummary(payload []byte) (*HomeNodeConfigSummary, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return nil, fmt.Errorf("decode Config: %w", err)
	}

	for _, field := range fields {
		if field.Number != 6 || field.WireType != protoWireBytes { // Config.lora
			continue
		}
		lora, err := decodeNativeLoRaConfig(field.Bytes)
		if err != nil {
			return nil, err
		}
		return lora, nil
	}
	return nil, nil
}

func decodeNativeLoRaConfig(payload []byte) (*HomeNodeConfigSummary, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return nil, fmt.Errorf("decode LoRaConfig: %w", err)
	}

	region := ""
	loraPreset := ""
	loraBandwidth := ""
	loraSpreading := ""
	loraCodingRate := ""

	for _, field := range fields {
		switch field.Number {
		case 7: // region
			if field.WireType == protoWireVarint {
				region = nativeRegionName(field.Varint)
			}
		case 2: // modem_preset
			if field.WireType == protoWireVarint {
				loraPreset = nativeModemPresetName(field.Varint)
			}
		case 3: // bandwidth
			if field.WireType == protoWireVarint {
				loraBandwidth = strconv.FormatUint(field.Varint, 10)
			}
		case 4: // spread_factor
			if field.WireType == protoWireVarint {
				loraSpreading = "SF" + strconv.FormatUint(field.Varint, 10)
			}
		case 5: // coding_rate
			if field.WireType == protoWireVarint {
				loraCodingRate = "4/" + strconv.FormatUint(field.Varint, 10)
			}
		}
	}

	if region == "" && loraPreset == "" && loraBandwidth == "" && loraSpreading == "" && loraCodingRate == "" {
		return nil, nil
	}

	return &HomeNodeConfigSummary{
		Available:      true,
		Region:         region,
		LoRaPreset:     loraPreset,
		LoRaBandwidth:  loraBandwidth,
		LoRaSpreading:  loraSpreading,
		LoRaCodingRate: loraCodingRate,
		PSKState:       "unknown",
		Source:         "native_serial",
	}, nil
}

func decodeNativeChannelSummary(payload []byte) (*HomeNodeConfigSummary, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return nil, fmt.Errorf("decode Channel: %w", err)
	}

	index := 0
	var settings []byte
	for _, field := range fields {
		switch field.Number {
		case 1:
			if field.WireType == protoWireVarint {
				index = int(field.Varint)
			}
		case 2:
			if field.WireType == protoWireBytes {
				settings = append([]byte(nil), field.Bytes...)
			}
		}
	}

	if len(settings) == 0 {
		return nil, nil
	}
	name, pskState, err := decodeNativeChannelSettings(settings)
	if err != nil {
		return nil, err
	}
	if name == "" && pskState == "unknown" && index == 0 {
		return nil, nil
	}

	return &HomeNodeConfigSummary{
		Available:         true,
		PrimaryChannel:    name,
		PrimaryChannelIdx: index,
		PSKState:          pskState,
		Source:            "native_serial",
	}, nil
}

func decodeNativeChannelSettings(payload []byte) (string, string, error) {
	fields, err := decodeProtoFields(payload)
	if err != nil {
		return "", "unknown", fmt.Errorf("decode ChannelSettings: %w", err)
	}

	name := ""
	pskState := "unknown"
	for _, field := range fields {
		switch field.Number {
		case 2:
			if field.WireType != protoWireBytes {
				continue
			}
			pskState = pskStateFromBytes(field.Bytes)
		case 3:
			if field.WireType == protoWireBytes {
				name = strings.TrimSpace(string(field.Bytes))
			}
		}
	}
	return name, pskState, nil
}

func pskStateFromBytes(value []byte) string {
	if len(value) == 0 {
		return "not_set"
	}
	for _, b := range value {
		if b != 0 {
			return "present"
		}
	}
	return "not_set"
}

func formatNodeID(value uint32) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("!%08x", value)
}

func nativeRegionName(value uint64) string {
	switch value {
	case 1:
		return "US"
	case 2:
		return "EU_433"
	case 3:
		return "EU_868"
	case 4:
		return "CN"
	case 5:
		return "JP"
	case 6:
		return "ANZ"
	case 7:
		return "RU"
	case 8:
		return "KR"
	case 9:
		return "TW"
	case 10:
		return "IN"
	case 11:
		return "NZ_865_868"
	case 12:
		return "TH"
	case 13:
		return "LORA_24"
	case 14:
		return "UA_433"
	case 15:
		return "UA_868"
	case 16:
		return "MY_433"
	case 17:
		return "MY_919"
	case 18:
		return "SG_923"
	default:
		return ""
	}
}

func nativeModemPresetName(value uint64) string {
	switch value {
	case 0:
		return "LONG_FAST"
	case 1:
		return "LONG_SLOW"
	case 2:
		return "VERY_LONG_SLOW"
	case 3:
		return "MEDIUM_SLOW"
	case 4:
		return "MEDIUM_FAST"
	case 5:
		return "SHORT_SLOW"
	case 6:
		return "SHORT_FAST"
	case 7:
		return "LONG_MODERATE"
	case 8:
		return "SHORT_TURBO"
	default:
		return ""
	}
}

const (
	protoWireVarint  = 0
	protoWireFixed64 = 1
	protoWireBytes   = 2
	protoWireFixed32 = 5
)

type protoField struct {
	Number   int
	WireType int
	Varint   uint64
	Fixed32  uint32
	Fixed64  uint64
	Bytes    []byte
}

func decodeProtoFields(payload []byte) ([]protoField, error) {
	fields := make([]protoField, 0, 12)
	index := 0
	for index < len(payload) {
		key, consumed, err := decodeProtoVarint(payload[index:])
		if err != nil {
			return nil, err
		}
		index += consumed
		if key == 0 {
			return nil, errors.New("invalid protobuf key 0")
		}

		field := protoField{
			Number:   int(key >> 3),
			WireType: int(key & 0x07),
		}

		switch field.WireType {
		case protoWireVarint:
			value, n, err := decodeProtoVarint(payload[index:])
			if err != nil {
				return nil, err
			}
			field.Varint = value
			index += n
		case protoWireFixed64:
			if len(payload[index:]) < 8 {
				return nil, io.ErrUnexpectedEOF
			}
			field.Fixed64 = binary.LittleEndian.Uint64(payload[index : index+8])
			index += 8
		case protoWireBytes:
			length, n, err := decodeProtoVarint(payload[index:])
			if err != nil {
				return nil, err
			}
			index += n
			if length > uint64(len(payload[index:])) {
				return nil, io.ErrUnexpectedEOF
			}
			field.Bytes = append([]byte(nil), payload[index:index+int(length)]...)
			index += int(length)
		case protoWireFixed32:
			if len(payload[index:]) < 4 {
				return nil, io.ErrUnexpectedEOF
			}
			field.Fixed32 = binary.LittleEndian.Uint32(payload[index : index+4])
			index += 4
		default:
			return nil, fmt.Errorf("unsupported protobuf wire type %d", field.WireType)
		}

		fields = append(fields, field)
	}
	return fields, nil
}

func decodeProtoVarint(payload []byte) (uint64, int, error) {
	var out uint64
	for index := 0; index < len(payload) && index < 10; index++ {
		b := payload[index]
		out |= uint64(b&0x7f) << (7 * index)
		if b < 0x80 {
			return out, index + 1, nil
		}
	}
	if len(payload) == 0 {
		return 0, 0, io.ErrUnexpectedEOF
	}
	return 0, 0, errors.New("invalid protobuf varint")
}

func encodeProtoVarint(value uint64) []byte {
	if value == 0 {
		return []byte{0}
	}
	out := make([]byte, 0, 10)
	for value >= 0x80 {
		out = append(out, byte(value&0x7f)|0x80)
		value >>= 7
	}
	out = append(out, byte(value))
	return out
}

func fieldUint32(field protoField) (uint32, bool) {
	switch field.WireType {
	case protoWireVarint:
		return uint32(field.Varint), true
	case protoWireFixed32:
		return field.Fixed32, true
	default:
		return 0, false
	}
}
