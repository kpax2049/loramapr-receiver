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
	"strings"
	"syscall"
	"time"

	"github.com/loramapr/loramapr-receiver/internal/buildinfo"
	"github.com/loramapr/loramapr-receiver/internal/config"
	"github.com/loramapr/loramapr-receiver/internal/diagnostics"
	"github.com/loramapr/loramapr-receiver/internal/install"
	"github.com/loramapr/loramapr-receiver/internal/logging"
	"github.com/loramapr/loramapr-receiver/internal/meshtastic"
	"github.com/loramapr/loramapr-receiver/internal/runtime"
	"github.com/loramapr/loramapr-receiver/internal/state"
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
		fmt.Printf("[FAIL] config load: %v\n", err)
		os.Exit(1)
	}

	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		fmt.Printf("[FAIL] state open: %v\n", err)
		os.Exit(1)
	}
	snapshot := store.Snapshot()

	deviceProbe, meshState := detectMeshtastic(cfg.Meshtastic)
	cloudProbe := diagnostics.ProbeCloudReachability(cfg.Cloud.BaseURL, 3*time.Second)
	networkProbe := diagnostics.ProbeLocalNetwork()
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        cfg.Runtime.Profile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		PortalState:           "unknown",
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        cloudProbe.Status == "reachable",
		MeshtasticState:       meshState,
		Now:                   time.Now().UTC(),
	})

	report := map[string]any{
		"receiver_version":     buildinfo.Current().Version,
		"release_channel":      buildinfo.Current().Channel,
		"build_commit":         buildinfo.Current().Commit,
		"config_path":          *configPath,
		"state_path":           cfg.Paths.StateFile,
		"pairing_phase":        snapshot.Pairing.Phase,
		"pairing_last_change":  snapshot.Pairing.LastChange,
		"cloud_base_url":       cfg.Cloud.BaseURL,
		"cloud_probe":          cloudProbe,
		"network_probe":        networkProbe,
		"meshtastic_transport": cfg.Meshtastic.Transport,
		"meshtastic_probe":     deviceProbe,
		"failure_code":         finding.Code,
		"failure_summary":      finding.Summary,
		"failure_hint":         finding.Hint,
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
	fmt.Printf("[INFO] cloud probe: %s", cloudProbe.Status)
	if cloudProbe.Detail != "" {
		fmt.Printf(" (%s)", cloudProbe.Detail)
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
		os.Exit(1)
	}
	snapshot := store.Snapshot()
	cloudProbe := diagnostics.ProbeCloudReachability(cfg.Cloud.BaseURL, 3*time.Second)
	networkProbe := diagnostics.ProbeLocalNetwork()
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	_, meshState := detectMeshtastic(cfg.Meshtastic)
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        cfg.Runtime.Profile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		PortalState:           "unknown",
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        cloudProbe.Status == "reachable",
		MeshtasticState:       meshState,
		Now:                   time.Now().UTC(),
	})

	output := map[string]any{
		"receiver_version": buildinfo.Current().Version,
		"release_channel":  buildinfo.Current().Channel,
		"build_commit":     buildinfo.Current().Commit,
		"config_path":      *configPath,
		"state_path":       cfg.Paths.StateFile,
		"installation_id":  snapshot.Installation.ID,
		"pairing_phase":    snapshot.Pairing.Phase,
		"cloud_endpoint":   cfg.Cloud.BaseURL,
		"runtime_mode":     snapshot.Runtime.Mode,
		"runtime_profile":  snapshot.Runtime.Profile,
		"meshtastic_mode":  cfg.Meshtastic.Transport,
		"meshtastic_state": meshState,
		"last_pairing_err": snapshot.Pairing.LastError,
		"failure_code":     finding.Code,
		"failure_summary":  finding.Summary,
		"failure_hint":     finding.Hint,
		"cloud_probe":      cloudProbe.Status,
		"network_probe":    networkProbe,
		"updated_at":       snapshot.Metadata.UpdatedAt,
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
		slog.Error("support snapshot failed: load config", "err", err)
		os.Exit(1)
	}
	store, err := state.Open(cfg.Paths.StateFile)
	if err != nil {
		slog.Error("support snapshot failed: open state", "err", err)
		os.Exit(1)
	}
	snapshot := store.Snapshot()
	deviceProbe, meshState := detectMeshtastic(cfg.Meshtastic)
	cloudProbe := diagnostics.ProbeCloudReachability(cfg.Cloud.BaseURL, 3*time.Second)
	networkProbe := diagnostics.ProbeLocalNetwork()
	networkAvailable, networkKnown := diagnostics.NetworkAvailable(networkProbe)
	finding := diagnostics.Evaluate(diagnostics.Input{
		RuntimeProfile:        cfg.Runtime.Profile,
		PairingPhase:          string(snapshot.Pairing.Phase),
		PairingLastChange:     snapshot.Pairing.LastChange,
		PairingLastError:      snapshot.Pairing.LastError,
		PortalState:           "unknown",
		NetworkAvailable:      networkAvailable,
		NetworkAvailableKnown: networkKnown,
		CloudReachable:        cloudProbe.Status == "reachable",
		MeshtasticState:       meshState,
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
	})

	payload, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		slog.Error("support snapshot failed: encode", "err", err)
		os.Exit(1)
	}
	payload = append(payload, '\n')

	if strings.TrimSpace(*outputPath) == "" {
		_, _ = os.Stdout.Write(payload)
		return
	}
	if err := os.WriteFile(*outputPath, payload, 0o600); err != nil {
		slog.Error("support snapshot failed: write file", "err", err, "path", *outputPath)
		os.Exit(1)
	}
	fmt.Printf("Support snapshot written: %s\n", *outputPath)
}

func signalNotifyContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
}

func printUsage() {
	fmt.Println("LoRaMapr Receiver")
	fmt.Println("Usage:")
	fmt.Println("  loramapr-receiverd run [flags]")
	fmt.Println("  loramapr-receiverd install [flags]")
	fmt.Println("  loramapr-receiverd uninstall [flags]")
	fmt.Println("  loramapr-receiverd doctor [flags]")
	fmt.Println("  loramapr-receiverd status [flags]")
	fmt.Println("  loramapr-receiverd support-snapshot [flags]")
	fmt.Println("")
	fmt.Println("If no subcommand is provided, run mode is used.")
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
