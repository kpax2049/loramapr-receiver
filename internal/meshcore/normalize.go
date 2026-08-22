package meshcore

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

const maximumAuthoritativeClockSkew = 10 * time.Minute

type ReceiverBinding struct {
	ReceiverAgentID string
	InstallationID  string
	AdapterVersion  string
	Clock           map[string]any
}

func NormalizeAdapterEvent(event protocoladapter.Event, binding ReceiverBinding, session Snapshot) (map[string]any, error) {
	if strings.TrimSpace(binding.InstallationID) == "" {
		return nil, errors.New("meshcore normalized event requires receiver installation ID")
	}
	if strings.TrimSpace(binding.AdapterVersion) == "" {
		return nil, errors.New("meshcore normalized event requires adapter version")
	}
	if event.ObservedAt.IsZero() {
		return nil, errors.New("meshcore adapter event requires observation time")
	}
	frame, ok := adapterPushFrame(event.Value)
	if !ok {
		return nil, fmt.Errorf("meshcore adapter event has unsupported value %T", event.Value)
	}
	if err := validateCapturedPush(frame.Payload); err != nil {
		return nil, err
	}
	if frame.Opcode != frame.Payload[0] {
		return nil, fmt.Errorf("%w: adapter opcode 0x%02x does not match payload opcode 0x%02x", ErrInvalidPush, frame.Opcode, frame.Payload[0])
	}

	base := normalizedBase(event, binding, frame)
	if err := validatePush(frame.Payload); err != nil {
		return normalizeCapturedRaw(base, frame.Payload), nil
	}
	// A LOG_RX frame contains complete on-air bytes. Its ADVERT signature is
	// independently verifiable and is therefore not delegated Companion trust.
	// Profile matching remains mandatory for semantic interpretation of the
	// delegated Companion-only surface below, especially NEW_ADVERT (0x8A).
	if frame.Opcode == PushLogRXData {
		return normalizeLogRX(base, frame.Payload, event.ObservedAt), nil
	}
	if !semanticProfileMatched(session) {
		return normalizeCapturedRaw(base, frame.Payload), nil
	}
	switch frame.Opcode {
	case PushRawData:
		return normalizeRawData(base, frame.Payload), nil
	case PushControlData:
		return normalizeControlData(base, frame.Payload), nil
	case PushNewAdvert:
		return normalizeDelegatedAdvert(base, frame.Payload, event.ObservedAt, session), nil
	default:
		return normalizeCapturedRaw(base, frame.Payload), nil
	}
}

func semanticProfileMatched(session Snapshot) bool {
	return session.State == SessionReady &&
		session.Trust.ProtocolCompatible &&
		session.Trust.ProfileMatched &&
		session.Trust.ProtocolVersion == ProtocolVersion &&
		session.Trust.FirmwareVersion == PinnedFirmwareVersion &&
		session.Trust.FirmwareBuild == PinnedFirmwareBuild &&
		strings.TrimSpace(session.Trust.Model) != "" &&
		session.Trust.AllowlistCommit == PinnedSourceCommit
}

func adapterPushFrame(value any) (PushFrame, bool) {
	switch typed := value.(type) {
	case PushFrame:
		return PushFrame{Opcode: typed.Opcode, Payload: append([]byte(nil), typed.Payload...)}, true
	case *PushFrame:
		if typed == nil {
			return PushFrame{}, false
		}
		return PushFrame{Opcode: typed.Opcode, Payload: append([]byte(nil), typed.Payload...)}, true
	default:
		return PushFrame{}, false
	}
}

