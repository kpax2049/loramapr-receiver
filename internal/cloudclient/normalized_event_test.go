package cloudclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const normalizedDeliveryID = "018f47a2-7b3c-7def-8123-000000000001"

func TestPostNormalizedEventAcceptedPreservesEnvelopeBytesAndHeaders(t *testing.T) {
	t.Parallel()

	envelope := []byte("{\n  \"deliveryId\": \"" + normalizedDeliveryID + "\",\n  \"retainedWhitespace\": true\n}\n")
	const envelopeSHA256 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	client := normalizedEventTestClient(t, func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", req.Method)
		}
		if req.URL.Path != "/api/receiver/events/v1" {
			t.Fatalf("unexpected path %q", req.URL.Path)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if string(body) != string(envelope) {
			t.Fatalf("envelope bytes changed\nwant: %q\n got: %q", envelope, body)
		}
		wantHeaders := map[string]string{
			"Content-Type":               "application/json",
			"x-api-key":                  "ingest-secret",
			"x-idempotency-key":          normalizedDeliveryID,
			"x-loramapr-envelope-sha256": envelopeSHA256,
			"X-Request-Id":               "req-normalized-accepted",
		}
		for name, want := range wantHeaders {
			if got := req.Header.Get(name); got != want {
				t.Fatalf("header %s: want %q, got %q", name, want, got)
			}
		}

		response := jsonResponse(http.StatusAccepted, `{
			"accepted":true,
			"deliveryId":"`+normalizedDeliveryID+`"
		}`)
		response.Header.Set("X-Request-Id", "cloud-accepted-1")
		return response, nil
	})

	result, err := client.PostNormalizedEvent(
		WithRequestID(context.Background(), "req-normalized-accepted"),
		"/api/receiver/events/v1",
		"ingest-secret",
		envelope,
		normalizedDeliveryID,
		envelopeSHA256,
	)
	if err != nil {
		t.Fatalf("PostNormalizedEvent returned error: %v", err)
	}
	if result.StatusCode != http.StatusAccepted || result.Duplicate {
		t.Fatalf("unexpected accepted result: %#v", result)
	}
	if result.DeliveryID != normalizedDeliveryID || result.RequestID != "cloud-accepted-1" {
		t.Fatalf("unexpected accepted metadata: %#v", result)
	}
}

func TestPostNormalizedEventDuplicateAcknowledgement(t *testing.T) {
	t.Parallel()

	client := normalizedEventTestClient(t, func(_ *http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusOK, `{
			"accepted":true,
			"duplicate":true,
			"deliveryId":"`+normalizedDeliveryID+`"
		}`)
		response.Header.Set("X-Request-Id", "cloud-duplicate-1")
		return response, nil
	})

	result, err := postNormalizedEventForTest(client)
	if err != nil {
		t.Fatalf("PostNormalizedEvent returned error: %v", err)
	}
	if result.StatusCode != http.StatusOK || !result.Duplicate {
		t.Fatalf("unexpected duplicate result: %#v", result)
	}
	if result.DeliveryID != normalizedDeliveryID || result.RequestID != "cloud-duplicate-1" {
		t.Fatalf("unexpected duplicate metadata: %#v", result)
	}
}

func TestPostNormalizedEventStructuredConflict(t *testing.T) {
	t.Parallel()

	client := normalizedEventTestClient(t, func(_ *http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusConflict, `{
			"code":"DELIVERY_ID_PAYLOAD_MISMATCH",
			"message":"delivery payload changed"
		}`)
		response.Header.Set("X-Request-Id", "cloud-conflict-1")
		return response, nil
	})

	_, err := postNormalizedEventForTest(client)
	if err == nil {
		t.Fatal("expected conflict error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusConflict || apiErr.Code != "DELIVERY_ID_PAYLOAD_MISMATCH" {
		t.Fatalf("unexpected conflict status/code: %#v", apiErr)
	}
	if apiErr.Message != "delivery payload changed" || apiErr.RequestID != "cloud-conflict-1" {
		t.Fatalf("unexpected conflict metadata: %#v", apiErr)
	}
	if apiErr.Retryable || IsRetryable(err) {
		t.Fatalf("409 conflict must not be retryable: %#v", apiErr)
	}
}

