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
	"strconv"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/clockattestation"
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
	ConfigVersion     string
	ReceiverLabel     string
	SiteLabel         string
	GroupLabel        string
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
	ReceiverLabel     string
	SiteLabel         string
	GroupLabel        string
	IngestAPIKeyID    string
	IngestAPIKey      string
	ConfigVersion     string
	IngestEndpoint    string
	HeartbeatEndpoint string
	ActivatedAt       time.Time
}

type ReceiverHeartbeat struct {
	RuntimeVersion  string
	Platform        string
	Arch            string
	LocalNodeID     string
	ObservedNodeIDs []string
	Status          map[string]any
	Adapters        []ReceiverAdapterStatus
}

// ReceiverAdapterStatus is the deliberately small cloud-safe projection of a
// local adapter. It never carries device paths, raw evidence, credentials, or
// arbitrary diagnostic strings.
type ReceiverAdapterStatus struct {
	Protocol        string                         `json:"protocol"`
	Enabled         bool                           `json:"enabled"`
	Configured      bool                           `json:"configured"`
	Lifecycle       string                         `json:"lifecycle"`
	Connected       bool                           `json:"connected"`
	Ready           bool                           `json:"ready"`
	Transport       string                         `json:"transport,omitempty"`
	ProtocolVersion string                         `json:"protocolVersion,omitempty"`
	ProfileState    string                         `json:"profileState,omitempty"`
	ErrorCode       string                         `json:"errorCode,omitempty"`
	Delivery        *ReceiverAdapterDeliveryStatus `json:"delivery,omitempty"`
}

type ReceiverAdapterDeliveryStatus struct {
	State            string `json:"state"`
	PendingCount     int    `json:"pendingCount,omitempty"`
	QuarantinedCount int    `json:"quarantinedCount,omitempty"`
	FailureCode      string `json:"failureCode,omitempty"`
}

type HomeAutoSessionManagedGeofence struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	RadiusM float64 `json:"radiusM"`
}

type HomeAutoSessionManagedCloudEndpoints struct {
	StartEndpoint string `json:"startEndpoint,omitempty"`
	StopEndpoint  string `json:"stopEndpoint,omitempty"`
}

type HomeAutoSessionManagedConfig struct {
	Version              string                               `json:"version,omitempty"`
	Enabled              *bool                                `json:"enabled,omitempty"`
	Mode                 string                               `json:"mode,omitempty"`
	Home                 HomeAutoSessionManagedGeofence       `json:"home"`
	TrackedNodeIDs       []string                             `json:"trackedNodeIds,omitempty"`
	StartDebounce        string                               `json:"startDebounce,omitempty"`
	StopDebounce         string                               `json:"stopDebounce,omitempty"`
	IdleStopTimeout      string                               `json:"idleStopTimeout,omitempty"`
	StartupReconcile     *bool                                `json:"startupReconcile,omitempty"`
	SessionNameTemplate  string                               `json:"sessionNameTemplate,omitempty"`
	SessionNotesTemplate string                               `json:"sessionNotesTemplate,omitempty"`
	Cloud                HomeAutoSessionManagedCloudEndpoints `json:"cloud,omitempty"`
}

type ReceiverHeartbeatAck struct {
	ReceiverAgentID       string
	OwnerID               string
	ReceiverLabel         string
	SiteLabel             string
	GroupLabel            string
	ConfigVersion         string
	LastHeartbeatAt       time.Time
	NodeCount             int
	HomeAutoSessionConfig *HomeAutoSessionManagedConfig
	ClockAttestation      *clockattestation.Candidate
}

type HomeAutoSessionStartRequest struct {
	TriggerNodeID string         `json:"triggerNodeId,omitempty"`
	DedupeKey     string         `json:"dedupeKey,omitempty"`
	Reason        string         `json:"reason,omitempty"`
	SessionName   string         `json:"sessionName,omitempty"`
	SessionNotes  string         `json:"sessionNotes,omitempty"`
	StartedAt     string         `json:"startedAt,omitempty"`
	Home          map[string]any `json:"home,omitempty"`
}

type HomeAutoSessionStartResult struct {
	SessionID      string
	StartedAt      time.Time
	StatusCode     int
	CloudRequestID string
}