func normalizedBase(event protocoladapter.Event, binding ReceiverBinding, frame PushFrame) map[string]any {
	raw := append([]byte(nil), frame.Payload...)
	receiver := map[string]any{
		"installationId": binding.InstallationID,
		"adapter":        "meshcore-companion",
		"adapterVersion": binding.AdapterVersion,
		"observedAt":     event.ObservedAt.UTC().Format(time.RFC3339Nano),
	}
	if value := strings.TrimSpace(binding.ReceiverAgentID); value != "" {
		receiver["receiverAgentId"] = value
	}
	contractVersion := "1.0"
	timeConfidence := "suspect"
	if binding.Clock != nil {
		receiver["clock"] = binding.Clock
		contractVersion = "1.1"
		timeConfidence = "receiver_clock"
	}
	return map[string]any{
		"contractVersion": contractVersion,
		"eventType":       "packet_observed",
		"protocol":        "meshcore",
		"receiver":        receiver,
		"capabilities": map[string]any{
			"receiver_rssi":   "not_observable",
			"receiver_snr":    "not_observable",
			"sender_identity": "not_observable",
			"receiver_clock":  capabilityAvailability(binding.Clock != nil),
		},
		"timeConfidence": timeConfidence,
		"authenticity": map[string]any{
			"state":  "unverified",
			"method": "none",
		},
		"source": map[string]any{
			"protocolVersion":   "13",
			"nativeMessageType": opcodeName(frame.Opcode),
			"nativeOpcode":      int(frame.Opcode),
			"nativeOpcodeName":  opcodeName(frame.Opcode),
			"rawEncoding":       "base64",
			"raw":               base64.StdEncoding.EncodeToString(raw),
			"rawSha256":         sha256Hex(raw),
		},
	}
}

func normalizeCapturedRaw(base map[string]any, payload []byte) map[string]any {
	base["contentKey"] = "meshcore:packet:v1:" + sha256Hex(payload)
	return base
}

func markReceiverRFObservable(base map[string]any) {
	capabilities := base["capabilities"].(map[string]any)
	capabilities["receiver_rssi"] = "available"
	capabilities["receiver_snr"] = "available"
}

func normalizeLogRX(base map[string]any, payload []byte, observedAt time.Time) map[string]any {
	markReceiverRFObservable(base)
	onAir := append([]byte(nil), payload[3:]...)
	base["contentKey"] = "meshcore:log-rx:v1:" + sha256Hex(onAir)
	packet, packetErr := parseWirePacket(onAir)
	base["radio"] = radioEvidence(payload[1], payload[2], packet, packetErr == nil)

	packet, advert, err := parseAndVerifySignedAdvert(onAir)
	if err != nil {
		return base
	}
	publicKey := hex.EncodeToString(advert.publicKey)
	base["eventType"] = "device_advertisement"
	base["subject"] = map[string]any{
		"protocolNamespace": "ed25519",
		"canonicalId":       publicKey,
		"verification":      "verified",
	}
	if advert.name != "" {
		base["subject"].(map[string]any)["displayId"] = advert.name
	}
	base["contentKey"] = "meshcore:advert:v1:" + advert.onAirSHA256
	base["radio"] = radioEvidence(payload[1], payload[2], packet, true)
	base["occurredAt"] = map[string]any{
		"value":  unixSeconds(advert.timestamp).Format(time.RFC3339),
		"origin": "protocol",
	}
	base["timeConfidence"] = protocolTimeConfidence(unixSeconds(advert.timestamp), observedAt, hasClock(base))
	base["authenticity"] = map[string]any{
		"state":              "verified",
		"method":             "raw_ed25519",
		"protocolVersion":    "1",
		"nativeOpcode":       int(PushLogRXData),
		"nativeOpcodeName":   "LOG_RX",
		"signingBytesSha256": advert.signingSHA256,
		"publicKey":          publicKey,
		"rawFrameSha256":     sha256Hex(payload),
	}
	source := base["source"].(map[string]any)
	source["evidence"] = map[string]any{
		"onAirAdvertBase64":  base64.StdEncoding.EncodeToString(onAir),
		"onAirAdvertSha256":  advert.onAirSHA256,
		"signingBytesSha256": advert.signingSHA256,
		"signatureAlgorithm": "ed25519",
		"verifier":           "meshcore-advert-v1",
	}
	capabilities := base["capabilities"].(map[string]any)
	capabilities["sender_identity"] = "available"
	capabilities["position_signed"] = capabilityAvailability(advert.hasPosition)
	if advert.hasPosition {
		base["position"] = map[string]any{
			"lat":          float64(advert.latitudeE6) / 1_000_000,
			"lon":          float64(advert.longitudeE6) / 1_000_000,
			"verification": "verified",
		}
	}
	return base
}

