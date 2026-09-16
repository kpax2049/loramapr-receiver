package meshcore

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	ProtocolVersion = byte(13)

	CommandAppStart             = byte(0x01)
	CommandResetPath            = byte(0x0d)
	CommandDeviceQuery          = byte(0x16)
	CommandGetContactByKey      = byte(0x1e)
	CommandSendTelemetryRequest = byte(0x27)
	CommandSendBinaryRequest    = byte(0x32)

	ResponseOK         = byte(0x00)
	ResponseError      = byte(0x01)
	ResponseContact    = byte(0x03)
	ResponseSelfInfo   = byte(0x05)
	ResponseSent       = byte(0x06)
	ResponseDeviceInfo = byte(0x0D)

	PushRawData           = byte(0x84)
	PushPathUpdated       = byte(0x81)
	PushLogRXData         = byte(0x88)
	PushNewAdvert         = byte(0x8A)
	PushTelemetryResponse = byte(0x8B)
	PushBinaryResponse    = byte(0x8C)
	PushControlData       = byte(0x8E)

	deviceInfoLength  = 82
	selfInfoMinLength = 58
	newAdvertLength   = 148

	PinnedFirmwareBuild   = "14 Aug 2026"
	PinnedFirmwareVersion = "v1.17.1"
	PinnedSourceCommit    = "d92964352441e53b93e8667b802e04f6e072b39e"
)

var (
	ErrSessionNotStarted        = errors.New("meshcore companion session has not started")
	ErrUnexpectedHandshakeFrame = errors.New("unexpected meshcore companion handshake frame")
	ErrUnsupportedProtocol      = errors.New("unsupported meshcore companion protocol")
	ErrTrustProfileMismatch     = errors.New("meshcore companion trust profile mismatch")
	ErrInvalidDeviceInfo        = errors.New("invalid meshcore companion device info")
	ErrInvalidSelfInfo          = errors.New("invalid meshcore companion self info")
	ErrUnsupportedPush          = errors.New("unsupported meshcore companion push frame")
	ErrInvalidPush              = errors.New("invalid meshcore companion push frame")
)

type SessionState string

const (
	SessionDisconnected    SessionState = "disconnected"
	SessionAwaitDeviceInfo SessionState = "await_device_info"
	SessionAwaitSelfInfo   SessionState = "await_self_info"
	SessionReady           SessionState = "ready"
	SessionFailed          SessionState = "failed"
)

type DeviceInfo struct {
	ProtocolVersion byte
	MaxContactsHalf byte
	MaxChannels     byte
	BLEPin          uint32
	FirmwareBuild   string
	Model           string
	FirmwareVersion string
	RepeaterEnabled bool
	PathHashMode    byte
}

type SelfInfo struct {
	AdvertType        byte
	TXPowerDBM        int8
	MaxTXPowerDBM     int8
	PublicKey         [32]byte
	LatitudeE6        int32
	LongitudeE6       int32
	MultiACKs         byte
	AdvertLocPolicy   byte
	TelemetryMode     byte
	ManualAddContacts byte
	FrequencyHz       uint32
	BandwidthHz       uint32
	SpreadingFactor   byte
	CodingRate        byte
	NodeName          string
}

// TrustProfile describes the exact source allowlist used to trust firmware's
// post-signature-validation NEW_ADVERT push. It is a compatibility assertion,
// not device attestation: the serial response is self-reported by the device.
type TrustProfile struct {
	Trusted                bool
	ProtocolCompatible     bool
	ProfileMatched         bool
	ProtocolVersion        byte
	FirmwareBuild          string
	FirmwareVersion        string
	Model                  string
	AllowlistCommit        string
	DeviceAttested         bool
	Transport              string
	DelegatedAdvertAllowed bool
	Reason                 string
}

type Snapshot struct {
	State      SessionState
	DeviceInfo *DeviceInfo
	SelfInfo   *SelfInfo
	Trust      TrustProfile
}

type PushFrame struct {
	Opcode  byte
	Payload []byte
}

