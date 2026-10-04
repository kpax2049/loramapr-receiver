package receiverevents

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	contractv1 "github.com/loramapr/loramapr-receiver/internal/contracts/protocolevents/v1"
	"github.com/ucarion/jcs"
)

const EndpointPath = "/api/receiver/events/v1"

type Prepared struct {
	DeliveryID     string
	Envelope       []byte
	EnvelopeSHA256 string
}

// Prepare assigns one UUIDv7 delivery identity, canonicalizes the whole event
// with RFC 8785 JCS, validates it against the vendored contract, and returns the
// exact immutable bytes that must be persisted before any delivery attempt.
func Prepare(event map[string]any, now time.Time) (Prepared, error) {
	deliveryID, err := NewUUIDv7(now, rand.Reader)
	if err != nil {
		return Prepared{}, err
	}
	return PrepareWithDeliveryID(event, deliveryID)
}

// PrepareWithDeliveryID is exposed for deterministic fixtures and replay. A
// caller may choose a new delivery ID for a replay but must never mutate a
// previously persisted delivery.
func PrepareWithDeliveryID(event map[string]any, deliveryID string) (Prepared, error) {
	if event == nil {
		return Prepared{}, errors.New("normalized event is required")
	}
	if !isUUIDv7(deliveryID) {
		return Prepared{}, errors.New("delivery id must be a lowercase UUIDv7")
	}

	encoded, err := json.Marshal(event)
	if err != nil {
		return Prepared{}, fmt.Errorf("encode normalized event: %w", err)
	}
	var copied map[string]any
	if err := json.Unmarshal(encoded, &copied); err != nil {
		return Prepared{}, fmt.Errorf("copy normalized event: %w", err)
	}
	if existing, ok := copied["deliveryId"]; ok && existing != "" && existing != deliveryID {
		return Prepared{}, errors.New("normalized event delivery id does not match assigned delivery")
	}
	copied["deliveryId"] = deliveryID

	canonical, err := jcs.Format(copied)
	if err != nil {
		return Prepared{}, fmt.Errorf("canonicalize normalized event: %w", err)
	}
	bytes := []byte(canonical)
	if err := contractv1.Validate(bytes); err != nil {
		return Prepared{}, err
	}
	digest := sha256.Sum256(bytes)
	return Prepared{
		DeliveryID:     deliveryID,
		Envelope:       append([]byte(nil), bytes...),
		EnvelopeSHA256: hex.EncodeToString(digest[:]),
	}, nil
}

func NewUUIDv7(now time.Time, entropy io.Reader) (string, error) {
	if entropy == nil {
		return "", errors.New("UUIDv7 entropy source is required")
	}
	millis := now.UTC().UnixMilli()
	if millis < 0 || millis > 0xffffffffffff {
		return "", errors.New("UUIDv7 timestamp is out of range")
	}
	var value [16]byte
	if _, err := io.ReadFull(entropy, value[:]); err != nil {
		return "", fmt.Errorf("read UUIDv7 entropy: %w", err)
	}
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	value[6] = (value[6] & 0x0f) | 0x70
	value[8] = (value[8] & 0x3f) | 0x80
	hexValue := hex.EncodeToString(value[:])
	return hexValue[0:8] + "-" + hexValue[8:12] + "-" + hexValue[12:16] + "-" + hexValue[16:20] + "-" + hexValue[20:32], nil
}

func isUUIDv7(value string) bool {
	if len(value) != 36 || value != strings.ToLower(value) || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	if value[19] != '8' && value[19] != '9' && value[19] != 'a' && value[19] != 'b' {
		return false
	}
	decoded, err := hex.DecodeString(strings.ReplaceAll(value, "-", ""))
	return err == nil && len(decoded) == 16
}
