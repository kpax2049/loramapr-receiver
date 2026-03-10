package install

import (
	"path/filepath"
	"strings"
)

type Layout struct {
	Root            string
	BinaryPath      string
	ConfigDir       string
	ConfigPath      string
	StateDir        string
	StatePath       string
	LogDir          string
	SystemdUnitDir  string
	SystemdUnitPath string
}

func DefaultLinuxLayout(targetRoot string) Layout {
	root := normalizeRoot(targetRoot)
	return Layout{
		Root:            root,
		BinaryPath:      joinRoot(root, "/usr/bin/loramapr-receiverd"),
		ConfigDir:       joinRoot(root, "/etc/loramapr"),
		ConfigPath:      joinRoot(root, "/etc/loramapr/receiver.json"),
		StateDir:        joinRoot(root, "/var/lib/loramapr"),
		StatePath:       joinRoot(root, "/var/lib/loramapr/receiver-state.json"),
		LogDir:          joinRoot(root, "/var/log/loramapr"),
		SystemdUnitDir:  joinRoot(root, "/etc/systemd/system"),
		SystemdUnitPath: joinRoot(root, "/etc/systemd/system/loramapr-receiverd.service"),
	}
}

func normalizeRoot(root string) string {
	trimmed := strings.TrimSpace(root)
	if trimmed == "" {
		return "/"
	}
	return filepath.Clean(trimmed)
}

func joinRoot(root, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "/" {
		return filepath.Clean(root)
	}
	trimmed := strings.TrimPrefix(path, "/")
	if root == "/" {
		return filepath.Join("/", trimmed)
	}
	return filepath.Join(root, trimmed)
}
