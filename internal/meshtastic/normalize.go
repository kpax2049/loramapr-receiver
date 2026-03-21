package meshtastic

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

func NormalizeLine(line []byte, now time.Time) (Event, error) {
	var raw map[string]any
	if err := json.Unmarshal(line, &raw); err != nil {
		return Event{}, fmt.Errorf("decode JSON: %w", err)
	}

	eventType := detectEventType(raw)
	switch eventType {
	case "packet", "meshtastic_packet", "mesh_packet":
		packet, received, err := normalizePacket(raw, now)
		if err != nil {
			return Event{}, err
		}
		return Event{Kind: EventPacket, Packet: &packet, Received: received}, nil
	case "status", "node_status", "config", "channel_config", "home_config":
		nodeStatus, received, err := normalizeNodeStatus(raw, now)
		if err != nil {
			return Event{}, err
		}
		return Event{Kind: EventStatus, Node: &nodeStatus, Received: received}, nil
	default:
		return Event{}, fmt.Errorf("unsupported meshtastic event type %q", eventType)
	}
}

func normalizePacket(raw map[string]any, now time.Time) (Packet, time.Time, error) {
	source := firstString(raw, "from", "from_id", "fromId", "source", "source_node_id", "sourceNodeId")
	if strings.TrimSpace(source) == "" {
		return Packet{}, time.Time{}, errors.New("packet source node ID is required")
	}
	destination := firstString(raw, "to", "to_id", "toId", "destination", "destination_node_id", "destinationNodeId")

	receivedAt := parseTime(raw, now, "received_at", "receivedAt", "rx_time", "rxTime", "timestamp")
	portNum := firstInt(raw, "port", "portnum", "port_num")
	if portNum == 0 {
		portNum = decodedPort(raw)
	}

	payload, err := decodePayload(raw)
	if err != nil {
		if !isCompatPacket(raw) {
			return Packet{}, time.Time{}, err
		}
		payload = nil
	}

	meta := map[string]string{}
	if rssi, ok := anyToString(raw["rssi"]); ok {
		meta["rssi"] = rssi
	}
	if rssi, ok := anyToString(raw["rxRssi"]); ok {
		meta["rssi"] = rssi
	}
	if snr, ok := anyToString(raw["snr"]); ok {
		meta["snr"] = snr
	}
	if snr, ok := anyToString(raw["rxSnr"]); ok {
		meta["snr"] = snr
	}
	if hop, ok := anyToString(raw["hop_limit"]); ok {
		meta["hop_limit"] = hop
	}
	if hop, ok := anyToString(raw["hopLimit"]); ok {
		meta["hop_limit"] = hop
	}
	if packetID, ok := anyToString(raw["id"]); ok {
		meta["packet_id"] = packetID
	}
	position := normalizePacketPosition(raw)

	return Packet{
		SourceNodeID:      strings.TrimSpace(source),
		DestinationNodeID: strings.TrimSpace(destination),
		PortNum:           portNum,
		Payload:           payload,
		ReceivedAt:        receivedAt,
		Position:          position,
		Meta:              meta,
	}, receivedAt, nil
}

func normalizeNodeStatus(raw map[string]any, now time.Time) (NodeStatus, time.Time, error) {
	localNodeID := strings.TrimSpace(firstString(raw, "local_node_id", "localNodeId", "node_id", "nodeId"))
	observed := stringList(raw, "observed_node_ids", "observedNodeIds", "nodes")
	homeCfg := normalizeHomeNodeConfig(raw)
	if localNodeID == "" && len(observed) == 0 && homeCfg == nil {
		return NodeStatus{}, time.Time{}, errors.New("status event missing node details")
	}

	return NodeStatus{
		LocalNodeID:     localNodeID,
		ObservedNodeIDs: observed,
		HomeConfig:      homeCfg,
	}, parseTime(raw, now, "received_at", "timestamp"), nil
}