type HomeAutoSessionStopRequest struct {
	SessionID     string `json:"sessionId,omitempty"`
	TriggerNodeID string `json:"triggerNodeId,omitempty"`
	DedupeKey     string `json:"dedupeKey,omitempty"`
	Reason        string `json:"reason,omitempty"`
	StoppedAt     string `json:"stoppedAt,omitempty"`
}

type HomeAutoSessionStopResult struct {
	SessionID      string
	StoppedAt      time.Time
	Status         string
	StatusCode     int
	CloudRequestID string
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
	RetryAfter time.Duration
	Route      string
	RequestID  string
	SessionID  string
}

type NormalizedDeliveryResult struct {
	StatusCode       int
	Duplicate        bool
	DeliveryID       string
	RequestID        string
	ClockAttestation *clockattestation.Candidate
}

type ClockAttestationCandidate = clockattestation.Candidate

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
	now     func() time.Time
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
		now: time.Now,
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
		ConfigVersion     string `json:"configVersion"`
		ReceiverLabel     string `json:"receiverLabel"`
		SiteLabel         string `json:"siteLabel"`
		GroupLabel        string `json:"groupLabel"`
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
		ConfigVersion:     strings.TrimSpace(response.ConfigVersion),
		ReceiverLabel:     response.ReceiverLabel,
		SiteLabel:         response.SiteLabel,
		GroupLabel:        response.GroupLabel,
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
		ReceiverLabel     string `json:"receiverLabel"`
		SiteLabel         string `json:"siteLabel"`
		GroupLabel        string `json:"groupLabel"`
		IngestAPIKeyID    string `json:"ingestApiKeyId"`
		IngestAPIKey      string `json:"ingestApiKeySecret"`
		ConfigVersion     string `json:"configVersion"`
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
		ReceiverLabel:     strings.TrimSpace(response.ReceiverLabel),
		SiteLabel:         strings.TrimSpace(response.SiteLabel),
		GroupLabel:        strings.TrimSpace(response.GroupLabel),
		IngestAPIKeyID:    response.IngestAPIKeyID,
		IngestAPIKey:      response.IngestAPIKey,
		ConfigVersion:     strings.TrimSpace(response.ConfigVersion),
		IngestEndpoint:    response.IngestEndpoint,
		HeartbeatEndpoint: response.HeartbeatEndpoint,
		ActivatedAt:       activatedAt,
	}, nil
}

func (c *HTTPClient) PostIngestEvent(
	ctx context.Context,
	ingestEndpoint string,
	apiKey string,
	payload map[string]any,
	idempotencyKey string,
) error {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		return errors.New("ingest API key is required")
	}

	headers := map[string]string{
		"x-api-key": trimmedKey,
	}
	if key := strings.TrimSpace(idempotencyKey); key != "" {
		headers["x-idempotency-key"] = key
	}

	var response struct {
		Status string `json:"status"`
	}
	return c.postJSON(ctx, ingestEndpoint, payload, headers, &response)
}

