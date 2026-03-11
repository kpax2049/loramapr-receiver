package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultPath is the local development fallback.
	DefaultPath = "./receiver.json"

	CurrentSchemaVersion = 2
)

type RunMode string

const (
	ModeAuto    RunMode = "auto"
	ModeSetup   RunMode = "setup"
	ModeService RunMode = "service"
)

type Config struct {
	SchemaVersion int              `json:"schema_version,omitempty"`
	Service       ServiceConfig    `json:"service"`
	Runtime       RuntimeConfig    `json:"runtime"`
	Paths         PathsConfig      `json:"paths"`
	Portal        PortalConfig     `json:"portal"`
	Cloud         CloudConfig      `json:"cloud"`
	Update        UpdateConfig     `json:"update"`
	Meshtastic    MeshtasticConfig `json:"meshtastic"`
	Logging       LoggingConfig    `json:"logging"`
}

type ServiceConfig struct {
	Mode      RunMode  `json:"mode"`
	Heartbeat Duration `json:"heartbeat"`
}

type RuntimeConfig struct {
	Profile string `json:"profile"`
}

type PathsConfig struct {
	StateFile string `json:"state_file"`
}

type PortalConfig struct {
	BindAddress string `json:"bind_address"`
}

type CloudConfig struct {
	BaseURL string `json:"base_url"`
}

type UpdateConfig struct {
	Enabled             bool     `json:"enabled"`
	ManifestURL         string   `json:"manifest_url,omitempty"`
	CheckInterval       Duration `json:"check_interval"`
	RequestTimeout      Duration `json:"request_timeout"`
	MinSupportedVersion string   `json:"min_supported_version,omitempty"`
}

type MeshtasticConfig struct {
	Transport string `json:"transport,omitempty"`
	Device    string `json:"device,omitempty"`
	Network   string `json:"network,omitempty"`
}

type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
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

func Default() Config {
	return Config{
		SchemaVersion: CurrentSchemaVersion,
		Service: ServiceConfig{
			Mode:      ModeAuto,
			Heartbeat: Duration(30 * time.Second),
		},
		Runtime: RuntimeConfig{
			Profile: "auto",
		},
		Paths: PathsConfig{
			StateFile: "./data/receiver-state.json",
		},
		Portal: PortalConfig{
			BindAddress: "127.0.0.1:8080",
		},
		Cloud: CloudConfig{
			BaseURL: "https://api.loramapr.example",
		},
		Update: UpdateConfig{
			Enabled:        false,
			CheckInterval:  Duration(6 * time.Hour),
			RequestTimeout: Duration(4 * time.Second),
		},
		Meshtastic: MeshtasticConfig{
			Transport: "serial",
		},
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
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
			if err := cfg.Validate(); err != nil {
				return Config{}, err
			}
			return cfg, nil
		}
		return Config{}, err
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.migrate(); err != nil {
		return Config{}, err
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion <= 0 {
		return errors.New("schema_version must be > 0")
	}
	if c.SchemaVersion > CurrentSchemaVersion {
		return fmt.Errorf("config schema version %d is newer than runtime supports (%d)", c.SchemaVersion, CurrentSchemaVersion)
	}

	mode := c.Service.Mode
	if mode == "" {
		mode = ModeAuto
	}
	switch mode {
	case ModeAuto, ModeSetup, ModeService:
	default:
		return fmt.Errorf("invalid service mode %q", c.Service.Mode)
	}

	if c.Service.Heartbeat.Std() <= 0 {
		return errors.New("heartbeat must be > 0")
	}

	switch strings.ToLower(strings.TrimSpace(c.Runtime.Profile)) {
	case "auto", "local-dev", "linux-service", "windows-user", "appliance-pi":
	default:
		return fmt.Errorf("invalid runtime.profile %q", c.Runtime.Profile)
	}

	if strings.TrimSpace(c.Paths.StateFile) == "" {
		return errors.New("paths.state_file is required")
	}

	if _, _, err := net.SplitHostPort(c.Portal.BindAddress); err != nil {
		return fmt.Errorf("invalid portal.bind_address %q: %w", c.Portal.BindAddress, err)
	}

	u, err := url.Parse(c.Cloud.BaseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("invalid cloud.base_url %q", c.Cloud.BaseURL)
	}

	if c.Update.CheckInterval.Std() <= 0 {
		return errors.New("update.check_interval must be > 0")
	}
	if c.Update.RequestTimeout.Std() <= 0 {
		return errors.New("update.request_timeout must be > 0")
	}
	if value := strings.TrimSpace(c.Update.ManifestURL); value != "" {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("invalid update.manifest_url %q", c.Update.ManifestURL)
		}
	}

	switch strings.ToLower(strings.TrimSpace(c.Meshtastic.Transport)) {
	case "serial", "json_stream", "disabled":
	default:
		return fmt.Errorf("invalid meshtastic.transport %q", c.Meshtastic.Transport)
	}

	switch strings.ToLower(c.Logging.Level) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("invalid logging.level %q", c.Logging.Level)
	}

	switch strings.ToLower(c.Logging.Format) {
	case "json", "text":
	default:
		return fmt.Errorf("invalid logging.format %q", c.Logging.Format)
	}

	return nil
}

func Save(path string, cfg Config) error {
	if path == "" {
		path = DefaultPath
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return err
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

func (c *Config) applyDefaults() {
	defaults := Default()
	if c.SchemaVersion == 0 {
		c.SchemaVersion = defaults.SchemaVersion
	}
	if c.Service.Mode == "" {
		c.Service.Mode = defaults.Service.Mode
	}
	if c.Service.Heartbeat.Std() <= 0 {
		c.Service.Heartbeat = defaults.Service.Heartbeat
	}
	if c.Runtime.Profile == "" {
		c.Runtime.Profile = defaults.Runtime.Profile
	}
	if c.Paths.StateFile == "" {
		c.Paths.StateFile = defaults.Paths.StateFile
	}
	if c.Portal.BindAddress == "" {
		c.Portal.BindAddress = defaults.Portal.BindAddress
	}
	if c.Cloud.BaseURL == "" {
		c.Cloud.BaseURL = defaults.Cloud.BaseURL
	}
	if c.Update.CheckInterval.Std() <= 0 {
		c.Update.CheckInterval = defaults.Update.CheckInterval
	}
	if c.Update.RequestTimeout.Std() <= 0 {
		c.Update.RequestTimeout = defaults.Update.RequestTimeout
	}
	if c.Meshtastic.Transport == "" {
		c.Meshtastic.Transport = defaults.Meshtastic.Transport
	}
	if c.Logging.Level == "" {
		c.Logging.Level = defaults.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = defaults.Logging.Format
	}
}

func (c *Config) migrate() error {
	version := c.SchemaVersion
	if version == 0 {
		version = 1
	}

	if version <= 1 {
		version = 2
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf("config schema version %d is newer than runtime supports (%d)", version, CurrentSchemaVersion)
	}

	c.SchemaVersion = version
	return nil
}
