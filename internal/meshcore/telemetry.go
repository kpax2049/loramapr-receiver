package meshcore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	telemetryPublicKeyLength = 32
	telemetryPrefixLength    = 6
	defaultTelemetryTimeout  = 45 * time.Second
	minimumTelemetryTimeout  = time.Second
)

var (
	ErrInvalidTelemetryTarget       = errors.New("invalid MeshCore telemetry target")
	ErrTelemetryAdapterDisconnected = errors.New("MeshCore adapter is disconnected")
	ErrTelemetryRequestInFlight     = errors.New("a MeshCore telemetry request is already in flight")
	ErrTelemetryFirmware            = errors.New("MeshCore firmware rejected telemetry request")
	ErrTelemetryTimeout             = errors.New("MeshCore telemetry response timed out")
	ErrTelemetryMismatchedResponse  = errors.New("MeshCore telemetry response source prefix does not match request target")
	ErrInvalidTelemetryPayload      = errors.New("invalid MeshCore telemetry payload")
)

// Telemetry contains the small CayenneLPP subset emitted by the stock Wio
// Tracker L1 Pro firmware. Unsupported LPP types are reported but never used
// to infer an identity or alter the fields parsed before them.
type Telemetry struct {
	BatteryPercentage *uint8   `json:"batteryPercentage,omitempty"`
	Voltage           *float64 `json:"voltage,omitempty"`
	Latitude          *float64 `json:"latitude,omitempty"`
	Longitude         *float64 `json:"longitude,omitempty"`
	AltitudeM         *float64 `json:"altitudeM,omitempty"`
	TemperatureC      *float64 `json:"temperatureC,omitempty"`
	UnsupportedTypes  []int    `json:"unsupportedTypes,omitempty"`
}

// TelemetryResult is deliberately an observation from a prefix-correlated
// request, not an authenticated identity assertion.
type TelemetryResult struct {
	TargetPublicKey string    `json:"targetPublicKey"`
	SourcePrefix    string    `json:"sourcePrefix"`
	ReceivedAt      time.Time `json:"receivedAt"`
	Telemetry       Telemetry `json:"telemetry"`
	RawFrame        []byte    `json:"-"`
}

type telemetryCompletion struct {
	result TelemetryResult
	err    error
}

type telemetryRequest struct {
	target [telemetryPublicKeyLength]byte
	done   chan telemetryCompletion
	timer  *time.Timer
	// suggestedTimer is separate from the initial bounded watchdog so a timer
	// callback racing with RESP_CODE_SENT cannot accidentally extend a request.
	suggestedTimer *time.Timer
}

func parseTelemetryTarget(value string) ([telemetryPublicKeyLength]byte, error) {
	var target [telemetryPublicKeyLength]byte
	if len(value) != hex.EncodedLen(len(target)) || strings.ToLower(value) != value {
		return target, ErrInvalidTelemetryTarget
	}
	decoded, err := hex.DecodeString(value)
	if err != nil || len(decoded) != len(target) {
		return target, ErrInvalidTelemetryTarget
	}
	copy(target[:], decoded)
	return target, nil
}

// BuildTelemetryRequest creates protocol-13 CMD_SEND_TELEMETRY_REQ:
// [0x27][reserved x3][destination public key x32].
func BuildTelemetryRequest(target [telemetryPublicKeyLength]byte) []byte {
	frame := make([]byte, 4+len(target))
	frame[0] = CommandSendTelemetryRequest
	copy(frame[4:], target[:])
	return frame
}

