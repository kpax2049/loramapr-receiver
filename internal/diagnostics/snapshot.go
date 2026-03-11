package diagnostics

import (
	"net"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
)

type CloudProbe struct {
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

type DeviceProbe struct {
	State          string   `json:"state"`
	DetectedDevice string   `json:"detected_device,omitempty"`
	Candidates     []string `json:"candidates,omitempty"`
	Detail         string   `json:"detail,omitempty"`
}

type SupportSnapshot struct {
	GeneratedAt time.Time `json:"generated_at"`
	Runtime     struct {
		Version    string `json:"version"`
		GoVersion  string `json:"go_version"`
		Platform   string `json:"platform"`
		Arch       string `json:"arch"`
		ConfigPath string `json:"config_path,omitempty"`
	} `json:"runtime"`
	Pairing struct {
		Phase      state.PairingPhase `json:"phase"`
		LastChange string             `json:"last_change,omitempty"`
		RetryCount int                `json:"retry_count,omitempty"`
		LastError  string             `json:"last_error,omitempty"`
		HasToken   bool               `json:"has_activation_token"`
		HasCode    bool               `json:"has_pairing_code"`
	} `json:"pairing"`
	Cloud struct {
		BaseURL             string     `json:"base_url"`
		PersistedEndpoint   string     `json:"persisted_endpoint,omitempty"`
		HasIngestCredential bool       `json:"has_ingest_credential"`
		Probe               CloudProbe `json:"probe"`
	} `json:"cloud"`
	Meshtastic struct {
		Transport      string      `json:"transport"`
		ConfiguredPath string      `json:"configured_path,omitempty"`
		Probe          DeviceProbe `json:"probe"`
	} `json:"meshtastic"`
	Diagnostics struct {
		FailureCode    FailureCode `json:"failure_code,omitempty"`
		FailureSummary string      `json:"failure_summary,omitempty"`
		FailureHint    string      `json:"failure_hint,omitempty"`
		RecentErrors   []string    `json:"recent_errors,omitempty"`
	} `json:"diagnostics"`
	Redaction struct {
		OmittedFields []string `json:"omitted_fields"`
	} `json:"redaction"`
}

type CollectOptions struct {
	Now          func() time.Time
	ProbeCloud   func(baseURL string, timeout time.Duration) CloudProbe
	DetectDevice func(cfg config.MeshtasticConfig) (meshtastic.DetectionResult, error)
	CloudTimeout time.Duration
	ConfigPath   string
}

func CollectSupportSnapshot(cfg config.Config, data state.Data, finding Finding, opts CollectOptions) SupportSnapshot {
	nowFn := opts.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	probeCloudFn := opts.ProbeCloud
	if probeCloudFn == nil {
		probeCloudFn = ProbeCloudReachability
	}
	detectFn := opts.DetectDevice
	if detectFn == nil {
		detectFn = meshtastic.DetectDevice
	}
	cloudTimeout := opts.CloudTimeout
	if cloudTimeout <= 0 {
		cloudTimeout = 3 * time.Second
	}

	now := nowFn().UTC()
	cloudProbe := probeCloudFn(cfg.Cloud.BaseURL, cloudTimeout)
	detectResult, detectErr := detectFn(cfg.Meshtastic)

	deviceProbe := DeviceProbe{
		State:      "not_present",
		Candidates: append([]string(nil), detectResult.Candidates...),
	}
	if detectErr != nil {
		deviceProbe.State = "error"
		deviceProbe.Detail = detectErr.Error()
	} else if strings.TrimSpace(detectResult.Device) != "" {
		deviceProbe.State = "detected"
		deviceProbe.DetectedDevice = detectResult.Device
	}

	runtimeVersion := readRuntimeVersion()
	out := SupportSnapshot{GeneratedAt: now}
	out.Runtime.Version = runtimeVersion
	out.Runtime.GoVersion = runtime.Version()
	out.Runtime.Platform = runtime.GOOS
	out.Runtime.Arch = runtime.GOARCH
	out.Runtime.ConfigPath = opts.ConfigPath

	out.Pairing.Phase = data.Pairing.Phase
	out.Pairing.LastChange = strings.TrimSpace(data.Pairing.LastChange)
	out.Pairing.RetryCount = data.Pairing.RetryCount
	out.Pairing.LastError = strings.TrimSpace(data.Pairing.LastError)
	out.Pairing.HasToken = strings.TrimSpace(data.Pairing.ActivationToken) != ""
	out.Pairing.HasCode = strings.TrimSpace(data.Pairing.PairingCode) != ""

	out.Cloud.BaseURL = strings.TrimSpace(cfg.Cloud.BaseURL)
	out.Cloud.PersistedEndpoint = strings.TrimSpace(data.Cloud.EndpointURL)
	out.Cloud.HasIngestCredential = strings.TrimSpace(data.Cloud.IngestAPIKey) != ""
	out.Cloud.Probe = cloudProbe

	out.Meshtastic.Transport = strings.TrimSpace(cfg.Meshtastic.Transport)
	out.Meshtastic.ConfiguredPath = strings.TrimSpace(cfg.Meshtastic.Device)
	out.Meshtastic.Probe = deviceProbe

	out.Diagnostics.FailureCode = finding.Code
	out.Diagnostics.FailureSummary = strings.TrimSpace(finding.Summary)
	out.Diagnostics.FailureHint = strings.TrimSpace(finding.Hint)
	out.Diagnostics.RecentErrors = collectRecentErrors(data, cloudProbe, deviceProbe)

	out.Redaction.OmittedFields = []string{
		"cloud.ingest_api_key_secret",
		"pairing.pairing_code",
		"pairing.activation_token",
	}

	return out
}

func ProbeCloudReachability(baseURL string, timeout time.Duration) CloudProbe {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" {
		return CloudProbe{Status: "invalid", Detail: "cloud base URL is invalid"}
	}

	host := parsed.Host
	if !strings.Contains(host, ":") {
		switch strings.ToLower(parsed.Scheme) {
		case "http":
			host += ":80"
		default:
			host += ":443"
		}
	}

	conn, dialErr := net.DialTimeout("tcp", host, timeout)
	if dialErr != nil {
		return CloudProbe{Status: "unreachable", Detail: dialErr.Error()}
	}
	_ = conn.Close()
	return CloudProbe{Status: "reachable"}
}

func readRuntimeVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" || info.Main.Version == "(devel)" {
		return "dev"
	}
	return info.Main.Version
}

func collectRecentErrors(data state.Data, cloud CloudProbe, device DeviceProbe) []string {
	errorsOut := []string{}
	if value := strings.TrimSpace(data.Pairing.LastError); value != "" {
		errorsOut = append(errorsOut, value)
	}
	if cloud.Status == "unreachable" && strings.TrimSpace(cloud.Detail) != "" {
		errorsOut = append(errorsOut, "cloud probe: "+cloud.Detail)
	}
	if device.State == "error" && strings.TrimSpace(device.Detail) != "" {
		errorsOut = append(errorsOut, "meshtastic detect: "+device.Detail)
	}
	if len(errorsOut) > 6 {
		return append([]string(nil), errorsOut[:6]...)
	}
	return errorsOut
}