func normalizeRawData(base map[string]any, payload []byte) map[string]any {
	markReceiverRFObservable(base)
	content := append([]byte(nil), payload[4:]...)
	base["contentKey"] = "meshcore:raw-data:v1:" + sha256Hex(content)
	base["radio"] = radioEvidence(payload[1], payload[2], wirePacket{}, false)
	return base
}

func normalizeControlData(base map[string]any, payload []byte) map[string]any {
	markReceiverRFObservable(base)
	content := append([]byte(nil), payload[4:]...)
	base["contentKey"] = "meshcore:control-data:v1:" + sha256Hex(content)
	radio := radioEvidence(payload[1], payload[2], wirePacket{}, false)
	radio["routeKind"] = "direct"
	base["radio"] = radio
	return base
}

func normalizeDelegatedAdvert(base map[string]any, payload []byte, observedAt time.Time, session Snapshot) map[string]any {
	base["contentKey"] = "meshcore:packet:v1:" + sha256Hex(payload)
	if !trustedDelegatedSession(session) {
		return base
	}
	contact, err := parseDelegatedAdvert(payload)
	if err != nil {
		return base
	}

	publicKey := hex.EncodeToString(contact.publicKey)
	rawSHA256 := sha256Hex(payload)
	base["eventType"] = "device_advertisement"
	base["subject"] = map[string]any{
		"protocolNamespace": "ed25519",
		"canonicalId":       publicKey,
		"verification":      "verified",
	}
	if contact.name != "" {
		base["subject"].(map[string]any)["displayId"] = contact.name
	}
	base["contentKey"] = "meshcore:delegated-advert:v1:" + rawSHA256
	base["occurredAt"] = map[string]any{
		"value":  unixSeconds(contact.timestamp).Format(time.RFC3339),
		"origin": "protocol",
	}
	base["timeConfidence"] = protocolTimeConfidence(unixSeconds(contact.timestamp), observedAt, hasClock(base))
	base["authenticity"] = map[string]any{
		"state":           "verified",
		"method":          "trusted_companion_validation",
		"validationStage": "firmware_post_signature_validation",
		"verifier":        "meshcore-companion-v1.17.1",
		"deviceAttested":  false,
	}
	if contact.hasPosition {
		base["position"] = map[string]any{
			"lat":          float64(contact.latitudeE6) / 1_000_000,
			"lon":          float64(contact.longitudeE6) / 1_000_000,
			"verification": "verified",
		}
	}
	capabilities := base["capabilities"].(map[string]any)
	capabilities["receiver_rssi"] = "not_observable"
	capabilities["receiver_snr"] = "not_observable"
	capabilities["sender_identity"] = "available"
	capabilities["position_signed"] = capabilityAvailability(contact.hasPosition)
	source := base["source"].(map[string]any)
	source["evidence"] = map[string]any{
		"kind":                          "meshcore_companion_delegated_advert_v1",
		"controlFrameSha256":            rawSHA256,
		"publicKeyCanonical":            publicKey,
		"onAirAdvertPresent":            false,
		"signaturePresent":              false,
		"transport":                     "physical_serial",
		"adapterTrustProfile":           "meshcore-companion-v1.17.1",
		"firmwareRelease":               "companion-v1.17.1",
		"deviceReportedFirmwareVersion": session.Trust.FirmwareVersion,
		"deviceReportedBuild":           session.Trust.FirmwareBuild,
		"deviceReportedModel":           session.Trust.Model,
		"deviceProtocol":                int(session.Trust.ProtocolVersion),
		"appTargetProtocol":             int(ProtocolVersion),
		"sourceCommit":                  PinnedSourceCommit,
		"sourceCommitProvenance":        "pinned_source_allowlist",
		"deviceAttestedSourceCommit":    false,
	}
	return base
}