// RequestTelemetry sends one manually initiated request. The adapter allows
// only one outstanding request because PUSH_CODE_TELEMETRY_RESPONSE exposes
// only a six-byte source-key prefix.
func (a *Adapter) RequestTelemetry(ctx context.Context, publicKey string) (TelemetryResult, error) {
	target, err := parseTelemetryTarget(publicKey)
	if err != nil {
		return TelemetryResult{}, err
	}
	if ctx == nil {
		ctx = context.Background()
	}

	a.telemetryMu.Lock()
	if a.telemetry != nil {
		a.telemetryMu.Unlock()
		return TelemetryResult{}, ErrTelemetryRequestInFlight
	}
	a.mu.RLock()
	link := a.link
	ready := a.status.State == StateConnected && a.status.Session.State == SessionReady
	closed := a.closed
	a.mu.RUnlock()
	if closed || !ready || link == nil {
		a.telemetryMu.Unlock()
		return TelemetryResult{}, ErrTelemetryAdapterDisconnected
	}

	request := &telemetryRequest{target: target, done: make(chan telemetryCompletion, 1)}
	request.timer = time.AfterFunc(defaultTelemetryTimeout, func() {
		a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryTimeout)
	})
	a.telemetry = request
	a.telemetryMu.Unlock()

	if err := link.WriteFrame(ctx, BuildTelemetryRequest(target)); err != nil {
		wrapped := fmt.Errorf("%w: %v", ErrTelemetryAdapterDisconnected, err)
		a.finishTelemetry(request, TelemetryResult{}, wrapped)
		return TelemetryResult{}, wrapped
	}

	select {
	case completion := <-request.done:
		return completion.result, completion.err
	case <-ctx.Done():
		a.finishTelemetry(request, TelemetryResult{}, ctx.Err())
		return TelemetryResult{}, ctx.Err()
	}
}

func (a *Adapter) handleTelemetryResponse(frame []byte, receivedAt time.Time) {
	result, prefix, err := parseTelemetryResponse(frame, receivedAt)
	if err != nil {
		a.finishCurrentTelemetry(TelemetryResult{}, err)
		return
	}

	a.telemetryMu.Lock()
	request := a.telemetry
	if request == nil {
		a.telemetryMu.Unlock()
		return // unsolicited or late: a prefix is not a canonical identity
	}
	if !bytes.Equal(prefix[:], request.target[:telemetryPrefixLength]) {
		a.telemetryMu.Unlock()
		a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryMismatchedResponse)
		return
	}
	result.TargetPublicKey = hex.EncodeToString(request.target[:])
	a.telemetryMu.Unlock()
	a.finishTelemetry(request, result, nil)
}

func (a *Adapter) handleTelemetryCommandResponse(frame ResponseFrame) {
	switch frame.Code {
	case ResponseError:
		a.finishCurrentTelemetry(TelemetryResult{}, ErrTelemetryFirmware)
	case ResponseSent:
		// Protocol 13: [0x06][is_flood][ack_hash x4][estimated_timeout_ms x4 LE].
		if len(frame.Payload) < 10 {
			a.finishCurrentTelemetry(TelemetryResult{}, ErrInvalidTelemetryPayload)
			return
		}
		requested := time.Duration(binary.LittleEndian.Uint32(frame.Payload[6:10])) * time.Millisecond
		if requested <= 0 || requested >= defaultTelemetryTimeout {
			return
		}
		if requested < minimumTelemetryTimeout {
			requested = minimumTelemetryTimeout
		}
		a.telemetryMu.Lock()
		request := a.telemetry
		if request != nil && request.suggestedTimer == nil {
			request.suggestedTimer = time.AfterFunc(requested, func() {
				a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryTimeout)
			})
		}
		a.telemetryMu.Unlock()
	}
}

func (a *Adapter) finishCurrentTelemetry(result TelemetryResult, err error) {
	a.telemetryMu.Lock()
	request := a.telemetry
	a.telemetryMu.Unlock()
	if request != nil {
		a.finishTelemetry(request, result, err)
	}
}

func (a *Adapter) finishTelemetry(request *telemetryRequest, result TelemetryResult, err error) {
	if request == nil {
		return
	}
	a.telemetryMu.Lock()
	if a.telemetry != request {
		a.telemetryMu.Unlock()
		return
	}
	a.telemetry = nil
	if request.timer != nil {
		request.timer.Stop()
	}
	if request.suggestedTimer != nil {
		request.suggestedTimer.Stop()
	}
	a.telemetryMu.Unlock()
	request.done <- telemetryCompletion{result: result, err: err}
}