func decodePayload(raw map[string]any) ([]byte, error) {
	if text := firstString(raw, "payload_b64", "payloadBase64", "payload_base64"); strings.TrimSpace(text) != "" {
		decoded, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, fmt.Errorf("decode payload_b64: %w", err)
		}
		return decoded, nil
	}
	if decoded, ok := decodePayloadFromCompatShape(raw); ok {
		return decoded, nil
	}

	payload := firstString(raw, "payload", "payload_text")
	if payload == "" {
		if values, ok := raw["payload_bytes"].([]any); ok {
			bytesOut := make([]byte, 0, len(values))
			for _, value := range values {
				intVal, ok := anyToInt(value)
				if !ok || intVal < 0 || intVal > 255 {
					continue
				}
				bytesOut = append(bytesOut, byte(intVal))
			}
			if len(bytesOut) > 0 {
				return bytesOut, nil
			}
		}
		return nil, errors.New("packet payload is required")
	}

	encoding := strings.ToLower(strings.TrimSpace(firstString(raw, "payload_encoding", "encoding")))
	if encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, fmt.Errorf("decode payload base64: %w", err)
		}
		return decoded, nil
	}

	return []byte(payload), nil
}

func parseTime(raw map[string]any, fallback time.Time, keys ...string) time.Time {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		if ts, ok := anyToTimestamp(value); ok {
			return ts.UTC()
		}
		text, ok := anyToString(value)
		if !ok {
			continue
		}
		if ts, err := time.Parse(time.RFC3339, text); err == nil {
			return ts.UTC()
		}
	}
	return fallback.UTC()
}

func stringList(raw map[string]any, keys ...string) []string {
	out := []string{}
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		items, ok := value.([]any)
		if !ok {
			continue
		}
		for _, item := range items {
			text, ok := anyToString(item)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			out = append(out, text)
		}
		if len(out) > 0 {
			break
		}
	}
	return mergeNodeIDs(nil, out)
}

func firstString(raw map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		text, ok := anyToString(value)
		if !ok {
			continue
		}
		if strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}

func firstInt(raw map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		intVal, ok := anyToInt(value)
		if ok {
			return intVal
		}
	}
	return 0
}

func anyToString(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	default:
		return "", false
	}
}

func anyToInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func anyToFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	default:
		return 0, false
	}
}

func normalizePacketPosition(raw map[string]any) *Position {
	if lat, lon, ok := directPosition(raw); ok {
		return &Position{Lat: lat, Lon: lon}
	}
	if nested, ok := raw["position"].(map[string]any); ok {
		if lat, lon, ok := directPosition(nested); ok {
			return &Position{Lat: lat, Lon: lon}
		}
	}
	if decoded := firstMap(raw, "decoded", "Decoded"); decoded != nil {
		if nested := firstMap(decoded, "position", "Position"); nested != nil {
			if lat, lon, ok := directPosition(nested); ok {
				return &Position{Lat: lat, Lon: lon}
			}
			if lat, lon, ok := intPosition(nested); ok {
				return &Position{Lat: lat, Lon: lon}
			}
		}
	}
	return nil
}

func directPosition(raw map[string]any) (float64, float64, bool) {
	lat, latOK := firstFloat(raw, "lat", "latitude", "lat_deg", "latitude_deg")
	lon, lonOK := firstFloat(raw, "lon", "lng", "longitude", "lon_deg", "longitude_deg")
	if !latOK || !lonOK {
		return 0, 0, false
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

func intPosition(raw map[string]any) (float64, float64, bool) {
	lat, latOK := firstFloat(raw, "latitude_i", "latitudeI", "lat_i", "latI")
	lon, lonOK := firstFloat(raw, "longitude_i", "longitudeI", "lon_i", "lonI")
	if !latOK || !lonOK {
		return 0, 0, false
	}
	lat = lat / 1e7
	lon = lon / 1e7
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}

func decodedPort(raw map[string]any) int {
	decoded := firstMap(raw, "decoded", "Decoded")
	if decoded == nil {
		return 0
	}
	if value := firstInt(decoded, "port", "portnum", "port_num"); value > 0 {
		return value
	}
	portName := strings.ToUpper(strings.TrimSpace(firstString(decoded, "portnum_name", "portnum", "port_name")))
	switch portName {
	case "TEXT_MESSAGE_APP":
		return 1
	case "POSITION_APP":
		return nativePortNumPositionApp
	case "NODEINFO_APP":
		return 4
	case "ROUTING_APP":
		return 5
	case "TELEMETRY_APP":
		return 67
	default:
		return 0
	}
}

func decodePayloadFromCompatShape(raw map[string]any) ([]byte, bool) {
	if decoded := firstMap(raw, "decoded", "Decoded"); decoded != nil {
		if payload := strings.TrimSpace(firstString(decoded, "payload", "raw", "payload_hex")); payload != "" {
			if bytesOut, err := decodeBestEffortBytes(payload); err == nil {
				return bytesOut, true
			}
		}
		if values, ok := decoded["payload_bytes"].([]any); ok {
			bytesOut := make([]byte, 0, len(values))
			for _, value := range values {
				intVal, ok := anyToInt(value)
				if !ok || intVal < 0 || intVal > 255 {
					continue
				}
				bytesOut = append(bytesOut, byte(intVal))
			}
			if len(bytesOut) > 0 {
				return bytesOut, true
			}
		}
	}

	if encrypted := strings.TrimSpace(firstString(raw, "encrypted", "encrypted_hex", "raw")); encrypted != "" {
		if bytesOut, err := decodeBestEffortBytes(encrypted); err == nil {
			return bytesOut, true
		}
	}
	return nil, false
}

func decodeBestEffortBytes(value string) ([]byte, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, errors.New("empty payload text")
	}
	if bytesOut, err := hex.DecodeString(trimmed); err == nil {
		return bytesOut, nil
	}
	if bytesOut, err := base64.StdEncoding.DecodeString(trimmed); err == nil {
		return bytesOut, nil
	}
	return []byte(trimmed), nil
}

func anyToTimestamp(value any) (time.Time, bool) {
	switch typed := value.(type) {
	case float64:
		return unixTimestamp(typed)
	case int:
		return unixTimestamp(float64(typed))
	case int64:
		return unixTimestamp(float64(typed))
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return time.Time{}, false
		}
		if parsed, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return unixTimestamp(parsed)
		}
	}
	return time.Time{}, false
}

