package clockattestation

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ucarion/jcs"
)

const (
	testReceiver = "018f8f5b-8c6d-7abc-8def-0123456789ab"
	testInstall  = "00112233445566778899aabbccddeeff"
)

func TestParseAndSynchronizedEnvelope(t *testing.T) {
	issued := time.Date(2026, 8, 21, 12, 0, 0, 123000000, time.UTC)
	first := parseTestCandidate(t, issued, "AAAAAAAAAAAAAAAAAAAAAA", issued.Add(time.Second))
	second := parseTestCandidate(t, issued.Add(time.Minute), "AQEBAQEBAQEBAQEBAQEBAQ", issued.Add(time.Minute+time.Second))
	a, err := first.BoundSample(testReceiver, testInstall)
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.BoundSample(testReceiver, testInstall)
	if err != nil {
		t.Fatal(err)
	}
	samples := Add(Add(nil, a), b)
	clock := Envelope(samples, b.ReceivedAt.Add(time.Minute), testReceiver, testInstall)
	if clock == nil || clock["state"] != "synchronized" || len(clock["samples"].([]any)) != 2 {
		t.Fatalf("unexpected synchronized clock: %#v", clock)
	}
}

func TestParseRejectsMalformedAndBindingMismatch(t *testing.T) {
	issued := time.Date(2026, 8, 21, 12, 0, 0, 123000000, time.UTC)
	candidate := parseTestCandidate(t, issued, "AAAAAAAAAAAAAAAAAAAAAA", issued)
	if _, err := candidate.BoundSample("another-agent", testInstall); err == nil {
		t.Fatal("binding mismatch accepted")
	}
	wire := testWire(t, issued, "AAAAAAAAAAAAAAAAAAAAAA")
	wire.Token += "="
	if _, err := Parse(wire, issued, true); err == nil {
		t.Fatal("padded token accepted")
	}
	wire = testWire(t, issued, "AAAAAAAAAAAAAAAAAAAAAA")
	if _, err := Parse(wire, issued, false); err == nil {
		t.Fatal("untrusted transport accepted")
	}
}

func TestEnvelopeRejectsDuplicateStaleAndDisagreeingSamples(t *testing.T) {
	issued := time.Date(2026, 8, 21, 12, 0, 0, 123000000, time.UTC)
	first := parseTestCandidate(t, issued, "AAAAAAAAAAAAAAAAAAAAAA", issued)
	second := parseTestCandidate(t, issued.Add(time.Minute), "AQEBAQEBAQEBAQEBAQEBAQ", issued.Add(time.Minute+4*time.Second))
	a, _ := first.BoundSample(testReceiver, testInstall)
	b, _ := second.BoundSample(testReceiver, testInstall)
	if got := Envelope([]Sample{a, b}, b.ReceivedAt, testReceiver, testInstall); got != nil {
		t.Fatal("offset disagreement accepted")
	}
	b.ReceivedAt = b.IssuedAt
	b.Nonce = a.Nonce
	if got := Envelope([]Sample{a, b}, b.ReceivedAt, testReceiver, testInstall); got != nil {
		t.Fatal("duplicate nonce accepted")
	}
	b.Nonce = "AQEBAQEBAQEBAQEBAQEBAQ"
	if got := Envelope([]Sample{a, b}, b.ReceivedAt.Add(MaxSampleAge+time.Millisecond), testReceiver, testInstall); got != nil {
		t.Fatal("stale sample accepted")
	}
}

func parseTestCandidate(t *testing.T, issued time.Time, nonce string, received time.Time) Candidate {
	t.Helper()
	candidate, err := Parse(testWire(t, issued, nonce), received, true)
	if err != nil {
		t.Fatal(err)
	}
	return *candidate
}

func testWire(t *testing.T, issued time.Time, nonce string) Wire {
	t.Helper()
	expires := issued.Add(MaxSampleAge)
	claims := map[string]any{"expiresAt": expires.Format(utcMilliseconds), "installationId": testInstall,
		"issuedAt": issued.Format(utcMilliseconds), "nonce": nonce, "receiverAgentId": testReceiver, "v": float64(1)}
	payload, err := jcs.Format(claims)
	if err != nil {
		t.Fatal(err)
	}
	mac := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	token := fmt.Sprintf("v1.%s.%s", base64.RawURLEncoding.EncodeToString([]byte(payload)), mac)
	if strings.Contains(token, "=") {
		t.Fatal("fixture token is padded")
	}
	return Wire{Token: token, IssuedAt: issued.Format(utcMilliseconds), ExpiresAt: expires.Format(utcMilliseconds)}
}
