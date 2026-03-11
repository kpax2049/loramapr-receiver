package release

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	manifestSchemaVersion = "receiver-manifest-fragment/v1"
)

type Artifact struct {
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Kind        string `json:"kind"`
	Format      string `json:"format"`
	Filename    string `json:"filename"`
	RelativeURL string `json:"relativeUrl"`
	SHA256      string `json:"checksumSha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	Recommended bool   `json:"recommended"`
}

type Manifest struct {
	SchemaVersion   string     `json:"schemaVersion"`
	ReceiverVersion string     `json:"receiverVersion"`
	Channel         string     `json:"channel"`
	Artifacts       []Artifact `json:"artifacts"`
}

type BuildOptions struct {
	Version      string
	Channel      string
	ArtifactsDir string
	URLPrefix    string
}

func BuildManifest(opts BuildOptions) (Manifest, error) {
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		return Manifest{}, errors.New("version is required")
	}
	channel := strings.TrimSpace(opts.Channel)
	if channel == "" {
		channel = "stable"
	}
	artifactsDir := strings.TrimSpace(opts.ArtifactsDir)
	if artifactsDir == "" {
		return Manifest{}, errors.New("artifacts directory is required")
	}

	checksums, err := loadChecksums(filepath.Join(artifactsDir, "SHA256SUMS"))
	if err != nil {
		return Manifest{}, err
	}

	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return Manifest{}, err
	}

	artifacts := make([]Artifact, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "SHA256SUMS" || strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".asc") {
			continue
		}

		kind, format, goos, arch, parseErr := parseArtifactName(version, name)
		if parseErr != nil {
			continue
		}

		fileInfo, statErr := entry.Info()
		if statErr != nil {
			return Manifest{}, statErr
		}

		checksum, ok := checksums[name]
		if !ok {
			return Manifest{}, fmt.Errorf("missing checksum for artifact %s", name)
		}

		platform := mapPlatform(goos)
		if platform == "" {
			continue
		}

		relativeURL := path.Join(opts.URLPrefix, channel, version, name)
		if strings.TrimSpace(opts.URLPrefix) == "" {
			relativeURL = path.Join("receiver", channel, version, name)
		}

		artifact := Artifact{
			Platform:    platform,
			Arch:        arch,
			Kind:        kind,
			Format:      format,
			Filename:    name,
			RelativeURL: relativeURL,
			SHA256:      checksum,
			SizeBytes:   fileInfo.Size(),
		}
		if goos == "linux" && kind == "deb_package" {
			artifact.Recommended = true
		}
		if platform == "raspberry_pi" && kind == "appliance_image" && arch == "arm64" {
			artifact.Recommended = true
		}
		if goos == "linux" && arch == "arm64" && kind == "systemd_layout" {
			artifact.Recommended = true
		}
		artifacts = append(artifacts, artifact)

		if goos == "linux" &&
			(kind == "systemd_layout" || kind == "deb_package") &&
			(arch == "arm64" || arch == "armv7") {
			piArtifact := artifact
			piArtifact.Platform = "raspberry_pi"
			piArtifact.Recommended = arch == "arm64"
			artifacts = append(artifacts, piArtifact)
		}
	}

	sort.Slice(artifacts, func(i, j int) bool {
		left := artifacts[i]
		right := artifacts[j]
		if left.Platform != right.Platform {
			return left.Platform < right.Platform
		}
		if left.Arch != right.Arch {
			return left.Arch < right.Arch
		}
		if left.Kind != right.Kind {
			return left.Kind < right.Kind
		}
		return left.Filename < right.Filename
	})

	return Manifest{
		SchemaVersion:   manifestSchemaVersion,
		ReceiverVersion: version,
		Channel:         channel,
		Artifacts:       artifacts,
	}, nil
}

func WriteManifest(path string, manifest Manifest) error {
	payload, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}

func loadChecksums(path string) (map[string]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	checksums := map[string]string{}
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		checksums[fields[len(fields)-1]] = fields[0]
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return checksums, nil
}

func parseArtifactName(version string, name string) (kind string, format string, goos string, arch string, err error) {
	prefix := "loramapr-receiver_" + version + "_"
	if !strings.HasPrefix(name, prefix) {
		return "", "", "", "", fmt.Errorf("unexpected artifact prefix: %s", name)
	}

	rest := strings.TrimPrefix(name, prefix)
	switch {
	case strings.HasSuffix(rest, ".img.xz"):
		format = "img.xz"
		rest = strings.TrimSuffix(rest, ".img.xz")
	case strings.HasSuffix(rest, ".tar.gz"):
		format = "tar.gz"
		rest = strings.TrimSuffix(rest, ".tar.gz")
	case strings.HasSuffix(rest, ".zip"):
		format = "zip"
		rest = strings.TrimSuffix(rest, ".zip")
	case strings.HasSuffix(rest, ".deb"):
		format = "deb"
		rest = strings.TrimSuffix(rest, ".deb")
	default:
		return "", "", "", "", fmt.Errorf("unsupported extension: %s", name)
	}

	kind = "binary"
	if format == "deb" {
		kind = "deb_package"
	}
	if format == "img.xz" {
		kind = "appliance_image"
	}
	if strings.HasSuffix(rest, "_systemd") {
		kind = "systemd_layout"
		rest = strings.TrimSuffix(rest, "_systemd")
	}

	parts := strings.Split(rest, "_")
	if len(parts) != 2 {
		return "", "", "", "", fmt.Errorf("invalid artifact naming: %s", name)
	}
	goos = parts[0]
	arch = parts[1]
	if goos == "linux" && (arch == "arm" || arch == "armhf") {
		arch = "armv7"
	}

	return kind, format, goos, arch, nil
}

func mapPlatform(goos string) string {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "linux":
		return "linux"
	case "pi", "raspberrypi":
		return "raspberry_pi"
	case "darwin":
		return "macos"
	case "windows":
		return "windows"
	default:
		return ""
	}
}

func BuildReleaseMetadata(version string, channel string, commit string, manifest Manifest) map[string]any {
	channel = strings.TrimSpace(channel)
	if channel == "" {
		channel = "stable"
	}
	metadata := map[string]any{
		"receiverVersion": version,
		"channel":         channel,
		"artifactCount":   len(manifest.Artifacts),
		"manifestSchema":  manifest.SchemaVersion,
	}
	trimmedCommit := strings.TrimSpace(commit)
	if trimmedCommit != "" {
		metadata["gitCommit"] = trimmedCommit
	}
	linuxCount := 0
	piCount := 0
	for _, artifact := range manifest.Artifacts {
		if artifact.Platform == "linux" {
			linuxCount++
		}
		if artifact.Platform == "raspberry_pi" {
			piCount++
		}
	}
	metadata["linuxArtifacts"] = linuxCount
	metadata["raspberryPiArtifacts"] = piCount
	return metadata
}

func WriteJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o644)
}
