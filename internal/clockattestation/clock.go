package clockattestation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/ucarion/jcs"
)

const (
	MaxReceiptSkew  = 5 * time.Second
	MaxOffsetDelta  = 2 * time.Second
	MaxSampleAge    = 15 * time.Minute
	utcMilliseconds = "2006-01-02T15:04:05.000Z"
)

type Wire struct {
	Token     string `json:"token"`
	IssuedAt  string `json:"issuedAt"`
	ExpiresAt string `json:"expiresAt"`
}

type Claims struct {
	ExpiresAt       time.Time
	InstallationID  string
	IssuedAt        time.Time
	Nonce           string
	ReceiverAgentID string
}

type Candidate struct {
	Token      string
	ReceivedAt time.Time
	Claims     Claims
}

type Sample struct {
	Token           string    `json:"token"`
	ReceivedAt      time.Time `json:"received_at"`
	IssuedAt        time.Time `json:"issued_at"`
	ExpiresAt       time.Time `json:"expires_at"`
	Nonce           string    `json:"nonce"`
	ReceiverAgentID string    `json:"receiver_agent_id"`
	InstallationID  string    `json:"installation_id"`
}

func Parse(w Wire, receivedAt time.Time, trustedTransport bool) (*Candidate, error) {
	if !trustedTransport {
		return nil, errors.New("clock attestation was not received over trusted cloud transport")
	}
	parts := strings.Split(w.Token, ".")
	if len(parts) != 3 || parts[0] != "v1" || strings.Contains(parts[1], "=") || strings.Contains(parts[2], "=") {
		return nil, errors.New("invalid clock attestation token grammar")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid clock attestation payload encoding")
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(mac) != 32 {
		return nil, errors.New("invalid clock attestation MAC encoding")
	}
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(&object); err != nil || len(object) != 6 {
		return nil, errors.New("invalid clock attestation claims")
	}
	canonical, err := jcs.Format(object)
	if err != nil || !bytes.Equal(payload, []byte(canonical)) {
		return nil, errors.New("clock attestation payload is not canonical JCS")
	}
	if version, ok := object["v"].(float64); !ok || version != 1 {
		return nil, errors.New("invalid clock attestation version")
	}
	issuedAt, err := exactTime(object, "issuedAt")
	if err != nil {
		return nil, err
	}
	expiresAt, err := exactTime(object, "expiresAt")
	if err != nil || expiresAt.Sub(issuedAt) != MaxSampleAge {
		return nil, errors.New("invalid clock attestation expiry")
	}
	receiverID, ok := exactString(object, "receiverAgentId")
	if !ok || strings.TrimSpace(receiverID) != receiverID || receiverID == "" {
		return nil, errors.New("invalid clock attestation receiver binding")
	}
	installationID, ok := exactString(object, "installationId")
	if !ok || strings.TrimSpace(installationID) != installationID || installationID == "" {
		return nil, errors.New("invalid clock attestation installation binding")
	}
	nonce, ok := exactString(object, "nonce")
	nonceBytes, nonceErr := base64.RawURLEncoding.DecodeString(nonce)
	if !ok || nonceErr != nil || len(nonce) != 22 || len(nonceBytes) != 16 || strings.Contains(nonce, "=") {
		return nil, errors.New("invalid clock attestation nonce")
	}
	_, err = time.Parse(utcMilliseconds, w.IssuedAt)
	if err != nil || w.IssuedAt != issuedAt.Format(utcMilliseconds) {
		return nil, errors.New("clock attestation issuedAt does not match claims")
	}
	_, err = time.Parse(utcMilliseconds, w.ExpiresAt)
	if err != nil || w.ExpiresAt != expiresAt.Format(utcMilliseconds) {
		return nil, errors.New("clock attestation expiresAt does not match claims")
	}
	receivedAt = receivedAt.UTC()
	if receivedAt.IsZero() || receivedAt.After(expiresAt) || abs(receivedAt.Sub(issuedAt)) > MaxReceiptSkew {
		return nil, errors.New("clock attestation is stale or outside receipt skew")
	}
	return &Candidate{Token: w.Token, ReceivedAt: receivedAt, Claims: Claims{
		ExpiresAt: expiresAt, InstallationID: installationID, IssuedAt: issuedAt,
		Nonce: nonce, ReceiverAgentID: receiverID,
	}}, nil
}

func exactString(object map[string]any, key string) (string, bool) {
	value, ok := object[key].(string)
	return value, ok
}

func exactTime(object map[string]any, key string) (time.Time, error) {
	value, ok := exactString(object, key)
	if !ok {
		return time.Time{}, fmt.Errorf("invalid clock attestation %s", key)
	}
	parsed, err := time.Parse(utcMilliseconds, value)
	if err != nil || value != parsed.UTC().Format(utcMilliseconds) {
		return time.Time{}, fmt.Errorf("invalid clock attestation %s", key)
	}
	return parsed.UTC(), nil
}

func (c Candidate) BoundSample(receiverAgentID, installationID string) (Sample, error) {
	if c.Claims.ReceiverAgentID != strings.TrimSpace(receiverAgentID) || c.Claims.InstallationID != strings.TrimSpace(installationID) {
		return Sample{}, errors.New("clock attestation binding mismatch")
	}
	return Sample{Token: c.Token, ReceivedAt: c.ReceivedAt, IssuedAt: c.Claims.IssuedAt,
		ExpiresAt: c.Claims.ExpiresAt, Nonce: c.Claims.Nonce,
		ReceiverAgentID: c.Claims.ReceiverAgentID, InstallationID: c.Claims.InstallationID}, nil
}

func Add(samples []Sample, sample Sample) []Sample {
	result := make([]Sample, 0, 2)
	for _, existing := range samples {
		if existing.Nonce != sample.Nonce {
			result = append(result, existing)
		}
	}
	result = append(result, sample)
	if len(result) > 2 {
		result = result[len(result)-2:]
	}
	return result
}

func Envelope(samples []Sample, observedAt time.Time, receiverAgentID, installationID string) map[string]any {
	if len(samples) != 2 {
		return nil
	}
	a, b := samples[0], samples[1]
	if b.ReceivedAt.Before(a.ReceivedAt) {
		a, b = b, a
	}
	observedAt = observedAt.UTC()
	if a.Nonce == b.Nonce || a.ReceiverAgentID != receiverAgentID || b.ReceiverAgentID != receiverAgentID ||
		a.InstallationID != installationID || b.InstallationID != installationID ||
		observedAt.Before(b.ReceivedAt) || observedAt.Sub(b.ReceivedAt) > MaxSampleAge ||
		observedAt.After(a.ExpiresAt) || observedAt.After(b.ExpiresAt) ||
		abs(a.ReceivedAt.Sub(a.IssuedAt)) > MaxReceiptSkew || abs(b.ReceivedAt.Sub(b.IssuedAt)) > MaxReceiptSkew ||
		abs(a.ReceivedAt.Sub(a.IssuedAt)-b.ReceivedAt.Sub(b.IssuedAt)) > MaxOffsetDelta {
		return nil
	}
	return map[string]any{"source": "cloud_attestation", "state": "synchronized", "samples": []any{
		map[string]any{"token": a.Token, "receivedAt": a.ReceivedAt.Format(time.RFC3339Nano)},
		map[string]any{"token": b.Token, "receivedAt": b.ReceivedAt.Format(time.RFC3339Nano)},
	}}
}

func abs(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