func parseTelemetryResponse(frame []byte, receivedAt time.Time) (TelemetryResult, [telemetryPrefixLength]byte, error) {
	var prefix [telemetryPrefixLength]byte
	if len(frame) < 8 || frame[0] != PushTelemetryResponse {
		return TelemetryResult{}, prefix, fmt.Errorf("%w: telemetry response frame", ErrInvalidTelemetryPayload)
	}
	copy(prefix[:], frame[2:8])
	telemetry, err := ParseTelemetryLPP(frame[8:])
	if err != nil {
		return TelemetryResult{}, prefix, err
	}
	return TelemetryResult{
		SourcePrefix: hex.EncodeToString(prefix[:]),
		ReceivedAt:   receivedAt.UTC(),
		Telemetry:    telemetry,
		RawFrame:     append([]byte(nil), frame...),
	}, prefix, nil
}

// ParseTelemetryLPP handles the stock MeshCore fields we need for manual
// validation. CayenneLPP has no type-length marker, so an unknown type ends
// parsing safely rather than guessing its size and losing byte alignment.
func ParseTelemetryLPP(payload []byte) (Telemetry, error) {
	var telemetry Telemetry
	for offset := 0; offset < len(payload); {
		if len(payload)-offset < 2 {
			return telemetry, fmt.Errorf("%w: truncated channel/type", ErrInvalidTelemetryPayload)
		}
		channel, kind := payload[offset], payload[offset+1]
		offset += 2
		if channel == 0 && kind == 0 {
			break
		}
		need := func(length int) ([]byte, error) {
			if len(payload)-offset < length {
				return nil, fmt.Errorf("%w: type %d needs %d bytes", ErrInvalidTelemetryPayload, kind, length)
			}
			value := payload[offset : offset+length]
			offset += length
			return value, nil
		}
		switch kind {
		case 103: // temperature: int16 BE, 0.1 C
			value, err := need(2)
			if err != nil {
				return telemetry, err
			}
			decoded := float64(int16(binary.BigEndian.Uint16(value))) / 10
			telemetry.TemperatureC = &decoded
		case 116: // voltage: uint16 BE, 0.01 V
			value, err := need(2)
			if err != nil {
				return telemetry, err
			}
			decoded := float64(binary.BigEndian.Uint16(value)) / 100
			telemetry.Voltage = &decoded
		case 120: // percentage: uint8, 1 percent
			value, err := need(1)
			if err != nil {
				return telemetry, err
			}
			decoded := value[0]
			telemetry.BatteryPercentage = &decoded
		case 121: // standalone altitude: int16 BE, 1 m
			value, err := need(2)
			if err != nil {
				return telemetry, err
			}
			decoded := float64(int16(binary.BigEndian.Uint16(value)))
			telemetry.AltitudeM = &decoded
		case 136: // GPS: latitude, longitude (1e-4 deg), altitude (0.01 m), signed int24 BE
			value, err := need(9)
			if err != nil {
				return telemetry, err
			}
			latitude := float64(int24BE(value[0:3])) / 10000
			longitude := float64(int24BE(value[3:6])) / 10000
			altitude := float64(int24BE(value[6:9])) / 100
			telemetry.Latitude, telemetry.Longitude, telemetry.AltitudeM = &latitude, &longitude, &altitude
		default:
			telemetry.UnsupportedTypes = append(telemetry.UnsupportedTypes, int(kind))
			return telemetry, nil
		}
	}
	return telemetry, nil
}

func int24BE(value []byte) int32 {
	result := int32(value[0])<<16 | int32(value[1])<<8 | int32(value[2])
	if result&0x800000 != 0 {
		result |= ^int32(0xFFFFFF)
	}
	return result
}
