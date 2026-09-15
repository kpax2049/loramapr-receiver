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
	routeSnapshotTimeout     = 2 * time.Second
	pathResetTimeout         = 5 * time.Second
)

var (
	ErrInvalidTelemetryTarget       = errors.New("invalid MeshCore telemetry target")
	ErrTelemetryAdapterDisconnected = errors.New("MeshCore adapter is disconnected")
	ErrTelemetryRequestInFlight     = errors.New("a MeshCore telemetry request is already in flight")
	ErrTelemetryFirmware            = errors.New("MeshCore firmware rejected telemetry request")
	ErrTelemetryTimeout             = errors.New("MeshCore telemetry response timed out")
	ErrTelemetryMismatchedResponse  = errors.New("MeshCore telemetry response source prefix does not match request target")
	ErrInvalidTelemetryPayload      = errors.New("invalid MeshCore telemetry payload")
	ErrPathResetFirmware            = errors.New("MeshCore firmware rejected path reset")
	ErrPathResetTimeout             = errors.New("MeshCore path reset response timed out")
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
	TargetPublicKey      string        `json:"targetPublicKey"`
	RequestedAt          time.Time     `json:"requestedAt"`
	SourcePrefix         string        `json:"sourcePrefix"`
	ReceivedAt           time.Time     `json:"receivedAt"`
	Telemetry            Telemetry     `json:"telemetry"`
	RouteAttempt         RouteEvidence `json:"routeAttempt"`
	ResponseRouteUnknown bool          `json:"responseRouteUnknown"`
	// RouteRecovery and PathUpdateObserved are request-side tracking evidence,
	// added before a successful tracking result enters the durable outbox.
	// They say nothing about the response route.
	RouteRecovery      string `json:"routeRecovery,omitempty"`
	PathUpdateObserved bool   `json:"pathUpdateObserved"`
	RawFrame           []byte `json:"-"`
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
	suggestedTimer  *time.Timer
	routeAttempt    RouteEvidence
	routeDone       chan RouteEvidence
	awaitingContact bool
	requestedAt     time.Time
}

