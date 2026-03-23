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
	SessionID string
	StartedAt time.Time
}

type HomeAutoSessionStopRequest struct {
	SessionID     string `json:"sessionId,omitempty"`
	TriggerNodeID string `json:"triggerNodeId,omitempty"`
	DedupeKey     string `json:"dedupeKey,omitempty"`
	Reason        string `json:"reason,omitempty"`
	StoppedAt     string `json:"stoppedAt,omitempty"`
}

type HomeAutoSessionStopResult struct {
	SessionID string
	StoppedAt time.Time
	Status    string
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
		RuntimeVersion  string         `json:"runtimeVersion,omitempty"`
		Platform        string         `json:"platform,omitempty"`
		Arch            string         `json:"arch,omitempty"`
		LocalNodeID     string         `json:"localNodeId,omitempty"`
		ObservedNodeIDs []string       `json:"observedNodeIds,omitempty"`
		Status          map[string]any `json:"status,omitempty"`
	}{
		RuntimeVersion:  strings.TrimSpace(heartbeat.RuntimeVersion),
		Platform:        strings.TrimSpace(heartbeat.Platform),
		Arch:            strings.TrimSpace(heartbeat.Arch),
		LocalNodeID:     strings.TrimSpace(heartbeat.LocalNodeID),
		ObservedNodeIDs: append([]string(nil), heartbeat.ObservedNodeIDs...),
		Status:          heartbeat.Status,
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
	}
	err := c.postJSON(ctx, heartbeatEndpoint, request, map[string]string{
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
	if err := c.postJSON(ctx, startEndpoint, request, headers, &response); err != nil {
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
		SessionID: strings.TrimSpace(response.SessionID),
		StartedAt: startedAt,
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
	if err := c.postJSON(ctx, stopEndpoint, request, headers, &response); err != nil {
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
		SessionID: strings.TrimSpace(response.SessionID),
		StoppedAt: stoppedAt,
		Status:    strings.TrimSpace(response.Status),
	}, nil
}

func (c *HTTPClient) postJSON(
	ctx context.Context,
	pathOrURL string,
	request any,
	headers map[string]string,
	response any,
) error {
	ctx, requestID := EnsureRequestID(ctx)

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
	if requestID != "" {
		httpReq.Header.Set("X-Request-Id", requestID)
	}
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