// ResponseFrame is an immediate Companion command response. Unlike PushFrame,
// it has no sender identity and must be correlated by the operation that sent
// the command.
type ResponseFrame struct {
	Code    byte
	Payload []byte
}

type HandleResult struct {
	Outbound []byte
	Push     *PushFrame
	Response *ResponseFrame
	Ready    bool
}

type CompanionSession struct {
	mu        sync.RWMutex
	appName   string
	state     SessionState
	device    *DeviceInfo
	self      *SelfInfo
	trust     TrustProfile
	transport TransportMetadata
}

func NewCompanionSession(appName string) *CompanionSession {
	return NewCompanionSessionForTransport(appName, TransportMetadata{Kind: "physical_serial", DelegatedAdvertAllowed: true})
}

// NewCompanionSessionForTransport keeps Companion protocol handling shared
// while making the narrowly delegated NEW_ADVERT policy transport-explicit.
func NewCompanionSessionForTransport(appName string, transport TransportMetadata) *CompanionSession {
	if strings.TrimSpace(transport.Kind) == "" {
		transport.Kind = "physical_serial"
	}
	return &CompanionSession{
		appName:   strings.TrimSpace(appName),
		transport: transport,
		state:     SessionDisconnected,
		trust: TrustProfile{
			AllowlistCommit:        PinnedSourceCommit,
			DeviceAttested:         false,
			Transport:              transport.Kind,
			DelegatedAdvertAllowed: transport.DelegatedAdvertAllowed,
			Reason:                 "serial session is disconnected",
		},
	}
}

// Begin starts a fresh transport session. DEVICE_QUERY is always first and
// advert trust from any prior connection is invalidated before bytes are sent.
func (s *CompanionSession) Begin() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = SessionAwaitDeviceInfo
	s.device = nil
	s.self = nil
	s.trust = TrustProfile{
		ProtocolVersion:        ProtocolVersion,
		AllowlistCommit:        PinnedSourceCommit,
		DeviceAttested:         false,
		Transport:              s.transport.Kind,
		DelegatedAdvertAllowed: s.transport.DelegatedAdvertAllowed,
		Reason:                 "awaiting pinned device profile and self info",
	}
	return []byte{CommandDeviceQuery, ProtocolVersion}
}

// Disconnect invalidates delegated advert trust. Reported identity/profile
// details remain available for diagnostics but cannot be reused on reconnect.
func (s *CompanionSession) Disconnect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = SessionDisconnected
	s.trust.Trusted = false
	s.trust.DeviceAttested = false
	s.trust.Reason = "serial session disconnected; reconnect must renegotiate"
}

