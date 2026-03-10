package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type PairingClient interface {
	ExchangePairingCode(ctx context.Context, pairingCode string) (BootstrapExchange, error)
	ActivateReceiver(ctx context.Context, activateEndpoint string, request ActivationRequest) (ActivationResult, error)
}

type BootstrapExchange struct {
	InstallSessionID  string
	FlowKey           string
	ActivationToken   string
	ActivationExpires time.Time
	ReceiverLabel     string
	ActivateEndpoint  string
	HeartbeatEndpoint string
	IngestEndpoint    string
}

type ActivationRequest struct {
	ActivationToken string
	Label           string
	RuntimeVersion  string
	Platform        string
	Arch            string
	Metadata        map[string]any
}

type ActivationResult struct {
	ReceiverAgentID   string
	OwnerID           string
	IngestAPIKeyID    string
	IngestAPIKey      string
	IngestEndpoint    string
	HeartbeatEndpoint string
	ActivatedAt       time.Time
}

type APIError struct {
	StatusCode int
	Message    string
	Retryable  bool
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("cloud api error status=%d", e.StatusCode)
	}
	return fmt.Sprintf("cloud api error status=%d: %s", e.StatusCode, e.Message)
}

func IsRetryable(err error) bool {
	if err == nil {
		return false
	}

	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Retryable
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, context.Canceled) {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}

	return false
}

type HTTPClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPClient(baseURL string, timeout time.Duration) *HTTPClient {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		client: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *HTTPClient) ExchangePairingCode(ctx context.Context, pairingCode string) (BootstrapExchange, error) {
	request := struct {
		PairingCode string `json:"pairingCode"`
	}{
		PairingCode: strings.TrimSpace(pairingCode),
	}

	var response struct {
		InstallSessionID  string `json:"installSessionId"`
		FlowKey           string `json:"flowKey"`
		ActivationToken   string `json:"activationToken"`
		ActivationExpires string `json:"activationExpiresAt"`
		ReceiverLabel     string `json:"receiverLabel"`
		ActivateEndpoint  string `json:"activateEndpoint"`
		HeartbeatEndpoint string `json:"heartbeatEndpoint"`
		IngestEndpoint    string `json:"ingestEndpoint"`
	}

	if err := c.postJSON(ctx, "/api/receiver/bootstrap/exchange", request, nil, &response); err != nil {
		return BootstrapExchange{}, err
	}

	expiresAt, err := time.Parse(time.RFC3339, response.ActivationExpires)
	if err != nil {
		return BootstrapExchange{}, fmt.Errorf("parse activation expiry: %w", err)
	}

	return BootstrapExchange{
		InstallSessionID:  response.InstallSessionID,
		FlowKey:           response.FlowKey,
		ActivationToken:   response.ActivationToken,
		ActivationExpires: expiresAt,
		ReceiverLabel:     response.ReceiverLabel,
		ActivateEndpoint:  response.ActivateEndpoint,
		HeartbeatEndpoint: response.HeartbeatEndpoint,
		IngestEndpoint:    response.IngestEndpoint,
	}, nil
}

func (c *HTTPClient) ActivateReceiver(
	ctx context.Context,
	activateEndpoint string,
	request ActivationRequest,
) (ActivationResult, error) {
	requestURL, err := c.resolveURL(activateEndpoint)
	if err != nil {
		return ActivationResult{}, err
	}

	type payload struct {
		ActivationToken string         `json:"activationToken"`
		Label           string         `json:"label,omitempty"`
		RuntimeVersion  string         `json:"runtimeVersion,omitempty"`
		Platform        string         `json:"platform,omitempty"`
		Arch            string         `json:"arch,omitempty"`
		Metadata        map[string]any `json:"metadata,omitempty"`
	}

	body := payload{
		ActivationToken: strings.TrimSpace(request.ActivationToken),
		Label:           strings.TrimSpace(request.Label),
		RuntimeVersion:  strings.TrimSpace(request.RuntimeVersion),
		Platform:        strings.TrimSpace(request.Platform),
		Arch:            strings.TrimSpace(request.Arch),
		Metadata:        request.Metadata,
	}

	var response struct {
		ReceiverAgentID   string `json:"receiverAgentId"`
		OwnerID           string `json:"ownerId"`
		IngestAPIKeyID    string `json:"ingestApiKeyId"`
		IngestAPIKey      string `json:"ingestApiKeySecret"`
		IngestEndpoint    string `json:"ingestEndpoint"`
		HeartbeatEndpoint string `json:"heartbeatEndpoint"`
		ActivatedAt       string `json:"activatedAt"`
	}

	if err := c.postJSON(ctx, requestURL, body, nil, &response); err != nil {
		return ActivationResult{}, err
	}

	activatedAt, err := time.Parse(time.RFC3339, response.ActivatedAt)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("parse activation time: %w", err)
	}

	return ActivationResult{
		ReceiverAgentID:   response.ReceiverAgentID,
		OwnerID:           response.OwnerID,
		IngestAPIKeyID:    response.IngestAPIKeyID,
		IngestAPIKey:      response.IngestAPIKey,
		IngestEndpoint:    response.IngestEndpoint,
		HeartbeatEndpoint: response.HeartbeatEndpoint,
		ActivatedAt:       activatedAt,
	}, nil
}

func (c *HTTPClient) postJSON(
	ctx context.Context,
	pathOrURL string,
	request any,
	headers map[string]string,
	response any,
) error {
	requestURL, err := c.resolveURL(pathOrURL)
	if err != nil {
		return err
	}

	body, err := json.Marshal(request)
	if err != nil {
		return err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		message := decodeErrorMessage(httpResp.Body)
		return &APIError{
			StatusCode: httpResp.StatusCode,
			Message:    message,
			Retryable:  retryableStatus(httpResp.StatusCode),
		}
	}

	if response == nil {
		_, _ = io.Copy(io.Discard, httpResp.Body)
		return nil
	}

	if err := json.NewDecoder(httpResp.Body).Decode(response); err != nil {
		return err
	}
	return nil
}

func (c *HTTPClient) resolveURL(pathOrURL string) (string, error) {
	raw := strings.TrimSpace(pathOrURL)
	if raw == "" {
		if c.baseURL == "" {
			return "", errors.New("cloud base URL is not configured")
		}
		return c.baseURL, nil
	}

	parsed, err := url.Parse(raw)
	if err == nil && parsed.IsAbs() {
		return parsed.String(), nil
	}

	if c.baseURL == "" {
		return "", errors.New("cloud base URL is not configured")
	}
	if strings.HasPrefix(raw, "/") {
		return c.baseURL + raw, nil
	}
	return c.baseURL + "/" + raw, nil
}

func retryableStatus(code int) bool {
	if code == http.StatusRequestTimeout || code == http.StatusTooManyRequests {
		return true
	}
	return code >= 500
}

func decodeErrorMessage(body io.Reader) string {
	var payload struct {
		Message any `json:"message"`
		Error   any `json:"error"`
	}
	data, err := io.ReadAll(io.LimitReader(body, 2048))
	if err != nil {
		return ""
	}
	if err := json.Unmarshal(data, &payload); err == nil {
		if msg := normalizeErrorMessage(payload.Message); msg != "" {
			return msg
		}
		if msg := normalizeErrorMessage(payload.Error); msg != "" {
			return msg
		}
	}
	return strings.TrimSpace(string(data))
}

func normalizeErrorMessage(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" {
				continue
			}
			parts = append(parts, text)
		}
		return strings.Join(parts, "; ")
	default:
		return ""
	}
}
