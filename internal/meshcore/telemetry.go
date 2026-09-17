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
	binaryTelemetryGuard     = 2 * time.Second
	expiredTagRetention      = defaultTelemetryTimeout
	routeSnapshotTimeout     = 2 * time.Second
	pathResetTimeout         = 5 * time.Second
)

// telemetryBinaryCapability is deliberately runtime-probed rather than
// inferred solely from a firmware label. Protocol-compatible older Companion
// builds can reject command 50; they retain the legacy command 39 behavior.
type telemetryBinaryCapability uint8

const (
	telemetryBinaryUnknown telemetryBinaryCapability = iota
	telemetryBinarySupported
	telemetryBinaryUnsupported
)

type telemetryRequestMode uint8

const (
	telemetryRequestLegacy telemetryRequestMode = iota
	telemetryRequestBinary
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
	RequestTag           *uint32       `json:"requestTag,omitempty"`
	ResponseTag          *uint32       `json:"responseTag,omitempty"`
	Tagged               bool          `json:"tagged"`
	Transport            string        `json:"transport,omitempty"`
	Capability           string        `json:"capability,omitempty"`
	FallbackReason       string        `json:"fallbackReason,omitempty"`
	ResponseOpcode       byte          `json:"-"`
	ReceivedAt           time.Time     `json:"receivedAt"`
	Telemetry            Telemetry     `json:"telemetry"`
	RouteAttempt         RouteEvidence `json:"routeAttempt"`
	ResponseRouteUnknown bool          `json:"responseRouteUnknown"`
	// RouteRecovery and PathUpdateObserved are request-side tracking evidence,
	// added before a successful tracking result enters the durable outbox.
	// They say nothing about the response route.
	RouteRecovery      string `json:"routeRecovery,omitempty"`
	PathUpdateObserved bool   `json:"pathUpdateObserved"`
	// RFEvidence is an explicitly heuristic association with a preceding raw
	// LOG_RX frame. It never changes telemetry identity or route semantics.
	RFEvidence RFEvidence `json:"rfEvidence"`
	RawFrame   []byte     `json:"-"`
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
	suggestedTimer       *time.Timer
	routeAttempt         RouteEvidence
	routeDone            chan RouteEvidence
	awaitingContact      bool
	requestedAt          time.Time
	requestFrameSequence uint64
	mode                 telemetryRequestMode
	tag                  uint32
	tagKnown             bool
	fallbackReason       string
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

// BuildBinaryTelemetryRequest creates the stock tagged request:
// [0x32][destination public key x32][0x03 telemetry request type].
// The Companion, not the client, assigns the uint32 tag in RESP_CODE_SENT.
func BuildBinaryTelemetryRequest(target [telemetryPublicKeyLength]byte) []byte {
	frame := make([]byte, 1+len(target)+1)
	frame[0] = CommandSendBinaryRequest
	copy(frame[1:], target[:])
	frame[len(frame)-1] = 0x03 // stock binary telemetry request type
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
		mode:         a.telemetryRequestModeLocked(),
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
	frame := BuildTelemetryRequest(target)
	if request.mode == telemetryRequestBinary {
		frame = BuildBinaryTelemetryRequest(target)
	}
	if err := link.WriteFrame(ctx, frame); err != nil {
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

func (a *Adapter) telemetryRequestModeLocked() telemetryRequestMode {
	if a.binaryTelemetryCapability == telemetryBinaryUnsupported {
		return telemetryRequestLegacy
	}
	return telemetryRequestBinary
}

func (a *Adapter) setTelemetryRequestedAt(request *telemetryRequest, at time.Time) {
	a.telemetryMu.Lock()
	defer a.telemetryMu.Unlock()
	if a.telemetry == request {
		request.requestedAt = at.UTC()
		request.requestFrameSequence = a.frameSequence
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
	if request.mode != telemetryRequestLegacy {
		a.telemetryMu.Unlock()
		return // legacy response cannot complete a tagged request
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

func (a *Adapter) handleBinaryTelemetryResponse(frame []byte, receivedAt time.Time, frameSequences ...uint64) {
	if len(frame) < 6 || frame[0] != PushBinaryResponse {
		return // malformed unsolicited input must not fail an unrelated request
	}
	tag := binary.LittleEndian.Uint32(frame[2:6])
	telemetry, err := ParseTelemetryLPP(frame[6:])
	if err != nil {
		// A matching request receives its payload error; a stale/unknown tag is
		// ignored so it cannot interrupt a newer request.
		a.telemetryMu.Lock()
		request := a.telemetry
		matches := request != nil && request.mode == telemetryRequestBinary && request.tagKnown && request.tag == tag
		a.telemetryMu.Unlock()
		if matches {
			a.finishTelemetry(request, TelemetryResult{}, err)
		}
		return
	}

	a.telemetryMu.Lock()
	a.pruneExpiredTelemetryTagsLocked(receivedAt)
	request := a.telemetry
	if request == nil || request.mode != telemetryRequestBinary || !request.tagKnown || request.tag != tag {
		if _, expired := a.expiredTelemetryTags[tag]; expired {
			a.logger.Warn("MeshCore late tagged telemetry response rejected", "response_tag", tag, "reason", "expired_request")
		}
		a.telemetryMu.Unlock()
		return // stale, expired, or unrelated binary response
	}
	if _, expired := a.expiredTelemetryTags[tag]; expired {
		a.logger.Warn("MeshCore late tagged telemetry response rejected", "response_tag", tag, "reason", "expired_request")
		a.telemetryMu.Unlock()
		return // never accept a tag that could identify a prior request
	}
	requestTag := tag
	responseSequence := uint64(0)
	if len(frameSequences) > 0 {
		responseSequence = frameSequences[0]
	}
	result := TelemetryResult{
		TargetPublicKey: hex.EncodeToString(request.target[:]),
		// 0x8C has no sender prefix. The exact tag-to-request mapping is the
		// equivalent target validation; retain the requested target prefix for
		// the existing normalized telemetry schema.
		SourcePrefix: hex.EncodeToString(request.target[:telemetryPrefixLength]),
		RequestTag:   &requestTag, ResponseTag: &requestTag, Tagged: true, Transport: "tagged_binary", Capability: "tagged_binary_supported", ResponseOpcode: PushBinaryResponse,
		RequestedAt: request.requestedAt, ReceivedAt: receivedAt.UTC(), Telemetry: telemetry,
		RawFrame: append([]byte(nil), frame...),
	}
	result.RFEvidence = a.correlateTaggedTelemetryRF(request, receivedAt.UTC(), responseSequence)
	a.telemetryMu.Unlock()
	a.logger.Info("MeshCore tagged telemetry response correlated", "request_tag", tag, "response_tag", tag, "request_started_at", result.RequestedAt,
		"rf_correlation", result.RFEvidence.Outcome, "rf_candidate_count", result.RFEvidence.CandidateCount,
		"rf_delta_ms", result.RFEvidence.DeltaMS, "rf_rssi", result.RFEvidence.RSSI, "rf_snr", result.RFEvidence.SNR)
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
		// Capability detection is intentionally behavioral: an older Companion
		// that rejects CMD_SEND_BINARY_REQ gets the legacy request exactly once.
		if request != nil && request.mode == telemetryRequestBinary && binaryRequestUnsupported(frame.Payload) {
			a.telemetryMu.Lock()
			if a.telemetry == request {
				a.binaryTelemetryCapability = telemetryBinaryUnsupported
				request.mode, request.tagKnown, request.fallbackReason = telemetryRequestLegacy, false, "unsupported_command"
			}
			a.telemetryMu.Unlock()
			a.logger.Info("MeshCore telemetry transport fell back to legacy", "transport", "legacy", "reason", "unsupported_command")
			if a.currentTelemetry(request) {
				a.mu.RLock()
				link := a.link
				a.mu.RUnlock()
				if link == nil || link.WriteFrame(context.Background(), BuildTelemetryRequest(request.target)) != nil {
					a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryAdapterDisconnected)
				}
			}
			return
		}
		a.finishCurrentTelemetry(TelemetryResult{}, ErrTelemetryFirmware)
	case ResponseSent:
		// Protocol 13: [0x06][is_flood][ack_hash x4][estimated_timeout_ms x4 LE].
		if len(frame.Payload) < 10 {
			a.finishCurrentTelemetry(TelemetryResult{}, ErrInvalidTelemetryPayload)
			return
		}
		requested := time.Duration(binary.LittleEndian.Uint32(frame.Payload[6:10])) * time.Millisecond
		a.telemetryMu.Lock()
		request := a.telemetry
		if request != nil && request.mode == telemetryRequestBinary {
			tag := binary.LittleEndian.Uint32(frame.Payload[2:6])
			a.pruneExpiredTelemetryTagsLocked(time.Now().UTC())
			if _, expired := a.expiredTelemetryTags[tag]; expired {
				a.telemetryMu.Unlock()
				a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryFirmware)
				return
			}
			wasUnknown := a.binaryTelemetryCapability == telemetryBinaryUnknown
			request.tag, request.tagKnown = tag, true
			a.binaryTelemetryCapability = telemetryBinarySupported
			if wasUnknown {
				a.logger.Info("MeshCore tagged binary telemetry capability detected", "transport", "tagged_binary", "capability", "supported")
			}
			a.logger.Info("MeshCore tagged telemetry request accepted", "request_tag", tag, "request_started_at", request.requestedAt, "estimated_timeout", requested)
		}
		if requested <= 0 || requested >= defaultTelemetryTimeout {
			a.telemetryMu.Unlock()
			return
		}
		if requested < minimumTelemetryTimeout {
			requested = minimumTelemetryTimeout
		}
		if request != nil && request.mode == telemetryRequestBinary {
			requested += binaryTelemetryGuard
		}
		if request != nil && request.suggestedTimer == nil {
			request.suggestedTimer = time.AfterFunc(requested, func() {
				a.finishTelemetry(request, TelemetryResult{}, ErrTelemetryTimeout)
			})
		}
		a.telemetryMu.Unlock()
	}
}

func binaryRequestUnsupported(payload []byte) bool {
	// ERR_CODE_UNSUPPORTED_CMD is 1 in the pinned stock source. Treat any
	// malformed error as a normal firmware error rather than silently changing
	// the transport path.
	return len(payload) >= 2 && payload[1] == 1
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
	if result.RequestedAt.IsZero() {
		result.RequestedAt = request.requestedAt
	}
	if request.mode == telemetryRequestBinary && request.tagKnown {
		tag := request.tag
		if result.RequestTag == nil {
			result.RequestTag = &tag
		}
		result.Tagged = true
		result.Transport = "tagged_binary"
		result.Capability = "tagged_binary_supported"
		a.expiredTelemetryTags[tag] = time.Now().UTC().Add(expiredTagRetention)
		if errors.Is(err, ErrTelemetryTimeout) {
			a.logger.Warn("MeshCore tagged telemetry request expired", "request_tag", tag, "request_started_at", result.RequestedAt)
		}
	} else if request.mode == telemetryRequestLegacy {
		result.Transport = "legacy"
		result.Capability = "legacy_fallback"
		result.FallbackReason = request.fallbackReason
	}
	if result.ResponseOpcode == 0 {
		result.ResponseOpcode = PushTelemetryResponse
	}
	result.RouteAttempt = request.routeAttempt.copy()
	// Neither stock telemetry response form proves a return path, flood/direct
	// mode, or receiver-local RF metadata. 0x8C additionally has no source
	// prefix; its exact tag-to-request mapping supplies target ownership.
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

func (a *Adapter) pruneExpiredTelemetryTagsLocked(now time.Time) {
	for tag, expiry := range a.expiredTelemetryTags {
		if !expiry.After(now) {
			delete(a.expiredTelemetryTags, tag)
		}
	}
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
		SourcePrefix:   hex.EncodeToString(prefix[:]),
		ResponseOpcode: PushTelemetryResponse,
		ReceivedAt:     receivedAt.UTC(),
		Telemetry:      telemetry,
		RawFrame:       append([]byte(nil), frame...),
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
