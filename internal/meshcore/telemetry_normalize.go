package meshcore

import (
	"encoding/base64"
	"fmt"
	"math"

	"github.com/loramapr/loramapr-receiver/internal/protocoladapter"
)

// NormalizeTelemetryResult represents a manually solicited response as a
// durable normalized event. The full request target is the subject; the
// returned six-byte prefix remains protocol evidence and is never an identity.
// A correlated response is deliberately not an independently signed advert.
func NormalizeTelemetryResult(result TelemetryResult, binding ReceiverBinding) (map[string]any, error) {
	target, err := parseTelemetryTarget(result.TargetPublicKey)
	if err != nil {
		return nil, err
	}
	if len(result.RawFrame) == 0 || result.ReceivedAt.IsZero() {
		return nil, fmt.Errorf("%w: telemetry response evidence is required", ErrInvalidTelemetryPayload)
	}
	if result.SourcePrefix != fmt.Sprintf("%x", target[:telemetryPrefixLength]) {
		return nil, ErrTelemetryMismatchedResponse
	}
	if err := validateNormalizedTelemetry(result.Telemetry); err != nil {
		return nil, err
	}

	base := normalizedBase(protocoladapter.Event{ObservedAt: result.ReceivedAt.UTC()}, binding, PushFrame{
		Opcode: PushTelemetryResponse, Payload: append([]byte(nil), result.RawFrame...),
	})
	base["eventType"] = "meshcore_solicited_telemetry"
	base["contentKey"] = "meshcore:solicited-telemetry:v1:" + fmt.Sprintf("%x", target[:]) + ":" + sha256Hex(result.RawFrame)
	base["subject"] = map[string]any{
		"protocolNamespace": "ed25519",
		"canonicalId":       result.TargetPublicKey,
		"verification":      "unverified",
	}
	base["capabilities"] = map[string]any{
		"sender_identity":     "available",
		"position_signed":     "unavailable",
		"solicited_telemetry": "available",
		"receiver_clock":      capabilityAvailability(binding.Clock != nil),
	}
	base["authenticity"] = map[string]any{
		"state":                     "request_correlated",
		"method":                    "solicited_request_correlation",
		"independentlySigned":       false,
		"cryptographicallyVerified": false,
	}
	telemetry := map[string]any{
		"correlation":  "request_correlated",
		"authenticity": "not_independently_signed",
		"sourcePrefix": result.SourcePrefix,
	}
	if result.Telemetry.Voltage != nil {
		telemetry["voltage"] = *result.Telemetry.Voltage
	}
	if result.Telemetry.Latitude != nil {
		telemetry["latitude"] = *result.Telemetry.Latitude
	}
	if result.Telemetry.Longitude != nil {
		telemetry["longitude"] = *result.Telemetry.Longitude
	}
	if result.Telemetry.AltitudeM != nil {
		telemetry["altitudeM"] = *result.Telemetry.AltitudeM
	}
	if result.Telemetry.TemperatureC != nil {
		telemetry["temperatureC"] = *result.Telemetry.TemperatureC
	}
	base["solicitedTelemetry"] = telemetry
	routeAttempt := result.RouteAttempt.copy()
	if routeAttempt.Mode == "" || routeAttempt.Source == "" {
		routeAttempt = unknownRouteEvidence("request_route_unavailable")
	}
	requestRoute := map[string]any{
		"mode":       routeAttempt.Mode,
		"pathLength": routeAttempt.PathLength,
		"source":     routeAttempt.Source,
	}
	if len(routeAttempt.Path) > 0 {
		// Each element is one raw hex-encoded path hash. Do not concatenate
		// them: a future 2- or 3-byte hash must remain distinguishable.
		requestRoute["path"] = append([]string(nil), routeAttempt.Path...)
	}
	if result.RouteRecovery != "" {
		requestRoute["recoveryEvent"] = result.RouteRecovery
	}
	requestRoute["pathUpdateObserved"] = result.PathUpdateObserved
	base["requestRoute"] = requestRoute
	// PUSH_CODE_TELEMETRY_RESPONSE carries no return-path proof.
	base["responseRoute"] = map[string]any{"known": false}
	source := base["source"].(map[string]any)
	source["raw"] = base64.StdEncoding.EncodeToString(result.RawFrame)
	source["rawSha256"] = sha256Hex(result.RawFrame)
	source["evidence"] = map[string]any{
		"kind":                   "meshcore_solicited_telemetry_response_v1",
		"sourcePrefix":           result.SourcePrefix,
		"requestTargetCanonical": result.TargetPublicKey,
	}
	return base, nil
}

func validateNormalizedTelemetry(telemetry Telemetry) error {
	finiteRange := func(value *float64, minimum, maximum float64, name string) error {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0) || *value < minimum || *value > maximum) {
			return fmt.Errorf("%w: %s out of range", ErrInvalidTelemetryPayload, name)
		}
		return nil
	}
	if err := finiteRange(telemetry.Voltage, 0, 100, "voltage"); err != nil {
		return err
	}
	if err := finiteRange(telemetry.Latitude, -90, 90, "latitude"); err != nil {
		return err
	}
	if err := finiteRange(telemetry.Longitude, -180, 180, "longitude"); err != nil {
		return err
	}
	if err := finiteRange(telemetry.AltitudeM, -10000, 100000, "altitudeM"); err != nil {
		return err
	}
	if err := finiteRange(telemetry.TemperatureC, -100, 200, "temperatureC"); err != nil {
		return err
	}
	if telemetry.Voltage == nil && telemetry.Latitude == nil && telemetry.Longitude == nil && telemetry.AltitudeM == nil && telemetry.TemperatureC == nil {
		return fmt.Errorf("%w: no supported telemetry fields", ErrInvalidTelemetryPayload)
	}
	if (telemetry.Latitude == nil) != (telemetry.Longitude == nil) {
		return fmt.Errorf("%w: latitude and longitude must be paired", ErrInvalidTelemetryPayload)
	}
	return nil
}
