package cloudclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Packet struct {
	Source    string            `json:"source"`
	Payload   []byte            `json:"payload"`
	Timestamp time.Time         `json:"timestamp"`
	Meta      map[string]string `json:"meta,omitempty"`
}

type Heartbeat struct {
	NodeID           string            `json:"node_id"`
	Status           string            `json:"status"`
	PairingState     string            `json:"pairing_state"`
	ObservedAt       time.Time         `json:"observed_at"`
	MeshtasticHealth string            `json:"meshtastic_health"`
	Details          map[string]string `json:"details,omitempty"`
}

type Client interface {
	SendPacket(ctx context.Context, packet Packet) error
	SendHeartbeat(ctx context.Context, hb Heartbeat) error
}

type HTTPClient struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewHTTPClient(baseURL, apiKey string) *HTTPClient {
	return &HTTPClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *HTTPClient) SendPacket(ctx context.Context, packet Packet) error {
	return c.postJSON(ctx, "/v1/receiver/packets", packet)
}

func (c *HTTPClient) SendHeartbeat(ctx context.Context, hb Heartbeat) error {
	return c.postJSON(ctx, "/v1/receiver/heartbeat", hb)
}

func (c *HTTPClient) postJSON(ctx context.Context, path string, payload any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("cloud client unexpected status: %s", resp.Status)
	}
	return nil
}
