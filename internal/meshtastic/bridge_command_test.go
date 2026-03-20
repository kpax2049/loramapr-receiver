package meshtastic

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/loramapr/loramapr-receiver/internal/config"
)

func TestResolveBridgeCommandDefault(t *testing.T) {
	t.Parallel()

	command, args, err := resolveBridgeCommand(config.MeshtasticConfig{}, "/dev/ttyACM0")
	if err != nil {
		t.Fatalf("resolveBridgeCommand returned error: %v", err)
	}
	if command == "" {
		t.Fatal("expected default command path")
	}
	if len(args) != 3 {
		t.Fatalf("expected default args, got %#v", args)
	}
	if args[0] != "meshtastic-bridge" || args[1] != "-device" || args[2] != "/dev/ttyACM0" {
		t.Fatalf("unexpected default args: %#v", args)
	}
}

func TestResolveBridgeCommandCustom(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	commandPath := filepath.Join(tmpDir, "bridge")
	if err := os.WriteFile(commandPath, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write command stub: %v", err)
	}

	command, args, err := resolveBridgeCommand(config.MeshtasticConfig{
		BridgeCommand: commandPath,
		BridgeArgs:    []string{"--port", "{{device}}", "--mirror", "${MESHTASTIC_PORT}"},
	}, "/dev/serial/by-id/usb-mesh")
	if err != nil {
		t.Fatalf("resolveBridgeCommand returned error: %v", err)
	}
	if command != commandPath {
		t.Fatalf("unexpected command path: %q", command)
	}
	if len(args) != 4 {
		t.Fatalf("unexpected args len: %#v", args)
	}
	if args[1] != "/dev/serial/by-id/usb-mesh" || args[3] != "/dev/serial/by-id/usb-mesh" {
		t.Fatalf("expected device token substitution, got %#v", args)
	}
}

func TestResolveBridgeCommandRequiresDevice(t *testing.T) {
	t.Parallel()

	_, _, err := resolveBridgeCommand(config.MeshtasticConfig{}, "")
	if err == nil {
		t.Fatal("expected error for empty device")
	}
}