func unixTimestamp(value float64) (time.Time, bool) {
	if value <= 0 {
		return time.Time{}, false
	}
	seconds := value
	// Treat very large values as milliseconds.
	if seconds > 1e12 {
		seconds = seconds / 1000
	}
	secPart := int64(seconds)
	nsecPart := int64((seconds - float64(secPart)) * float64(time.Second))
	return time.Unix(secPart, nsecPart).UTC(), true
}

func detectEventType(raw map[string]any) string {
	eventType := strings.ToLower(firstString(raw, "type", "event_type", "kind"))
	if eventType != "" {
		return eventType
	}
	if isCompatPacket(raw) {
		return "packet"
	}
	if firstMap(raw, "channel", "primary_channel_config", "primaryChannelConfig") != nil {
		return "status"
	}
	if firstString(raw, "region", "primary_channel", "primaryChannel") != "" {
		return "status"
	}
	return eventType
}

func isCompatPacket(raw map[string]any) bool {
	if firstString(raw, "fromId", "from", "from_id", "source") != "" {
		return true
	}
	if firstMap(raw, "decoded", "Decoded") != nil {
		return true
	}
	if _, ok := raw["rxTime"]; ok {
		return true
	}
	if _, ok := raw["rxSnr"]; ok {
		return true
	}
	return false
}

func firstFloat(raw map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		if out, ok := anyToFloat(value); ok {
			return out, true
		}
	}
	return 0, false
}

