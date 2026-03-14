package diagnostics

import (
	"fmt"
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
	Identity    struct {
		InstallationID    string `json:"installation_id,omitempty"`
		LocalName         string `json:"local_name,omitempty"`
		Hostname          string `json:"hostname,omitempty"`
		CloudReceiverID   string `json:"cloud_receiver_id,omitempty"`
		CloudReceiverName string `json:"cloud_receiver_name,omitempty"`
		CloudSiteName     string `json:"cloud_site_name,omitempty"`
		CloudGroupName    string `json:"cloud_group_name,omitempty"`
	} `json:"identity"`
	Runtime struct {
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
		Transport      string `json:"transport"`
		ConfiguredPath string `json:"configured_path,omitempty"`
		Connection     string `json:"connection_state,omitempty"`
		ConfigSummary  struct {
			Available         bool       `json:"available"`
			UnavailableReason string     `json:"unavailable_reason,omitempty"`
			Region            string     `json:"region,omitempty"`
			PrimaryChannel    string     `json:"primary_channel,omitempty"`
			PrimaryChannelIdx int        `json:"primary_channel_index,omitempty"`
			PSKState          string     `json:"psk_state,omitempty"`
			LoRaPreset        string     `json:"lora_preset,omitempty"`
			LoRaBandwidth     string     `json:"lora_bandwidth,omitempty"`
			LoRaSpreading     string     `json:"lora_spreading,omitempty"`
			LoRaCodingRate    string     `json:"lora_coding_rate,omitempty"`
			ShareURLAvailable bool       `json:"share_url_available"`
			ShareURLRedacted  string     `json:"share_url_redacted,omitempty"`
			Source            string     `json:"source,omitempty"`
			UpdatedAt         *time.Time `json:"updated_at,omitempty"`
		} `json:"config_summary"`
		Probe DeviceProbe `json:"probe"`
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
	HomeAutoSession struct {
		Enabled               bool       `json:"enabled"`
		Mode                  string     `json:"mode,omitempty"`
		EffectiveConfigSource string     `json:"effective_config_source,omitempty"`
		EffectiveConfigVer    string     `json:"effective_config_version,omitempty"`
		CloudConfigPresent    bool       `json:"cloud_config_present,omitempty"`
		LastFetchedConfigVer  string     `json:"last_fetched_config_version,omitempty"`
		LastAppliedConfigVer  string     `json:"last_applied_config_version,omitempty"`
		LastConfigApplyResult string     `json:"last_config_apply_result,omitempty"`
		LastConfigApplyError  string     `json:"last_config_apply_error,omitempty"`
		DesiredConfigEnabled  *bool      `json:"desired_config_enabled,omitempty"`
		DesiredConfigMode     string     `json:"desired_config_mode,omitempty"`
		State                 string     `json:"state,omitempty"`
		ControlState          string     `json:"control_state,omitempty"`
		ActiveStateSource     string     `json:"active_state_source,omitempty"`
		Summary               string     `json:"summary,omitempty"`
		HomeSummary           string     `json:"home_summary,omitempty"`
		TrackedNodeIDs        []string   `json:"tracked_node_ids,omitempty"`
		TrackedNodeState      string     `json:"tracked_node_state,omitempty"`
		ReconciliationState   string     `json:"reconciliation_state,omitempty"`
		PendingAction         string     `json:"pending_action,omitempty"`
		ActiveSessionID       string     `json:"active_session_id,omitempty"`
		ActiveTriggerNode     string     `json:"active_trigger_node_id,omitempty"`
		LastDecisionReason    string     `json:"last_decision_reason,omitempty"`
		LastError             string     `json:"last_error,omitempty"`
		LastAction            string     `json:"last_action,omitempty"`
		LastActionResult      string     `json:"last_action_result,omitempty"`
		LastActionAt          *time.Time `json:"last_action_at,omitempty"`
		LastSuccessfulAction  string     `json:"last_successful_action,omitempty"`
		LastSuccessfulAt      *time.Time `json:"last_successful_at,omitempty"`
		BlockedReason         string     `json:"blocked_reason,omitempty"`
		ConsecutiveFailures   int        `json:"consecutive_failures,omitempty"`
		CooldownUntil         *time.Time `json:"cooldown_until,omitempty"`
		GPSStatus             string     `json:"gps_status,omitempty"`
		GPSReason             string     `json:"gps_reason,omitempty"`
		GPSNodeID             string     `json:"gps_node_id,omitempty"`
		GPSUpdatedAt          *time.Time `json:"gps_updated_at,omitempty"`
		LastDecisionAt        *time.Time `json:"last_decision_at,omitempty"`
	} `json:"home_auto_session"`
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
	out.Identity.InstallationID = strings.TrimSpace(data.Installation.ID)
	out.Identity.LocalName = strings.TrimSpace(data.Installation.LocalName)
	out.Identity.Hostname = strings.TrimSpace(data.Installation.Hostname)
	out.Identity.CloudReceiverID = strings.TrimSpace(data.Cloud.ReceiverID)
	out.Identity.CloudReceiverName = strings.TrimSpace(data.Cloud.ReceiverLabel)
	out.Identity.CloudSiteName = strings.TrimSpace(data.Cloud.SiteLabel)
	out.Identity.CloudGroupName = strings.TrimSpace(data.Cloud.GroupLabel)

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
	out.Meshtastic.ConfigSummary.Available = false
	out.Meshtastic.ConfigSummary.PSKState = "unknown"
	out.Meshtastic.ConfigSummary.UnavailableReason = "home node config summary not reported"
	if localProbe.Snapshot != nil {
		mesh := localProbe.Snapshot.MeshtasticConfig
		out.Meshtastic.ConfigSummary.Available = mesh.Available
		out.Meshtastic.ConfigSummary.UnavailableReason = strings.TrimSpace(mesh.UnavailableReason)
		out.Meshtastic.ConfigSummary.Region = strings.TrimSpace(mesh.Region)
		out.Meshtastic.ConfigSummary.PrimaryChannel = strings.TrimSpace(mesh.PrimaryChannel)
		out.Meshtastic.ConfigSummary.PrimaryChannelIdx = mesh.PrimaryChannelIdx
		out.Meshtastic.ConfigSummary.PSKState = strings.TrimSpace(mesh.PSKState)
		out.Meshtastic.ConfigSummary.LoRaPreset = strings.TrimSpace(mesh.LoRaPreset)
		out.Meshtastic.ConfigSummary.LoRaBandwidth = strings.TrimSpace(mesh.LoRaBandwidth)
		out.Meshtastic.ConfigSummary.LoRaSpreading = strings.TrimSpace(mesh.LoRaSpreading)
		out.Meshtastic.ConfigSummary.LoRaCodingRate = strings.TrimSpace(mesh.LoRaCodingRate)
		out.Meshtastic.ConfigSummary.ShareURLAvailable = mesh.ShareURLAvailable
		out.Meshtastic.ConfigSummary.ShareURLRedacted = strings.TrimSpace(mesh.ShareURLRedacted)
		out.Meshtastic.ConfigSummary.Source = strings.TrimSpace(mesh.Source)
		out.Meshtastic.ConfigSummary.UpdatedAt = cloneTimePtr(mesh.UpdatedAt)
		if out.Meshtastic.ConfigSummary.PSKState == "" {
			out.Meshtastic.ConfigSummary.PSKState = "unknown"
		}
		if out.Meshtastic.ConfigSummary.UnavailableReason == "" && !out.Meshtastic.ConfigSummary.Available {
			out.Meshtastic.ConfigSummary.UnavailableReason = "home node config summary unavailable"
		}
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
	out.HomeAutoSession.Enabled = cfg.HomeAutoSession.Enabled
	if data.HomeAutoSession.DesiredConfigEnabled != nil {
		out.HomeAutoSession.Enabled = *data.HomeAutoSession.DesiredConfigEnabled
	}
	out.HomeAutoSession.Mode = strings.TrimSpace(string(cfg.HomeAutoSession.Mode))
	if value := strings.TrimSpace(data.HomeAutoSession.DesiredConfigMode); value != "" {
		out.HomeAutoSession.Mode = value
	}
	out.HomeAutoSession.EffectiveConfigSource = strings.TrimSpace(data.HomeAutoSession.EffectiveConfigSource)
	out.HomeAutoSession.EffectiveConfigVer = strings.TrimSpace(data.HomeAutoSession.EffectiveConfigVersion)
	out.HomeAutoSession.CloudConfigPresent = data.HomeAutoSession.CloudConfigPresent
	out.HomeAutoSession.LastFetchedConfigVer = strings.TrimSpace(data.HomeAutoSession.LastFetchedConfigVer)
	out.HomeAutoSession.LastAppliedConfigVer = strings.TrimSpace(data.HomeAutoSession.LastAppliedConfigVer)
	out.HomeAutoSession.LastConfigApplyResult = strings.TrimSpace(data.HomeAutoSession.LastConfigApplyResult)
	out.HomeAutoSession.LastConfigApplyError = strings.TrimSpace(data.HomeAutoSession.LastConfigApplyError)
	if data.HomeAutoSession.DesiredConfigEnabled != nil {
		value := *data.HomeAutoSession.DesiredConfigEnabled
		out.HomeAutoSession.DesiredConfigEnabled = &value
	}
	out.HomeAutoSession.DesiredConfigMode = strings.TrimSpace(data.HomeAutoSession.DesiredConfigMode)
	out.HomeAutoSession.HomeSummary = formatHomeSummary(cfg.HomeAutoSession.Home)
	out.HomeAutoSession.TrackedNodeIDs = append([]string(nil), cfg.HomeAutoSession.TrackedNodeIDs...)
	out.HomeAutoSession.State = strings.TrimSpace(data.HomeAutoSession.ModuleState)
	out.HomeAutoSession.ControlState = strings.TrimSpace(data.HomeAutoSession.ControlState)
	out.HomeAutoSession.ActiveStateSource = strings.TrimSpace(data.HomeAutoSession.ActiveStateSource)
	out.HomeAutoSession.ReconciliationState = strings.TrimSpace(data.HomeAutoSession.ReconciliationState)
	out.HomeAutoSession.PendingAction = strings.TrimSpace(data.HomeAutoSession.PendingAction)
	out.HomeAutoSession.ActiveSessionID = strings.TrimSpace(data.HomeAutoSession.ActiveSessionID)
	out.HomeAutoSession.ActiveTriggerNode = strings.TrimSpace(data.HomeAutoSession.ActiveTriggerNode)
	out.HomeAutoSession.LastDecisionReason = strings.TrimSpace(data.HomeAutoSession.LastDecisionReason)
	out.HomeAutoSession.LastError = strings.TrimSpace(data.HomeAutoSession.LastError)
	out.HomeAutoSession.LastAction = strings.TrimSpace(data.HomeAutoSession.LastAction)
	out.HomeAutoSession.LastActionResult = strings.TrimSpace(data.HomeAutoSession.LastActionResult)
	out.HomeAutoSession.LastActionAt = cloneTimePtr(data.HomeAutoSession.LastActionAt)
	out.HomeAutoSession.LastSuccessfulAction = strings.TrimSpace(data.HomeAutoSession.LastSuccessfulAction)
	out.HomeAutoSession.LastSuccessfulAt = cloneTimePtr(data.HomeAutoSession.LastSuccessfulActionAt)
	out.HomeAutoSession.BlockedReason = strings.TrimSpace(data.HomeAutoSession.BlockedReason)
	out.HomeAutoSession.ConsecutiveFailures = data.HomeAutoSession.ConsecutiveFailures
	out.HomeAutoSession.CooldownUntil = cloneTimePtr(data.HomeAutoSession.CooldownUntil)
	out.HomeAutoSession.GPSStatus = strings.TrimSpace(data.HomeAutoSession.GPSStatus)
	out.HomeAutoSession.GPSReason = strings.TrimSpace(data.HomeAutoSession.GPSReason)
	out.HomeAutoSession.GPSNodeID = strings.TrimSpace(data.HomeAutoSession.GPSNodeID)
	out.HomeAutoSession.GPSUpdatedAt = cloneTimePtr(data.HomeAutoSession.GPSUpdatedAt)
	out.HomeAutoSession.LastDecisionAt = cloneTimePtr(data.HomeAutoSession.LastDecisionAt)

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
		if value := strings.TrimSpace(localProbe.Snapshot.InstallationID); value != "" {
			out.Identity.InstallationID = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.LocalName); value != "" {
			out.Identity.LocalName = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.Hostname); value != "" {
			out.Identity.Hostname = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.CloudReceiverID); value != "" {
			out.Identity.CloudReceiverID = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.CloudReceiverLabel); value != "" {
			out.Identity.CloudReceiverName = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.CloudSiteLabel); value != "" {
			out.Identity.CloudSiteName = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.CloudGroupLabel); value != "" {
			out.Identity.CloudGroupName = value
		}
		out.HomeAutoSession.Enabled = localProbe.Snapshot.HomeAutoSession.Enabled
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.Mode); value != "" {
			out.HomeAutoSession.Mode = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.State); value != "" {
			out.HomeAutoSession.State = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.ControlState); value != "" {
			out.HomeAutoSession.ControlState = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.ActiveStateSource); value != "" {
			out.HomeAutoSession.ActiveStateSource = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.ReconciliationState); value != "" {
			out.HomeAutoSession.ReconciliationState = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.PendingAction); value != "" {
			out.HomeAutoSession.PendingAction = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.Summary); value != "" {
			out.HomeAutoSession.Summary = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.HomeSummary); value != "" {
			out.HomeAutoSession.HomeSummary = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.TrackedNodeState); value != "" {
			out.HomeAutoSession.TrackedNodeState = value
		}
		if len(localProbe.Snapshot.HomeAutoSession.TrackedNodeIDs) > 0 {
			out.HomeAutoSession.TrackedNodeIDs = append([]string(nil), localProbe.Snapshot.HomeAutoSession.TrackedNodeIDs...)
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.ActiveSessionID); value != "" {
			out.HomeAutoSession.ActiveSessionID = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.ActiveTriggerNode); value != "" {
			out.HomeAutoSession.ActiveTriggerNode = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.LastDecisionReason); value != "" {
			out.HomeAutoSession.LastDecisionReason = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.LastError); value != "" {
			out.HomeAutoSession.LastError = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.LastAction); value != "" {
			out.HomeAutoSession.LastAction = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.LastActionResult); value != "" {
			out.HomeAutoSession.LastActionResult = value
		}
		if localProbe.Snapshot.HomeAutoSession.LastActionAt != nil {
			out.HomeAutoSession.LastActionAt = cloneTimePtr(localProbe.Snapshot.HomeAutoSession.LastActionAt)
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.LastSuccessfulAction); value != "" {
			out.HomeAutoSession.LastSuccessfulAction = value
		}
		if localProbe.Snapshot.HomeAutoSession.LastSuccessfulAt != nil {
			out.HomeAutoSession.LastSuccessfulAt = cloneTimePtr(localProbe.Snapshot.HomeAutoSession.LastSuccessfulAt)
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.BlockedReason); value != "" {
			out.HomeAutoSession.BlockedReason = value
		}
		if localProbe.Snapshot.HomeAutoSession.ConsecutiveFailures > 0 {
			out.HomeAutoSession.ConsecutiveFailures = localProbe.Snapshot.HomeAutoSession.ConsecutiveFailures
		}
		if localProbe.Snapshot.HomeAutoSession.CooldownUntil != nil {
			out.HomeAutoSession.CooldownUntil = cloneTimePtr(localProbe.Snapshot.HomeAutoSession.CooldownUntil)
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.GPSStatus); value != "" {
			out.HomeAutoSession.GPSStatus = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.GPSReason); value != "" {
			out.HomeAutoSession.GPSReason = value
		}
		if value := strings.TrimSpace(localProbe.Snapshot.HomeAutoSession.GPSNodeID); value != "" {
			out.HomeAutoSession.GPSNodeID = value
		}
		if localProbe.Snapshot.HomeAutoSession.GPSUpdatedAt != nil {
			out.HomeAutoSession.GPSUpdatedAt = cloneTimePtr(localProbe.Snapshot.HomeAutoSession.GPSUpdatedAt)
		}
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
		"meshtastic_config.share_url",
		"meshtastic_config.share_qr_text",
	}

	return out
}

func formatHomeSummary(home config.HomeGeofenceConfig) string {
	if home.RadiusM <= 0 {
		return "home geofence not configured"
	}
	return strings.TrimSpace(strings.Join([]string{
		trimFloat(home.Lat),
		trimFloat(home.Lon),
		"radius_m=" + trimFloat(home.RadiusM),
	}, " "))
}

func trimFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.5f", value), "0"), ".")
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
	copySnap.MeshtasticConfig.ShareURL = ""
	copySnap.MeshtasticConfig.ShareQRText = ""
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
