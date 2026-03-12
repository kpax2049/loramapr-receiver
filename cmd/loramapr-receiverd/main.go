package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/buildinfo"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/diagnostics"
	"github.com/loramapr/loramapr-receiver/internal/install"
	"github.com/loramapr/loramapr-receiver/internal/logging"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/pairing"
	"github.com/loramapr/loramapr-receiver/internal/runtime"
	"github.com/loramapr/loramapr-receiver/internal/state"
	"github.com/loramapr/loramapr-receiver/internal/status"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		runCommand(nil)
		return
	}

	cmd := args[0]
	switch cmd {
	case "run":
		runCommand(args[1:])
	case "install":
		installCommand(args[1:])
	case "uninstall":
		uninstallCommand(args[1:])
	case "doctor":
		doctorCommand(args[1:])
	case "status":
		statusCommand(args[1:])
	case "support-snapshot":
		supportSnapshotCommand(args[1:])
	case "reset-pairing":
		resetPairingCommand(args[1:])
	default:
		if strings.HasPrefix(cmd, "-") {
			runCommand(args)
			return
		}
		printUsage()
		os.Exit(2)
	}
}

func runCommand(args []string) {
	flags := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := flags.String("config", config.DefaultPath, "path to receiver config file")
	modeOverride := flags.String("mode", "", "runtime mode override: auto|setup|service")
	_ = flags.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("load config failed", "err", err, "config", *configPath)
		os.Exit(1)
	}

	if *modeOverride != "" {
		cfg.Service.Mode = config.RunMode(*modeOverride)
		if err := cfg.Validate(); err != nil {
			slog.Error("invalid mode override", "err", err, "mode", *modeOverride)
			os.Exit(1)
		}
	}

	logger, err := logging.New(cfg.Logging)
	if err != nil {
		slog.Error("initialize logger failed", "err", err)
		os.Exit(1)
	}

	svc, err := runtime.New(cfg, logger)
	if err != nil {
		logger.Error("create runtime failed", "err", err)
		if hint := upgradeCompatibilityHint(err); hint != "" {
			logger.Error("runtime compatibility hint", "hint", hint)
		}
		os.Exit(1)
	}

	ctx, cancel := signalNotifyContext()
	defer cancel()

	if err := svc.Run(ctx); err != nil {
		logger.Error("runtime failed", "err", err)
		os.Exit(1)
	}

	logger.Info("runtime stopped")
}

func installCommand(args []string) {
	flags := flag.NewFlagSet("install", flag.ExitOnError)
	targetRoot := flags.String("target-root", "/", "installation root prefix (default /)")
	dryRun := flags.Bool("dry-run", false, "print planned install operations without writing files")
	force := flags.Bool("force", false, "overwrite existing config file")
	serviceUser := flags.String("service-user", "loramapr", "systemd service user")
	serviceGroup := flags.String("service-group", "loramapr", "systemd service group")
	_ = flags.Parse(args)

	result, err := install.InstallLinuxSystemd(install.LinuxInstallOptions{
		TargetRoot:   *targetRoot,
		ServiceUser:  *serviceUser,
		ServiceGroup: *serviceGroup,
		DryRun:       *dryRun,
		Force:        *force,
	})
	if err != nil {
		slog.Error("install failed", "err", err)
		os.Exit(1)
	}

	fmt.Printf("Install layout root: %s\n", result.Layout.Root)
	for _, op := range result.Operations {
		fmt.Printf("- %s %s\n", op.Action, op.Path)
	}
	if *dryRun {
		fmt.Println("Dry run complete; no files were modified.")
		return
	}
	fmt.Println("Install complete.")
	fmt.Println("Next steps:")
	fmt.Println("1. sudo systemctl daemon-reload")
	fmt.Println("2. sudo systemctl enable --now loramapr-receiverd")
}

func uninstallCommand(args []string) {
	flags := flag.NewFlagSet("uninstall", flag.ExitOnError)
	targetRoot := flags.String("target-root", "/", "installation root prefix")
	dryRun := flags.Bool("dry-run", false, "print planned uninstall operations without deleting files")
	purgeState := flags.Bool("purge-state", false, "remove persisted state file and state directory")
	_ = flags.Parse(args)

	result, err := install.UninstallLinuxSystemd(install.LinuxUninstallOptions{
		TargetRoot: *targetRoot,
		DryRun:     *dryRun,
		PurgeState: *purgeState,
	})
	if err != nil {
		slog.Error("uninstall failed", "err", err)
		os.Exit(1)
	}

	fmt.Printf("Uninstall layout root: %s\n", result.Layout.Root)
	for _, op := range result.Operations {
		fmt.Printf("- %s %s\n", op.Action, op.Path)
	}
	if *dryRun {
		fmt.Println("Dry run complete; no files were removed.")
		return
	}
	fmt.Println("Uninstall complete.")
}