func (s *CompanionSession) Handle(payload []byte) (HandleResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(payload) == 0 {
		return s.fail(ErrUnexpectedHandshakeFrame)
	}

	switch s.state {
	case SessionAwaitDeviceInfo:
		if payload[0] != ResponseDeviceInfo {
			return s.fail(fmt.Errorf("%w: got opcode 0x%02x, want 0x%02x", ErrUnexpectedHandshakeFrame, payload[0], ResponseDeviceInfo))
		}
		device, err := parseDeviceInfo(payload)
		if err != nil {
			return s.fail(err)
		}
		if device.ProtocolVersion != ProtocolVersion {
			return s.fail(fmt.Errorf("%w: got %d, want %d", ErrUnsupportedProtocol, device.ProtocolVersion, ProtocolVersion))
		}
		profileMatched := device.FirmwareBuild == PinnedFirmwareBuild &&
			device.FirmwareVersion == PinnedFirmwareVersion &&
			strings.TrimSpace(device.Model) != ""

		s.device = device
		s.trust.ProtocolCompatible = true
		s.trust.ProfileMatched = profileMatched
		s.trust.ProtocolVersion = device.ProtocolVersion
		s.trust.FirmwareBuild = device.FirmwareBuild
		s.trust.FirmwareVersion = device.FirmwareVersion
		s.trust.Model = device.Model
		if !profileMatched {
			s.trust.Reason = fmt.Sprintf(
				"%v: build=%q version=%q model=%q; protocol-compatible raw capture only",
				ErrTrustProfileMismatch,
				device.FirmwareBuild,
				device.FirmwareVersion,
				device.Model,
			)
		} else {
			s.trust.Reason = "pinned profile matched; awaiting SELF_INFO"
		}
		s.state = SessionAwaitSelfInfo
		return HandleResult{Outbound: buildAppStart(s.appName)}, nil
	case SessionAwaitSelfInfo:
		if payload[0] != ResponseSelfInfo {
			return s.fail(fmt.Errorf("%w: got opcode 0x%02x, want 0x%02x", ErrUnexpectedHandshakeFrame, payload[0], ResponseSelfInfo))
		}
		self, err := parseSelfInfo(payload)
		if err != nil {
			return s.fail(err)
		}
		s.self = self
		s.state = SessionReady
		s.trust.Trusted = s.trust.ProfileMatched
		s.trust.DeviceAttested = false
		if s.trust.Trusted {
			if s.trust.DelegatedAdvertAllowed {
				s.trust.Reason = "pinned self-reported firmware profile negotiated over physical serial; not device attestation"
			} else {
				s.trust.Reason = "pinned profile matched; transport permits raw signed evidence only and delegated adverts remain untrusted"
			}
		} else {
			s.trust.Reason = "protocol-compatible Companion profile mismatch; delegated trust disabled, raw capture enabled"
		}
		return HandleResult{Ready: true}, nil

	case SessionReady:
		if err := validateCapturedPush(payload); err != nil {
			return HandleResult{}, err
		}
		if payload[0] == ResponseOK || payload[0] == ResponseError || payload[0] == ResponseSent || payload[0] == ResponseContact {
			if err := validateCommandResponse(payload); err != nil {
				return HandleResult{}, err
			}
			copied := append([]byte(nil), payload...)
			return HandleResult{Ready: true, Response: &ResponseFrame{Code: copied[0], Payload: copied}}, nil
		}
		if payload[0] == PushTelemetryResponse {
			if err := validateTelemetryResponse(payload); err != nil {
				return HandleResult{}, err
			}
		}
		if payload[0] == PushBinaryResponse {
			if err := validateBinaryResponse(payload); err != nil {
				return HandleResult{}, err
			}
		}
		copied := append([]byte(nil), payload...)
		return HandleResult{Ready: true, Push: &PushFrame{Opcode: copied[0], Payload: copied}}, nil

	case SessionDisconnected:
		return HandleResult{}, ErrSessionNotStarted
	case SessionFailed:
		return HandleResult{}, errors.New("meshcore companion session failed; reconnect required")
	default:
		return s.fail(fmt.Errorf("invalid meshcore companion session state %q", s.state))
	}
}

func validateCommandResponse(payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty command response", ErrInvalidPush)
	}
	if payload[0] == ResponseSent && len(payload) < 10 {
		return fmt.Errorf("%w: SENT got %d bytes, need at least 10", ErrInvalidPush, len(payload))
	}
	// writeContactRespFrame emits a fixed 148-byte payload. We only parse the
	// public key and route fields, but validate the complete pinned layout.
	if payload[0] == ResponseContact && len(payload) != newAdvertLength {
		return fmt.Errorf("%w: CONTACT got %d bytes, want %d", ErrInvalidPush, len(payload), newAdvertLength)
	}
	return nil
}

func validateTelemetryResponse(payload []byte) error {
	// [code][reserved][source public-key prefix x6][CayenneLPP payload...]
	if len(payload) < 8 {
		return fmt.Errorf("%w: TELEMETRY_RESPONSE got %d bytes, need at least 8", ErrInvalidPush, len(payload))
	}
	return nil
}

func validateBinaryResponse(payload []byte) error {
	// [code][reserved][request tag uint32 LE][response data...]
	if len(payload) < 6 {
		return fmt.Errorf("%w: BINARY_RESPONSE got %d bytes, need at least 6", ErrInvalidPush, len(payload))
	}
	return nil
}