func TestPostNormalizedEventDisabledRetryAfter(t *testing.T) {
	t.Parallel()

	client := normalizedEventTestClient(t, func(_ *http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusServiceUnavailable, `{
			"code":"NORMALIZED_INTAKE_DISABLED",
			"message":"normalized intake is disabled"
		}`)
		response.Header.Set("Retry-After", "120")
		return response, nil
	})

	_, err := postNormalizedEventForTest(client)
	if err == nil {
		t.Fatal("expected disabled error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable || apiErr.Code != "NORMALIZED_INTAKE_DISABLED" {
		t.Fatalf("unexpected disabled status/code: %#v", apiErr)
	}
	if !apiErr.Retryable || !IsRetryable(err) {
		t.Fatalf("503 disabled response must be retryable: %#v", apiErr)
	}
	if apiErr.RetryAfter != 120*time.Second {
		t.Fatalf("expected Retry-After 2m, got %s", apiErr.RetryAfter)
	}
}

func TestPostNormalizedEventQuotaExhaustionRetriesAfterCloudDelay(t *testing.T) {
	t.Parallel()

	client := normalizedEventTestClient(t, func(_ *http.Request) (*http.Response, error) {
		response := jsonResponse(http.StatusTooManyRequests, `{
			"accepted":false,
			"code":"STORAGE_QUOTA_EXCEEDED",
			"message":"Storage quota exceeded",
			"retryable":true,
			"retryAfterSeconds":3600
		}`)
		response.Header.Set("Retry-After", "3600")
		return response, nil
	})

	_, err := postNormalizedEventForTest(client)
	if err == nil {
		t.Fatal("expected quota error")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Code != "STORAGE_QUOTA_EXCEEDED" {
		t.Fatalf("unexpected quota status/code: %#v", apiErr)
	}
	if !apiErr.Retryable || !IsRetryable(err) || apiErr.RetryAfter != time.Hour {
		t.Fatalf("quota error must preserve retry semantics: %#v", apiErr)
	}
}

func TestPostNormalizedEventRejectsInconsistentSuccessAcknowledgement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{
			name:   "accepted status marked duplicate",
			status: http.StatusAccepted,
			body:   `{"accepted":true,"duplicate":true,"deliveryId":"` + normalizedDeliveryID + `"}`,
		},
		{
			name:   "ok status not marked duplicate",
			status: http.StatusOK,
			body:   `{"accepted":true,"duplicate":false,"deliveryId":"` + normalizedDeliveryID + `"}`,
		},
		{
			name:   "delivery id mismatch",
			status: http.StatusAccepted,
			body:   `{"accepted":true,"deliveryId":"018f47a2-7b3c-7def-8123-000000000099"}`,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			client := normalizedEventTestClient(t, func(_ *http.Request) (*http.Response, error) {
				return jsonResponse(test.status, test.body), nil
			})

			_, err := postNormalizedEventForTest(client)
			if err == nil {
				t.Fatal("expected inconsistent acknowledgement error")
			}
			if !strings.Contains(err.Error(), "inconsistent normalized delivery acknowledgement") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func normalizedEventTestClient(
	t *testing.T,
	roundTrip func(*http.Request) (*http.Response, error),
) *HTTPClient {
	t.Helper()
	return &HTTPClient{
		baseURL: "https://api.example.com",
		client:  &http.Client{Transport: roundTripFunc(roundTrip)},
	}
}

func postNormalizedEventForTest(client *HTTPClient) (NormalizedDeliveryResult, error) {
	return client.PostNormalizedEvent(
		context.Background(),
		"/api/receiver/events/v1",
		"ingest-secret",
		[]byte(`{"deliveryId":"`+normalizedDeliveryID+`"}`),
		normalizedDeliveryID,
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	)
}
