package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

type Operation struct {
	Action string
	Path   string
}

type LinuxInstallOptions struct {
	TargetRoot   string
	ServiceUser  string
	ServiceGroup string
	DryRun       bool
	Force        bool
}

type LinuxUninstallOptions struct {
	TargetRoot string
	DryRun     bool
	PurgeState bool
}

type LinuxInstallResult struct {
	Layout     Layout
	Operations []Operation
}

func InstallLinuxSystemd(opts LinuxInstallOptions) (LinuxInstallResult, error) {
	layout := DefaultLinuxLayout(opts.TargetRoot)
	serviceUser := strings.TrimSpace(opts.ServiceUser)
	if serviceUser == "" {
		serviceUser = "loramapr"
	}
	serviceGroup := strings.TrimSpace(opts.ServiceGroup)
	if serviceGroup == "" {
		serviceGroup = serviceUser
	}

	operations := []Operation{
		{Action: "mkdir", Path: layout.ConfigDir},
		{Action: "mkdir", Path: layout.StateDir},
		{Action: "mkdir", Path: layout.LogDir},
		{Action: "mkdir", Path: layout.SystemdUnitDir},
		{Action: "write", Path: layout.ConfigPath},
		{Action: "write", Path: layout.SystemdUnitPath},
	}

	if opts.DryRun {
		return LinuxInstallResult{Layout: layout, Operations: operations}, nil
	}

	for _, dir := range []string{layout.ConfigDir, layout.StateDir, layout.LogDir, layout.SystemdUnitDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return LinuxInstallResult{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}

	if !opts.Force && fileExists(layout.ConfigPath) {
		return LinuxInstallResult{}, fmt.Errorf("config file already exists: %s (use --force to overwrite)", layout.ConfigPath)
	}
	if err := os.WriteFile(layout.ConfigPath, []byte(defaultLinuxConfig()), 0o640); err != nil {
		return LinuxInstallResult{}, fmt.Errorf("write config: %w", err)
	}

	unitText := defaultSystemdUnit(layout, serviceUser, serviceGroup)
	if err := os.WriteFile(layout.SystemdUnitPath, []byte(unitText), 0o644); err != nil {
		return LinuxInstallResult{}, fmt.Errorf("write systemd unit: %w", err)
	}

	return LinuxInstallResult{Layout: layout, Operations: operations}, nil
}

func UninstallLinuxSystemd(opts LinuxUninstallOptions) (LinuxInstallResult, error) {
	layout := DefaultLinuxLayout(opts.TargetRoot)
	operations := []Operation{
		{Action: "remove", Path: layout.SystemdUnitPath},
		{Action: "remove", Path: layout.ConfigPath},
	}
	if opts.PurgeState {
		operations = append(operations,
			Operation{Action: "remove", Path: layout.StatePath},
			Operation{Action: "rmdir", Path: layout.StateDir},
		)
	}

	if opts.DryRun {
		return LinuxInstallResult{Layout: layout, Operations: operations}, nil
	}

	_ = removeIfExists(layout.SystemdUnitPath)
	_ = removeIfExists(layout.ConfigPath)
	if opts.PurgeState {
		_ = removeIfExists(layout.StatePath)
		_ = os.Remove(layout.StateDir)
	}

	return LinuxInstallResult{Layout: layout, Operations: operations}, nil
}

func defaultLinuxConfig() string {
	cfg := config.Default()
	cfg.Paths.StateFile = "/var/lib/loramapr/receiver-state.json"
	cfg.Service.Mode = config.ModeAuto
	cfg.Portal.BindAddress = "127.0.0.1:8080"
	cfg.Logging.Level = "info"
	cfg.Logging.Format = "json"

	preview, err := json.MarshalIndent(cfg, "", "  ")
	if err == nil {
		return string(preview) + "\n"
	}

	// Fallback should only trigger on unexpected local filesystem errors.
	return "{\n  \"service\": {\n    \"mode\": \"auto\",\n    \"heartbeat\": \"30s\"\n  },\n  \"runtime\": {\n    \"profile\": \"auto\"\n  },\n  \"paths\": {\n    \"state_file\": \"/var/lib/loramapr/receiver-state.json\"\n  },\n  \"portal\": {\n    \"bind_address\": \"127.0.0.1:8080\"\n  },\n  \"cloud\": {\n    \"base_url\": \"https://api.loramapr.example\"\n  },\n  \"meshtastic\": {\n    \"transport\": \"serial\"\n  },\n  \"logging\": {\n    \"level\": \"info\",\n    \"format\": \"json\"\n  }\n}\n"
}

func defaultSystemdUnit(layout Layout, serviceUser, serviceGroup string) string {
	return fmt.Sprintf(`[Unit]
Description=LoRaMapr Receiver Service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s -config %s
WorkingDirectory=%s
Restart=on-failure
RestartSec=5
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=full
ProtectHome=true

[Install]
WantedBy=multi-user.target
`, serviceUser, serviceGroup, layout.BinaryPath, layout.ConfigPath, filepath.Dir(layout.StatePath))
}

func removeIfExists(path string) error {
	if _, err := os.Stat(path); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return os.Remove(path)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