func (s *CompanionSession) Snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := Snapshot{State: s.state, Trust: s.trust}
	if s.device != nil {
		value := *s.device
		result.DeviceInfo = &value
	}
	if s.self != nil {
		value := *s.self
		result.SelfInfo = &value
	}
	return result
}

func (s *CompanionSession) fail(err error) (HandleResult, error) {
	s.state = SessionFailed
	s.trust.Trusted = false
	s.trust.DeviceAttested = false
	s.trust.Reason = err.Error()
	return HandleResult{}, err
}

func parseDeviceInfo(payload []byte) (*DeviceInfo, error) {
	if len(payload) != deviceInfoLength {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidDeviceInfo, len(payload), deviceInfoLength)
	}
	return &DeviceInfo{
		ProtocolVersion: payload[1],
		MaxContactsHalf: payload[2],
		MaxChannels:     payload[3],
		BLEPin:          binary.LittleEndian.Uint32(payload[4:8]),
		FirmwareBuild:   fixedString(payload[8:20]),
		Model:           fixedString(payload[20:60]),
		FirmwareVersion: fixedString(payload[60:80]),
		RepeaterEnabled: payload[80] != 0,
		PathHashMode:    payload[81],
	}, nil
}

func parseSelfInfo(payload []byte) (*SelfInfo, error) {
	if len(payload) < selfInfoMinLength {
		return nil, fmt.Errorf("%w: got %d bytes, need at least %d", ErrInvalidSelfInfo, len(payload), selfInfoMinLength)
	}
	result := &SelfInfo{
		AdvertType:        payload[1],
		TXPowerDBM:        int8(payload[2]),
		MaxTXPowerDBM:     int8(payload[3]),
		LatitudeE6:        int32(binary.LittleEndian.Uint32(payload[36:40])),
		LongitudeE6:       int32(binary.LittleEndian.Uint32(payload[40:44])),
		MultiACKs:         payload[44],
		AdvertLocPolicy:   payload[45],
		TelemetryMode:     payload[46],
		ManualAddContacts: payload[47],
		FrequencyHz:       binary.LittleEndian.Uint32(payload[48:52]),
		BandwidthHz:       binary.LittleEndian.Uint32(payload[52:56]),
		SpreadingFactor:   payload[56],
		CodingRate:        payload[57],
		NodeName:          fixedString(payload[58:]),
	}
	copy(result.PublicKey[:], payload[4:36])
	return result, nil
}

func buildAppStart(appName string) []byte {
	appName = strings.TrimSpace(appName)
	payload := make([]byte, 8, 8+len(appName))
	payload[0] = CommandAppStart
	payload[1] = ProtocolVersion
	return append(payload, []byte(appName)...)
}

func validatePush(payload []byte) error {
	if err := validateCapturedPush(payload); err != nil {
		return err
	}
	minimum := 0
	switch payload[0] {
	case PushPathUpdated:
		minimum = 1 + telemetryPublicKeyLength
	case PushRawData:
		minimum = 4
	case PushLogRXData:
		minimum = 3
	case PushNewAdvert:
		if len(payload) != newAdvertLength {
			return fmt.Errorf("%w: NEW_ADVERT got %d bytes, want %d", ErrInvalidPush, len(payload), newAdvertLength)
		}
		return nil
	case PushControlData:
		minimum = 4
	default:
		return fmt.Errorf("%w: opcode 0x%02x", ErrUnsupportedPush, payload[0])
	}
	if len(payload) < minimum {
		return fmt.Errorf("%w: opcode 0x%02x got %d bytes, need at least %d", ErrInvalidPush, payload[0], len(payload), minimum)
	}
	return nil
}

func validateCapturedPush(payload []byte) error {
	if len(payload) == 0 {
		return fmt.Errorf("%w: empty payload", ErrInvalidPush)
	}
	if len(payload) > MaxPayloadSize {
		return fmt.Errorf("%w: got %d bytes, maximum is %d", ErrOversizedFrame, len(payload), MaxPayloadSize)
	}
	return nil
}

func fixedString(value []byte) string {
	if index := strings.IndexByte(string(value), 0); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(string(value))
}
