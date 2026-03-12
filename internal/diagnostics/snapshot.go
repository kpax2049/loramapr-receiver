package diagnostics

import (
	"net"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/buildinfo"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
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
		Version     string `json:"version"`
		Channel     string `json:"channel"`
		Commit      string `json:"commit,omitempty"`
		BuildDate   string `json:"build_date,omitempty"`
		BuildID     string `json:"build_id,omitempty"`
		GoVersion   string `json:"go_version"`
		Platform    string `json:"platform"`
		Arch        string `json:"arch"`
		InstallType string `json:"install_type,omitempty"`
		Mode        string `json:"mode,omitempty"`
		ConfigPath  string `json:"config_path,omitempty"`
	} `json:"runtime"`
	Config struct {
		SchemaVersion     int    `json:"schema_version"`
		StateSchema       int    `json:"state_schema_version"`
		StatePath         string `json:"state_path,omitempty"`
		RuntimeProfile    string `json:"runtime_profile,omitempty"`
		PortalBindAddress string `json:"portal_bind_address,omitempty"`
	} `json:"config"`
	Pairing struct {
		Phase      state.PairingPhase `json:"phase"`
		Authorized bool               `json:"authorized"`
		LastChange string             `json:"last_change,omitempty"`
		RetryCount int                `json:"retry_count,omitempty"`
		LastError  string             `json:"last_error,omitempty"`
		HasToken   bool               `json:"has_activation_token"`
		HasCode    bool               `json:"has_pairing_code"`
	} `json:"pairing"`
	Cloud struct {
		BaseURL             string     `json:"base_url"`
		PersistedEndpoint   string     `json:"persisted_endpoint,omitempty"`
		ConfigVersion       string     `json:"config_version,omitempty"`
		HasIngestCredential bool       `json:"has_ingest_credential"`
		Probe               CloudProbe `json:"probe"`
	} `json:"cloud"`
	Network struct {
		PortalBind   string           `json:"portal_bind"`
		Probe        NetworkProbe     `json:"probe"`
		LocalRuntime LocalStatusProbe `json:"local_runtime"`
	} `json:"network"`
	Meshtastic struct {
		Transport      string      `json:"transport"`
		ConfiguredPath string      `json:"configured_path,omitempty"`
		Connection     string      `json:"connection_state,omitempty"`
		Probe          DeviceProbe `json:"probe"`
	} `json:"meshtastic"`
	Update struct {
		Enabled            bool       `json:"enabled"`
		ManifestURL        string     `json:"manifest_url,omitempty"`
		Status             string     `json:"status,omitempty"`
		Summary            string     `json:"summary,omitempty"`
		Hint               string     `json:"hint,omitempty"`
		ManifestVersion    string     `json:"manifest_version,omitempty"`
		ManifestChannel    string     `json:"manifest_channel,omitempty"`
		RecommendedVersion string     `json:"recommended_version,omitempty"`
		LastCheckedAt      *time.Time `json:"last_checked_at,omitempty"`
		ConfiguredMin      string     `json:"configured_min_supported_version,omitempty"`
	} `json:"update"`
	Operations struct {
		Overall string             `json:"overall"`
		Summary string             `json:"summary"`
		Checks  []OperationalCheck `json:"checks"`
	} `json:"operations"`
	Attention struct {
		State          AttentionState    `json:"state"`
		Category       AttentionCategory `json:"category,omitempty"`
		Code           string            `json:"code,omitempty"`
		Summary        string            `json:"summary,omitempty"`
		Hint           string            `json:"hint,omitempty"`
		ActionRequired bool              `json:"action_required"`
	} `json:"attention"`
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
	ProbeNetwork func() NetworkProbe
	ProbeLocal   func(bindAddress string, timeout time.Duration) LocalStatusProbe
	DetectDevice func(cfg config.MeshtasticConfig) (meshtastic.DetectionResult, error)
	CloudTimeout time.Duration
	ConfigPath   string
	StatePath    string
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
	probeNetworkFn := opts.ProbeNetwork
	if probeNetworkFn == nil {
		probeNetworkFn = ProbeLocalNetwork
	}
	probeLocalFn := opts.ProbeLocal
	if probeLocalFn == nil {
		probeLocalFn = ProbeLocalRuntimeStatus
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
	networkProbe := probeNetworkFn()
	localProbe := probeLocalFn(cfg.Portal.BindAddress, 2*time.Second)
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

	build := buildinfo.Current()
	out := SupportSnapshot{GeneratedAt: now}
	out.Runtime.Version = build.Version
	out.Runtime.Channel = build.Channel
	out.Runtime.Commit = build.Commit
	out.Runtime.BuildDate = build.BuildDate
	out.Runtime.BuildID = build.BuildID
	out.Runtime.GoVersion = runtime.Version()
	out.Runtime.Platform = runtime.GOOS
	out.Runtime.Arch = runtime.GOARCH
	out.Runtime.InstallType = inferInstallType(data.Runtime.InstallType, cfg.Runtime.Profile)
	out.Runtime.Mode = strings.TrimSpace(data.Runtime.Mode)
	out.Runtime.ConfigPath = opts.ConfigPath

	out.Config.SchemaVersion = cfg.SchemaVersion
	out.Config.StateSchema = data.SchemaVersion
	out.Config.RuntimeProfile = strings.TrimSpace(cfg.Runtime.Profile)
	out.Config.PortalBindAddress = strings.TrimSpace(cfg.Portal.BindAddress)
	out.Config.StatePath = strings.TrimSpace(opts.StatePath)
	if out.Config.StatePath == "" {
		out.Config.StatePath = strings.TrimSpace(cfg.Paths.StateFile)
	}

	out.Pairing.Phase = data.Pairing.Phase
	out.Pairing.Authorized = data.Pairing.Phase == state.PairingSteadyState && strings.TrimSpace(data.Cloud.IngestAPIKey) != ""
	out.Pairing.LastChange = strings.TrimSpace(data.Pairing.LastChange)
	out.Pairing.RetryCount = data.Pairing.RetryCount
	out.Pairing.LastError = strings.TrimSpace(data.Pairing.LastError)
	out.Pairing.HasToken = strings.TrimSpace(data.Pairing.ActivationToken) != ""
	out.Pairing.HasCode = strings.TrimSpace(data.Pairing.PairingCode) != ""

	out.Cloud.BaseURL = strings.TrimSpace(cfg.Cloud.BaseURL)
	out.Cloud.PersistedEndpoint = strings.TrimSpace(data.Cloud.EndpointURL)
	out.Cloud.ConfigVersion = strings.TrimSpace(data.Cloud.ConfigVersion)
	out.Cloud.HasIngestCredential = strings.TrimSpace(data.Cloud.IngestAPIKey) != ""
	out.Cloud.Probe = cloudProbe

	out.Network.PortalBind = strings.TrimSpace(cfg.Portal.BindAddress)
	out.Network.Probe = networkProbe
	out.Network.LocalRuntime = summarizeLocalProbe(localProbe)

	out.Meshtastic.Transport = strings.TrimSpace(cfg.Meshtastic.Transport)
	out.Meshtastic.ConfiguredPath = strings.TrimSpace(cfg.Meshtastic.Device)
	out.Meshtastic.Connection = deviceProbe.State
	if localProbe.Snapshot != nil {
		out.Meshtastic.Connection = snapshotComponentState(localProbe.Snapshot, "meshtastic")
	}
	out.Meshtastic.Probe = deviceProbe

	out.Update.Enabled = cfg.Update.Enabled
	out.Update.ManifestURL = strings.TrimSpace(cfg.Update.ManifestURL)
	out.Update.Status = strings.TrimSpace(data.Update.Status)
	if localProbe.Snapshot != nil && strings.TrimSpace(localProbe.Snapshot.UpdateStatus) != "" {
		out.Update.Status = strings.TrimSpace(localProbe.Snapshot.UpdateStatus)
	}
	out.Update.Summary = strings.TrimSpace(data.Update.Summary)
	out.Update.Hint = strings.TrimSpace(data.Update.Hint)
	out.Update.ManifestVersion = strings.TrimSpace(data.Update.ManifestVersion)
	out.Update.ManifestChannel = strings.TrimSpace(data.Update.ManifestChannel)
	out.Update.RecommendedVersion = strings.TrimSpace(data.Update.RecommendedVersion)
	out.Update.LastCheckedAt = cloneTimePtr(data.Update.LastCheckedAt)
	out.Update.ConfiguredMin = strings.TrimSpace(cfg.Update.MinSupportedVersion)

	opsInput := OperationalInput{
		Now:                 now,
		Lifecycle:           strings.TrimSpace(data.Runtime.Mode),
		Ready:               false,
		PairingPhase:        string(data.Pairing.Phase),
		HasIngestCredential: strings.TrimSpace(data.Cloud.IngestAPIKey) != "",
		CloudReachable:      cloudProbe.Status == "reachable",
		CloudProbeStatus:    cloudProbe.Status,
		MeshtasticState:     out.Meshtastic.Connection,
		UpdateStatus:        out.Update.Status,
	}
	if localProbe.Snapshot != nil {
		opsInput.Lifecycle = string(localProbe.Snapshot.Lifecycle)
		opsInput.Ready = localProbe.Snapshot.Ready
		opsInput.ReadyReason = localProbe.Snapshot.ReadyReason
		opsInput.CloudReachable = localProbe.Snapshot.CloudReachable
		opsInput.MeshtasticState = snapshotComponentState(localProbe.Snapshot, "meshtastic")
		opsInput.IngestQueueDepth = localProbe.Snapshot.IngestQueueDepth
		opsInput.LastPacketAck = cloneTimePtr(localProbe.Snapshot.LastPacketAck)
		opsInput.LastPacketQueued = cloneTimePtr(localProbe.Snapshot.LastPacketQueued)
		opsInput.UpdateStatus = strings.TrimSpace(localProbe.Snapshot.UpdateStatus)
	}
	ops := EvaluateOperational(opsInput)
	out.Operations.Overall = ops.Overall
	out.Operations.Summary = ops.Summary
	out.Operations.Checks = append([]OperationalCheck(nil), ops.Checks...)
	attention := DeriveAttention(finding, ops)
	out.Attention.State = attention.State
	out.Attention.Category = attention.Category
	out.Attention.Code = attention.Code
	out.Attention.Summary = attention.Summary
	out.Attention.Hint = attention.Hint
	out.Attention.ActionRequired = attention.ActionRequired

	out.Diagnostics.FailureCode = finding.Code
	out.Diagnostics.FailureSummary = strings.TrimSpace(finding.Summary)
	out.Diagnostics.FailureHint = strings.TrimSpace(finding.Hint)
	out.Diagnostics.RecentErrors = collectRecentErrors(data, cloudProbe, networkProbe, deviceProbe, localProbe)

	out.Redaction.OmittedFields = []string{
		"cloud.ingest_api_key_secret",
		"pairing.pairing_code",
		"pairing.activation_token",
		"cloud.credential_ref",
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

func collectRecentErrors(data state.Data, cloud CloudProbe, network NetworkProbe, device DeviceProbe, localProbe LocalStatusProbe) []string {
	errorsOut := []string{}
	if value := strings.TrimSpace(data.Pairing.LastError); value != "" {
		errorsOut = append(errorsOut, value)
	}
	if cloud.Status == "unreachable" && strings.TrimSpace(cloud.Detail) != "" {
		errorsOut = append(errorsOut, "cloud probe: "+cloud.Detail)
	}
	if strings.ToLower(strings.TrimSpace(network.Status)) == "unavailable" && strings.TrimSpace(network.Detail) != "" {
		errorsOut = append(errorsOut, "network probe: "+network.Detail)
	}
	if device.State == "error" && strings.TrimSpace(device.Detail) != "" {
		errorsOut = append(errorsOut, "meshtastic detect: "+device.Detail)
	}
	if localProbe.Status == "unreachable" && strings.TrimSpace(localProbe.Detail) != "" {
		errorsOut = append(errorsOut, "local runtime probe: "+localProbe.Detail)
	}
	if localProbe.Snapshot != nil && strings.TrimSpace(localProbe.Snapshot.LastError) != "" {
		errorsOut = append(errorsOut, "runtime: "+strings.TrimSpace(localProbe.Snapshot.LastError))
	}
	if len(errorsOut) > 6 {
		return append([]string(nil), errorsOut[:6]...)
	}
	return errorsOut
}

func cloneTimePtr(input *time.Time) *time.Time {
	if input == nil {
		return nil
	}
	value := input.UTC()
	return &value
}

func inferInstallType(stateInstallType string, profile string) string {
	value := strings.TrimSpace(stateInstallType)
	if value != "" {
		return value
	}
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "appliance-pi":
		return "pi-appliance"
	case "linux-service":
		return "linux-package"
	case "windows-user":
		return "windows-user"
	default:
		return "manual"
	}
}

func summarizeLocalProbe(probe LocalStatusProbe) LocalStatusProbe {
	out := LocalStatusProbe{
		Status: probe.Status,
		Detail: probe.Detail,
		URL:    probe.URL,
	}
	if probe.Snapshot == nil {
		return out
	}
	copySnap := *probe.Snapshot
	copySnap.Components = nil
	copySnap.RecentFailures = nil
	out.Snapshot = &copySnap
	return out
}

func snapshotComponentState(snapshot *status.Snapshot, name string) string {
	if snapshot == nil || snapshot.Components == nil {
		return "unknown"
	}
	component, ok := snapshot.Components[name]
	if !ok {
		return "unknown"
	}
	value := strings.TrimSpace(component.State)
	if value == "" {
		return "unknown"
	}
	return value
}