func (c *HTTPClient) PostNormalizedEvent(
	ctx context.Context,
	ingestEndpoint string,
	apiKey string,
	envelope []byte,
	deliveryID string,
	envelopeSHA256 string,
) (NormalizedDeliveryResult, error) {
	if strings.TrimSpace(apiKey) == "" {
		return NormalizedDeliveryResult{}, errors.New("ingest API key is required")
	}
	if len(envelope) == 0 || strings.TrimSpace(deliveryID) == "" || strings.TrimSpace(envelopeSHA256) == "" {
		return NormalizedDeliveryResult{}, errors.New("normalized delivery bytes, id, and hash are required")
	}
	ctx, requestID := EnsureRequestID(ctx)
	requestURL, err := c.resolveURL(ingestEndpoint)
	if err != nil {
		return NormalizedDeliveryResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(envelope))
	if err != nil {
		return NormalizedDeliveryResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", strings.TrimSpace(apiKey))
	request.Header.Set("x-idempotency-key", strings.TrimSpace(deliveryID))
	request.Header.Set("x-loramapr-envelope-sha256", strings.TrimSpace(envelopeSHA256))
	if requestID != "" {
		request.Header.Set("X-Request-Id", requestID)
	}

	response, err := c.client.Do(request)
	if err != nil {
		return NormalizedDeliveryResult{}, err
	}
	defer response.Body.Close()
	receivedAt := c.currentTime()
	responseRequest := responseRequestID(response.Header)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusAccepted {
		decoded := decodeErrorPayload(response.Body)
		return NormalizedDeliveryResult{}, &APIError{
			StatusCode: response.StatusCode,
			Code:       decoded.Code,
			Message:    decoded.Message,
			Retryable:  retryableStatus(response.StatusCode),
			RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now().UTC()),
			Route:      requestURL,
			RequestID:  responseRequest,
		}
	}
	var payload struct {
		Accepted         bool            `json:"accepted"`
		Duplicate        bool            `json:"duplicate"`
		DeliveryID       string          `json:"deliveryId"`
		ClockAttestation json.RawMessage `json:"clockAttestation"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&payload); err != nil {
		return NormalizedDeliveryResult{}, fmt.Errorf("decode normalized delivery response: %w", err)
	}
	if !payload.Accepted || payload.DeliveryID != deliveryID ||
		(response.StatusCode == http.StatusOK && !payload.Duplicate) ||
		(response.StatusCode == http.StatusAccepted && payload.Duplicate) {
		return NormalizedDeliveryResult{}, errors.New("cloud returned an inconsistent normalized delivery acknowledgement")
	}
	result := NormalizedDeliveryResult{
		StatusCode: response.StatusCode,
		Duplicate:  payload.Duplicate,
		DeliveryID: payload.DeliveryID,
		RequestID:  responseRequest,
	}
	result.ClockAttestation = c.parseClockAttestation(decodeClockWire(payload.ClockAttestation), receivedAt, request, response)
	return result, nil
}

func (c *HTTPClient) SendReceiverHeartbeat(
	ctx context.Context,
	heartbeatEndpoint string,
	apiKey string,
	heartbeat ReceiverHeartbeat,
) (ReceiverHeartbeatAck, error) {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		return ReceiverHeartbeatAck{}, errors.New("ingest API key is required")
	}

	request := struct {
		RuntimeVersion  string                  `json:"runtimeVersion,omitempty"`
		Platform        string                  `json:"platform,omitempty"`
		Arch            string                  `json:"arch,omitempty"`
		LocalNodeID     string                  `json:"localNodeId,omitempty"`
		ObservedNodeIDs []string                `json:"observedNodeIds,omitempty"`
		Status          map[string]any          `json:"status,omitempty"`
		Adapters        []ReceiverAdapterStatus `json:"adapters,omitempty"`
	}{
		RuntimeVersion:  strings.TrimSpace(heartbeat.RuntimeVersion),
		Platform:        strings.TrimSpace(heartbeat.Platform),
		Arch:            strings.TrimSpace(heartbeat.Arch),
		LocalNodeID:     strings.TrimSpace(heartbeat.LocalNodeID),
		ObservedNodeIDs: append([]string(nil), heartbeat.ObservedNodeIDs...),
		Status:          heartbeat.Status,
		Adapters:        append([]ReceiverAdapterStatus(nil), heartbeat.Adapters...),
	}

	var response struct {
		ReceiverAgentID       string                        `json:"receiverAgentId"`
		OwnerID               string                        `json:"ownerId"`
		ReceiverLabel         string                        `json:"receiverLabel"`
		SiteLabel             string                        `json:"siteLabel"`
		GroupLabel            string                        `json:"groupLabel"`
		ConfigVersion         string                        `json:"configVersion"`
		LastHeartbeatAt       string                        `json:"lastHeartbeatAt"`
		NodeCount             int                           `json:"nodeCount"`
		HomeAutoSessionConfig *HomeAutoSessionManagedConfig `json:"homeAutoSessionConfig"`
		ClockAttestation      json.RawMessage               `json:"clockAttestation"`
	}
	meta, err := c.postJSONWithMeta(ctx, heartbeatEndpoint, request, map[string]string{
		"x-api-key": trimmedKey,
	}, &response)
	if err != nil {
		return ReceiverHeartbeatAck{}, err
	}

	lastHeartbeatAt, err := time.Parse(time.RFC3339, response.LastHeartbeatAt)
	if err != nil {
		return ReceiverHeartbeatAck{}, fmt.Errorf("parse heartbeat time: %w", err)
	}

	return ReceiverHeartbeatAck{
		ReceiverAgentID:       response.ReceiverAgentID,
		OwnerID:               response.OwnerID,
		ReceiverLabel:         strings.TrimSpace(response.ReceiverLabel),
		SiteLabel:             strings.TrimSpace(response.SiteLabel),
		GroupLabel:            strings.TrimSpace(response.GroupLabel),
		ConfigVersion:         strings.TrimSpace(response.ConfigVersion),
		LastHeartbeatAt:       lastHeartbeatAt,
		NodeCount:             response.NodeCount,
		HomeAutoSessionConfig: response.HomeAutoSessionConfig,
		ClockAttestation:      c.parseClockAttestation(decodeClockWire(response.ClockAttestation), meta.ReceivedAt, meta.Request, meta.Response),
	}, nil
}

func (c *HTTPClient) StartHomeAutoSession(
	ctx context.Context,
	startEndpoint string,
	apiKey string,
	request HomeAutoSessionStartRequest,
) (HomeAutoSessionStartResult, error) {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		return HomeAutoSessionStartResult{}, errors.New("ingest API key is required")
	}

	headers := map[string]string{
		"x-api-key": trimmedKey,
	}
	if key := strings.TrimSpace(request.DedupeKey); key != "" {
		headers["x-idempotency-key"] = key
	}

	var response struct {
		SessionID string `json:"sessionId"`
		StartedAt string `json:"startedAt"`
	}
	meta, err := c.postJSONWithMeta(ctx, startEndpoint, request, headers, &response)
	if err != nil {
		return HomeAutoSessionStartResult{}, err
	}

	startedAt := time.Now().UTC()
	if strings.TrimSpace(response.StartedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, response.StartedAt)
		if err != nil {
			return HomeAutoSessionStartResult{}, fmt.Errorf("parse home auto session startedAt: %w", err)
		}
		startedAt = parsed.UTC()
	}

	return HomeAutoSessionStartResult{
		SessionID:      strings.TrimSpace(response.SessionID),
		StartedAt:      startedAt,
		StatusCode:     meta.StatusCode,
		CloudRequestID: meta.RequestID,
	}, nil
}

func (c *HTTPClient) StopHomeAutoSession(
	ctx context.Context,
	stopEndpoint string,
	apiKey string,
	request HomeAutoSessionStopRequest,
) (HomeAutoSessionStopResult, error) {
	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey == "" {
		return HomeAutoSessionStopResult{}, errors.New("ingest API key is required")
	}

	headers := map[string]string{
		"x-api-key": trimmedKey,
	}
	if key := strings.TrimSpace(request.DedupeKey); key != "" {
		headers["x-idempotency-key"] = key
	}

	var response struct {
		SessionID string `json:"sessionId"`
		StoppedAt string `json:"stoppedAt"`
		Status    string `json:"status"`
	}
	meta, err := c.postJSONWithMeta(ctx, stopEndpoint, request, headers, &response)
	if err != nil {
		return HomeAutoSessionStopResult{}, err
	}

	stoppedAt := time.Now().UTC()
	if strings.TrimSpace(response.StoppedAt) != "" {
		parsed, err := time.Parse(time.RFC3339, response.StoppedAt)
		if err != nil {
			return HomeAutoSessionStopResult{}, fmt.Errorf("parse home auto session stoppedAt: %w", err)
		}
		stoppedAt = parsed.UTC()
	}

	return HomeAutoSessionStopResult{
		SessionID:      strings.TrimSpace(response.SessionID),
		StoppedAt:      stoppedAt,
		Status:         strings.TrimSpace(response.Status),
		StatusCode:     meta.StatusCode,
		CloudRequestID: meta.RequestID,
	}, nil
}

func (c *HTTPClient) postJSON(
	ctx context.Context,
	pathOrURL string,
	request any,
	headers map[string]string,
	response any,
) error {
	_, err := c.postJSONWithMeta(ctx, pathOrURL, request, headers, response)
	return err
}

type postResponseMeta struct {
	StatusCode int
	RequestID  string
	ReceivedAt time.Time
	Request    *http.Request
	Response   *http.Response
}

func (c *HTTPClient) postJSONWithMeta(
	ctx context.Context,
	pathOrURL string,
	request any,
	headers map[string]string,
	response any,
) (postResponseMeta, error) {
	ctx, requestID := EnsureRequestID(ctx)

	requestURL, err := c.resolveURL(pathOrURL)
	if err != nil {
		return postResponseMeta{}, err
	}

	body, err := json.Marshal(request)
	if err != nil {
		return postResponseMeta{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return postResponseMeta{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}
	for key, value := range headers {
		httpReq.Header.Set(key, value)
	}

	httpResp, err := c.client.Do(httpReq)
	if err != nil {
		return postResponseMeta{}, err
	}
	defer httpResp.Body.Close()
	respRequestID := responseRequestID(httpResp.Header)
	meta := postResponseMeta{
		StatusCode: httpResp.StatusCode,
		RequestID:  respRequestID,
		ReceivedAt: c.currentTime(),
		Request:    httpReq,
		Response:   httpResp,
	}

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		decoded := decodeErrorPayload(httpResp.Body)
		return meta, &APIError{
			StatusCode: httpResp.StatusCode,
			Code:       decoded.Code,
			Message:    decoded.Message,
			Retryable:  retryableStatus(httpResp.StatusCode),
			RetryAfter: parseRetryAfter(httpResp.Header.Get("Retry-After"), time.Now().UTC()),
			Route:      requestURL,
			RequestID:  respRequestID,
			SessionID:  decoded.SessionID,
		}
	}

	if response == nil {
		_, _ = io.Copy(io.Discard, httpResp.Body)
		return meta, nil
	}

	if err := json.NewDecoder(httpResp.Body).Decode(response); err != nil {
		return meta, err
	}
	return meta, nil
}

func (c *HTTPClient) parseClockAttestation(wire *clockattestation.Wire, receivedAt time.Time, request *http.Request, response *http.Response) *clockattestation.Candidate {
	if wire == nil {
		return nil
	}
	trusted := c.trustedClockTransport(request, response)
	candidate, err := clockattestation.Parse(*wire, receivedAt, trusted)
	if err != nil {
		return nil
	}
	return candidate
}

func decodeClockWire(raw json.RawMessage) *clockattestation.Wire {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var wire clockattestation.Wire
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return nil
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil
	}
	return &wire
}

func (c *HTTPClient) currentTime() time.Time {
	if c.now != nil {
		return c.now().UTC()
	}
	return time.Now().UTC()
}

func (c *HTTPClient) trustedClockTransport(request *http.Request, response *http.Response) bool {
	if request == nil || request.URL == nil || response == nil || response.TLS == nil || !response.TLS.HandshakeComplete ||
		response.Request == nil || response.Request.URL == nil || response.Request != request || response.Request.Response != nil {
		return false
	}
	base, err := url.Parse(c.baseURL)
	if err != nil || !sameHTTPSOrigin(base, request.URL) || !sameHTTPSOrigin(base, response.Request.URL) {
		return false
	}
	return true
}

func sameHTTPSOrigin(left, right *url.URL) bool {
	if left == nil || right == nil || !strings.EqualFold(left.Scheme, "https") || !strings.EqualFold(right.Scheme, "https") {
		return false
	}
	return strings.EqualFold(left.Hostname(), right.Hostname()) && effectiveHTTPSPort(left) == effectiveHTTPSPort(right)
}

func effectiveHTTPSPort(value *url.URL) string {
	if port := value.Port(); port != "" {
		return port
	}
	return "443"
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

type decodedError struct {
	Code      string
	Message   string
	SessionID string
}

func decodeErrorPayload(body io.Reader) decodedError {
	result := decodedError{}
	var payload struct {
		Code      string         `json:"code"`
		Message   any            `json:"message"`
		Error     any            `json:"error"`
		SessionID string         `json:"sessionId"`
		Data      map[string]any `json:"data"`
	}
	data, err := io.ReadAll(io.LimitReader(body, 2048))
	if err != nil {
		return result
	}
	result.Message = strings.TrimSpace(string(data))
	if err := json.Unmarshal(data, &payload); err == nil {
		result.Code = strings.TrimSpace(payload.Code)
		if msg := normalizeErrorMessage(payload.Message); msg != "" {
			result.Message = msg
		} else if msg := normalizeErrorMessage(payload.Error); msg != "" {
			result.Message = msg
		}
		result.SessionID = strings.TrimSpace(payload.SessionID)
		if result.SessionID == "" && payload.Data != nil {
			if raw, ok := payload.Data["sessionId"]; ok {
				if value, ok := raw.(string); ok {
					result.SessionID = strings.TrimSpace(value)
				}
			}
		}
	}
	return result
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(value)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
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

func responseRequestID(header http.Header) string {
	if header == nil {
		return ""
	}
	if value := strings.TrimSpace(header.Get("X-Request-Id")); value != "" {
		return value
	}
	return strings.TrimSpace(header.Get("X-Request-ID"))
}