type delegatedAdvert struct {
	publicKey   []byte
	timestamp   uint32
	latitudeE6  int32
	longitudeE6 int32
	hasPosition bool
	name        string
}

func trustedDelegatedSession(session Snapshot) bool {
	return semanticProfileMatched(session) &&
		session.Trust.Trusted &&
		!session.Trust.DeviceAttested &&
		session.Trust.AllowlistCommit == PinnedSourceCommit
}

func parseDelegatedAdvert(payload []byte) (delegatedAdvert, error) {
	if len(payload) != newAdvertLength || payload[0] != PushNewAdvert {
		return delegatedAdvert{}, ErrInvalidPush
	}
	contactType := payload[33]
	if contactType < 1 || contactType > 4 {
		return delegatedAdvert{}, fmt.Errorf("invalid delegated advert contact type %d", contactType)
	}
	latitudeE6, longitudeE6 := delegatedPosition(payload)
	if latitudeE6 < -90_000_000 || latitudeE6 > 90_000_000 ||
		longitudeE6 < -180_000_000 || longitudeE6 > 180_000_000 {
		return delegatedAdvert{}, errors.New("delegated advert coordinates are out of range")
	}
	return delegatedAdvert{
		publicKey:   append([]byte(nil), payload[1:33]...),
		timestamp:   binary.LittleEndian.Uint32(payload[132:136]),
		latitudeE6:  latitudeE6,
		longitudeE6: longitudeE6,
		hasPosition: latitudeE6 != 0 || longitudeE6 != 0,
		name:        fixedString(payload[100:132]),
	}, nil
}

func radioEvidence(snrX4 byte, rssi byte, packet wirePacket, includeRoute bool) map[string]any {
	signedSNR := int(int8(snrX4))
	radio := map[string]any{
		"metrics": []any{
			map[string]any{
				"kind":   "snr",
				"value":  float64(signedSNR) / 4,
				"unit":   "dB",
				"origin": "receiver_local",
				"source": map[string]any{
					"value":    signedSNR,
					"unit":     "quarter_dB",
					"encoding": "meshcore_snr_x4",
				},
			},
			map[string]any{
				"kind":   "rssi",
				"value":  int(int8(rssi)),
				"unit":   "dBm",
				"origin": "receiver_local",
			},
		},
	}
	if includeRoute {
		radio["routeKind"] = packet.routeKind
		if len(packet.path) > 0 {
			radio["path"] = append([]string(nil), packet.path...)
		}
	}
	return radio
}

func opcodeName(opcode byte) string {
	switch opcode {
	case PushRawData:
		return "RAW_DATA"
	case PushLogRXData:
		return "LOG_RX"
	case PushNewAdvert:
		return "NEW_ADVERT"
	case PushControlData:
		return "CONTROL_DATA"
	default:
		return fmt.Sprintf("UNKNOWN_0X%02X", opcode)
	}
}

func unixSeconds(value uint32) time.Time {
	return time.Unix(int64(value), 0).UTC()
}

func protocolTimeConfidence(occurredAt time.Time, observedAt time.Time, synchronized bool) string {
	if !synchronized {
		return "suspect"
	}
	difference := occurredAt.Sub(observedAt.UTC())
	if difference < 0 {
		difference = -difference
	}
	if difference <= maximumAuthoritativeClockSkew {
		return "authoritative_protocol"
	}
	return "suspect"
}

func hasClock(base map[string]any) bool {
	receiver, ok := base["receiver"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = receiver["clock"]
	return ok
}

func capabilityAvailability(available bool) string {
	if available {
		return "available"
	}
	return "unavailable"
}

func delegatedPosition(payload []byte) (int32, int32) {
	return int32(binary.LittleEndian.Uint32(payload[136:140])), int32(binary.LittleEndian.Uint32(payload[140:144]))
}
