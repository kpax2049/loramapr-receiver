package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultPath is a development fallback and will be overridden by packaging.
	DefaultPath = "./receiver.json"
)

type Config struct {
	NodeID      string        `json:"node_id"`
	PortalAddr  string        `json:"portal_addr"`
	Heartbeat   Duration      `json:"heartbeat"`
	Cloud       CloudConfig   `json:"cloud"`
	Meshtastic  MeshConfig    `json:"meshtastic"`
	StoragePath string        `json:"storage_path"`
}

type Duration time.Duration

func (d Duration) Std() time.Duration {
	return time.Duration(d)
}

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*d = Duration(0)
		return nil
	}

	if strings.HasPrefix(raw, "\"") {
		var asText string
		if err := json.Unmarshal(data, &asText); err != nil {
			return err
		}
		parsed, err := time.ParseDuration(asText)
		if err != nil {
			return fmt.Errorf("invalid duration %q: %w", asText, err)
		}
		*d = Duration(parsed)
		return nil
	}

	asInt, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid duration value %q", raw)
	}
	*d = Duration(time.Duration(asInt))
	return nil
}

type CloudConfig struct {
	BaseURL   string `json:"base_url"`
	PairToken string `json:"pair_token"`
	APIKey    string `json:"api_key"`
}

type MeshConfig struct {
	Transport string `json:"transport"`
	Device    string `json:"device"`
	Network   string `json:"network"`
}

func Default() Config {
	return Config{
		NodeID:      "unpaired",
		PortalAddr:  "127.0.0.1:8080",
		Heartbeat:   Duration(30 * time.Second),
		StoragePath: "./data",
		Cloud: CloudConfig{
			BaseURL: "https://api.loramapr.example",
		},
		Meshtastic: MeshConfig{
			Transport: "serial",
		},
	}
}

func Load(path string) (Config, error) {
	if path == "" {
		path = DefaultPath
	}

	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return Config{}, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultPath
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