func doctorCommand(args []string) {
	flags := flag.NewFlagSet("doctor", flag.ExitOnError)
	configPath := flags.String("config", config.DefaultPath, "path to receiver config file")
	jsonOutput := flags.Bool("json", false, "print diagnostics report as json")
	_ = flags.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		printDoctorLoadFailure(*jsonOutput, *configPath, "", err)
		os.Exit(1)
	}

	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		printDoctorLoadFailure(*jsonOutput, *configPath, cfg.Paths.StateFile, err)
		os.Exit(1)
	}
	snapshot := store.Snapshot()

	now := time.Now().UTC()
	deviceProbe, meshState := detectMeshtastic(cfg.Meshtastic)
	cloudProbe := diagnostics.ProbeCloudReachability(cfg.Cloud.BaseURL, 3*time.Second)
	networkProbe := diagnostics.ProbeLocalNetwork()
	localProbe := diagnostics.ProbeLocalRuntimeStatus(cfg.Portal.BindAddress, 2*time.Second)
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        cfg.Runtime.Profile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		RuntimeLastError:      localProbeRuntimeLastError(localProbe),
		PortalState:           localProbeComponentState(localProbe, "portal"),
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        localProbeCloudReachable(localProbe, cloudProbe),
		MeshtasticState:       localProbeMeshtasticState(localProbe, meshState),
		UpdateStatus:          localProbeUpdateStatus(localProbe, snapshot.Update.Status),
		IngestQueueDepth:      localProbeIngestQueueDepth(localProbe),
		LastPacketQueued:      localProbeLastPacketQueued(localProbe),
		LastPacketAck:         localProbeLastPacketAck(localProbe),
		Now:                   now,
	})

	ops := diagnostics.EvaluateOperational(diagnostics.OperationalInput{
		Now:                 now,
		Lifecycle:           localProbeLifecycle(localProbe, snapshot.Runtime.Mode),
		Ready:               localProbeReady(localProbe),
		ReadyReason:         localProbeReadyReason(localProbe),
		PairingPhase:        string(snapshot.Pairing.Phase),
		HasIngestCredential: pairingAuthorized(snapshot),
		CloudReachable:      localProbeCloudReachable(localProbe, cloudProbe),
		CloudProbeStatus:    cloudProbe.Status,
		MeshtasticState:     localProbeMeshtasticState(localProbe, meshState),
		IngestQueueDepth:    localProbeIngestQueueDepth(localProbe),
		LastPacketQueued:    localProbeLastPacketQueued(localProbe),
		LastPacketAck:       localProbeLastPacketAck(localProbe),
		UpdateStatus:        localProbeUpdateStatus(localProbe, snapshot.Update.Status),
	})
	attention := diagnostics.DeriveAttention(finding, ops)

	build := buildinfo.Current()
	report := map[string]any{
		"receiver_version":      build.Version,
		"release_channel":       build.Channel,
		"build_commit":          build.Commit,
		"build_date":            build.BuildDate,
		"build_id":              build.BuildID,
		"platform":              goruntime.GOOS,
		"arch":                  goruntime.GOARCH,
		"install_type":          installType(snapshot.Runtime.InstallType, cfg.Runtime.Profile),
		"config_path":           *configPath,
		"state_path":            cfg.Paths.StateFile,
		"config_schema_version": cfg.SchemaVersion,
		"state_schema_version":  snapshot.SchemaVersion,
		"installation_id":       snapshot.Installation.ID,
		"local_name":            snapshot.Installation.LocalName,
		"hostname":              snapshot.Installation.Hostname,
		"cloud_receiver_id":     snapshot.Cloud.ReceiverID,
		"cloud_receiver_label":  snapshot.Cloud.ReceiverLabel,
		"cloud_site_label":      snapshot.Cloud.SiteLabel,
		"cloud_group_label":     snapshot.Cloud.GroupLabel,
		"pairing_phase":         snapshot.Pairing.Phase,
		"pairing_last_change":   snapshot.Pairing.LastChange,
		"pairing_authorized":    pairingAuthorized(snapshot),
		"cloud_base_url":        cfg.Cloud.BaseURL,
		"cloud_probe":           cloudProbe,
		"network_probe":         networkProbe,
		"local_runtime_probe":   summarizeLocalProbeForOutput(localProbe),
		"cloud_config_version":  snapshot.Cloud.ConfigVersion,
		"update_status":         snapshot.Update.Status,
		"update_summary":        snapshot.Update.Summary,
		"update_hint":           snapshot.Update.Hint,
		"update_checked_at":     snapshot.Update.LastCheckedAt,
		"update_manifest": map[string]any{
			"version":     snapshot.Update.ManifestVersion,
			"channel":     snapshot.Update.ManifestChannel,
			"recommended": snapshot.Update.RecommendedVersion,
		},
		"home_auto_session": map[string]any{
			"enabled":                     cfg.HomeAutoSession.Enabled,
			"mode":                        cfg.HomeAutoSession.Mode,
			"effective_config_source":     snapshot.HomeAutoSession.EffectiveConfigSource,
			"effective_config_version":    snapshot.HomeAutoSession.EffectiveConfigVersion,
			"cloud_config_present":        snapshot.HomeAutoSession.CloudConfigPresent,
			"last_fetched_config_version": snapshot.HomeAutoSession.LastFetchedConfigVer,
			"last_applied_config_version": snapshot.HomeAutoSession.LastAppliedConfigVer,
			"last_config_apply_result":    snapshot.HomeAutoSession.LastConfigApplyResult,
			"last_config_apply_error":     snapshot.HomeAutoSession.LastConfigApplyError,
			"desired_config_enabled":      snapshot.HomeAutoSession.DesiredConfigEnabled,
			"desired_config_mode":         snapshot.HomeAutoSession.DesiredConfigMode,
			"state":                       snapshot.HomeAutoSession.ModuleState,
			"control_state":               snapshot.HomeAutoSession.ControlState,
			"active_state_source":         snapshot.HomeAutoSession.ActiveStateSource,
			"reconciliation_state":        snapshot.HomeAutoSession.ReconciliationState,
			"pending_action":              snapshot.HomeAutoSession.PendingAction,
			"active_session_id":           snapshot.HomeAutoSession.ActiveSessionID,
			"active_trigger_node":         snapshot.HomeAutoSession.ActiveTriggerNode,
			"last_decision":               snapshot.HomeAutoSession.LastDecisionReason,
			"last_action":                 snapshot.HomeAutoSession.LastAction,
			"last_action_result":          snapshot.HomeAutoSession.LastActionResult,
			"last_action_at":              snapshot.HomeAutoSession.LastActionAt,
			"last_error":                  snapshot.HomeAutoSession.LastError,
			"last_successful_action":      snapshot.HomeAutoSession.LastSuccessfulAction,
			"blocked_reason":              snapshot.HomeAutoSession.BlockedReason,
			"consecutive_failures":        snapshot.HomeAutoSession.ConsecutiveFailures,
			"gps_status":                  snapshot.HomeAutoSession.GPSStatus,
			"gps_reason":                  snapshot.HomeAutoSession.GPSReason,
		},
		"meshtastic_transport": cfg.Meshtastic.Transport,
		"meshtastic_probe":     deviceProbe,
		"failure_code":         finding.Code,
		"failure_summary":      finding.Summary,
		"failure_hint":         finding.Hint,
		"attention_state":      attention.State,
		"attention_category":   attention.Category,
		"attention_code":       attention.Code,
		"attention_summary":    attention.Summary,
		"attention_hint":       attention.Hint,
		"attention_required":   attention.ActionRequired,
		"operational_status":   ops.Overall,
		"operational_summary":  ops.Summary,
		"operational_checks":   ops.Checks,
	}

	if *jsonOutput {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fmt.Printf("[FAIL] doctor json encode: %v\n", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("[OK] config load: %s\n", *configPath)
	fmt.Printf("[OK] state open: %s\n", cfg.Paths.StateFile)
	fmt.Printf("[INFO] pairing phase: %s (%s)\n", snapshot.Pairing.Phase, snapshot.Pairing.LastChange)
	fmt.Printf("[INFO] build: version=%s channel=%s build_id=%s platform=%s/%s install_type=%s\n",
		build.Version,
		build.Channel,
		build.BuildID,
		goruntime.GOOS,
		goruntime.GOARCH,
		installType(snapshot.Runtime.InstallType, cfg.Runtime.Profile),
	)
	fmt.Printf("[INFO] config/state schema: config=%d state=%d\n", cfg.SchemaVersion, snapshot.SchemaVersion)
	fmt.Printf(
		"[INFO] identity: installation_id=%s local_name=%s hostname=%s cloud_receiver_id=%s cloud_receiver_label=%s\n",
		emptyFallback(snapshot.Installation.ID, "unknown"),
		emptyFallback(snapshot.Installation.LocalName, "unknown"),
		emptyFallback(snapshot.Installation.Hostname, "unknown"),
		emptyFallback(snapshot.Cloud.ReceiverID, "unknown"),
		emptyFallback(snapshot.Cloud.ReceiverLabel, "unknown"),
	)
	if strings.TrimSpace(snapshot.Cloud.SiteLabel) != "" || strings.TrimSpace(snapshot.Cloud.GroupLabel) != "" {
		fmt.Printf(
			"[INFO] grouping hints: site=%s group=%s\n",
			emptyFallback(snapshot.Cloud.SiteLabel, "n/a"),
			emptyFallback(snapshot.Cloud.GroupLabel, "n/a"),
		)
	}
	fmt.Printf(
		"[INFO] home auto session: enabled=%t mode=%s config_source=%s config_version=%s state=%s control=%s source=%s reconcile=%s pending=%s active_session=%s\n",
		cfg.HomeAutoSession.Enabled,
		cfg.HomeAutoSession.Mode,
		emptyFallback(snapshot.HomeAutoSession.EffectiveConfigSource, "local_fallback"),
		emptyFallback(snapshot.HomeAutoSession.EffectiveConfigVersion, "local-default"),
		emptyFallback(snapshot.HomeAutoSession.ModuleState, "unknown"),
		emptyFallback(snapshot.HomeAutoSession.ControlState, "unknown"),
		emptyFallback(snapshot.HomeAutoSession.ActiveStateSource, "none"),
		emptyFallback(snapshot.HomeAutoSession.ReconciliationState, "n/a"),
		emptyFallback(snapshot.HomeAutoSession.PendingAction, "none"),
		emptyFallback(snapshot.HomeAutoSession.ActiveSessionID, "none"),
	)
	fmt.Printf(
		"[INFO] home auto config apply: cloud_present=%t last_fetched=%s last_applied=%s result=%s\n",
		snapshot.HomeAutoSession.CloudConfigPresent,
		emptyFallback(snapshot.HomeAutoSession.LastFetchedConfigVer, "none"),
		emptyFallback(snapshot.HomeAutoSession.LastAppliedConfigVer, "none"),
		emptyFallback(snapshot.HomeAutoSession.LastConfigApplyResult, "none"),
	)
	if strings.TrimSpace(snapshot.HomeAutoSession.LastConfigApplyError) != "" {
		fmt.Printf("[INFO] home auto config apply error: %s\n", snapshot.HomeAutoSession.LastConfigApplyError)
	}
	if strings.TrimSpace(snapshot.HomeAutoSession.LastDecisionReason) != "" {
		fmt.Printf("[INFO] home auto decision: %s\n", snapshot.HomeAutoSession.LastDecisionReason)
	}
	if strings.TrimSpace(snapshot.HomeAutoSession.LastAction) != "" || strings.TrimSpace(snapshot.HomeAutoSession.LastActionResult) != "" {
		fmt.Printf(
			"[INFO] home auto last action: action=%s result=%s\n",
			emptyFallback(snapshot.HomeAutoSession.LastAction, "none"),
			emptyFallback(snapshot.HomeAutoSession.LastActionResult, "none"),
		)
	}
	if strings.TrimSpace(snapshot.HomeAutoSession.GPSStatus) != "" {
		fmt.Printf("[INFO] home auto gps: %s", snapshot.HomeAutoSession.GPSStatus)
		if reason := strings.TrimSpace(snapshot.HomeAutoSession.GPSReason); reason != "" {
			fmt.Printf(" (%s)", reason)
		}
		fmt.Println()
	}
	if strings.TrimSpace(snapshot.HomeAutoSession.BlockedReason) != "" {
		fmt.Printf("[WARN] home auto blocked: %s\n", snapshot.HomeAutoSession.BlockedReason)
	}
	if strings.TrimSpace(snapshot.HomeAutoSession.LastError) != "" {
		fmt.Printf("[WARN] home auto last error: %s\n", snapshot.HomeAutoSession.LastError)
	}
	fmt.Printf("[INFO] cloud probe: %s", cloudProbe.Status)
	if cloudProbe.Detail != "" {
		fmt.Printf(" (%s)", cloudProbe.Detail)
	}
	fmt.Println()
	fmt.Printf("[INFO] local runtime probe: %s", localProbe.Status)
	if localProbe.Detail != "" {
		fmt.Printf(" (%s)", localProbe.Detail)
	}
	fmt.Println()
	fmt.Printf("[INFO] meshtastic probe: %s", meshState)
	if detected := deviceProbe["detected_device"]; detected != "" {
		fmt.Printf(" (%s)", detected)
	}
	fmt.Println()
	if finding.Code != diagnostics.FailureNone {
		fmt.Printf("[WARN] failure state: %s - %s\n", finding.Code, finding.Summary)
		if finding.Hint != "" {
			fmt.Printf("[HINT] %s\n", finding.Hint)
		}
	} else {
		fmt.Println("[OK] no active failure states detected")
	}
	if strings.TrimSpace(snapshot.Update.Status) != "" {
		fmt.Printf("[INFO] update status: %s", snapshot.Update.Status)
		if summary := strings.TrimSpace(snapshot.Update.Summary); summary != "" {
			fmt.Printf(" (%s)", summary)
		}
		fmt.Println()
	}
	fmt.Printf("[INFO] operational status: %s (%s)\n", ops.Overall, ops.Summary)
	if attention.State != diagnostics.AttentionNone {
		fmt.Printf("[ATTN] %s [%s] - %s\n", attention.State, attention.Code, attention.Summary)
		if attention.Hint != "" {
			fmt.Printf("[HINT] %s\n", attention.Hint)
		}
	}
	for _, check := range ops.Checks {
		fmt.Printf("[CHECK] %s: %s - %s\n", check.Level, check.ID, check.Summary)
		if check.Hint != "" {
			fmt.Printf("[HINT] %s\n", check.Hint)
		}
	}
	fmt.Println("Doctor checks completed.")
}

func statusCommand(args []string) {
	flags := flag.NewFlagSet("status", flag.ExitOnError)
	configPath := flags.String("config", config.DefaultPath, "path to receiver config file")
	_ = flags.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("status failed: load config", "err", err)
		os.Exit(1)
	}
	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		slog.Error("status failed: open state", "err", err)
		if hint := upgradeCompatibilityHint(err); hint != "" {
			slog.Error("status compatibility hint", "hint", hint)
		}
		os.Exit(1)
	}
	snapshot := store.Snapshot()
	now := time.Now().UTC()
	cloudProbe := diagnostics.ProbeCloudReachability(cfg.Cloud.BaseURL, 3*time.Second)
	networkProbe := diagnostics.ProbeLocalNetwork()
	localProbe := diagnostics.ProbeLocalRuntimeStatus(cfg.Portal.BindAddress, 2*time.Second)
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	_, meshState := detectMeshtastic(cfg.Meshtastic)
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        cfg.Runtime.Profile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		RuntimeLastError:      localProbeRuntimeLastError(localProbe),
		PortalState:           localProbeComponentState(localProbe, "portal"),
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        localProbeCloudReachable(localProbe, cloudProbe),
		MeshtasticState:       localProbeMeshtasticState(localProbe, meshState),
		UpdateStatus:          localProbeUpdateStatus(localProbe, snapshot.Update.Status),
		IngestQueueDepth:      localProbeIngestQueueDepth(localProbe),
		LastPacketQueued:      localProbeLastPacketQueued(localProbe),
		LastPacketAck:         localProbeLastPacketAck(localProbe),
		Now:                   now,
	})
	ops := diagnostics.EvaluateOperational(diagnostics.OperationalInput{
		Now:                 now,
		Lifecycle:           localProbeLifecycle(localProbe, snapshot.Runtime.Mode),
		Ready:               localProbeReady(localProbe),
		ReadyReason:         localProbeReadyReason(localProbe),
		PairingPhase:        string(snapshot.Pairing.Phase),
		HasIngestCredential: pairingAuthorized(snapshot),
		CloudReachable:      localProbeCloudReachable(localProbe, cloudProbe),
		CloudProbeStatus:    cloudProbe.Status,
		MeshtasticState:     localProbeMeshtasticState(localProbe, meshState),
		IngestQueueDepth:    localProbeIngestQueueDepth(localProbe),
		LastPacketQueued:    localProbeLastPacketQueued(localProbe),
		LastPacketAck:       localProbeLastPacketAck(localProbe),
		UpdateStatus:        localProbeUpdateStatus(localProbe, snapshot.Update.Status),
	})
	attention := diagnostics.DeriveAttention(finding, ops)

	build := buildinfo.Current()
	output := map[string]any{
		"receiver_version":      build.Version,
		"release_channel":       build.Channel,
		"build_commit":          build.Commit,
		"build_date":            build.BuildDate,
		"build_id":              build.BuildID,
		"platform":              goruntime.GOOS,
		"arch":                  goruntime.GOARCH,
		"config_path":           *configPath,
		"state_path":            cfg.Paths.StateFile,
		"config_schema_version": cfg.SchemaVersion,
		"state_schema_version":  snapshot.SchemaVersion,
		"installation_id":       snapshot.Installation.ID,
		"local_name":            snapshot.Installation.LocalName,
		"hostname":              snapshot.Installation.Hostname,
		"cloud_receiver_id":     snapshot.Cloud.ReceiverID,
		"cloud_receiver_label":  snapshot.Cloud.ReceiverLabel,
		"cloud_site_label":      snapshot.Cloud.SiteLabel,
		"cloud_group_label":     snapshot.Cloud.GroupLabel,
		"pairing_phase":         snapshot.Pairing.Phase,
		"pairing_authorized":    pairingAuthorized(snapshot),
		"cloud_endpoint":        cfg.Cloud.BaseURL,
		"runtime_mode":          snapshot.Runtime.Mode,
		"runtime_profile":       snapshot.Runtime.Profile,
		"install_type":          installType(snapshot.Runtime.InstallType, cfg.Runtime.Profile),
		"meshtastic_mode":       cfg.Meshtastic.Transport,
		"meshtastic_state":      localProbeMeshtasticState(localProbe, meshState),
		"local_runtime_probe":   summarizeLocalProbeForOutput(localProbe),
		"cloud_config_version":  snapshot.Cloud.ConfigVersion,
		"update_status":         snapshot.Update.Status,
		"update_summary":        snapshot.Update.Summary,
		"update_hint":           snapshot.Update.Hint,
		"update_checked_at":     snapshot.Update.LastCheckedAt,
		"update_manifest": map[string]any{
			"version":     snapshot.Update.ManifestVersion,
			"channel":     snapshot.Update.ManifestChannel,
			"recommended": snapshot.Update.RecommendedVersion,
		},
		"home_auto_session": map[string]any{
			"enabled":                cfg.HomeAutoSession.Enabled,
			"mode":                   cfg.HomeAutoSession.Mode,
			"state":                  snapshot.HomeAutoSession.ModuleState,
			"control_state":          snapshot.HomeAutoSession.ControlState,
			"active_state_source":    snapshot.HomeAutoSession.ActiveStateSource,
			"reconciliation_state":   snapshot.HomeAutoSession.ReconciliationState,
			"pending_action":         snapshot.HomeAutoSession.PendingAction,
			"active_session_id":      snapshot.HomeAutoSession.ActiveSessionID,
			"active_trigger_node":    snapshot.HomeAutoSession.ActiveTriggerNode,
			"last_decision":          snapshot.HomeAutoSession.LastDecisionReason,
			"last_action":            snapshot.HomeAutoSession.LastAction,
			"last_action_result":     snapshot.HomeAutoSession.LastActionResult,
			"last_action_at":         snapshot.HomeAutoSession.LastActionAt,
			"last_error":             snapshot.HomeAutoSession.LastError,
			"last_successful_action": snapshot.HomeAutoSession.LastSuccessfulAction,
			"blocked_reason":         snapshot.HomeAutoSession.BlockedReason,
			"consecutive_failures":   snapshot.HomeAutoSession.ConsecutiveFailures,
			"gps_status":             snapshot.HomeAutoSession.GPSStatus,
			"gps_reason":             snapshot.HomeAutoSession.GPSReason,
		},
		"last_pairing_err":    snapshot.Pairing.LastError,
		"failure_code":        finding.Code,
		"failure_summary":     finding.Summary,
		"failure_hint":        finding.Hint,
		"attention_state":     attention.State,
		"attention_category":  attention.Category,
		"attention_code":      attention.Code,
		"attention_summary":   attention.Summary,
		"attention_hint":      attention.Hint,
		"attention_required":  attention.ActionRequired,
		"cloud_probe":         cloudProbe.Status,
		"network_probe":       networkProbe,
		"operational_status":  ops.Overall,
		"operational_summary": ops.Summary,
		"operational_checks":  ops.Checks,
		"updated_at":          snapshot.Metadata.UpdatedAt,
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		slog.Error("status failed: encode", "err", err)
		os.Exit(1)
	}
}

