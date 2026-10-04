package cloudclient

import (
	"crypto/tls"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/clockattestation"
)

func TestTrustedClockTransportRequiresExactHTTPSOriginAndTLS(t *testing.T) {
	client := &HTTPClient{baseURL: "https://api.example.com"}
	request := &http.Request{URL: mustURL(t, "https://api.example.com/events")}
	response := &http.Response{TLS: &tls.ConnectionState{HandshakeComplete: true}, Request: request}
	if !client.trustedClockTransport(request, response) {
		t.Fatal("exact HTTPS origin with completed TLS was rejected")
	}
	for name, mutate := range map[string]func(*http.Request, *http.Response){
		"http": func(req *http.Request, _ *http.Response) { req.URL = mustURL(t, "http://api.example.com/events") },
		"cross origin": func(_ *http.Request, resp *http.Response) {
			resp.Request = &http.Request{URL: mustURL(t, "https://other.example.com/events")}
		},
		"redirect": func(_ *http.Request, resp *http.Response) {
			resp.Request = &http.Request{
				URL:      mustURL(t, "https://api.example.com/events"),
				Response: &http.Response{StatusCode: http.StatusTemporaryRedirect},
			}
		},
		"different port": func(req *http.Request, _ *http.Response) { req.URL = mustURL(t, "https://api.example.com:444/events") },
		"missing TLS":    func(_ *http.Request, resp *http.Response) { resp.TLS = nil },
	} {
		t.Run(name, func(t *testing.T) {
			reqCopy := request.Clone(request.Context())
			respCopy := *response
			mutate(reqCopy, &respCopy)
			if client.trustedClockTransport(reqCopy, &respCopy) {
				t.Fatal("untrusted transport accepted")
			}
		})
	}
}

func TestInvalidOptionalClockDoesNotPoisonAcknowledgement(t *testing.T) {
	client := &HTTPClient{baseURL: "https://api.example.com"}
	request := &http.Request{URL: mustURL(t, "https://api.example.com/events")}
	response := &http.Response{TLS: &tls.ConnectionState{HandshakeComplete: true}, Request: request}
	invalid := &clockattestation.Wire{Token: "not-a-token", IssuedAt: "bad", ExpiresAt: "bad"}
	if got := client.parseClockAttestation(invalid, time.Now().UTC(), request, response); got != nil {
		t.Fatal("invalid optional attestation returned a candidate")
	}
}

func TestHTTPClientPreservesExistingRedirectBehavior(t *testing.T) {
	client := NewHTTPClient("https://api.example.com", time.Second)
	if client.client.CheckRedirect != nil {
		t.Fatal("clock attestation must not change redirect behavior for existing cloud calls")
	}
}

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
