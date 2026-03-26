package meshtastic

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

const bridgeDecodeFailureThreshold = 16
const bridgeKeepaliveInterval = 45 * time.Second

// RunNativeBridge reads native Meshtastic serial frames from a device path and
// emits normalized NDJSON events to out.
func RunNativeBridge(ctx context.Context, device string, out io.Writer, logger *slog.Logger) error {
	device = strings.TrimSpace(device)
	if device == "" {
		return fmt.Errorf("meshtastic bridge: device is required")
	}
	if logger == nil {
		logger = slog.Default()
	}

	stream, err := openReadWriteCloser(device)
	if err != nil {
		return fmt.Errorf("meshtastic bridge open device: %w", err)
	}
	defer stream.Close()

	// Match the legacy bridge startup behavior: request config once so nodes that
	// stay quiet until a control request still emit status/packet frames.
	if err := writeAll(stream, buildNativeFrame(buildToRadioWantConfigPayload(nativeSerialWantConfigID))); err != nil {
		logger.Warn("meshtastic bridge bootstrap write skipped", "device", device, "err", err)
	}
	keepaliveCtx, cancelKeepalive := context.WithCancel(ctx)
	defer cancelKeepalive()
	go runBridgeKeepalive(keepaliveCtx, stream, logger, device)

	encoder := json.NewEncoder(out)
	encoder.SetEscapeHTML(false)

	scanner := &nativeFrameScanner{
		reader:       bufio.NewReader(stream),
		maxPayload:   nativeMaxFramePayloadSize,
		maxDiscarded: nativeMaxDiscardedBytes,
	}

	decodeFailures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		payload, _, err := scanner.NextFrame()
		if err != nil {
			return fmt.Errorf("meshtastic bridge read frame: %w", err)
		}

		event, handled, err := decodeNativeFrame(payload, time.Now().UTC())
		if err != nil {
			decodeFailures++
			logger.Warn("meshtastic bridge decode failed", "device", device, "err", err, "consecutive_failures", decodeFailures)
			if decodeFailures >= bridgeDecodeFailureThreshold {
				return fmt.Errorf("meshtastic bridge decode failed repeatedly: %w", err)
			}
			continue
		}
		if !handled {
			continue
		}
		decodeFailures = 0

		record, ok := bridgeEventRecord(event)
		if !ok {
			continue
		}
		if err := encoder.Encode(record); err != nil {
			return fmt.Errorf("meshtastic bridge write event: %w", err)
		}
	}
}

func runBridgeKeepalive(ctx context.Context, stream io.Writer, logger *slog.Logger, device string) {
	ticker := time.NewTicker(bridgeKeepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := writeAll(stream, buildNativeFrame(buildToRadioWantConfigPayload(nativeSerialWantConfigID))); err != nil {
				logger.Warn("meshtastic bridge keepalive write skipped", "device", device, "err", err)
				continue
			}
			logger.Debug("meshtastic bridge keepalive sent", "device", device)
		}
	}
}

func bridgeEventRecord(event Event) (map[string]any, bool) {
	switch event.Kind {
	case EventPacket:
		if event.Packet == nil {
			return nil, false
		}
		receivedAt := event.Packet.ReceivedAt
		if receivedAt.IsZero() {
			receivedAt = event.Received
		}
		if receivedAt.IsZero() {
			receivedAt = time.Now().UTC()
		}
		record := map[string]any{
			"type":        "packet",
			"fromId":      event.Packet.SourceNodeID,
			"from":        event.Packet.SourceNodeID,
			"toId":        event.Packet.DestinationNodeID,
			"to":          event.Packet.DestinationNodeID,
			"port":        event.Packet.PortNum,
			"payload_b64": base64.StdEncoding.EncodeToString(event.Packet.Payload),
			"received_at": receivedAt.UTC().Format(time.RFC3339Nano),
		}
		decoded := map[string]any{
			"portnum": event.Packet.PortNum,
		}
		if len(event.Packet.Payload) > 0 {
			decoded["payload"] = base64.StdEncoding.EncodeToString(event.Packet.Payload)
			decoded["payload_encoding"] = "base64"
		}
		if event.Packet.Position != nil {
			record["position"] = map[string]any{
				"lat": event.Packet.Position.Lat,
				"lon": event.Packet.Position.Lon,
			}
			decoded["position"] = map[string]any{
				"latitude":  event.Packet.Position.Lat,
				"longitude": event.Packet.Position.Lon,
			}
		}
		record["decoded"] = decoded
		for key, value := range event.Packet.Meta {
			if key == "" || strings.TrimSpace(value) == "" {
				continue
			}
			record[key] = value
		}
		if packetID := strings.TrimSpace(event.Packet.Meta["packet_id"]); packetID != "" {
			record["id"] = packetID
		}
		return record, true
	case EventStatus:
		if event.Node == nil {
			return nil, false
		}
		receivedAt := event.Received
		if receivedAt.IsZero() {
			receivedAt = time.Now().UTC()
		}
		record := map[string]any{
			"type":              "status",
			"local_node_id":     event.Node.LocalNodeID,
			"observed_node_ids": append([]string(nil), event.Node.ObservedNodeIDs...),
			"received_at":       receivedAt.UTC().Format(time.RFC3339Nano),
		}
		if event.Node.HomeConfig != nil {
			cfg := event.Node.HomeConfig
			record["region"] = cfg.Region
			record["primary_channel"] = cfg.PrimaryChannel
			record["primary_channel_index"] = cfg.PrimaryChannelIdx
			record["psk_state"] = cfg.PSKState
			record["lora_preset"] = cfg.LoRaPreset
			record["lora_bandwidth"] = cfg.LoRaBandwidth
			record["lora_spreading"] = cfg.LoRaSpreading
			record["lora_coding_rate"] = cfg.LoRaCodingRate
			record["channel_url"] = cfg.ShareURL
			record["share_url"] = cfg.ShareURL
		}
		return record, true
	default:
		return nil, false
	}
}