func normalizeHomeNodeConfig(raw map[string]any) *HomeNodeConfigSummary {
	region := strings.TrimSpace(firstString(raw, "region", "lora_region", "loraRegion"))
	primaryName := strings.TrimSpace(firstString(raw, "primary_channel", "primaryChannel", "channel_name", "channelName"))
	primaryIdx := firstInt(raw, "primary_channel_index", "primaryChannelIndex", "channel_index", "channelIndex")
	pskState := normalizePSKState(
		raw,
		firstString(raw, "psk_state", "pskState"),
		firstBool(raw, "primary_channel_psk_present", "primaryChannelPskPresent", "psk_present", "pskPresent", "has_psk"),
	)
	loraPreset := strings.TrimSpace(firstString(raw, "lora_preset", "loraPreset", "modem_preset", "modemPreset"))
	loraBandwidth := strings.TrimSpace(firstString(raw, "lora_bw", "loraBw", "bandwidth", "lora_bandwidth"))
	loraSpreading := strings.TrimSpace(firstString(raw, "lora_sf", "loraSf", "spreading_factor", "spreadingFactor"))
	loraCodingRate := strings.TrimSpace(firstString(raw, "lora_cr", "loraCr", "coding_rate", "codingRate"))
	shareURL := strings.TrimSpace(firstString(raw,
		"channel_url",
		"channelUrl",
		"share_url",
		"shareUrl",
		"primary_channel_url",
		"primaryChannelUrl",
	))

	channel := firstMap(raw, "channel", "primary_channel_config", "primaryChannelConfig")
	if channel != nil {
		if region == "" {
			region = strings.TrimSpace(firstString(channel, "region", "lora_region", "loraRegion"))
		}
		if primaryName == "" {
			primaryName = strings.TrimSpace(firstString(channel, "name", "channel_name", "channelName", "primary_name"))
		}
		if primaryIdx == 0 {
			primaryIdx = firstInt(channel, "index", "channel_index", "channelIndex")
		}
		if pskState == "unknown" {
			pskState = normalizePSKState(
				channel,
				firstString(channel, "psk_state", "pskState"),
				firstBool(channel, "psk_present", "pskPresent", "has_psk"),
			)
		}
		if loraPreset == "" {
			loraPreset = strings.TrimSpace(firstString(channel, "lora_preset", "loraPreset", "modem_preset", "modemPreset"))
		}
		if loraBandwidth == "" {
			loraBandwidth = strings.TrimSpace(firstString(channel, "lora_bw", "loraBw", "bandwidth", "lora_bandwidth"))
		}
		if loraSpreading == "" {
			loraSpreading = strings.TrimSpace(firstString(channel, "lora_sf", "loraSf", "spreading_factor", "spreadingFactor"))
		}
		if loraCodingRate == "" {
			loraCodingRate = strings.TrimSpace(firstString(channel, "lora_cr", "loraCr", "coding_rate", "codingRate"))
		}
		if shareURL == "" {
			shareURL = strings.TrimSpace(firstString(channel, "channel_url", "channelUrl", "share_url", "shareUrl"))
		}
	}

	hasData := region != "" || primaryName != "" || primaryIdx > 0 || pskState != "unknown" ||
		loraPreset != "" || loraBandwidth != "" || loraSpreading != "" || loraCodingRate != "" || shareURL != ""
	if !hasData {
		return nil
	}

	shareRedacted := redactShareURL(shareURL)
	shareAvailable := shareURL != ""
	return &HomeNodeConfigSummary{
		Available:         true,
		UnavailableReason: "",
		Region:            region,
		PrimaryChannel:    primaryName,
		PrimaryChannelIdx: primaryIdx,
		PSKState:          pskState,
		LoRaPreset:        loraPreset,
		LoRaBandwidth:     loraBandwidth,
		LoRaSpreading:     loraSpreading,
		LoRaCodingRate:    loraCodingRate,
		ShareURL:          shareURL,
		ShareURLRedacted:  shareRedacted,
		ShareURLAvailable: shareAvailable,
		ShareQRText:       shareURL,
		Source:            "status_event",
	}
}

func firstMap(raw map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok || value == nil {
			continue
		}
		if typed, ok := value.(map[string]any); ok {
			return typed
		}
	}
	return nil
}

func firstBool(raw map[string]any, keys ...string) *bool {
	for _, key := range keys {
		value, ok := raw[key]
		if !ok {
			continue
		}
		typed, ok := anyToBool(value)
		if !ok {
			continue
		}
		return &typed
	}
	return nil
}

func anyToBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "1", "true", "yes", "on", "present", "set":
			return true, true
		case "0", "false", "no", "off", "unset", "missing":
			return false, true
		default:
			return false, false
		}
	case int:
		return typed != 0, true
	case int64:
		return typed != 0, true
	case float64:
		return typed != 0, true
	default:
		return false, false
	}
}

func normalizePSKState(raw map[string]any, explicit string, present *bool) string {
	value := strings.ToLower(strings.TrimSpace(explicit))
	switch value {
	case "present", "set", "configured", "available":
		return "present"
	case "not_set", "unset", "missing", "none", "absent":
		return "not_set"
	case "unknown":
		return "unknown"
	}
	if present != nil {
		if *present {
			return "present"
		}
		return "not_set"
	}
	if inferred := firstBool(raw, "psk_present", "pskPresent", "has_psk", "hasPsk"); inferred != nil {
		if *inferred {
			return "present"
		}
		return "not_set"
	}
	return "unknown"
}

func redactShareURL(value string) string {
	text := strings.TrimSpace(value)
	if text == "" {
		return ""
	}
	out := text
	if idx := strings.Index(out, "#"); idx >= 0 {
		return out[:idx+1] + "<redacted>"
	}
	if idx := strings.Index(out, "?"); idx >= 0 {
		return out[:idx+1] + "<redacted>"
	}
	if len(out) <= 24 {
		return "<redacted>"
	}
	return out[:12] + "..." + out[len(out)-6:]
}
