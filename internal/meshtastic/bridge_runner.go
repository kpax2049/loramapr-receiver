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

	stream, err := openReadOnlyCloser(device)
	if err != nil {
		return fmt.Errorf("meshtastic bridge open device: %w", err)
	}
	defer stream.Close()

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
			"from":        event.Packet.SourceNodeID,
			"to":          event.Packet.DestinationNodeID,
			"port":        event.Packet.PortNum,
			"payload_b64": base64.StdEncoding.EncodeToString(event.Packet.Payload),
			"received_at": receivedAt.UTC().Format(time.RFC3339Nano),
		}
		if event.Packet.Position != nil {
			record["position"] = map[string]any{
				"lat": event.Packet.Position.Lat,
				"lon": event.Packet.Position.Lon,
			}
		}
		for key, value := range event.Packet.Meta {
			if key == "" || strings.TrimSpace(value) == "" {
				continue
			}
			record[key] = value
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