func supportSnapshotCommand(args []string) {
	flags := flag.NewFlagSet("support-snapshot", flag.ExitOnError)
	configPath := flags.String("config", config.DefaultPath, "path to receiver config file")
	outputPath := flags.String("out", "", "optional output file path (defaults to stdout)")
	_ = flags.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		configSchema, statePath := bestEffortConfigMarkers(*configPath)
		stateSchema := bestEffortStateSchema(statePath)
		snapshot := buildCompatibilitySupportSnapshot(*configPath, statePath, configSchema, stateSchema, err)
		if writeErr := writeSupportSnapshot(snapshot, *outputPath); writeErr != nil {
			slog.Error("support snapshot failed: write compatibility snapshot", "err", writeErr)
			os.Exit(1)
		}
		os.Exit(1)
	}
	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		configSchema, statePath := bestEffortConfigMarkers(*configPath)
		stateSchema := bestEffortStateSchema(cfg.Paths.StateFile)
		if configSchema == 0 {
			configSchema = cfg.SchemaVersion
		}
		if statePath == "" {
			statePath = cfg.Paths.StateFile
		}
		snapshot := buildCompatibilitySupportSnapshot(*configPath, statePath, configSchema, stateSchema, err)
		if writeErr := writeSupportSnapshot(snapshot, *outputPath); writeErr != nil {
			slog.Error("support snapshot failed: write compatibility snapshot", "err", writeErr)
			os.Exit(1)
		}
		os.Exit(1)
	}
	snapshot := store.Snapshot()
	deviceProbe, meshState := detectMeshtastic(cfg.Meshtastic)
	cloudProbe := diagnostics.ProbeCloudReachability(cfg.Cloud.BaseURL, 3*time.Second)
	networkProbe := diagnostics.ProbeLocalNetwork()
	localProbe := diagnostics.ProbeLocalRuntimeStatus(cfg.Portal.BindAddress, 2*time.Second)
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        cfg.Runtime.Profile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		RuntimeLastError:      localProbeRuntimeLastError(localProbe),
		PortalState:           localProbeComponentState(localProbe, "portal"),
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        localProbeCloudReachable(localProbe, cloudProbe),
		MeshtasticState:       localProbeMeshtasticState(localProbe, meshState),
		UpdateStatus:          localProbeUpdateStatus(localProbe, snapshot.Update.Status),
		IngestQueueDepth:      localProbeIngestQueueDepth(localProbe),
		LastPacketQueued:      localProbeLastPacketQueued(localProbe),
		LastPacketAck:         localProbeLastPacketAck(localProbe),
		Now:                   time.Now().UTC(),
	})
	report := diagnostics.CollectSupportSnapshot(cfg, snapshot, finding, diagnostics.CollectOptions{
		Now: func() time.Time { return time.Now().UTC() },
		ProbeCloud: func(_ string, _ time.Duration) diagnostics.CloudProbe {
			return cloudProbe
		},
		ProbeNetwork: func() diagnostics.NetworkProbe {
			return networkProbe
		},
		ProbeLocal: func(_ string, _ time.Duration) diagnostics.LocalStatusProbe {
			return localProbe
		},
		DetectDevice: func(_ config.MeshtasticConfig) (meshtastic.DetectionResult, error) {
			probe := meshtastic.DetectionResult{
				Candidates: asStringSlice(deviceProbe["candidates"]),
				Device:     strings.TrimSpace(deviceProbe["detected_device"]),
			}
			if deviceProbe["state"] == "error" {
				return probe, errors.New(deviceProbe["detail"])
			}
			return probe, nil
		},
		ConfigPath: *configPath,
		StatePath:  cfg.Paths.StateFile,
	})

	if err := writeSupportSnapshot(report, *outputPath); err != nil {
		slog.Error("support snapshot failed", "err", err, "path", *outputPath)
		os.Exit(1)
	}
}

