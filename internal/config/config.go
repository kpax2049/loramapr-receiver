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

	CurrentSchemaVersion = 3
)

type RunMode string

const (
	ModeAuto    RunMode = "auto"
	ModeSetup   RunMode = "setup"
	ModeService RunMode = "service"
)

type Config struct {
	SchemaVersion    int                   `json:"schema_version,omitempty"`
	Service          ServiceConfig         `json:"service"`
	Runtime          RuntimeConfig         `json:"runtime"`
	Paths            PathsConfig           `json:"paths"`
	Portal           PortalConfig          `json:"portal"`
	Cloud            CloudConfig           `json:"cloud"`
	Update           UpdateConfig          `json:"update"`
	Meshtastic       MeshtasticConfig      `json:"meshtastic"`
	HomeAutoSession  HomeAutoSessionConfig `json:"home_auto_session,omitempty"`
	Logging          LoggingConfig         `json:"logging"`
	LoadedFromConfig string                `json:"-"`
}

type ServiceConfig struct {
	Mode      RunMode  `json:"mode"`
	Heartbeat Duration `json:"heartbeat"`
}

type RuntimeConfig struct {
	Profile   string `json:"profile"`
	LocalName string `json:"local_name,omitempty"`
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

type HomeAutoSessionMode string

const (
	HomeAutoSessionModeOff     HomeAutoSessionMode = "off"
	HomeAutoSessionModeObserve HomeAutoSessionMode = "observe"
	HomeAutoSessionModeControl HomeAutoSessionMode = "control"
)

type HomeAutoSessionConfig struct {
	Enabled              bool                    `json:"enabled"`
	Mode                 HomeAutoSessionMode     `json:"mode,omitempty"`
	Home                 HomeGeofenceConfig      `json:"home"`
	TrackedNodeIDs       []string                `json:"tracked_node_ids,omitempty"`
	StartDebounce        Duration                `json:"start_debounce"`
	StopDebounce         Duration                `json:"stop_debounce"`
	IdleStopTimeout      Duration                `json:"idle_stop_timeout"`
	StartupReconcile     bool                    `json:"startup_reconcile"`
	SessionNameTemplate  string                  `json:"session_name_template,omitempty"`
	SessionNotesTemplate string                  `json:"session_notes_template,omitempty"`
	Cloud                HomeAutoSessionCloudCfg `json:"cloud,omitempty"`
}

type HomeGeofenceConfig struct {
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	RadiusM float64 `json:"radius_m"`
}

type HomeAutoSessionCloudCfg struct {
	StartEndpoint string `json:"start_endpoint,omitempty"`
	StopEndpoint  string `json:"stop_endpoint,omitempty"`
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
		HomeAutoSession: HomeAutoSessionConfig{
			Enabled:          false,
			Mode:             HomeAutoSessionModeOff,
			StartDebounce:    Duration(30 * time.Second),
			StopDebounce:     Duration(30 * time.Second),
			IdleStopTimeout:  Duration(15 * time.Minute),
			StartupReconcile: true,
			Cloud: HomeAutoSessionCloudCfg{
				StartEndpoint: "/api/receiver/home-auto-session/start",
				StopEndpoint:  "/api/receiver/home-auto-session/stop",
			},
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
	cfg.LoadedFromConfig = path
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
	cfg.LoadedFromConfig = path
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
	if value := strings.TrimSpace(c.Runtime.LocalName); value != "" {
		if len(value) > 80 {
			return errors.New("runtime.local_name must be <= 80 characters")
		}
		if strings.ContainsAny(value, "\r\n\t") {
			return errors.New("runtime.local_name must not contain control whitespace")
		}
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
	if err := c.validateHomeAutoSession(); err != nil {
		return err
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
	cfg.LoadedFromConfig = path
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
	c.Runtime.LocalName = strings.TrimSpace(c.Runtime.LocalName)
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
	if c.HomeAutoSession.Mode == "" {
		c.HomeAutoSession.Mode = defaults.HomeAutoSession.Mode
	}
	if c.HomeAutoSession.StartDebounce.Std() <= 0 {
		c.HomeAutoSession.StartDebounce = defaults.HomeAutoSession.StartDebounce
	}
	if c.HomeAutoSession.StopDebounce.Std() <= 0 {
		c.HomeAutoSession.StopDebounce = defaults.HomeAutoSession.StopDebounce
	}
	if c.HomeAutoSession.IdleStopTimeout.Std() <= 0 {
		c.HomeAutoSession.IdleStopTimeout = defaults.HomeAutoSession.IdleStopTimeout
	}
	if strings.TrimSpace(c.HomeAutoSession.Cloud.StartEndpoint) == "" {
		c.HomeAutoSession.Cloud.StartEndpoint = defaults.HomeAutoSession.Cloud.StartEndpoint
	}
	if strings.TrimSpace(c.HomeAutoSession.Cloud.StopEndpoint) == "" {
		c.HomeAutoSession.Cloud.StopEndpoint = defaults.HomeAutoSession.Cloud.StopEndpoint
	}
	c.HomeAutoSession.TrackedNodeIDs = normalizeNodeIDs(c.HomeAutoSession.TrackedNodeIDs)
	c.HomeAutoSession.SessionNameTemplate = strings.TrimSpace(c.HomeAutoSession.SessionNameTemplate)
	c.HomeAutoSession.SessionNotesTemplate = strings.TrimSpace(c.HomeAutoSession.SessionNotesTemplate)
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
	if version <= 2 {
		version = 3
	}
	if version > CurrentSchemaVersion {
		return fmt.Errorf("config schema version %d is newer than runtime supports (%d)", version, CurrentSchemaVersion)
	}

	c.SchemaVersion = version
	return nil
}

func (c Config) validateHomeAutoSession() error {
	mode := HomeAutoSessionMode(strings.ToLower(strings.TrimSpace(string(c.HomeAutoSession.Mode))))
	switch mode {
	case HomeAutoSessionModeOff, HomeAutoSessionModeObserve, HomeAutoSessionModeControl:
	default:
		return fmt.Errorf("invalid home_auto_session.mode %q", c.HomeAutoSession.Mode)
	}

	if c.HomeAutoSession.StartDebounce.Std() <= 0 {
		return errors.New("home_auto_session.start_debounce must be > 0")
	}
	if c.HomeAutoSession.StopDebounce.Std() <= 0 {
		return errors.New("home_auto_session.stop_debounce must be > 0")
	}
	if c.HomeAutoSession.IdleStopTimeout.Std() <= 0 {
		return errors.New("home_auto_session.idle_stop_timeout must be > 0")
	}
	if len(c.HomeAutoSession.SessionNameTemplate) > 120 {
		return errors.New("home_auto_session.session_name_template must be <= 120 characters")
	}
	if len(c.HomeAutoSession.SessionNotesTemplate) > 512 {
		return errors.New("home_auto_session.session_notes_template must be <= 512 characters")
	}

	if !c.HomeAutoSession.Enabled || mode == HomeAutoSessionModeOff {
		return nil
	}

	if c.HomeAutoSession.Home.Lat < -90 || c.HomeAutoSession.Home.Lat > 90 {
		return errors.New("home_auto_session.home.lat must be between -90 and 90")
	}
	if c.HomeAutoSession.Home.Lon < -180 || c.HomeAutoSession.Home.Lon > 180 {
		return errors.New("home_auto_session.home.lon must be between -180 and 180")
	}
	if c.HomeAutoSession.Home.RadiusM <= 0 {
		return errors.New("home_auto_session.home.radius_m must be > 0")
	}
	if len(normalizeNodeIDs(c.HomeAutoSession.TrackedNodeIDs)) == 0 {
		return errors.New("home_auto_session.tracked_node_ids requires at least one node ID when enabled")
	}
	if len(normalizeNodeIDs(c.HomeAutoSession.TrackedNodeIDs)) > 64 {
		return errors.New("home_auto_session.tracked_node_ids must be <= 64 entries")
	}

	return nil
}

func normalizeNodeIDs(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	out := make([]string, 0, len(input))
	for _, raw := range input {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		lower := strings.ToLower(value)
		if _, exists := seen[lower]; exists {
			continue
		}
		seen[lower] = struct{}{}
		out = append(out, value)
	}
	return out
}