type pathResetRequest struct {
	target [telemetryPublicKeyLength]byte
	done   chan error
	timer  *time.Timer
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

func buildContactByKeyRequest(target [telemetryPublicKeyLength]byte) []byte {
	frame := make([]byte, 1+len(target))
	frame[0] = CommandGetContactByKey
	copy(frame[1:], target[:])
	return frame
}

// BuildPathResetRequest creates CMD_RESET_PATH [0x0d][public key x32]. The
// reset changes Companion-owned contact state only after RESP_CODE_OK.
func BuildPathResetRequest(target [telemetryPublicKeyLength]byte) []byte {
	frame := make([]byte, 1+len(target))
	frame[0] = CommandResetPath
	copy(frame[1:], target[:])
	return frame
}

// ResetPath asks the Companion to invalidate its cached route for a contact.
// It shares the telemetry arbiter, so a path reset cannot interleave with a
// prefix-correlated telemetry request or its contact route snapshot.
func (a *Adapter) ResetPath(ctx context.Context, publicKey string) error {
	target, err := parseTelemetryTarget(publicKey)
	if err != nil {
		return err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.telemetryMu.Lock()
	if a.telemetry != nil || a.pathReset != nil {
		a.telemetryMu.Unlock()
		return ErrTelemetryRequestInFlight
	}
	a.mu.RLock()
	link := a.link
	ready := a.status.State == StateConnected && a.status.Session.State == SessionReady
	closed := a.closed
	a.mu.RUnlock()
	if closed || !ready || link == nil {
		a.telemetryMu.Unlock()
		return ErrTelemetryAdapterDisconnected
	}
	request := &pathResetRequest{target: target, done: make(chan error, 1)}
	request.timer = time.AfterFunc(pathResetTimeout, func() {
		a.finishPathReset(request, ErrPathResetTimeout)
	})
	a.pathReset = request
	a.telemetryMu.Unlock()
	if err := link.WriteFrame(ctx, BuildPathResetRequest(target)); err != nil {
		wrapped := fmt.Errorf("%w: %v", ErrTelemetryAdapterDisconnected, err)
		a.finishPathReset(request, wrapped)
		return wrapped
	}
	select {
	case err := <-request.done:
		return err
	case <-ctx.Done():
		a.finishPathReset(request, ctx.Err())
		return ctx.Err()
	}
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
	if a.telemetry != nil || a.pathReset != nil {
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

	request := &telemetryRequest{
		target:       target,
		done:         make(chan telemetryCompletion, 1),
		routeAttempt: unknownRouteEvidence("contact_out_path_unavailable"),
	}
	request.timer = time.AfterFunc(defaultTelemetryTimeout, func() {
		a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryTimeout)
	})
	a.telemetry = request
	a.telemetryMu.Unlock()

	// The contact record is Companion-owned state. Snapshot it immediately
	// before the telemetry command so the attempt can distinguish cached
	// zero-hop, cached routed-path, and flood selection. Failure to obtain the
	// optional snapshot never changes telemetry polling policy or blocks a send.
	a.captureTelemetryRoute(ctx, link, request)
	if !a.currentTelemetry(request) {
		completion := <-request.done
		return completion.result, completion.err
	}

	a.setTelemetryRequestedAt(request, time.Now().UTC())
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

func (a *Adapter) setTelemetryRequestedAt(request *telemetryRequest, at time.Time) {
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	if a.telemetry == request {
		request.requestedAt = at.UTC()
	}
}

func (a *Adapter) captureTelemetryRoute(ctx context.Context, link CompanionLink, request *telemetryRequest) {
	if request == nil || !a.currentTelemetry(request) {
		return
	}
	a.telemetryMu.Lock()
	if a.telemetry != request {
		a.telemetryMu.Unlock()
		return
	}
	request.awaitingContact = true
	request.routeDone = make(chan RouteEvidence, 1)
	routeDone := request.routeDone
	a.telemetryMu.Unlock()
	if err := link.WriteFrame(ctx, buildContactByKeyRequest(request.target)); err != nil {
		a.completeRouteSnapshot(request, unknownRouteEvidence("contact_out_path_unavailable"))
		return
	}
	timer := time.NewTimer(routeSnapshotTimeout)
	defer timer.Stop()
	select {
	case evidence := <-routeDone:
		a.setRouteAttempt(request, evidence)
	case <-timer.C:
		a.completeRouteSnapshot(request, unknownRouteEvidence("contact_out_path_unavailable"))
	case <-ctx.Done():
		a.completeRouteSnapshot(request, unknownRouteEvidence("contact_out_path_unavailable"))
	}
}

func (a *Adapter) currentTelemetry(request *telemetryRequest) bool {
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	return a.telemetry == request
}

func (a *Adapter) setRouteAttempt(request *telemetryRequest, evidence RouteEvidence) {
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	if a.telemetry == request {
		request.routeAttempt = evidence.copy()
	}
}

func (a *Adapter) completeRouteSnapshot(request *telemetryRequest, evidence RouteEvidence) {
	a.telemetryMu.Lock()
	if a.telemetry != request || !request.awaitingContact {
		a.telemetryMu.Unlock()
		return
	}
	request.awaitingContact = false
	routeDone := request.routeDone
	a.telemetryMu.Unlock()
	select {
	case routeDone <- evidence:
	default:
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
	result.RequestedAt = request.requestedAt
	a.telemetryMu.Unlock()
	a.finishTelemetry(request, result, nil)
}

func (a *Adapter) handleTelemetryCommandResponse(frame ResponseFrame) {
	if a.handlePathResetCommandResponse(frame) {
		return
	}
	if frame.Code == ResponseContact {
		a.handleTelemetryContactResponse(frame.Payload)
		return
	}
	a.telemetryMu.Lock()
	request := a.telemetry
	if request != nil && request.awaitingContact {
		// A contact lookup error is only missing route observability. It must
		// not replace the telemetry request with a fabricated firmware failure.
		a.telemetryMu.Unlock()
		a.completeRouteSnapshot(request, unknownRouteEvidence("contact_out_path_unavailable"))
		return
	}
	if request != nil && frame.Code == ResponseSent && len(frame.Payload) >= 2 {
		if frame.Payload[1] != 0 {
			request.routeAttempt = RouteEvidence{Mode: RouteModeFlood, Source: "response_sent"}
		} else if request.routeAttempt.Mode == RouteModeZeroHop || request.routeAttempt.Mode == RouteModeExplicitPath {
			request.routeAttempt.Source = "contact_out_path+response_sent"
		} else {
			// RESP_CODE_SENT calls both zero-hop and cached routed sends "direct".
			request.routeAttempt = unknownRouteEvidence("response_sent_direct_path_unavailable")
		}
	}
	a.telemetryMu.Unlock()
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

func (a *Adapter) handlePathResetCommandResponse(frame ResponseFrame) bool {
	a.telemetryMu.Lock()
	request := a.pathReset
	a.telemetryMu.Unlock()
	if request == nil {
		return false
	}
	switch frame.Code {
	case ResponseOK:
		a.finishPathReset(request, nil)
	case ResponseError:
		a.finishPathReset(request, ErrPathResetFirmware)
	default:
		return false
	}
	return true
}

func (a *Adapter) finishCurrentPathReset(err error) {
	a.telemetryMu.Lock()
	request := a.pathReset
	a.telemetryMu.Unlock()
	if request != nil {
		a.finishPathReset(request, err)
	}
}

func (a *Adapter) finishPathReset(request *pathResetRequest, err error) {
	if request == nil {
		return
	}
	a.telemetryMu.Lock()
	if a.pathReset != request {
		a.telemetryMu.Unlock()
		return
	}
	a.pathReset = nil
	if request.timer != nil {
		request.timer.Stop()
	}
	a.telemetryMu.Unlock()
	request.done <- err
}

func (a *Adapter) handleTelemetryContactResponse(frame []byte) {
	if len(frame) != newAdvertLength || frame[0] != ResponseContact {
		return
	}
	a.telemetryMu.Lock()
	request := a.telemetry
	if request == nil || !request.awaitingContact || !bytes.Equal(frame[1:1+telemetryPublicKeyLength], request.target[:]) {
		a.telemetryMu.Unlock()
		return
	}
	a.telemetryMu.Unlock()
	// [code][pubkey x32][type][flags][out_path_len][out_path x64]...
	a.completeRouteSnapshot(request, routeEvidenceFromContactOutPath(frame[35], frame[36:100]))
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
	result.TargetPublicKey = hex.EncodeToString(request.target[:])
	result.RouteAttempt = request.routeAttempt.copy()
	// PUSH_CODE_TELEMETRY_RESPONSE contains a source-key prefix and CayenneLPP
	// payload only. It does not prove its return path, flood/direct mode, or
	// receiver-local RF metadata.
	result.ResponseRouteUnknown = true
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