func resetPairingCommand(args []string) {
	flags := flag.NewFlagSet("reset-pairing", flag.ExitOnError)
	configPath := flags.String("config", config.DefaultPath, "path to receiver config file")
	deauthorize := flags.Bool("deauthorize", true, "clear durable receiver credentials and require full re-pair")
	_ = flags.Parse(args)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("reset pairing failed: load config", "err", err)
		os.Exit(1)
	}
	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		slog.Error("reset pairing failed: open state", "err", err)
		os.Exit(1)
	}

	manager := pairing.NewManager(store, status.New(), nil, nil, pairing.ActivationIdentity{})
	if err := manager.ResetPairing(*deauthorize); err != nil {
		slog.Error("reset pairing failed", "err", err)
		os.Exit(1)
	}

	mode := "reset"
	if *deauthorize {
		mode = "deauthorized"
	}
	fmt.Printf("Pairing state reset complete (%s).\n", mode)
	fmt.Println("Next step: open local portal and submit a fresh pairing code.")
}

func signalNotifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func printUsage() {
	fmt.Println("LoRaMapr Receiver")
	fmt.Println("Usage:")
	fmt.Println("  loramapr-receiverd run [flags]               Start receiver service runtime")
	fmt.Println("  loramapr-receiverd install [flags]           Install Linux/systemd layout (advanced/manual)")
	fmt.Println("  loramapr-receiverd uninstall [flags]         Remove Linux/systemd layout (advanced/manual)")
	fmt.Println("  loramapr-receiverd doctor [flags]            Run local diagnostics checks")
	fmt.Println("  loramapr-receiverd status [flags]            Print structured local status")
	fmt.Println("  loramapr-receiverd support-snapshot [flags]  Export redacted support bundle")
	fmt.Println("  loramapr-receiverd reset-pairing [flags]     Clear local receiver credentials and pair again")
	fmt.Println("")
	fmt.Println("If no subcommand is provided, run mode is used.")
	fmt.Println("For install guides, see docs/raspberry-pi-appliance.md and docs/linux-pi-distribution.md.")
}

func detectMeshtastic(cfg config.MeshtasticConfig) (map[string]string, string) {
	result, err := meshtastic.DetectDevice(cfg)
	probe := map[string]string{
		"state": "not_present",
	}
	if err != nil {
		probe["state"] = "error"
		probe["detail"] = err.Error()
		return probe, "degraded"
	}
	if strings.TrimSpace(result.Device) != "" {
		probe["state"] = "detected"
		probe["detected_device"] = result.Device
		return probe, "detected"
	}
	if len(result.Candidates) > 0 {
		probe["candidates"] = strings.Join(result.Candidates, ",")
	}
	return probe, "not_present"
}

func asStringSlice(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		text := strings.TrimSpace(part)
		if text == "" {
			continue
		}
		out = append(out, text)
	}
	return out
}

func printDoctorLoadFailure(jsonOutput bool, configPath string, statePath string, err error) {
	if !jsonOutput {
		fmt.Printf("[FAIL] diagnostics initialization: %v\n", err)
		if hint := upgradeCompatibilityHint(err); hint != "" {
			fmt.Printf("[HINT] %s\n", hint)
		}
		return
	}

	configSchema, parsedStatePath := bestEffortConfigMarkers(configPath)
	if strings.TrimSpace(statePath) == "" {
		statePath = parsedStatePath
	}
	stateSchema := bestEffortStateSchema(statePath)
	report := map[string]any{
		"receiver_version":      buildinfo.Current().Version,
		"release_channel":       buildinfo.Current().Channel,
		"build_commit":          buildinfo.Current().Commit,
		"build_date":            buildinfo.Current().BuildDate,
		"build_id":              buildinfo.Current().BuildID,
		"config_path":           configPath,
		"state_path":            statePath,
		"config_schema_version": configSchema,
		"state_schema_version":  stateSchema,
		"local_name":            "",
		"hostname":              "",
		"cloud_receiver_id":     "",
		"cloud_receiver_label":  "",
		"cloud_site_label":      "",
		"cloud_group_label":     "",
		"home_auto_session": map[string]any{
			"enabled":                false,
			"mode":                   "off",
			"state":                  "disabled",
			"control_state":          "disabled",
			"active_state_source":    "none",
			"reconciliation_state":   "clean_idle",
			"pending_action":         "",
			"active_session_id":      "",
			"active_trigger_node":    "",
			"tracked_node_state":     "",
			"last_decision":          "",
			"last_action":            "",
			"last_action_result":     "",
			"last_action_at":         "",
			"last_error":             "",
			"last_successful_action": "",
			"blocked_reason":         "",
			"consecutive_failures":   0,
			"gps_status":             "",
			"gps_reason":             "",
		},
		"failure_code":       diagnostics.FailureLocalSchemaIncompat,
		"failure_summary":    "Local config or state schema is incompatible with this runtime",
		"failure_hint":       upgradeCompatibilityHint(err),
		"attention_state":    diagnostics.AttentionUrgent,
		"attention_category": diagnostics.AttentionCategoryCompatibility,
		"attention_code":     diagnostics.FailureLocalSchemaIncompat,
		"attention_summary":  "Local compatibility issue requires operator action",
		"attention_hint":     upgradeCompatibilityHint(err),
		"attention_required": true,
		"error":              strings.TrimSpace(err.Error()),
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(report)
}

func summarizeLocalProbeForOutput(probe diagnostics.LocalStatusProbe) map[string]any {
	out := map[string]any{
		"status": probe.Status,
		"detail": probe.Detail,
		"url":    probe.URL,
	}
	if probe.Snapshot == nil {
		return out
	}
	out["lifecycle"] = probe.Snapshot.Lifecycle
	out["ready"] = probe.Snapshot.Ready
	out["ready_reason"] = probe.Snapshot.ReadyReason
	out["pairing_phase"] = probe.Snapshot.PairingPhase
	out["cloud_reachable"] = probe.Snapshot.CloudReachable
	out["installation_id"] = probe.Snapshot.InstallationID
	out["local_name"] = probe.Snapshot.LocalName
	out["hostname"] = probe.Snapshot.Hostname
	out["cloud_receiver_id"] = probe.Snapshot.CloudReceiverID
	out["cloud_receiver_label"] = probe.Snapshot.CloudReceiverLabel
	out["cloud_site_label"] = probe.Snapshot.CloudSiteLabel
	out["cloud_group_label"] = probe.Snapshot.CloudGroupLabel
	out["update_status"] = probe.Snapshot.UpdateStatus
	out["home_auto_session"] = map[string]any{
		"enabled":                probe.Snapshot.HomeAutoSession.Enabled,
		"mode":                   probe.Snapshot.HomeAutoSession.Mode,
		"state":                  probe.Snapshot.HomeAutoSession.State,
		"control_state":          probe.Snapshot.HomeAutoSession.ControlState,
		"active_state_source":    probe.Snapshot.HomeAutoSession.ActiveStateSource,
		"reconciliation_state":   probe.Snapshot.HomeAutoSession.ReconciliationState,
		"pending_action":         probe.Snapshot.HomeAutoSession.PendingAction,
		"active_session_id":      probe.Snapshot.HomeAutoSession.ActiveSessionID,
		"active_trigger_node":    probe.Snapshot.HomeAutoSession.ActiveTriggerNode,
		"tracked_node_state":     probe.Snapshot.HomeAutoSession.TrackedNodeState,
		"last_decision":          probe.Snapshot.HomeAutoSession.LastDecisionReason,
		"last_action":            probe.Snapshot.HomeAutoSession.LastAction,
		"last_action_result":     probe.Snapshot.HomeAutoSession.LastActionResult,
		"last_action_at":         probe.Snapshot.HomeAutoSession.LastActionAt,
		"last_error":             probe.Snapshot.HomeAutoSession.LastError,
		"last_successful_action": probe.Snapshot.HomeAutoSession.LastSuccessfulAction,
		"blocked_reason":         probe.Snapshot.HomeAutoSession.BlockedReason,
		"consecutive_failures":   probe.Snapshot.HomeAutoSession.ConsecutiveFailures,
		"gps_status":             probe.Snapshot.HomeAutoSession.GPSStatus,
		"gps_reason":             probe.Snapshot.HomeAutoSession.GPSReason,
	}
	out["ingest_queue_depth"] = probe.Snapshot.IngestQueueDepth
	out["last_packet_ack"] = probe.Snapshot.LastPacketAck
	return out
}

func localProbeComponentState(probe diagnostics.LocalStatusProbe, name string) string {
	if probe.Snapshot == nil || probe.Snapshot.Components == nil {
		return "unknown"
	}
	component, ok := probe.Snapshot.Components[name]
	if !ok {
		return "unknown"
	}
	value := strings.TrimSpace(component.State)
	if value == "" {
		return "unknown"
	}
	return value
}

func localProbeMeshtasticState(probe diagnostics.LocalStatusProbe, fallback string) string {
	value := localProbeComponentState(probe, "meshtastic")
	if value == "unknown" {
		return strings.TrimSpace(fallback)
	}
	return value
}

func localProbeCloudReachable(probe diagnostics.LocalStatusProbe, cloudProbe diagnostics.CloudProbe) bool {
	if probe.Snapshot != nil {
		return probe.Snapshot.CloudReachable
	}
	return cloudProbe.Status == "reachable"
}

func localProbeRuntimeLastError(probe diagnostics.LocalStatusProbe) string {
	if probe.Snapshot == nil {
		return ""
	}
	return strings.TrimSpace(probe.Snapshot.LastError)
}

func localProbeUpdateStatus(probe diagnostics.LocalStatusProbe, fallback string) string {
	if probe.Snapshot == nil {
		return strings.TrimSpace(fallback)
	}
	value := strings.TrimSpace(probe.Snapshot.UpdateStatus)
	if value == "" {
		return strings.TrimSpace(fallback)
	}
	return value
}

func localProbeIngestQueueDepth(probe diagnostics.LocalStatusProbe) int {
	if probe.Snapshot == nil {
		return 0
	}
	return probe.Snapshot.IngestQueueDepth
}

func localProbeLastPacketQueued(probe diagnostics.LocalStatusProbe) *time.Time {
	if probe.Snapshot == nil {
		return nil
	}
	return probe.Snapshot.LastPacketQueued
}

func localProbeLastPacketAck(probe diagnostics.LocalStatusProbe) *time.Time {
	if probe.Snapshot == nil {
		return nil
	}
	return probe.Snapshot.LastPacketAck
}

func localProbeLifecycle(probe diagnostics.LocalStatusProbe, fallback string) string {
	if probe.Snapshot == nil {
		return strings.TrimSpace(fallback)
	}
	return strings.TrimSpace(string(probe.Snapshot.Lifecycle))
}

func localProbeReady(probe diagnostics.LocalStatusProbe) bool {
	if probe.Snapshot == nil {
		return false
	}
	return probe.Snapshot.Ready
}

func localProbeReadyReason(probe diagnostics.LocalStatusProbe) string {
	if probe.Snapshot == nil {
		return ""
	}
	return strings.TrimSpace(probe.Snapshot.ReadyReason)
}

func pairingAuthorized(snapshot state.Data) bool {
	return snapshot.Pairing.Phase == state.PairingSteadyState && strings.TrimSpace(snapshot.Cloud.IngestAPIKey) != ""
}

func writeSupportSnapshot(report diagnostics.SupportSnapshot, outputPath string) error {
	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')

	if strings.TrimSpace(outputPath) == "" {
		_, _ = os.Stdout.Write(payload)
		return nil
	}
	if err := os.WriteFile(outputPath, payload, 0o600); err != nil {
		return err
	}
	fmt.Printf("Support snapshot written: %s\n", outputPath)
	return nil
}

func buildCompatibilitySupportSnapshot(configPath string, statePath string, configSchema int, stateSchema int, err error) diagnostics.SupportSnapshot {
	now := time.Now().UTC()
	build := buildinfo.Current()
	report := diagnostics.SupportSnapshot{
		GeneratedAt: now,
	}
	report.Runtime.Version = build.Version
	report.Runtime.Channel = build.Channel
	report.Runtime.Commit = build.Commit
	report.Runtime.BuildDate = build.BuildDate
	report.Runtime.BuildID = build.BuildID
	report.Runtime.GoVersion = goruntime.Version()
	report.Runtime.Platform = goruntime.GOOS
	report.Runtime.Arch = goruntime.GOARCH
	report.Runtime.ConfigPath = strings.TrimSpace(configPath)

	report.Config.SchemaVersion = configSchema
	report.Config.StateSchema = stateSchema
	report.Config.StatePath = strings.TrimSpace(statePath)

	report.Diagnostics.FailureCode = diagnostics.FailureLocalSchemaIncompat
	report.Diagnostics.FailureSummary = "Local config or state schema is incompatible with this runtime"
	report.Diagnostics.FailureHint = upgradeCompatibilityHint(err)
	report.Diagnostics.RecentErrors = []string{strings.TrimSpace(err.Error())}

	report.Redaction.OmittedFields = []string{
		"cloud.ingest_api_key_secret",
		"pairing.pairing_code",
		"pairing.activation_token",
		"cloud.credential_ref",
	}
	return report
}

func bestEffortConfigMarkers(path string) (schemaVersion int, statePath string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, ""
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0, ""
	}
	if raw, ok := payload["schema_version"]; ok {
		schemaVersion = asInt(raw)
	}
	if rawPaths, ok := payload["paths"].(map[string]any); ok {
		if rawStatePath, ok := rawPaths["state_file"]; ok {
			if value, ok := rawStatePath.(string); ok {
				statePath = strings.TrimSpace(value)
			}
		}
	}
	return schemaVersion, statePath
}

func bestEffortStateSchema(path string) int {
	if strings.TrimSpace(path) == "" {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		return 0
	}
	return asInt(payload["schema_version"])
}

func asInt(raw any) int {
	switch value := raw.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	default:
		return 0
	}
}

func upgradeCompatibilityHint(err error) string {
	if err == nil {
		return ""
	}
	text := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(text, "state schema version is newer"):
		return "This runtime is older than the persisted state schema. Upgrade receiver binary or reset local state only if intended."
	case strings.Contains(text, "config schema version") && strings.Contains(text, "newer"):
		return "This config file schema is newer than this runtime supports. Upgrade receiver runtime or use a compatible config schema."
	default:
		return ""
	}
}

func installType(stateInstallType string, profile string) string {
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

func emptyFallback(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}
